"""Own process accounting: PID RSS plus wait4 peaks even between1s samples."""
from __future__ import annotations

import atexit
import json
import os
from pathlib import Path
import resource
import signal
import subprocess
import sys
import threading
import time
from typing import Any

TRACE_ENV='PULSE_COLLECTION_PROCESS_TRACE'


def peak_bytes(usage:Any)->int:
    return int(usage.ru_maxrss)*(1024 if sys.platform.startswith('linux') else 1)


def event(kind:str,pid:int,role:str,**values:Any)->None:
    target=os.environ.get(TRACE_ENV)
    if not target:return
    record={'at':time.time(),'kind':kind,'pid':pid,'role':role,'parent_pid':os.getpid(),**values}
    descriptor=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_APPEND,0o600)
    try:os.write(descriptor,(json.dumps(record,separators=(',',':'))+'\n').encode())
    finally:os.close(descriptor)


def self_peak(role:str)->None:event('peak',os.getpid(),role,peak_bytes=peak_bytes(resource.getrusage(resource.RUSAGE_SELF)))


def track_self(directory:Path,role:str)->None:
    os.environ[TRACE_ENV]=str((directory/'process-trace.jsonl').resolve())
    event('start',os.getpid(),role)
    self_peak(role)
    atexit.register(lambda:event('exit',os.getpid(),role,peak_bytes=peak_bytes(resource.getrusage(resource.RUSAGE_SELF))))


class Process:
    """Popen I/O with own wait4 reaping; Popen cannot discard the child RSS peak."""
    def __init__(self,args:list[str],*,role:str='helper-child',**kwargs:Any):
        self.process=subprocess.Popen(args,**kwargs)
        self.pid=self.process.pid;self.role=role;self._wait_lock=threading.Lock()
        self.stdin=self.process.stdin;self.stdout=self.process.stdout;self.stderr=self.process.stderr
        event('start',self.pid,role)

    @property
    def returncode(self)->int|None:return self.process.returncode

    def poll(self)->int|None:
        with self._wait_lock:
            if self.process.returncode is not None:return self.process.returncode
            try:pid,status,usage=os.wait4(self.pid,os.WNOHANG)
            except ChildProcessError:raise RuntimeError('own child reaped outside accounting')
            if pid:
                self.process.returncode=os.waitstatus_to_exitcode(status)
                event('exit',self.pid,self.role,peak_bytes=peak_bytes(usage))
            return self.process.returncode

    def wait(self,timeout:float|None=None)->int:
        deadline=None if timeout is None else time.monotonic()+timeout
        while self.poll() is None:
            if deadline is not None and time.monotonic()>deadline:raise subprocess.TimeoutExpired('own child',timeout)
            time.sleep(.01)
        assert self.process.returncode is not None
        return self.process.returncode

    def terminate(self)->None:
        if self.poll() is None:os.kill(self.pid,signal.SIGTERM)

    def kill(self)->None:
        if self.poll() is None:os.kill(self.pid,signal.SIGKILL)


def run(args:list[str],*,input:str|None=None,text:bool=True,capture_output:bool=True,
        timeout:float=30,cwd:Path|None=None,env:dict[str,str]|None=None,check:bool=False,
        stdout:Any=None,stderr:Any=None)->subprocess.CompletedProcess:
    process=Process(args,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,cwd=cwd,env=env,role=Path(args[0]).name)
    output=[bytearray(),bytearray()]
    def reader(stream:Any,buffer:bytearray)->None:
        while data:=stream.read(65536):
            if len(buffer)+len(data)>64*1024**2:process.kill();return
            buffer.extend(data)
    threads=[threading.Thread(target=reader,args=(process.stdout,output[0])),threading.Thread(target=reader,args=(process.stderr,output[1]))]
    for thread in threads:thread.start()
    try:
        if input is not None:process.stdin.write(input.encode())
        process.stdin.close()
    except BrokenPipeError:
        process.stdin.close()
    try:code=process.wait(timeout)
    except subprocess.TimeoutExpired:
        process.kill();process.wait(3);raise
    finally:
        for thread in threads:thread.join(timeout=3)
    result=subprocess.CompletedProcess(args,code,output[0].decode() if text else bytes(output[0]),output[1].decode() if text else bytes(output[1]))
    if stdout is not None:stdout.write(result.stdout)
    if check and code:raise subprocess.CalledProcessError(code,'own child')
    return result


def read_trace(path:Path):
    if not path.exists():return
    with path.open() as stream:
        for line in stream:
            if line.strip():yield json.loads(line)


def host_usage(directory:Path,since:float)->tuple[int,int,dict[str,int]]:
    records=read_trace(directory/'process-trace.jsonl')
    active:dict[int,dict[str,Any]]={};peaks:dict[int,int]={};recent_exit:dict[int,int]={}
    for record in records:
        pid=record['pid']
        if record['kind']=='start':active[pid]=record
        if 'peak_bytes' in record:peaks[pid]=max(peaks.get(pid,0),record['peak_bytes'])
        if record['kind']=='exit':
            active.pop(pid,None)
            if record['at']>=since:recent_exit[pid]=peaks.get(pid,0)
    current={}
    if active:
        result=run(['ps','-o','pid=,rss=','-p',','.join(map(str,active))],timeout=2)
        for line in result.stdout.splitlines():
            pid,rss=map(int,line.split());current[pid]=max(rss*1024,peaks.get(pid,0))
            if pid in active:event('peak',pid,active[pid]['role'],peak_bytes=current[pid])
    # Include exact wait4 peaks of short children that lived between samples.
    for record in read_trace(directory/'process-trace.jsonl'):
        if record['kind']=='exit' and record['at']>=since:recent_exit[record['pid']]=record.get('peak_bytes',0)
    for pid,peak in recent_exit.items():current[pid]=max(current.get(pid,0),peak)
    return sum(current.values()),max(current.values(),default=0),{str(pid):value for pid,value in current.items()}
