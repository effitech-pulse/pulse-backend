"""Bounded local Engine monitoring, restricted to exact recorded own containers."""
from __future__ import annotations

import http.client
import json
import os
from pathlib import Path
import re
import socket
import struct
import subprocess
import threading
import urllib.parse
from typing import Any

from harness import LABEL, command, owned_id

ROLES={'postgres','rabbit','fixture','api','worker1','worker2'}
HEX=re.compile(r'[0-9a-f]{64}\Z')
MAX_FRAME=32768


class Connection(http.client.HTTPConnection):
    def __init__(self,path:str,timeout:float=2):
        super().__init__('localhost',timeout=timeout);self.path=path
    def connect(self)->None:
        self.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout);self.sock.connect(self.path)


def context_socket()->str:
    endpoint=command(['docker','context','inspect','--format','{{.Endpoints.docker.Host}}'])
    if not endpoint.startswith('unix://') or not Path(endpoint[7:]).is_absolute():raise ValueError('local Unix Docker context required')
    return endpoint[7:]


def _id(state:dict,identity:str)->None:
    if not HEX.fullmatch(identity) or identity not in state['containers']:raise ValueError('unknown monitor container')


def _snapshot(state:dict,identity:str,path:str,timeout:float)->list:
    _id(state,identity);client=Connection(path,timeout)
    try:
        filters=urllib.parse.quote(json.dumps({'id':[identity]},separators=(',',':')),safe='')
        client.request('GET','/containers/json?all=1&filters='+filters)
        response=client.getresponse();raw=response.read(1024**2+1)
        if response.status!=200 or len(raw)>1024**2:raise ValueError('bounded own inspect failed')
        rows=json.loads(raw)
        if not isinstance(rows,list):raise ValueError('invalid own snapshot')
        return rows
    finally:client.close()


def _record(state:dict,identity:str,rows:list)->dict:
    if len(rows)!=1 or rows[0].get('Id')!=identity:raise ValueError('exact own snapshot missing')
    row=rows[0]
    if row.get('State') not in ('running','paused','exited','dead','created'):raise ValueError('unknown own physical state')
    role=next((role for role in ROLES if state.get(role+'_id')==identity),None)
    if role is None or row.get('Names')!=['/'+state['project']+'-'+role]:raise ValueError('own container name/role mismatch')
    record={'Id':row['Id'],'Config':{'Labels':row.get('Labels',{})},'Name':row['Names'][0],'State':{'Running':row['State'] in ('running','paused')}}
    owned_id(record,state['run_id'],state['containers']);return record


def inspect(state:dict,identity:str,path:str,*,timeout:float=.25)->dict:
    return _record(state,identity,_snapshot(state,identity,path,timeout))


def absent(state:dict,identity:str,path:str)->bool:
    """Positive zero-result exact-ID query, distinct from failed/mismatched inspection."""
    rows=_snapshot(state,identity,path,.25)
    if not rows:return True
    _record(state,identity,rows)
    return False


def _frames(stream:Any,write:Any)->None:
    while True:
        header=stream.read(8)
        if not header:return
        if len(header)!=8:raise ValueError('truncated Engine frame')
        kind,a,b,c,size=struct.unpack('!BBBBI',header)
        if kind!=1 or a or b or c or not 0<size<=MAX_FRAME:raise ValueError('invalid Engine monitor frame')
        body=bytearray()
        while len(body)<size:
            chunk=stream.read(size-len(body))
            if not chunk:raise ValueError('truncated Engine payload')
            body.extend(chunk)
        write(bytes(body))


class Monitor:
    """Background ownership/exec handshake; sampler consumes only bounded pipe I/O."""
    def __init__(self,state:dict,identity:str,role:str,path:str):
        _id(state,identity)
        if role not in ROLES:raise ValueError('unknown monitor role')
        self.state=state;self.identity=identity;self.role=role;self.path=path
        read_fd,self.write_fd=os.pipe();self.stdout=os.fdopen(read_fd,'rb',buffering=0)
        self.stdin=None;self.returncode=None;self.client=None;self.error=None
        self.stage='inspect';self.stopped=threading.Event();self.thread=threading.Thread(target=self._run,daemon=True);self.thread.start()

    def _run(self)->None:
        try:
            record=inspect(self.state,self.identity,self.path,timeout=2)
            if not record['State']['Running'] or record['Name']!='/'+self.state['project']+'-'+self.role:raise ValueError('monitor identity role/state mismatch')
            self.stage='create';client=Connection(self.path);self.client=client
            payload={'AttachStdout':True,'AttachStderr':True,'AttachStdin':False,'Tty':False,'Privileged':False,'Cmd':['/fixture','monitor',self.role]}
            client.request('POST','/containers/'+self.identity+'/exec',json.dumps(payload),{'Content-Type':'application/json'})
            response=client.getresponse();raw=response.read(4097)
            if response.status!=201 or len(raw)>4096:raise ValueError('bounded monitor exec creation failed')
            identity=json.loads(raw).get('Id','')
            if not HEX.fullmatch(identity):raise ValueError('invalid fixed monitor exec identity')
            self.stage='start'
            client.request('POST','/exec/'+identity+'/start',json.dumps({'Detach':False,'Tty':False}),{'Content-Type':'application/json'})
            response=client.getresponse()
            if response.status!=200:raise ValueError('monitor stream unavailable')
            def write(data:bytes)->None:
                view=memoryview(data)
                while view and not self.stopped.is_set():view=view[os.write(self.write_fd,view):]
            self.stage='frames'
            _frames(response,write)
            self.returncode=0
        except Exception as error:
            self.error=type(error).__name__;self.returncode=2
        finally:
            if self.client:self.client.close()
            os.close(self.write_fd)

    def poll(self)->int|None:return self.returncode
    def wait(self,timeout:float|None=None)->int:
        self.thread.join(timeout)
        if self.thread.is_alive():raise subprocess.TimeoutExpired('owned monitor',timeout)
        return self.returncode or 0
    def terminate(self)->None:
        self.stopped.set()
        if self.client and self.client.sock:
            try:self.client.sock.shutdown(socket.SHUT_RDWR)
            except OSError:pass
    def kill(self)->None:self.terminate()
