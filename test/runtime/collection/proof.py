"""Критерии наблюдаемой terminal delivery; отсутствие Fetch само по себе недостаточно."""
from __future__ import annotations

import math
import hashlib
import json


def terminal_delivery_proven(*,event_id:str,observed_event:str,state:str,outcome:str,
                             before_updated:float,after_updated:float,ready:int,unacked:int,
                             quarantine_before:int,quarantine_after:int)->bool:
    return (observed_event==event_id and state=='completed' and outcome=='ignored'
            and after_updated>before_updated and ready==0 and unacked==0
            and quarantine_after==quarantine_before)

GLOBAL_METRICS={'aggregate_bytes','harness_max_process_bytes','database_bytes','pgdata_bytes','wal_bytes','wal_delta_bytes','temp_delta_bytes','stand_bytes','evidence_bytes','logs_bytes','fixture_files_bytes','transient_bytes'}


def active_roles(events:list[dict],at:float,case_seconds:float,run_id:str|None=None)->dict:
    active={}
    for event in sorted(events,key=lambda row:row['at']):
        if event['at']>at:break
        prior=active.get(event['role'])
        if prior and prior['state']=='retired':raise ValueError('retired role cannot reenter lifecycle')
        if event['state']=='retired':
            body=event.get('removal_receipt',{})
            if event['role']!='worker2' or not prior or prior['state']!='stopped' or not prior.get('observed') or prior['id']!=event['id']:raise ValueError('invalid optional role retirement')
            if not run_id or event.get('run_id')!=run_id or body.get('run_id')!=run_id or body.get('run_label')!=run_id:raise ValueError('retirement run authority mismatch')
            if body.get('id')!=event['id'] or body.get('role')!='worker2' or body.get('prior_stop')!=prior:raise ValueError('retirement identity/stop mismatch')
            if not body.get('physical_stopped') or not body.get('absence_verified') or not body.get('supervisor_ack_without_role'):raise ValueError('retirement lacks physical removal proof')
            if not prior['at']<=body.get('supervisor_ack_at',0)<=body.get('removed_at',0)<=body.get('absence_verified_at',0)<=event['at']<=prior.get('stop_deadline_at',0):raise ValueError('retirement ordering/deadline mismatch')
            canonical=json.dumps(body,sort_keys=True,separators=(',',':')).encode()
            if hashlib.sha256(canonical).hexdigest()!=event.get('removal_receipt_sha256'):raise ValueError('retirement receipt changed')
        active[event['role']]=event
    for event in active.values():
        if event['state']=='stopped' and (event.get('stop_deadline_at',0)<at or event['stop_deadline_at']-event['at']>case_seconds+1):raise ValueError('planned stop window absent or expired')
    return active


def validate_resource_span(samples:list[dict],*,start:float,end:float,lifecycle:list[dict],
                           stops:list[dict],trace:list[dict],limits:dict,run_id:str|None=None)->dict:
    """Reject absent/stale/gapped evidence and rebind every process peak to full span."""
    if end<=start or not samples or not lifecycle or not trace:raise ValueError('missing resource span/lifecycle/process trace')
    if any(row.get('status')!='PASS' for row in stops):raise ValueError('resource stop tainted stand')
    rows=sorted(samples,key=lambda value:value['at'])
    relevant=[row for row in rows if start<=row['at']<=end+1.5]
    if not relevant or relevant[0]['at']>start+1.5 or relevant[-1]['at']<end-1.5:raise ValueError('resource span not covered')
    if any(right['at']-left['at']>3 for left,right in zip(relevant,relevant[1:])):raise ValueError('resource gap across files')
    events=sorted(lifecycle,key=lambda value:value['at'])
    seen={row['role'] for row in events if row['state']=='running' and row['at']<=end}
    if not {'postgres','rabbit','fixture','api','worker1'}<=seen:raise ValueError('required runtime role never observed')
    if any(not row.get('observed') or row['state'] not in ('running','stopped','retired') for row in events):raise ValueError('unobserved role window')
    processes={};peaks={}
    for row in trace:
        pid=row['pid']
        if row['kind']=='start':processes[pid]={'start':row['at'],'end':None,'role':row['role']}
        if row['kind']=='exit':
            if pid not in processes:raise ValueError('process exit without start')
            processes[pid]['end']=row['at']
        if 'peak_bytes' in row:peaks[pid]=max(peaks.get(pid,0),row['peak_bytes'])
    own_roles={row['role'] for row in processes.values() if row['start']<=end and (row['end'] is None or row['end']>=start)}
    if not {'actual','supervisor','forwarder'}<=own_roles:raise ValueError('host harness role missing')
    if any(pid not in peaks for pid,value in processes.items() if value['start']<=end and (value['end'] is None or value['end']>=start)):raise ValueError('host process peak missing')
    maximum=0
    for index,row in enumerate(relevant):
        if row.get('breaches'):raise ValueError('resource breach recorded')
        if not GLOBAL_METRICS<=row.get('measured',{}).keys():raise ValueError('required resource metrics missing')
        if any(not isinstance(value,(int,float)) or not math.isfinite(value) or value<0 for value in row['measured'].values()):raise ValueError('invalid numeric resource evidence')
        active=active_roles(events,row['at'],limits['case_seconds'],run_id)
        required={role:event for role,event in active.items() if event['state']=='running'}
        if not {'postgres','rabbit'}<=active.keys():raise ValueError('initial infrastructure not observed')
        for role,event in required.items():
            # Every running role must be bound to an actual1s monitor sample.
            key=role+'_bytes'
            if key not in row['measured'] or key not in row.get('cgroup_peaks',{}):raise ValueError('running role metrics missing')
            peak=row['cgroup_peaks'][key]
            if not isinstance(peak,(int,float)) or not math.isfinite(peak) or peak<row['measured'][key] or peak>limits[key]:raise ValueError('invalid or excessive container peak')
            if row.get('role_ids',{}).get(role)!=event['id']:raise ValueError('running role identity mismatch')
            at=row.get('source_sample_at',{}).get(role)
            if at is None or abs(row['at']-at/1e9)>3:raise ValueError('stale role counter')
        interval_start=relevant[index-1]['at'] if index else start
        host=sum(peaks[pid] for pid,value in processes.items() if value['start']<=row['at'] and (value['end'] is None or value['end']>=interval_start))
        container=sum(row.get('cgroup_peaks',{}).get(role+'_bytes',0) for role in required)
        aggregate=host+container
        if aggregate>limits['aggregate_bytes']:raise ValueError('full host/container peak aggregate breach')
        if any(peaks[pid]>limits['harness_max_process_bytes'] for pid,value in processes.items() if value['start']<=row['at'] and (value['end'] is None or value['end']>=interval_start)):raise ValueError('host process peak breach')
        for key,value in row['measured'].items():
            if key in limits and value>limits[key]:raise ValueError('numeric resource breach')
        maximum=max(maximum,aggregate)
    return {'samples':len(relevant),'start':start,'end':end,'full_peak_aggregate_bytes':maximum,'host_processes':len(processes),'full_span_verified':True}
