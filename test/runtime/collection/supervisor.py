#!/usr/bin/env python3
"""Реальные cgroup/PG/disk counters через постоянные owned monitor streams, 1s."""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import resource
import selectors
import signal
import subprocess
import processes
import own_engine
import time
from typing import Any

from harness import LIMITS, breaches, docker, inspect_owned, save


def tree_bytes(root: Path) -> int:
    return sum(path.stat().st_size for path in root.rglob('*') if not path.is_symlink() and path.is_file())


def stop_owned(state: dict[str, Any]) -> None:
    for identity in state['containers']:
        inspect_owned(state, identity)
    for identity in state['containers']:
        docker('stop', '-t', '2', identity)


def main() -> None:
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-dir',type=Path,required=True)
    parser.add_argument('--allow-isolated-mutation',action='store_true',required=True)
    options=parser.parse_args()
    directory=options.state_dir
    processes.track_self(directory,'supervisor')
    state=json.loads((directory/'state.json').read_text())
    run=True
    def shutdown(_signum:int,_frame:Any)->None:
        nonlocal run
        run=False
    signal.signal(signal.SIGTERM,shutdown);signal.signal(signal.SIGINT,shutdown)
    selector=selectors.DefaultSelector()
    engine_socket=own_engine.context_socket()
    monitor_processes:dict[str,own_engine.Monitor]={}
    log_processes:dict[str,subprocess.Popen[bytes]]={}
    log_files:dict[str,Any]={}
    logs=directory/'logs';logs.mkdir(exist_ok=True)
    buffers:dict[str,bytes]={}
    latest:dict[str,dict[str,Any]]={}
    attached:dict[str,float]={}
    initial:dict[str,int]={}
    disk_snapshots:dict[str,int]={}
    roles:dict[str,str]={}
    pending_stops:dict[str,float]={}
    def observed_restart(identity:str)->bool:
        row=latest.get(identity)
        if row is None or abs(time.time()-row['at']/1e9)>3:return False
        path=directory/'container-lifecycle.jsonl'
        events=[json.loads(line) for line in path.read_text().splitlines() if line] if path.exists() else []
        observed=[row for row in events if row.get('observed') and row.get('id')==identity and row.get('role')==roles[identity] and attached[identity]<=row['at']<=time.time()]
        stops=[row['at'] for row in observed if row['state']=='stopped']
        return bool(stops and any(row['state']=='running' and row['at']>max(stops) for row in observed))

    def rearm(identity:str)->None:
        process=own_engine.Monitor(state,identity,roles[identity],engine_socket)
        os.set_blocking(process.stdout.fileno(),False);selector.register(process.stdout,selectors.EVENT_READ,identity)
        monitor_processes[identity]=process;buffers[identity]=b'';latest.pop(identity,None);attached[identity]=time.time()
        pending_stops.pop(identity,None)

    started=time.monotonic()
    save(directory/('supervisor-start-'+str(time.time_ns())+'.json'),{'pid':os.getpid(),'stream_interval_seconds':1,'state':str(directory/'state.json')})
    path=directory/('resource-samples-'+str(time.time_ns())+'.jsonl')
    next_sample=time.monotonic()+1
    last_published_at:float|None=None
    def check_sampling_deadline(*,deferred:bool=False,at:float|None=None)->float|None:
        now=time.time() if at is None else at;deadlines=[]
        if last_published_at is not None:deadlines.append(last_published_at+3)
        for identity in monitor_processes:
            row=latest.get(identity)
            deadlines.append((row['at']/1e9 if row is not None else attached[identity])+3)
        path=directory/'container-lifecycle.jsonl'
        observed={}
        if path.exists():
            for event in sorted((json.loads(line) for line in path.read_text().splitlines() if line),key=lambda value:value['at']):
                if event.get('observed') and event['at']<=now:observed[event['role']]=event
        for event in observed.values():
            if event['state']=='running':
                row=latest.get(event['id'])
                if row is None or row['at']/1e9<event['at']:deadlines.append(event['at']+3)
        deadline=min(deadlines) if deadlines else None
        if deadline is not None and (now>=deadline if deferred else now>deadline):raise ValueError('monitor sampling stale')
        return deadline
    try:
        with path.open('x') as output:
            while run:
                if (directory/'supervisor-stop-request').exists():break
                check_sampling_deadline()
                state=json.loads((directory/'state.json').read_text())
                for identity in state['containers']:
                    if identity in monitor_processes or identity in state.get('expected_stopped',[]):continue
                    role=next((role for role in own_engine.ROLES if state.get(role+'_id')==identity),None)
                    if role is None:raise ValueError('unbudgeted role')
                    process=own_engine.Monitor(state,identity,role,engine_socket)
                    assert process.stdout is not None
                    os.set_blocking(process.stdout.fileno(),False)
                    selector.register(process.stdout,selectors.EVENT_READ,identity)
                    monitor_processes[identity]=process;buffers[identity]=b'';attached[identity]=time.time();roles[identity]=role
                    logging=processes.Process(['docker','logs','--follow','--timestamps',identity],stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
                    assert logging.stdout is not None
                    os.set_blocking(logging.stdout.fileno(),False)
                    selector.register(logging.stdout,selectors.EVENT_READ,(identity,'log'))
                    log_processes[identity]=logging
                    log_files[identity]=os.fdopen(os.open(logs/(identity+'.log'),os.O_WRONLY|os.O_CREAT|os.O_APPEND,0o600),'ab',buffering=0)
                deadline=check_sampling_deadline()
                wait=min(1,next_sample-time.monotonic())
                if deadline is not None:wait=min(wait,deadline-time.time())
                for event,_ in selector.select(timeout=max(0,wait)):
                    identity=event.data
                    if isinstance(identity,tuple):
                        identity=identity[0]
                        chunk=os.read(event.fileobj.fileno(),65536)
                        if chunk:log_files[identity].write(chunk)
                        else:
                            selector.unregister(event.fileobj)
                            log_processes[identity].wait(2)
                            log_files[identity].close();log_files.pop(identity,None);log_processes.pop(identity,None)
                        continue
                    data=os.read(event.fileobj.fileno(),65536)
                    if not data:
                        ledger=json.loads((directory/'state.json').read_text())
                        if ledger.get('run_id')!=state.get('run_id'):raise ValueError('active monitor lost')
                        restarted=False
                        if identity not in ledger.get('expected_stopped',[]):
                            restarted=observed_restart(identity)
                            if not restarted or not own_engine.inspect(state,identity,engine_socket)['State']['Running']:raise ValueError('active monitor lost')
                        selector.unregister(event.fileobj)
                        monitor_processes[identity].wait(2)
                        if restarted:rearm(identity)
                        else:pending_stops[identity]=time.time()
                        continue
                    buffers[identity]+=data
                    if len(buffers[identity])>65536:raise ValueError('monitor frame overflow')
                    while b'\n' in buffers[identity]:
                        line,buffers[identity]=buffers[identity].split(b'\n',1)
                        row=json.loads(line)
                        if row.get('error') or row['role']!=roles[identity]:raise ValueError('monitor unavailable')
                        latest[identity]=row
                        disk_snapshots[identity]=row.get('pgdata',0)+row.get('disk',0)
                        if row['role']=='postgres' and not initial:
                            initial={'wal_lsn':row['wal_lsn'],'temp':row.get('temp',0)}
                for identity,since in list(pending_stops.items()):
                    if own_engine.inspect(state,identity,engine_socket)['State']['Running']:
                        if time.time()-since<3 and observed_restart(identity):
                            rearm(identity);continue
                        row=latest.get(identity)
                        if time.time()-since>=3 or row is None or time.time()-row['at']/1e9>3:raise ValueError('planned stop not physically observed')
                        continue
                    lifecycle={'at':time.time(),'role':roles[identity],'id':identity,'state':'stopped','observed':True,'stop_deadline_at':time.time()+state.get('limits',LIMITS)['case_seconds']}
                    with (directory/'container-lifecycle.jsonl').open('a') as stream:stream.write(json.dumps(lifecycle)+'\n')
                    pending_stops.pop(identity,None);monitor_processes.pop(identity,None);buffers.pop(identity,None);latest.pop(identity,None);attached.pop(identity,None);roles.pop(identity,None)
                check_sampling_deadline()
                if time.monotonic()<next_sample:
                    check_sampling_deadline(deferred=True)
                    continue
                measured:dict[str,int|float]={}
                peaks:dict[str,int]={}
                for identity in monitor_processes:
                    row=latest.get(identity)
                    if row is None:
                        if time.time()-attached[identity]>3:raise ValueError('monitor startup deadline')
                        continue
                    if time.time()-row['at']/1e9>3:raise ValueError('monitor sampling stale')
                    measured[row['role']+'_bytes']=row['current'];peaks[row['role']+'_bytes']=row['peak']
                    if row['role']=='postgres':
                        measured.update(database_bytes=row['database'],pgdata_bytes=row['pgdata'],wal_bytes=row['wal'],
                                        wal_delta_bytes=row['wal_lsn']-initial['wal_lsn'],temp_delta_bytes=row.get('temp',0)-initial['temp'])
                if any(identity not in latest for identity in monitor_processes):
                    check_sampling_deadline(deferred=True)
                    next_sample=time.monotonic()+.1
                    continue
                volume_bytes=sum(disk_snapshots.values())
                memory=sum(value for key,value in measured.items() if key in ('api_bytes','worker1_bytes','worker2_bytes','fixture_bytes','postgres_bytes','rabbit_bytes'))
                processes.self_peak('supervisor')
                harness_rss,harness_max,host_pids=processes.host_usage(directory,time.time()-1.5)
                measured['harness_max_process_bytes']=harness_max
                measured['aggregate_bytes']=memory+harness_rss
                host_bytes=tree_bytes(directory)
                if state.get('build_dir'):host_bytes+=tree_bytes(Path(state['build_dir']))
                measured['stand_bytes']=host_bytes+volume_bytes+state.get('helper_copies_bytes',0)+state.get('image_bytes',0)
                measured['evidence_bytes']=tree_bytes(directory)
                for folder,key in [('fixture','fixture_files_bytes'),('tmp','transient_bytes'),('logs','logs_bytes')]:
                    measured[key]=tree_bytes(directory/folder) if (directory/folder).exists() else 0
                if (directory/'actual-started.json').exists():
                    actual=json.loads((directory/'actual-started.json').read_text())
                    measured['suite_seconds']=time.time()-actual['unix_seconds']
                    if actual.get('case_started_at'):measured['case_seconds']=time.time()-actual['case_started_at']
                if time.monotonic()-started>86400:raise ValueError('stand retention exceeded')
                limits=state.get('limits',LIMITS)
                # One cutoff binds topology, real counter epochs and publication.
                # A lifecycle mutation after this cutoff belongs to the next row;
                # a mutation while building this candidate invalidates it.
                capture_cutoff=time.time()
                check_sampling_deadline(at=capture_cutoff)
                ledger_raw=(directory/'state.json').read_text()
                ledger=json.loads(ledger_raw)
                if ledger.get('run_id')!=state['run_id']:raise ValueError('active monitor lost')
                lifecycle_path=directory/'container-lifecycle.jsonl'
                lifecycle_raw=lifecycle_path.read_text() if lifecycle_path.exists() else ''
                from proof import active_roles
                active=active_roles([json.loads(line) for line in lifecycle_raw.splitlines() if line],capture_cutoff,limits['case_seconds'],run_id=ledger['run_id'])
                counter_snapshot={identity:dict(value) for identity,value in latest.items()}
                role_snapshot=dict(roles)
                incomplete=False
                for role,event in active.items():
                    if event['state']!='running':continue
                    identity=event['id']
                    if ledger.get(role+'_id')!=identity or identity not in ledger['containers']:raise ValueError('active monitor lost')
                    counter=counter_snapshot.get(identity)
                    if counter is None or role_snapshot.get(identity)!=role or counter['at']/1e9<event['at']:
                        if capture_cutoff-event['at']>=3:raise ValueError('monitor startup deadline')
                        incomplete=True;continue
                    if abs(capture_cutoff-counter['at']/1e9)>3:raise ValueError('monitor sampling stale')
                    if role+'_bytes' not in measured or role+'_bytes' not in peaks:raise ValueError('monitor unavailable')
                if incomplete:
                    check_sampling_deadline(deferred=True)
                    next_sample=time.monotonic()+.1
                    continue
                if state.get('profile')=='module':
                    stage_path=directory/'module-stage-started.json'
                    stage=json.loads(stage_path.read_text()) if stage_path.exists() else {'unix_seconds':time.time()-(time.monotonic()-started)}
                    measured['suite_seconds']=time.time()-stage['unix_seconds']
                violated=breaches(measured,limits)+['peak:'+key for key,value in peaks.items() if value>limits[key]]
                sample={'at':capture_cutoff,'measured':measured,'cgroup_peaks':peaks,'harness_peak_rss_bytes':harness_rss,'host_pids':host_pids,'role_ids':{role_snapshot[key]:key for key in counter_snapshot},'breaches':violated,'source_sample_at':{role_snapshot[key]:value['at'] for key,value in counter_snapshot.items()}}
                serialized=json.dumps(sample)+'\n'
                if (directory/'state.json').read_text()!=ledger_raw or (lifecycle_path.read_text() if lifecycle_path.exists() else '')!=lifecycle_raw:
                    check_sampling_deadline(deferred=True)
                    next_sample=time.monotonic()+.1
                    continue
                check_sampling_deadline()
                output.write(serialized);output.flush()
                last_published_at=capture_cutoff
                ready=directory/'.supervisor-ready.tmp';ready.write_text(json.dumps({'at':sample['at'],'role_ids':sample['role_ids'],'source_sample_at':sample['source_sample_at']}));ready.replace(directory/'supervisor-ready.json')
                if violated:
                    stop_owned(state)
                    save(directory/('resource-stop-'+str(time.time_ns())+'.json'),{'status':'FAIL','reason':'budget_breach','metrics':violated})
                    return
                next_sample=time.monotonic()+1
    except Exception as error:
        if (directory/'supervisor-stop-request').exists():return
        stop_owned(state)
        save(directory/('resource-stop-'+str(time.time_ns())+'.json'),{'status':'UNKNOWN','reason':'measurement_unavailable','error_type':type(error).__name__,'safe_code':str(error) if isinstance(error,ValueError) and str(error) in {'monitor startup deadline','monitor sampling stale','active monitor lost','monitor unavailable','monitor frame overflow','unbudgeted role','planned stop not physically observed'} else 'measurement_failure'})
    finally:
        for handle in log_files.values():handle.close()
        for process in list(monitor_processes.values())+list(log_processes.values()):
            if process.stdin:process.stdin.close()
            if process.poll() is None:process.terminate()
            try:process.wait(3)
            except subprocess.TimeoutExpired:process.kill();process.wait(3)
        selector.close()


if __name__=='__main__':main()
