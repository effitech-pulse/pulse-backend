#!/usr/bin/env python3
"""Bounded loopback→single persistent stdio tunnel; internal network unchanged."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import resource
import signal
import socket
import struct
import subprocess
import processes
import threading
import time

from harness import inspect_owned, save

FRAME=32768
MAX_CONNECTIONS=16
HEADER=struct.Struct('!IBI')


def main() -> None:
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-dir',type=Path,required=True)
    options=parser.parse_args()
    directory=options.state_dir
    processes.track_self(directory,'forwarder')
    state=json.loads((directory/'state.json').read_text())
    identity=state['postgres_id']
    inspect_owned(state,identity)
    listeners=[]
    for port,role in [(55442,b'pg'),(5675,b'rabbit'),(8094,b'api'),(8093,b'fixture'),(15675,b'management')]:
        listener=socket.socket()
        listener.bind(('127.0.0.1',port));listener.listen(MAX_CONNECTIONS)
        listeners.append((listener,role))
    process=processes.Process(['docker','exec','-i',identity,'/fixture','tunnel'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
    assert process.stdin is not None and process.stdout is not None
    connections:dict[int,socket.socket]={}
    guard=threading.Lock();write_guard=threading.Lock();running=threading.Event();running.set()
    counter=0
    def send(identity:int,kind:int,body:bytes=b'')->None:
        if len(body)>FRAME:raise ValueError('frame exceeds frozen limit')
        with write_guard:
            process.stdin.write(HEADER.pack(identity,kind,len(body))+body);process.stdin.flush()
    def close(identity:int)->None:
        with guard:connection=connections.pop(identity,None)
        if connection is not None:
            try:connection.shutdown(socket.SHUT_RDWR)
            except OSError:pass
            connection.close()
    def read_exact(size:int)->bytes:
        body=b''
        while len(body)<size:
            chunk=process.stdout.read(size-len(body))
            if not chunk:raise EOFError('own tunnel closed')
            body+=chunk
        return body
    def receiver()->None:
        try:
            while running.is_set():
                identity,kind,size=HEADER.unpack(read_exact(HEADER.size))
                if size>FRAME or kind not in (2,3):raise ValueError('invalid tunnel frame')
                body=read_exact(size)
                with guard:connection=connections.get(identity)
                if kind==3:close(identity)
                elif connection is not None:connection.sendall(body)
        except (OSError,ValueError,EOFError):running.clear()
    def client(identity:int,connection:socket.socket)->None:
        try:
            while running.is_set():
                data=connection.recv(FRAME)
                if not data:break
                send(identity,2,data)
        except (OSError,ValueError):pass
        finally:
            close(identity)
            try:send(identity,3)
            except OSError:running.clear()
    def accept(listener:socket.socket,role:bytes)->None:
        nonlocal counter
        while running.is_set():
            try:connection,_=listener.accept()
            except OSError:return
            with guard:
                if len(connections)>=MAX_CONNECTIONS:connection.close();continue
                counter+=1;identity=counter;connections[identity]=connection
            send(identity,1,role)
            threading.Thread(target=client,args=(identity,connection),daemon=True).start()
    def stop(_signum:int,_frame:object)->None:running.clear()
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
    threading.Thread(target=receiver,daemon=True).start()
    for listener,role in listeners:threading.Thread(target=accept,args=(listener,role),daemon=True).start()
    save(directory/('forwarder-start-'+str(time.time_ns())+'.json'),{'pg_id':identity,'ports':[55442,5675,8094,8093,15675],'connections_max':16,'frame_max':32768,'external_egress':False})
    print('READY loopback55442/5675/8094/8093/15675 via bounded own stdio tunnel',flush=True)
    while running.wait(1):
        if process.poll() is not None:break
        value={'at':time.time(),'rss_peak_bytes':resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,'connections':len(connections)}
        temporary=directory/'.forwarder-metrics.tmp';temporary.write_text(json.dumps(value));temporary.replace(directory/'forwarder-metrics.json')
        # Event.wait returns immediately while set; sleep is bounded to one second.
        time.sleep(1)
    for listener,_ in listeners:listener.close()
    for identity in list(connections):close(identity)
    if process.stdin:process.stdin.close()
    try:process.wait(timeout=3)
    except subprocess.TimeoutExpired:process.terminate()


if __name__=='__main__':main()
