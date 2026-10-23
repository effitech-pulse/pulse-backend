#!/usr/bin/env python3
"""Opt-in A01–A11 process proof. Run only against own reviewed frozen binaries.

Uses the existing psycopg2 runtime; never installs dependencies. Raw responses stay
private in the task directory. An explicit coordinator gate and exclusive PG
window are required independently of test-infrastructure setup.
"""
from __future__ import annotations

import argparse
import base64
import contextlib
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import secrets
import selectors
import own_engine
import shutil
import subprocess
import processes
import threading
import time
from typing import Any, Callable
import urllib.error
import urllib.parse
import urllib.request

from build import digest, verify_freeze
from harness import LABEL, LIMITS, docker, inspect_owned, inventory, save, verify_baseline

TERMINAL={'succeeded','failed','cancelled','revoked','expired','quarantined'}


class Runtime:
    def __init__(self,directory:Path,binaries:Path,gate:Path):
        self.directory=directory
        processes.track_self(directory,'actual')
        self.binaries=binaries.resolve()
        self.state=json.loads((directory/'state.json').read_text())
        if self.state.get('profile','actual')!='actual':raise ValueError('MODULE lane cannot supply ACT evidence')
        authorization=json.loads(gate.read_text())
        if not authorization.get('actual_authorized') or authorization.get('run_id')!=self.state['run_id']:
            raise ValueError('explicit coordinator actual gate required')
        if authorization['binary_freeze_sha256']!=digest(binaries/'binary-freeze.json'):
            raise ValueError('binary freeze changed')
        freeze=json.loads((binaries/'binary-freeze.json').read_text())
        for name,metadata in freeze['binaries'].items():
            if digest(binaries/name)!=metadata['sha256']:raise ValueError('binary changed after freeze')
        self.manifest=json.loads((directory/'fixture/manifest.json').read_text())
        self.schema=self.manifest['test_schema']
        import psycopg2
        from psycopg2 import sql
        self.psycopg2=psycopg2;self.sql=sql
        self.connection=psycopg2.connect((directory/'test-database-url').read_text().strip(),connect_timeout=3)
        self.connection.autocommit=True
        with self.connection.cursor() as cursor:
            cursor.execute(sql.SQL('SET search_path TO {}').format(sql.Identifier(self.schema)))
            cursor.execute("SET application_name='q2c2_actual_observer'")
        self.session=(directory/'session-token').read_text().strip() if (directory/'session-token').exists() else ''
        self.evidence:dict[str,Any]={}
        self.serial=self.state.get('request_serial',0)
        self.opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.control_values:dict[str,Any]={'revision':'generation-a'}

    def query(self,statement:str,parameters:tuple=())->list[tuple]:
        with self.connection.cursor() as cursor:
            cursor.execute(statement,parameters)
            return cursor.fetchall() if cursor.description else []

    def update_state(self,changes:dict[str,Any])->None:
        state=json.loads((self.directory/'state.json').read_text());state.update(changes)
        temporary=self.directory/'.actual-state.tmp';temporary.write_text(json.dumps(state,indent=2)+'\n')
        temporary.replace(self.directory/'state.json');self.state=state

    def request(self,method:str,path:str,data:Any=None,*,fixture:bool=False,session:str|None=None,timeout:float=12)->tuple[int,Any]:
        url=('http://127.0.0.1:8093' if fixture else 'http://127.0.0.1:8094')+path
        body=None if data is None else json.dumps(data,separators=(',',':')).encode()
        headers={'Content-Type':'application/json','Origin':'http://127.0.0.1:8094'}
        if not fixture:headers['Cookie']='pulse_session='+(session if session is not None else self.session)
        elif path.startswith('/gate/'):headers['Cookie']='pulse_session='+self.session
        request=urllib.request.Request(url,body,headers,method=method)
        try:
            response=self.opener.open(request,timeout=timeout)
        except urllib.error.HTTPError as error:response=error
        with response:
            raw=response.read(65537)
            if len(raw)>65536:raise ValueError('unexpected response limit')
            parsed=json.loads(raw) if raw else None
            # Raw safe status still private until declared canary scan succeeds.
            name='http-'+str(time.time_ns())+'.json'
            save(self.directory/name,{'status':response.status,'body':parsed},private=True)
            return response.status,parsed

    def control(self,**changes:Any)->None:
        self.control_values.update(changes)
        code,_=self.request('POST','/control',self.control_values,fixture=True)
        if code!=204:raise AssertionError('fixture control rejected')

    def counters(self)->dict[str,Any]:
        code,value=self.request('GET','/counters',fixture=True)
        if code!=200:raise AssertionError('counter unavailable')
        return value

    def wait(self,predicate:Callable[[],Any],*,timeout:float=100)->Any:
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            try:value=predicate()
            except (OSError,urllib.error.URLError):value=None
            if value:return value
            time.sleep(.1)
        raise TimeoutError('bounded proof barrier not reached')

    def status(self,job:str)->dict[str,Any]:
        code,value=self.request('GET','/api/v1/collection-jobs/'+job)
        if code!=200:raise AssertionError('status HTTP'+str(code))
        return value

    def done(self,job:str,expected:str='succeeded',timeout:float=110)->dict[str,Any]:
        def terminal()->Any:
            row=self.query('SELECT state FROM collection_job WHERE id=%s',(job,))
            return row[0][0] if row and row[0][0] in TERMINAL else None
        state=self.wait(terminal,timeout=timeout)
        if state!=expected:raise AssertionError('terminal '+str(state)+' expected '+expected)
        return self.status(job)

    def input(self,label:str,*,minutes:int=2,day:int=20,source:str='source-a',fixture:str='dense-v1')->dict[str,Any]:
        self.serial+=1
        self.update_state({'request_serial':self.serial})
        start=dt.datetime(2026,9,day,tzinfo=dt.timezone.utc)+dt.timedelta(minutes=self.serial*3)
        if minutes==1440:start=dt.datetime(2026,10,19,tzinfo=dt.timezone.utc)
        return {'replay_key':label+'_'+secrets.token_hex(10),'fixture_id':fixture,'source_id':source,
                'start':start.isoformat().replace('+00:00','Z'),'end':(start+dt.timedelta(minutes=minutes)).isoformat().replace('+00:00','Z')}

    def submit(self,request:dict[str,Any])->str:
        code,status=self.request('POST','/api/v1/collection-jobs',request)
        if code not in (200,202):raise AssertionError('submit HTTP'+str(code))
        return status['id']

    def observe_role(self,role:str,identity:str,state:str)->None:
        record=inspect_owned(self.state,identity)
        running=record['State']['Running']
        if running!=(state=='running'):raise AssertionError('planned container state not physically observed')
        descriptor=os.open(self.directory/'container-lifecycle.jsonl',os.O_WRONLY|os.O_CREAT|os.O_APPEND,0o600)
        try:os.write(descriptor,(json.dumps({'at':time.time(),'role':role,'id':identity,'state':state,'observed':True,**({'stop_deadline_at':time.time()+LIMITS['case_seconds']} if state=='stopped' else {})})+'\n').encode())
        finally:os.close(descriptor)

    def signal(self,role:str,signal:str)->None:
        identity=self.state[role+'_id'];inspect_owned(self.state,identity)
        if signal in ('KILL','TERM'):
            expected=list(self.state.get('expected_stopped',[]))
            if identity not in expected:expected.append(identity)
            self.update_state({'expected_stopped':expected})
        docker('kill','--signal',signal,identity)
        if signal in ('KILL','TERM'):
            self.wait(lambda:not inspect_owned(self.state,identity)['State']['Running'],timeout=10)
            self.observe_role(role,identity,'stopped')

    def prearm_worker_stop(self,role:str)->str:
        identity=self.state[role+'_id'];record=inspect_owned(self.state,identity)
        if not record['State']['Running']:raise AssertionError('physical fault requires running owned worker')
        expected=list(self.state.get('expected_stopped',[]))
        if identity not in expected:expected.append(identity)
        self.update_state({'expected_stopped':expected})
        return identity

    def stop_prearmed_worker(self,role:str)->None:
        identity=self.state[role+'_id']
        if identity not in self.state.get('expected_stopped',[]):raise ValueError('worker stop not prearmed')
        if inspect_owned(self.state,identity)['State']['Running']:
            try:self.signal(role,'TERM')
            except RuntimeError:
                if inspect_owned(self.state,identity)['State']['Running']:raise
        record=inspect_owned(self.state,identity)
        if record['State']['Running'] or record['State'].get('OOMKilled') is not False:raise AssertionError('owned physical stop not observed')
        self.observe_role(role,identity,'stopped')

    def restart(self,role:str)->None:
        identity=self.state[role+'_id'];inspect_owned(self.state,identity)
        docker('start',identity)
        self.observe_role(role,identity,'running')
        self.update_state({'expected_stopped':[item for item in self.state.get('expected_stopped',[]) if item!=identity]})

    def snapshot(self)->dict[str,Any]:
        result={}
        tables={
            'values':('telemetry_measurement','well_id,measured_at,values,quality,source_id,collection_provenance','well_id,measured_at'),
            'capture':('collection_job_capture','job_id,raw_sha256,canonical_sha256,raw_rows,unique_rows,provenance','job_id'),
            'checkpoint':('collection_job_checkpoint','job_id,capture_sha256,inserted,identical','job_id'),
            'coverage':('collection_job_coverage','job_id,source_id,binding_sha256,data_revision,start_at,end_at,state,maximum_measured_at','job_id'),
            'source_controls':('telemetry_source','id,config,last_received,last_measured,error,next_poll,lease_until,writer_generation,writer_token_sha256,writer_profile_version','id'),
            'inbox':('collection_job_inbox','event_id,job_id,generation,fence,state,outcome','event_id'),
            'log':('telemetry_collection_log','id,source_id,accepted,duplicates,rejected,message','id'),
        }
        for label,(table,fields,order) in tables.items():
            # All identifiers are internal literal allowlist entries, never user input.
            count,sha=self.query('SELECT count(*),md5(coalesce(jsonb_agg(to_jsonb(v))::text,\'[]\')) FROM (SELECT '+fields+' FROM '+table+' ORDER BY '+order+') v')[0]
            result[label]={'count':count,'digest':sha}
        result['jobs']=[{'id':row[0],'state':row[1],'attempt':row[2],'generation':row[3],'fence':row[4]} for row in self.query('SELECT id,state,attempt,generation,fence FROM collection_job ORDER BY id')]
        return result

    def facts(self,**values:Any)->None:self.evidence.update(values)

    def run_case(self,name:str,operation:Callable[[],None])->dict[str,Any]:
        started=time.time()
        marker=self.directory/'actual-started.json'
        if marker.exists():clock=json.loads(marker.read_text())
        else:clock={'unix_seconds':started}
        clock['case_started_at']=started;clock['case']=name
        temporary=self.directory/'.actual-clock.tmp';temporary.write_text(json.dumps(clock));temporary.replace(marker)
        self.evidence={}
        before=self.snapshot()
        outcome='PASS';reason=None;operation_error=None;snapshot_error=None
        operation_frames=[];snapshot_frames=[];operation_http_status=None;snapshot_http_status=None
        def diagnostic(error:Exception)->tuple[list[dict[str,Any]],int|None]:
            frames=[];trace=error.__traceback__
            while trace is not None:
                frames.append({'file':Path(trace.tb_frame.f_code.co_filename).name,'function':trace.tb_frame.f_code.co_name,'line':trace.tb_lineno})
                if len(frames)>32:frames.pop(0)
                trace=trace.tb_next
            code=error.code if isinstance(error,urllib.error.HTTPError) else None
            return frames,code if type(code) is int and 100<=code<=599 else None
        try:operation()
        except Exception as error:
            outcome='UNKNOWN' if isinstance(error,(TimeoutError,OSError)) else 'FAIL'
            reason=operation_error=type(error).__name__
            operation_frames,operation_http_status=diagnostic(error)
        try:after=self.snapshot()
        except Exception as error:
            after=None;snapshot_error=type(error).__name__
            snapshot_frames,snapshot_http_status=diagnostic(error)
            if outcome=='PASS':outcome='UNKNOWN';reason=snapshot_error
        record={'case':name,'status':outcome,'reason':reason,'operation_error_type':operation_error,'snapshot_error_type':snapshot_error,'operation_error_frames':operation_frames,'snapshot_error_frames':snapshot_frames,'operation_http_status':operation_http_status,'snapshot_http_status':snapshot_http_status,'wall_seconds':time.time()-started,'before':before,'after':after,'evidence':self.evidence,'run_id':self.state['run_id']}
        save(self.directory/(name+'-'+str(time.time_ns())+'.json'),record)
        clock.pop('case_started_at',None)
        temporary=self.directory/'.actual-clock.tmp';temporary.write_text(json.dumps(clock));temporary.replace(marker)
        if outcome!='PASS':
            for identity in self.state['containers']:
                inspect_owned(self.state,identity)
            for identity in self.state['containers']:docker('stop','-t','2',identity)
            raise RuntimeError('actual case stopped own stand: '+name+' '+outcome)
        return record

    @contextlib.contextmanager
    def checkpoint_barrier(self):
        connection=self.psycopg2.connect((self.directory/'test-database-url').read_text().strip())
        connection.autocommit=True
        key=903002
        with connection.cursor() as cursor:cursor.execute('SELECT pg_advisory_lock(%s)',(key,))
        self.query('CREATE FUNCTION q2c2_apply_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(903002); RETURN NEW; END $$')
        self.query('CREATE TRIGGER q2c2_apply_barrier BEFORE INSERT ON collection_job_checkpoint FOR EACH ROW EXECUTE FUNCTION q2c2_apply_barrier()')
        try:yield
        finally:
            connection.close()
            self.query('DROP TRIGGER q2c2_apply_barrier ON collection_job_checkpoint')
            self.query('DROP FUNCTION q2c2_apply_barrier()')

    def blocked_pid(self,job:str)->int|None:
        rows=self.query("SELECT j.physical_backend_pid FROM collection_job j JOIN pg_stat_activity a ON a.pid=j.physical_backend_pid WHERE j.id=%s AND a.wait_event='advisory'",(job,))
        return rows[0][0] if rows else None

    def requested_rows(self,job:str)->dict[str,Any]:
        well,start,end=self.query("SELECT 'pulse:'||(intent->'binding'->>'well_id'),(intent->>'start')::timestamptz,(intent->>'end')::timestamptz FROM collection_job WHERE id=%s",(job,))[0]
        rows=self.query('SELECT well_id,measured_at,values,quality,source_id,collection_provenance FROM telemetry_measurement WHERE well_id=%s AND measured_at>=%s AND measured_at<%s ORDER BY measured_at',(well,start,end))
        return {row[0]+'|'+row[1].isoformat():{'values':row[2],'quality':row[3],'source_id':row[4],'provenance':row[5]} for row in rows}

    def no_effect(self,job:str,existing:dict[str,Any]|None=None)->None:
        checkpoints,coverage=self.query('SELECT (SELECT count(*) FROM collection_job_checkpoint WHERE job_id=%s),(SELECT count(*) FROM collection_job_coverage WHERE job_id=%s)',(job,job))[0]
        if checkpoints or coverage:raise AssertionError('unexpected coverage/checkpoint')
        if self.requested_rows(job)!=(existing or {}):raise AssertionError('whole requested PK set changed before recovery')

    def captured_applied(self,job:str)->None:
        raw,provenance,sha=self.query('SELECT canonical,provenance,canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(job,))[0]
        canonical=json.loads(bytes(raw))
        expected={}
        for point in canonical['points']:
            timestamp=dt.datetime.fromisoformat(point['timestamp'].replace('Z','+00:00')).isoformat()
            expected[point['well_id']+'|'+timestamp]={'values':point['values'],'quality':point.get('quality',''),'source_id':provenance['source_id'],'provenance':provenance}
        if self.requested_rows(job)!=expected:raise AssertionError('complete captured PK/value/provenance set not applied exactly')
        checkpoint=self.query('SELECT capture_sha256,inserted+identical FROM collection_job_checkpoint WHERE job_id=%s',(job,))[0]
        if checkpoint!=(sha,len(expected)):raise AssertionError('capture/checkpoint set mismatch')

    def case_a01(self)->None:
        self.signal('worker1','TERM')
        self.signal('rabbit','TERM')
        self.control(hold_api_response=True,drop_api_response=True)
        request=self.input('a01')
        prior=self.counters()['held_api_responses']
        errors=[]
        def client():
            try:self.request('POST','/gate/api/v1/collection-jobs',request,fixture=True)
            except Exception as error:errors.append(type(error).__name__)
        thread=threading.Thread(target=client);thread.start()
        self.wait(lambda:self.counters()['held_api_responses']>prior,timeout=8)
        job=self.query('SELECT id FROM collection_job ORDER BY accepted_at DESC LIMIT 1')[0][0]
        self.signal('api','KILL')
        self.control(hold_api_response=False)
        thread.join(timeout=12)
        if thread.is_alive() or not errors:raise AssertionError('client response not actually lost')
        self.restart('api');self.wait(lambda:self.request('GET','/api/v1/auth/me')[0]==200,timeout=10)
        if self.submit(request)!=job:raise AssertionError('accept replay mismatch')
        job_count,outbox_count=self.query('SELECT count(*),(SELECT count(*) FROM collection_job_outbox WHERE job_id=%s) FROM collection_job WHERE id=%s',(job,job))[0]
        if (job_count,outbox_count)!=(1,1):raise AssertionError('acceptance duplicated')
        self.restart('rabbit');self.start_worker('worker1')
        self.done(job);self.control(drop_api_response=False)
        self.facts(before_client_response=True,upstream_response_already_received_by_proxy=True,job=job,client_errors=errors,job_count=1,outbox_count=1,reconciled=True)

    def case_a02(self)->None:
        self.control(delay_ms=7000,revision='before-a')
        prior=self.counters()['source_gets'];request=self.input('a02_before',day=21);job=self.submit(request)
        intent=self.query('SELECT md5(intent::text) FROM collection_job WHERE id=%s',(job,))[0][0]
        self.wait(lambda:self.counters()['source_gets']>prior,timeout=10)
        self.signal('worker1','KILL');self.control(delay_ms=0,revision='before-b');self.restart('worker1')
        self.done(job)
        revision=self.query("SELECT provenance->>'data_revision' FROM collection_job_capture WHERE job_id=%s",(job,))[0][0]
        if revision!='before-b' or self.query('SELECT md5(intent::text) FROM collection_job WHERE id=%s',(job,))[0][0]!=intent:raise AssertionError('pre-capture semantics')
        self.control(revision='after-a')
        with self.checkpoint_barrier():
            job2=self.submit(self.input('a02_after',day=22));self.wait(lambda:self.blocked_pid(job2),timeout=10)
            capture=self.query('SELECT raw_sha256,canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(job2,))[0]
            self.signal('worker1','KILL')
        count=self.counters()['source_gets'];self.control(removed=True);self.restart('worker1');self.done(job2)
        if capture!=self.query('SELECT raw_sha256,canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(job2,))[0] or self.counters()['source_gets']!=count:raise AssertionError('captured input reread or changed')
        self.control(removed=False)
        self.facts(pre_capture_revision=revision,intent_unchanged=True,post_capture_hashes=list(capture),post_capture_source_gets_unchanged=True)

    def case_a03(self)->None:
        self.control(revision='max-a',duplicate=True,pad_bytes=2097152)
        job=self.submit(self.input('a03_max',minutes=1440));result=self.done(job)
        row=self.query('SELECT octet_length(raw),raw_rows,unique_rows FROM collection_job_capture WHERE job_id=%s',(job,))[0]
        if row!=(2097152,2880,1440):raise AssertionError('combined max control absent')
        values=self.query("SELECT values FROM telemetry_measurement WHERE well_id='pulse:1' AND measured_at='2026-10-19T00:00:00Z'::timestamptz")[0][0]
        if len(values)!=16 or any(name.startswith('set_') for name in values) or values['F_out']!=50 or values['Ia'] is not None or values['R_izol'] is not None or values['Ib']!=0:raise AssertionError('null/marker/zero/setpoint canonical control')
        self.control(pad_bytes=2097153)
        oversized=self.submit(self.input('a03_oversize',day=23));oversize_result=self.done(oversized,'failed');self.no_effect(oversized)
        if oversize_result['error_code']!='input_limit':raise AssertionError('overflow was not running input_limit')
        if self.query('SELECT count(*) FROM collection_job_capture WHERE job_id=%s',(oversized,))[0][0]:raise AssertionError('oversize captured')
        self.control(pad_bytes=0,extra_rows=1)
        raw=self.submit(self.input('a03_rows',minutes=1440));existing_max=self.requested_rows(raw);rows_result=self.done(raw,'failed');self.no_effect(raw,existing_max)
        if rows_result['error_code']!='input_limit':raise AssertionError('row overflow was not input_limit')
        self.control(duplicate=False,extra_rows=0,empty=True)
        empty=self.submit(self.input('a03_empty',day=23,fixture='empty-v1'));empty_result=self.done(empty)
        if empty_result['coverage']['state']!='complete_empty' or empty_result['write']['inserted']!=0:raise AssertionError('explicit empty coverage')
        self.control(empty=False)
        self.facts(explicit_empty_coverage=True,valid_max_before_overflow=True,combined_max=list(row),runtime_body_cap_plus_one_rejected=True,raw_row_2881_rejected=True,other_boundaries='frozen module unit/schema gates')

    def case_a04(self)->None:
        self.control(hold_ack=True,revision='ack-a')
        prior=self.counters()['held_acks'];request=self.input('a04_ack',day=24);job=self.submit(request)
        self.done(job);self.wait(lambda:self.counters()['held_acks']>prior,timeout=8)
        before=self.snapshot()
        event,updated=self.query('SELECT i.event_id,extract(epoch FROM i.updated_at) FROM collection_job_inbox i JOIN collection_job_outbox o ON o.event_id=i.event_id WHERE i.job_id=%s ORDER BY o.generation DESC LIMIT 1',(job,))[0]
        queue_before=self.queue_state('ready',unacked=1)
        quarantine_before=self.queue_state('quarantine')['messages']
        redeliver_before=queue_before.get('message_stats',{}).get('redeliver',0)
        source_before=self.counters()['source_gets']
        self.signal('worker1','KILL');self.control(hold_ack=False);self.restart('worker1')
        delivery=self.wait(lambda:(value if (value:=self.delivery_state(event,float(updated),quarantine_before))['proven'] and value['broker_redeliver']>redeliver_before else None),timeout=20)
        if self.counters()['source_gets']!=source_before:raise AssertionError('lost ACK redelivery fetched source')
        after=self.snapshot()
        for key in ('values','checkpoint','coverage','log'):
            if before[key]!=after[key]:raise AssertionError('lostACK duplicated effects')
        rollback_before=self.snapshot()['values']
        with self.checkpoint_barrier():
            rollback=self.submit(self.input('a04_rollback',day=25));self.wait(lambda:self.blocked_pid(rollback),timeout=10)
            self.signal('worker1','KILL')
        self.no_effect(rollback)
        if self.snapshot()['values']!=rollback_before:raise AssertionError('rollback leaked measurements outside requested PK set')
        capture=self.query('SELECT canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(rollback,))[0][0]
        self.restart('worker1');self.done(rollback);self.captured_applied(rollback)
        if capture!=self.query('SELECT capture_sha256 FROM collection_job_checkpoint WHERE job_id=%s',(rollback,))[0][0]:raise AssertionError('rollback changed capture')
        self.facts(same_event_redelivery=delivery,client_ack_held=True,broker_redelivery_after_worker_loss=True,effects_exactly_once=True,rollback_before_checkpoint=True,same_capture=capture)

    def case_a05(self)->None:
        self.control(delay_ms=7000,revision='lease-a');prior=self.counters()['source_gets']
        job=self.submit(self.input('a05',day=26));self.wait(lambda:self.counters()['source_gets']>prior,timeout=8)
        old=self.query('SELECT fence,physical_backend_pid FROM collection_job WHERE id=%s',(job,))[0]
        self.signal('worker1','STOP');paused=time.monotonic();self.control(delay_ms=0)
        self.start_worker('worker2');result=self.done(job,timeout=110)
        duration=time.monotonic()-paused
        if duration<30:raise AssertionError('lease did not physically expire')
        after=self.snapshot();new=self.query('SELECT fence,attempt FROM collection_job WHERE id=%s',(job,))[0]
        self.signal('worker1','CONT');time.sleep(2)
        current=self.snapshot()
        if any(current[key]!=after[key] for key in ('values','capture','checkpoint','coverage','log','source_controls')):raise AssertionError('late owner wrote')
        self.signal('worker2','TERM')
        self.remove_owned_container(self.state['worker2_id'],'worker2',optional_retirement=True)
        self.facts(sigstop_real_seconds=duration,old_fence=old[0],old_owned_pid=old[1],new_fence=new[0],new_attempt=new[1],old_late_effects=0)

    def case_a06(self)->None:
        self.control(revision='physical-a')
        physical_before=self.snapshot()['values']
        with self.checkpoint_barrier():
            job=self.submit(self.input('a06',day=27));pid=self.wait(lambda:self.blocked_pid(job),timeout=10)
            old_fence,old_session=self.query('SELECT fence,physical_backend_pid FROM collection_job WHERE id=%s',(job,))[0]
            captures=self.query('SELECT canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(job,))
            if old_session!=pid or len(captures)!=1:raise AssertionError('physical capture/claim identity changed')
            capture=captures[0][0];source_before=self.counters()['source_gets']
            identity=self.prearm_worker_stop('worker1')
            # Session loss may end the finite worker process. Declare the exact
            # combined process fault BEFORE terminating its owned Apply session.
            if not self.query('SELECT pg_terminate_backend(%s)',(pid,))[0][0]:raise AssertionError('session not terminated')
            self.stop_prearmed_worker('worker1')
            self.no_effect(job)
            if self.snapshot()['values']!=physical_before:raise AssertionError('partial SQL escaped rollback anywhere')
            if self.query('SELECT canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(job,))!=captures:raise AssertionError('durable capture changed before restart')
            self.restart('worker1')
            def new_physical_claim():
                candidate=self.blocked_pid(job)
                if candidate is None or candidate==pid:return None
                fence,session=self.query('SELECT fence,physical_backend_pid FROM collection_job WHERE id=%s',(job,))[0]
                return {'fence':fence,'pid':session} if fence>old_fence and session==candidate else None
            recovered=self.wait(new_physical_claim,timeout=75)
            if self.counters()['source_gets']!=source_before:raise AssertionError('physical recovery refetched source')
        result=self.done(job);self.captured_applied(job)
        if self.counters()['source_gets']!=source_before or self.query('SELECT capture_sha256 FROM collection_job_checkpoint WHERE job_id=%s',(job,))[0][0]!=capture:raise AssertionError('physical recovery changed captured input')
        self.facts(combined_pg_session_termination_and_worker_restart=True,owned_physical_pid=pid,terminated_inside_apply=True,
                   stopped_worker_id=identity,old_fence=old_fence,new_fence=recovered['fence'],new_physical_pid=recovered['pid'],
                   partial_rows=0,checkpoint_before_recovery=0,same_capture=capture,source_get_delta=0,new_attempt=result['attempt'])

    def start_worker(self,role:str)->None:
        if self.state.get(role+'_id'):
            self.restart(role);return
        self.launch(role,'/collection-worker',256)

    def launch(self,role:str,entry:str,memory:int,*,off:bool=False,old:bool=False)->str:
        image=self.state['old_image_id'] if old else self.state['runtime_image_id']
        env=self.directory/('api-off.env' if off else ('old-api.env' if old else 'application.env'))
        args=['run','-d','--pull=never','--name',self.state['project']+'-'+role,'--label',LABEL+'='+self.state['run_id'],
              '--network',self.state['networks'][0],'--network-alias',('api' if role=='api' else role),'--memory',str(memory)+'m','--memory-swap',str(memory)+'m',
              '--cpus','1','--pids-limit','128','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges',
              '--log-driver','local','--log-opt','max-size=4m','--log-opt','max-file=2','--env-file',str(env.resolve()),
              '--mount','type=bind,src='+str((self.directory/'fixture').resolve())+',dst=/own,readonly',
              '--entrypoint',entry,image]
        identity=docker(*args)
        state=json.loads((self.directory/'state.json').read_text());state['containers'].append(identity)
        self.update_state({'containers':state['containers'],role+'_id':identity})
        self.observe_role(role,identity,'running')
        return identity

    def second_session(self)->str:
        path=self.directory/'session-token-actor2'
        if path.exists():return path.read_text().strip()
        token=secrets.token_hex(32)
        self.query("INSERT INTO auth_session(token_hash,user_id,expires_at)VALUES(%s,2,clock_timestamp()+interval '8 hours')",(hashlib.sha256(token.encode()).hexdigest(),))
        save(path,token+'\n',private=True)
        return token

    def source(self,name:str='source-a')->dict[str,Any]:
        code,rows=self.request('GET','/api/v1/integrations')
        if code!=200:raise AssertionError('source read denied')
        return next(row for row in rows if row['id']==name)

    @contextlib.contextmanager
    def source_activation_guard(self,source_id:str):
        # Only the two static own fixture IDs can enter SQL; no input SQL quoting.
        source_literals={'source-a':"'source-a'",'source-b':"'source-b'"}
        if source_id not in source_literals:raise ValueError('unknown own fixture source')
        self.query("CREATE FUNCTION q2c2_source_activation_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.next_poll=clock_timestamp()+interval '1 day'; RETURN NEW; END $$")
        try:
            self.query("CREATE TRIGGER q2c2_source_activation_guard BEFORE UPDATE OF config ON telemetry_source FOR EACH ROW WHEN (NEW.id="+source_literals[source_id]+" AND NEW.config->>'status'='running') EXECUTE FUNCTION q2c2_source_activation_guard()")
            try:yield
            finally:self.query('DROP TRIGGER q2c2_source_activation_guard ON telemetry_source')
        finally:self.query('DROP FUNCTION q2c2_source_activation_guard()')

    def edit_source(self,value:dict[str,Any])->None:
        if value['id'] not in ('source-a','source-b'):raise ValueError('unknown own fixture source')
        intended=value.get('status','draft')
        if intended not in ('draft','running','paused'):raise ValueError('invalid intended source status')
        code,saved=self.request('PUT','/api/v1/integrations/'+value['id'],value)
        if code!=200 or not isinstance(saved,dict) or saved.get('status')!='draft':raise AssertionError('source mutation failed')
        if intended in ('running','paused'):
            # Production Save intentionally drafts the source. Use its separate
            # status API; the own test guard atomically retains the future poll.
            guard=self.source_activation_guard(value['id']) if intended=='running' else contextlib.nullcontext()
            with guard:
                code,active=self.request('PATCH','/api/v1/integrations/'+value['id']+'/status',{'status':intended})
                if code!=200 or not isinstance(active,dict) or active.get('status')!=intended:raise AssertionError('source status restoration failed')
        # Explicit own-stand operator seed; later A09 next_poll-only updates remain due.
        self.query("UPDATE telemetry_source SET next_poll=clock_timestamp()+interval '1 day' WHERE id=%s",(value['id'],))

    def case_a07(self)->None:
        actor2=self.second_session()
        outcomes={}
        for index,kind in enumerate(('account','source','binding')):
            self.control(delay_ms=7000,revision='authority-'+kind)
            prior=self.counters()['source_gets'];job=self.submit(self.input('a07_'+kind,day=28))
            self.wait(lambda:self.counters()['source_gets']>prior,timeout=40)
            source_seen_at=time.monotonic();self.prearm_worker_stop('worker1')
            restore:Callable[[],None]
            if kind=='account':
                code,users=self.request('GET','/api/v1/auth/users',session=actor2)
                user=next(row for row in users if row['id']==1)
                updated={key:user[key] for key in ('login','name','role','scope','active','revision')};updated['active']=False
                if self.request('PUT','/api/v1/auth/users/1',updated,session=actor2)[0]!=200:raise AssertionError('actor mutation failed')
                def restore_actor():
                    _,users=self.request('GET','/api/v1/auth/users',session=actor2)
                    current=next(row for row in users if row['id']==1)
                    payload={key:current[key] for key in ('login','name','role','scope','active','revision')};payload['active']=True
                    if self.request('PUT','/api/v1/auth/users/1',payload,session=actor2)[0]!=200:raise AssertionError('actor restore failed')
                restore=restore_actor
            elif kind=='source':
                source=self.source();changed=dict(source,name=source['name']+' changed');self.edit_source(changed)
                restore=lambda:self.edit_source(source)
            else:
                code,well=self.request('GET','/api/v1/wells/1')
                if code!=200:raise AssertionError('well unavailable')
                if self.request('PATCH','/api/v1/wells/1',{'revision':well['revision'],'unitek_id':-900002})[0]!=200:raise AssertionError('binding mutation failed')
                def restore_binding():
                    _,current=self.request('GET','/api/v1/wells/1')
                    if self.request('PATCH','/api/v1/wells/1',{'revision':current['revision'],'unitek_id':-900000})[0]!=200:raise AssertionError('binding restore failed')
                restore=restore_binding
            self.wait(lambda:self.query('SELECT state FROM collection_job WHERE id=%s',(job,))[0][0]=='revoked',timeout=15)
            # Fixture either completes its real7s delay or observes request cancellation.
            # Keep the old process alive through that window before deliberate stop.
            self.wait(lambda:time.monotonic()-source_seen_at>=7,timeout=8)
            late_io_seconds=time.monotonic()-source_seen_at
            self.no_effect(job)
            capture_count=self.query('SELECT count(*) FROM collection_job_capture WHERE job_id=%s',(job,))[0][0]
            if capture_count:raise AssertionError('late authority IO captured')
            self.stop_prearmed_worker('worker1')
            restore()
            if kind=='account':
                token=secrets.token_hex(32)
                self.query("INSERT INTO auth_session(token_hash,user_id,expires_at)VALUES(%s,1,clock_timestamp()+interval '8 hours')",(hashlib.sha256(token.encode()).hexdigest(),))
                current=self.directory/'session-token';current.rename(self.directory/('session-token-retired-'+str(time.time_ns())))
                save(current,token+'\n',private=True);self.session=token
            self.restart('worker1')
            outcomes[kind]={'state':'revoked','new_effects':0,'capture_count':capture_count,'late_io_window_seconds':late_io_seconds,'worker_physically_stopped_and_restarted':True}
            self.facts(authority=outcomes)
        self.control(delay_ms=0,revision='cancel-a')
        with self.checkpoint_barrier():
            job=self.submit(self.input('a07_cancel',day=29))
            self.wait(lambda:self.blocked_pid(job),timeout=40)
            captured=self.query('SELECT canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(job,))[0][0]
            revision=self.query('SELECT revision FROM collection_job WHERE id=%s',(job,))[0][0]
            cancelled=[]
            thread=threading.Thread(target=lambda:cancelled.append(self.request('POST','/api/v1/collection-jobs/'+job+'/cancel',{'revision':revision})))
            thread.start();self.signal('worker1','KILL');thread.join(timeout=10)
            if thread.is_alive() or not cancelled or cancelled[0][0]!=200:raise AssertionError('cancel race unresolved')
        final=self.done(job,'cancelled');self.no_effect(job)
        if self.query('SELECT canonical_sha256 FROM collection_job_capture WHERE job_id=%s',(job,))[0][0]!=captured:raise AssertionError('cancel discarded capture')
        self.restart('worker1')
        self.control(delay_ms=0)
        succeeded=self.submit(self.input('a07_after_success',day=29));status=self.done(succeeded)
        before=self.snapshot();code,terminal=self.request('POST','/api/v1/collection-jobs/'+succeeded+'/cancel',{'revision':status['revision']})
        if code!=200 or terminal['state']!='succeeded' or self.snapshot()['values']!=before['values']:raise AssertionError('cancel after success changed rows')
        self.facts(authority=outcomes,cancel_terminal=final['state'],cancel_after_success_preserved=True)

    def export_owned_log(self,identity:str,role:str|None=None)->None:
        record=inspect_owned(self.state,identity)
        role=role or record['Name'].rsplit('-',1)[-1]
        if record['Name']!='/'+self.state['project']+'-'+role:raise ValueError('owned log identity mismatch')
        folder=self.directory/'logs';folder.mkdir(exist_ok=True)
        path=folder/(identity+'-full-'+str(time.time_ns())+'.log')
        process=processes.Process(['docker','logs','--timestamps',identity],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,role='docker-log-export')
        selector=selectors.DefaultSelector();selector.register(process.stdout,selectors.EVENT_READ)
        os.set_blocking(process.stdout.fileno(),False);deadline=time.monotonic()+20;size=0
        try:
            with os.fdopen(os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'wb') as output:
                while True:
                    if time.monotonic()>deadline:raise TimeoutError('owned log export deadline')
                    if not selector.select(.25):continue
                    data=os.read(process.stdout.fileno(),32768)
                    if not data:break
                    size+=len(data)
                    if sum(file.stat().st_size for file in folder.glob('*.log'))+len(data)>self.state.get('limits',{}).get('logs_bytes',LIMITS['logs_bytes']):raise ValueError('owned log budget exceeded')
                    output.write(data)
            if process.wait(3)!=0:raise ValueError('complete owned log export unavailable')
        finally:
            selector.close()
            if process.poll() is None:process.terminate()
            try:process.wait(3)
            except subprocess.TimeoutExpired:process.kill();process.wait(3)
            process.stdout.close()
        manifest_path=folder/'owned-log-manifest.json'
        manifest=json.loads(manifest_path.read_text()) if manifest_path.exists() else {'version':1,'run_id':self.state['run_id'],'containers':{}}
        if manifest.get('run_id')!=self.state['run_id']:raise ValueError('owned log run mismatch')
        entry=manifest['containers'].setdefault(identity,{'role':role,'retired':False,'exports':[]})
        entry['exports'].append({'path':str(path.relative_to(self.directory)),'bytes':size,'sha256':digest(path),'complete':True})
        temporary=folder/'.owned-log-manifest.tmp';temporary.write_text(json.dumps(manifest,indent=2)+'\n');temporary.replace(manifest_path)

    def declared_owned_logs(self)->list[Path]:
        folder=self.directory/'logs';manifest_path=folder/'owned-log-manifest.json'
        if not manifest_path.exists():raise ValueError('complete owned log manifest missing')
        manifest=json.loads(manifest_path.read_text())
        expected=set(self.state['containers'])|set(self.state.get('retired_containers',[]))
        if manifest.get('run_id')!=self.state['run_id'] or set(manifest.get('containers',{}))!=expected:raise ValueError('complete active/retired log ID coverage missing')
        for identity,entry in manifest['containers'].items():
            if not entry.get('exports'):raise ValueError('complete owned log export missing')
            if bool(entry.get('retired'))!=(identity in self.state.get('retired_containers',[])):raise ValueError('owned log retirement mismatch')
            for export in entry['exports']:
                path=(self.directory/export['path']).resolve()
                if not path.is_relative_to(folder.resolve()) or not path.is_file() or not export.get('complete') or path.stat().st_size!=export['bytes'] or digest(path)!=export['sha256']:raise ValueError('complete owned log export changed')
        return sorted(folder.glob('*.log'))

    def remove_owned_container(self,identity:str,role:str,*,optional_retirement:bool=False)->None:
        if optional_retirement and role!='worker2':raise ValueError('only optional worker2 may retire')
        if self.state.get(role+'_id')!=identity:raise ValueError('owned removal role identity mismatch')
        record=inspect_owned(self.state,identity)
        if record['State']['Running']:raise ValueError('owned removal requires physical stop')
        observed=[json.loads(line) for line in (self.directory/'container-lifecycle.jsonl').read_text().splitlines()]
        stop_times=[row['at'] for row in observed if row.get('observed') and row['id']==identity and row['role']==role and row['state']=='stopped']
        if not stop_times:raise ValueError('owned removal stop evidence missing')
        stopped_at=max(stop_times)
        def acknowledged():
            path=self.directory/'supervisor-ready.json'
            if not path.exists():return False
            row=json.loads(path.read_text())
            return row if stopped_at<=row['at']<=time.time() and time.time()-row['at']<=1.5 and role not in row['role_ids'] and role not in row['source_sample_at'] else False
        acknowledgement=self.wait(acknowledged,timeout=5)
        events=[json.loads(line) for line in (self.directory/'container-lifecycle.jsonl').read_text().splitlines()]
        stops=[row for row in events if row.get('observed') and row['id']==identity and row['role']==role and row['state']=='stopped']
        if not stops:raise ValueError('owned removal stop evidence missing')
        prior=max(stops,key=lambda row:row['at'])
        if acknowledgement['at']<prior['at']:raise ValueError('owned removal acknowledgement precedes stop')
        self.export_owned_log(identity,role)
        record=inspect_owned(self.state,identity)
        if record['State']['Running']:raise ValueError('owned removal physical state changed')
        socket_path=own_engine.context_socket();docker('rm',identity);removed_at=time.time()
        self.wait(lambda:own_engine.absent(self.state,identity,socket_path),timeout=3)
        body={'id':identity,'role':role,'run_id':self.state['run_id'],'run_label':record['Config']['Labels'][LABEL],'prior_stop':prior,'physical_stopped':True,'supervisor_ack_without_role':True,'supervisor_ack_at':acknowledgement['at'],'removed_at':removed_at,'absence_verified':True,'absence_verified_at':time.time()}
        receipt=self.directory/('owned-removal-'+identity+'-'+str(time.time_ns())+'.json');save(receipt,body)
        manifest_path=self.directory/'logs/owned-log-manifest.json';manifest=json.loads(manifest_path.read_text());manifest['containers'][identity]['retired']=True
        temporary=manifest_path.with_name('.owned-log-manifest.tmp');temporary.write_text(json.dumps(manifest,indent=2)+'\n');temporary.replace(manifest_path)
        changes={'containers':[value for value in self.state['containers'] if value!=identity],'retired_containers':self.state.get('retired_containers',[])+[identity],role+'_id':None}
        self.update_state(changes)
        if optional_retirement:
            event={'at':time.time(),'role':role,'id':identity,'state':'retired','observed':True,'run_id':self.state['run_id'],'removal_receipt':body,'removal_receipt_sha256':hashlib.sha256(json.dumps(body,sort_keys=True,separators=(',',':')).encode()).hexdigest(),'receipt_path':receipt.name,'receipt_file_sha256':digest(receipt)}
            descriptor=os.open(self.directory/'container-lifecycle.jsonl',os.O_WRONLY|os.O_APPEND)
            try:os.write(descriptor,(json.dumps(event)+'\n').encode())
            finally:os.close(descriptor)

    def management(self,method:str,path:str,data:Any=None)->Any:
        credentials=dict(line.split('=',1) for line in (self.directory/'rabbit.env').read_text().splitlines() if '=' in line)
        auth=base64.b64encode((credentials['RABBITMQ_DEFAULT_USER']+':'+credentials['RABBITMQ_DEFAULT_PASS']).encode()).decode()
        headers={'Content-Type':'application/json','Authorization':'Basic '+auth}
        request=urllib.request.Request('http://127.0.0.1:15675'+path,None if data is None else json.dumps(data).encode(),headers,method=method)
        with self.opener.open(request,timeout=3) as response:
            raw=response.read(65537)
            if len(raw)>65536:raise ValueError('bounded management response exceeded')
            return json.loads(raw) if raw else None

    def queue_state(self,queue:str,*,unacked:int|None=None)->dict[str,Any]:
        from broker import valid_queue_gauges
        if queue not in ('ready','quarantine'):raise ValueError('unknown fixed test queue')
        def observed():
            value=self.management('GET','/api/queues/pulse-q2c2-synthetic/pulse.collection.v1.'+queue)
            return value if valid_queue_gauges(value) and (unacked is None or value['messages_unacknowledged']==unacked) else None
        return self.wait(observed,timeout=10)

    def redeliver(self,job:str)->str:
        event,generation=self.query('SELECT event_id,generation FROM collection_job_outbox WHERE job_id=%s ORDER BY generation DESC LIMIT 1',(job,))[0]
        message={'version':1,'kind':'collection.ready.v1','event_id':event,'job_id':job,'generation':generation}
        result=self.management('POST','/api/exchanges/pulse-q2c2-synthetic/pulse.collection.v1/publish',{'properties':{'delivery_mode':2,'message_id':event,'content_type':'application/json','type':'collection.ready.v1'},'routing_key':'ready','payload':json.dumps(message,separators=(',',':')),'payload_encoding':'string'})
        if not result.get('routed'):raise AssertionError('terminal redelivery not routed')
        return event

    def delivery_state(self,event:str,before:float,quarantine_before:int)->dict[str,Any]:
        from proof import terminal_delivery_proven
        rows=self.query('SELECT event_id,state,outcome,extract(epoch FROM updated_at) FROM collection_job_inbox WHERE event_id=%s',(event,))
        if not rows:return {'proven':False}
        queue=self.queue_state('ready')
        quarantine=self.queue_state('quarantine')
        row=rows[0]
        evidence={'event_id':event,'observed_event':row[0],'state':row[1],'outcome':row[2],'before_updated':before,'after_updated':float(row[3]),
                  'ready':queue['messages_ready'],'unacked':queue['messages_unacknowledged'],'quarantine_before':quarantine_before,'quarantine_after':quarantine['messages']}
        return {'proven':terminal_delivery_proven(**evidence),'evidence':evidence,'broker_redeliver':queue.get('message_stats',{}).get('redeliver',0)}

    def case_a08(self)->None:
        self.control(revision='retry-a',failures=2)
        job=self.submit(self.input('a08_retry',day=30));result=self.done(job)
        actions=self.query("SELECT kind,extract(epoch FROM created_at) FROM collection_job_action WHERE job_id=%s AND kind IN ('retry','claimed') ORDER BY id",(job,))
        attempts=self.query('SELECT attempt,generation FROM collection_job WHERE id=%s',(job,))[0]
        if attempts[0]!=3 or attempts[1]>8:raise AssertionError('attempt/generation budget')
        retries=[float(at) for kind,at in actions if kind=='retry'];claims=[float(at) for kind,at in actions if kind=='claimed']
        if len(retries)!=2 or len(claims)!=3 or claims[1]-retries[0]<4.9 or claims[2]-retries[1]<14.9:raise AssertionError('persisted retry schedule')
        prior=self.counters()['source_gets']
        event,updated=self.query('SELECT i.event_id,extract(epoch FROM i.updated_at) FROM collection_job_inbox i JOIN collection_job_outbox o ON o.event_id=i.event_id WHERE i.job_id=%s ORDER BY o.generation DESC LIMIT 1',(job,))[0]
        quarantine_before=self.queue_state('quarantine')['messages']
        self.signal('worker1','TERM')
        self.wait(lambda:self.queue_state('ready')['consumers']==0,timeout=10)
        if self.redeliver(job)!=event:raise AssertionError('terminal event identity changed')
        negative=self.delivery_state(event,float(updated),quarantine_before)
        if negative['proven']:raise AssertionError('no-consumer negative control falsely proved delivery')
        self.restart('worker1')
        delivery=self.wait(lambda:(value if (value:=self.delivery_state(event,float(updated),quarantine_before))['proven'] else None),timeout=20)
        if self.counters()['source_gets']!=prior:raise AssertionError('terminal delivery fetched')
        self.control(failures=3)
        exhausted=self.submit(self.input('a08_exhaust',day=30));self.done(exhausted,'failed')
        if self.query('SELECT attempt FROM collection_job WHERE id=%s',(exhausted,))[0][0]!=3:raise AssertionError('exhaustion cap')
        self.control(failures=0,hole=True)
        invalid=self.submit(self.input('a08_invalid',day=30,minutes=3));self.done(invalid,'failed')
        if self.query('SELECT attempt FROM collection_job WHERE id=%s',(invalid,))[0][0]!=1:raise AssertionError('permanent hotloop')
        self.control(hole=False,delay_ms=7000)
        count=self.counters()['source_gets'];lost=self.submit(self.input('a08_lost',day=30));self.wait(lambda:self.counters()['source_gets']>count,timeout=8)
        self.signal('worker1','KILL');self.signal('rabbit','TERM');self.control(delay_ms=0)
        self.restart('rabbit');self.restart('worker1');self.done(lost)
        self.facts(terminal_delivery=delivery,no_consumer_negative_control_rejected=True,attempts=attempts[0],generations=attempts[1],persisted_backoffs=[claims[1]-retries[0],claims[2]-retries[1]],terminal_delivery_fetch_delta=0,exhaustion_attempts=3,invalid_attempts=1,worker_broker_lost_delivery_recovered=True)

    def case_a09(self)->None:
        legacy=self.binaries/'darwin-legacy-probe'
        if not legacy.exists():raise ValueError('reviewed current-store legacy probe required')
        self.signal('worker1','TERM')
        probe=processes.Process([str(legacy),'--database-url-file',str(self.directory/'test-database-url'),'--schema',self.schema,'--source','source-a','--allow-isolated-mutation'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True)
        assert probe.stdout is not None and probe.stdin is not None
        claimed=json.loads(probe.stdout.readline());started=time.monotonic()
        expired=json.loads(probe.stdout.readline())
        duration=time.monotonic()-started
        if claimed['phase']!='claimed' or expired['phase']!='expired' or duration<59:raise AssertionError('legacy expiry was not physical')
        self.control(revision='legacy-a')
        request=self.input('a09_hold',day=30);job=self.submit(request)
        self.query("UPDATE telemetry_source SET next_poll=clock_timestamp()-interval '1 second' WHERE id='source-a'")
        before=self.query("SELECT md5(jsonb_build_array(writer_generation,writer_token_sha256,writer_profile_version,lease_until,last_received,last_measured,error)::text) FROM telemetry_source WHERE id='source-a'")[0][0]
        source_gets=self.counters()['source_gets']
        if self.request('POST','/api/v1/integrations/source-a/collect')[0]!=409:raise AssertionError('manual Collect crossed hold')
        ingest_code,_=self.request('POST','/api/v1/integrations/source-a/ingest',{'records':[{'well_id':'-900000','timestamp':request['start'],'f0':50}]})
        if ingest_code not in (400,409,422):raise AssertionError('Ingest mutated selected pull source')
        time.sleep(2)
        if self.counters()['source_gets']!=source_gets:raise AssertionError('Schedule fetched while held')
        probe.stdin.write('finish\n');probe.stdin.flush()
        verified=json.loads(probe.stdout.readline())
        if probe.wait(timeout=8)!=0 or verified['phase']!='verified':raise AssertionError('expired legacy write proof failed')
        after=self.query("SELECT md5(jsonb_build_array(writer_generation,writer_token_sha256,writer_profile_version,lease_until,last_received,last_measured,error)::text) FROM telemetry_source WHERE id='source-a'")[0][0]
        if before!=after:raise AssertionError('legacy controls changed')
        self.query("UPDATE telemetry_source SET next_poll=clock_timestamp()+interval '1 day' WHERE id='source-a'")
        self.restart('worker1');self.done(job)
        protected=self.query("SELECT measured_at,values FROM telemetry_measurement WHERE well_id='pulse:1' ORDER BY measured_at LIMIT 1")[0]
        timestamp=protected[0].isoformat().replace('+00:00','Z');values=protected[1]
        before=self.snapshot()
        code,_=self.request('POST','/api/v1/telemetry/import',{'points':[{'well_id':'1','timestamp':timestamp,'values':values}]})
        if code!=409:raise AssertionError('Import protected global key did not409')
        source=self.source('source-b');pushed=dict(source,kind='json_push',endpoint='')
        self.edit_source(pushed)
        fields={mapping['field']:values.get(mapping['parameter'],0) for mapping in self.manifest['fixtures'][0]['mappings']}
        fields.update(well_id='-900000',timestamp=timestamp)
        code,_=self.request('POST','/api/v1/integrations/source-b/ingest',{'records':[fields]})
        if code!=409:raise AssertionError('other-source protected collision did not409')
        self.edit_source(source)
        if self.snapshot()['values']!=before['values']:raise AssertionError('legacy collision changed rows')
        unattributed=self.input('a09_unattributed',minutes=1,day=30)
        incoming={'F_out':50,'Ia':None,'R_izol':None}
        for channel in self.manifest['fixtures'][0]['channels']:
            if not channel['is_set']:incoming.setdefault(channel['name'],0)
        code,_=self.request('POST','/api/v1/telemetry/import',{'points':[{'well_id':'1','timestamp':unattributed['start'],'values':incoming}]})
        if code!=200:raise AssertionError('unattributed legacy fixture not inserted')
        conflict=self.submit(unattributed);existing_legacy=self.requested_rows(conflict);self.done(conflict,'failed');self.no_effect(conflict,existing_legacy)
        error=self.query('SELECT error_code FROM collection_job WHERE id=%s',(conflict,))[0][0]
        if error!='unverifiable_existing':raise AssertionError('unattributed equality promoted')
        # Credential-bearing selected source rejects before capture; only a fake canary.
        canary=(self.directory/'canary').read_text().strip();source=self.source()
        credentialed=dict(source,secret=canary);self.edit_source(credentialed)
        captures=self.query('SELECT count(*) FROM collection_job_capture')[0][0]
        rejected,_=self.request('POST','/api/v1/collection-jobs',self.input('a09_credentials',minutes=1,day=30))
        if rejected not in (400,403,409,422):raise AssertionError('credentialed source admitted')
        self.edit_source(dict(source,secret=''))
        if captures!=self.query('SELECT count(*) FROM collection_job_capture')[0][0]:raise AssertionError('credentials captured')
        self.facts(real_legacy_expiry_seconds=duration,expired_probe=verified,collect_http409=True,schedule_fetch_delta=0,ingest_http=ingest_code,protected_import_http409=True,protected_other_source_http409=True,unattributed_conflict=error,credentialed_source_rejected_before_capture=True)

    def case_a10(self)->None:
        self.signal('worker1','TERM')
        pending=self.submit(self.input('a10_pending',minutes=1,day=30))
        self.query('UPDATE collection_job_guard SET accepting=false WHERE id=true')
        jobs=self.query("SELECT id,revision FROM collection_job WHERE state IN ('pending_dispatch','running','retry_wait')")
        for job,revision in jobs:
            code,_=self.request('POST','/api/v1/collection-jobs/'+job+'/cancel',{'revision':revision})
            if code not in (200,409):raise AssertionError('rollback cancel failed')
        for role in ('worker1','worker2'):
            if self.state.get(role+'_id'):
                record=inspect_owned(self.state,self.state[role+'_id'])
                if record['State']['Running']:self.signal(role,'TERM')
        self.signal('api','TERM')
        self.wait(lambda:self.query("SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE %s AND pid<>pg_backend_pid() AND (state<>'idle' OR xact_start IS NOT NULL)",('collection-runtime-%',))[0][0]==0,timeout=10)
        if self.query("SELECT count(*) FROM collection_job WHERE state IN ('pending_dispatch','running','retry_wait') OR lease_until IS NOT NULL")[0][0]:raise AssertionError('active lease/job at rollback')
        preserved=self.snapshot()
        # Remove only own API container to start another executable with the same
        # network alias. The ID remains in history; active ledger is updated.
        self.remove_owned_container(self.state['api_id'],'api')
        self.launch('api','/api',128,off=True)
        self.wait(lambda:self.request('GET','/api/v1/auth/me')[0]==200,timeout=10)
        for job,_,_,_,_ in [(row['id'],row['state'],row['attempt'],row['generation'],row['fence']) for row in preserved['jobs']]:
            if self.request('GET','/api/v1/collection-jobs/'+job)[0]!=200:raise AssertionError('modeoff read unavailable')
        if self.request('POST','/api/v1/collection-jobs',self.input('off_submit',day=30))[0]!=503:raise AssertionError('off admission enabled')
        if any(self.snapshot()[key]!=preserved[key] for key in ('values','capture','checkpoint','coverage')):raise AssertionError('off rollback changed durable data')
        self.signal('api','TERM')
        self.wait(lambda:self.query("SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE %s",('collection-runtime-%',))[0][0]==0,timeout=10)
        self.remove_owned_container(self.state['api_id'],'api')
        # All sources are disabled as an explicit operator downgrade action after
        # every new telemetry writer is drained. Old API additionally has PG read-only.
        self.query("UPDATE telemetry_source SET config=jsonb_set(config,'{status}','\"paused\"'::jsonb),next_poll=clock_timestamp()+interval '1 day'")
        self.launch('api','/api',128,old=True)
        self.wait(lambda:self.request('GET','/api/v1/auth/me')[0]==200,timeout=10)
        time.sleep(2)
        if any(self.snapshot()[key]!=preserved[key] for key in ('values','capture','checkpoint','coverage','log')):raise AssertionError('old read-only API changed accepted data')
        self.facts(accepting_disabled=True,all_nonterminal_cancelled=True,new_writers_drained=True,active_owned_transactions=0,modeoff_status_reads=True,new_submit_503=True,immutable_archive_old_base='7fc37f6e737c252b37d6deef644ff444ba6f89a1',old_sources_disabled=True,old_pg_readonly=True,durable_data_preserved=True)

    def finish(self)->None:
        from harness import LIMITS, teardown
        from scan import scan
        before=self.snapshot()
        counters=self.counters()
        if counters['ml_rpc_calls']!=0:raise AssertionError('forbidden ML method called')
        logs=self.directory/'logs';logs.mkdir(exist_ok=True)
        surfaces=[]
        for identity in self.state['containers']:self.export_owned_log(identity)
        surfaces.extend(self.declared_owned_logs())
        text={
            'job':self.query('SELECT id,state,error_code,availability FROM collection_job ORDER BY id'),
            'action':self.query('SELECT job_id,kind,reason_code FROM collection_job_action ORDER BY id'),
            'collection_log':self.query('SELECT source_id,message FROM telemetry_collection_log ORDER BY id'),
        }
        safe=self.directory/'safe-db-text.json';save(safe,json.dumps(text,default=str),private=True);surfaces.append(safe)
        queues=self.management('GET','/api/queues/pulse-q2c2-synthetic')
        queue_metrics=[]
        queue_bodies=[]
        for queue in queues:
            if queue['messages']>8 or queue.get('message_bytes',0)>8192:raise AssertionError('queue budget breached')
            queue_metrics.append({key:queue.get(key) for key in ('name','messages','messages_ready','messages_unacknowledged','message_bytes','arguments')})
            for message in self.management('POST','/api/queues/pulse-q2c2-synthetic/'+urllib.parse.quote(queue['name'],safe='')+'/get',{'count':8,'ackmode':'ack_requeue_true','encoding':'auto','truncate':1025}):
                payload=message.get('payload','')
                if len(payload.encode())>1024:raise AssertionError('queue body limit exceeded')
                value=json.loads(payload)
                if set(value)!={'version','kind','event_id','job_id','generation'}:raise AssertionError('queue body schema')
                queue_bodies.append(value)
        queue_surface=self.directory/'safe-queue-bodies.json';save(queue_surface,queue_bodies,private=True);surfaces.append(queue_surface)
        # Every exported status/error response is declared, rather than scanning an
        # arbitrary convenient subset. Credentials/manifest input are not exports.
        surfaces.extend(self.directory.glob('http-*.json'))
        report=scan((self.directory/'canary').read_bytes().strip(),surfaces)
        if report['status']!='PASS':raise AssertionError('canary leaked to declared surface')
        from proof import validate_resource_span
        sample_end=time.time()
        start=json.loads((self.directory/'actual-started.json').read_text())['unix_seconds']
        samples=[json.loads(line) for path in self.directory.glob('resource-samples-*.jsonl') for line in path.read_text().splitlines() if line]
        lifecycle=[json.loads(line) for line in (self.directory/'container-lifecycle.jsonl').read_text().splitlines()]
        stops=[json.loads(path.read_text()) for path in self.directory.glob('resource-stop-*.json')]
        for event in lifecycle:
            if event['state']=='retired':
                path=(self.directory/event['receipt_path']).resolve()
                if not path.is_relative_to(self.directory.resolve()) or not path.is_file() or digest(path)!=event['receipt_file_sha256'] or json.loads(path.read_text())!=event['removal_receipt']:raise ValueError('immutable retirement receipt missing or changed')
        processes.self_peak('actual')
        coverage=validate_resource_span(samples,start=start,end=sample_end,lifecycle=lifecycle,stops=stops,trace=list(processes.read_trace(self.directory/'process-trace.jsonl')),limits=LIMITS,run_id=self.state['run_id'])
        peaks={}
        for row in samples:
            for key,value in row.get('measured',{}).items():peaks[key]=max(peaks.get(key,0),value)
        samples_count=len(samples)
        baseline=json.loads(Path(self.state['baseline']).read_text())
        verify_baseline(baseline['working_containers'],inventory())
        record={'case':'A11','status':'PENDING_TEARDOWN','before':before,'ml_rpc_calls':0,'canary_scan':report,'queue_metrics':queue_metrics,'resource_peaks':peaks,'resource_samples':samples_count,'resource_coverage':coverage,'acceptance_limits':LIMITS}
        save(self.directory/('A11-pre-teardown-'+str(time.time_ns())+'.json'),record)
        self.connection.close()
        teardown(self.directory)
        # Persistent Docker children must be reaped before the FINAL proof so
        # transient/exit wait4 peaks cannot disappear between1s samples.
        def children_reaped():
            started=set();exited=set()
            for row in processes.read_trace(self.directory/'process-trace.jsonl'):
                if row['kind']=='start':started.add(row['pid'])
                if row['kind']=='exit':exited.add(row['pid'])
            return started-{os.getpid()}<=exited
        self.wait(children_reaped,timeout=10)
        processes.self_peak('actual')
        record['resource_coverage']=validate_resource_span(samples,start=start,end=sample_end,lifecycle=lifecycle,stops=stops,trace=list(processes.read_trace(self.directory/'process-trace.jsonl')),limits=LIMITS,run_id=self.state['run_id'])
        record['status']='PASS';record['exact_owned_teardown']=True;record['original_eight_ids_unchanged']=True
        save(self.directory/('A11-final-'+str(time.time_ns())+'.json'),record)

    def prepare(self)->None:
        for role in ('postgres','rabbit'):self.observe_role(role,self.state[role+'_id'],'running')
        if self.state.get('api_id') or self.state.get('fixture_id'):raise ValueError('refuse duplicate actual deployment')
        context=self.binaries/'image-context';context.mkdir()
        for source,target in [('linux-api','api'),('linux-collection-worker','collection-worker'),('linux-fixture','fixture')]:shutil.copy2(self.binaries/source,context/target)
        shutil.copy2(self.directory/'fixture/parameters.json',context/'parameters.json')
        shutil.copy2(Path(__file__).parent/'Dockerfile',context/'Dockerfile')
        image_tag=self.state['project']+':runtime'
        docker('build','--pull=false','--network=none','--label',LABEL+'='+self.state['run_id'],'-t',image_tag,str(context.resolve()))
        image=docker('image','inspect','--format','{{.Id}}',image_tag)
        self.update_state({'runtime_image_id':image,'owned_image_ids':self.state.get('owned_image_ids',[])+[image]})
        old_context=self.binaries/'old-image-context';old_context.mkdir()
        shutil.copy2(self.binaries/'linux-old-api',old_context/'api');shutil.copy2(self.binaries/'linux-fixture',old_context/'fixture')
        (old_context/'Dockerfile').write_text('FROM scratch\nCOPY api /api\nCOPY fixture /fixture\nUSER 65532:65532\nENTRYPOINT ["/api"]\n')
        old_tag=self.state['project']+':old';docker('build','--pull=false','--network=none','--label',LABEL+'='+self.state['run_id'],'-t',old_tag,str(old_context.resolve()))
        old_image=docker('image','inspect','--format','{{.Id}}',old_tag)
        self.update_state({'runtime_image_id':image,'old_image_id':old_image,'owned_image_ids':[image,old_image],'build_dir':str(self.binaries.resolve()),'image_bytes':sum(int(docker('image','inspect','--format','{{.Size}}',identity)) for identity in (image,old_image))})
        seed=self.binaries/'darwin-seed'
        completed=processes.run([str(seed),'--manifest',str(self.directory/'fixture/manifest.json'),'--database-url-file',str(self.directory/'test-database-url'),'--session-file',str(self.directory/'session-token'),'--allow-isolated-mutation'],capture_output=True,timeout=60)
        save(self.directory/('explicit-seed-'+str(time.time_ns())+'.json'),{'exit_code':completed.returncode,'stdout_sha256':hashlib.sha256(completed.stdout.encode()).hexdigest()})
        if completed.returncode:raise RuntimeError('explicit own schema seed failed')
        self.session=(self.directory/'session-token').read_text().strip()
        url=(self.directory/'test-database-url').read_text().strip().replace('@127.0.0.1:55442','@postgres:5432')+'&search_path='+self.schema+'&application_name=collection-runtime-process'
        canary='q2c2_canary_'+secrets.token_hex(24);save(self.directory/'canary',canary+'\n',private=True)
        encryption_key=base64.b64encode(secrets.token_bytes(32)).decode()
        fields={'DATABASE_URL':url,'AUTH_MODE':'local','AUTH_COOKIE_SECURE':'false','APP_SCOPE':'research','HTTP_ADDR':'0.0.0.0:8000',
                'ML_AUTORUN_ENABLED':'false','ML_SERVICE_TARGET':'fixture:50051','ML_SERVICE_API_KEY':canary,'DB_POOL_SIZE':'5','TZ':'UTC','GIN_MODE':'release',
                'PARAM_DICT_PATH':'/own/parameters.json','CORS_ORIGINS':'http://127.0.0.1:8094','INTEGRATION_ALLOWED_HOSTS':'fixture',
                'INTEGRATION_SECRET_KEY':encryption_key,'COLLECTION_JOB_MODE':'synthetic','COLLECTION_JOB_MANIFEST':'/own/manifest.json',
                'COLLECTION_JOB_MANIFEST_SHA256':digest(self.directory/'fixture/manifest.json'),'COLLECTION_JOB_RUN_ID':self.state['run_id'],
                'COLLECTION_JOB_TEST_SCHEMA':self.schema,'COLLECTION_JOB_AMQP_URL':(self.directory/'collection_worker_internal_amqp_url').read_text().strip().replace('@rabbit:5672','@fixture:5673'),
                'COLLECTION_JOB_PRODUCER_AMQP_URL':(self.directory/'collection_producer_internal_amqp_url').read_text().strip(),
                'FILE_CHECK_MODE':'off','COHORT_JOB_MODE':'off'}
        save(self.directory/'application.env',''.join(key+'='+value+'\n' for key,value in fields.items()),private=True)
        off=dict(fields)
        for key in list(off):
            if key.startswith('COLLECTION_JOB_'):off[key]='off' if key=='COLLECTION_JOB_MODE' else ''
        save(self.directory/'api-off.env',''.join(key+'='+value+'\n' for key,value in off.items()),private=True)
        old=dict(off);old['DATABASE_URL']+='&default_transaction_read_only=on'
        save(self.directory/'old-api.env',''.join(key+'='+value+'\n' for key,value in old.items()),private=True)
        self.launch('fixture','/fixture',64);self.wait(lambda:self.counters(),timeout=10)
        self.launch('api','/api',128);self.wait(lambda:self.request('GET','/api/v1/auth/me')[0]==200,timeout=10)
        self.start_worker('worker1')
        startup=self.submit(self.input('startup_control',minutes=1,day=19));self.done(startup)
        self.queue_state('ready');self.queue_state('quarantine')
        if self.counters()['ml_rpc_calls']!=0:raise AssertionError('forbidden ML RPC during valid control')
        temporary=self.directory/'tmp';temporary.mkdir(exist_ok=True)
        invalid=processes.run([str(self.binaries/'darwin-api')],env=dict(fields,COLLECTION_JOB_MODE='real'),cwd=temporary,capture_output=True,timeout=10)
        if invalid.returncode==0:raise AssertionError('invalid config did not reject')
        save(self.directory/'invalid-config-control.json',{'status':'REJECTED','native_binary_sha256':digest(self.binaries/'darwin-api'),'exit_code':invalid.returncode,'before_pg_or_listener':True})
        def monitors_ready():
            path=self.directory/'supervisor-ready.json'
            if not path.exists():return False
            if any(self.directory.glob('resource-stop-*.json')):raise AssertionError('setup sensor failure taints stand')
            ready=json.loads(path.read_text());now=time.time()
            required=('postgres','rabbit','fixture','api','worker1')
            return ready if now-ready['at']<=1.5 and all(ready['role_ids'].get(role)==self.state[role+'_id'] and abs(now-ready['source_sample_at'].get(role,0)/1e9)<=3 for role in required) else False
        ready=self.wait(monitors_ready,timeout=10)
        save(self.directory/'actual-started.json',{'unix_seconds':time.time(),'setup_readiness_at':ready['at'],'setup_is_outside_actual_span':True})
        save(self.directory/'valid-startup-control.json',{'status':'PASS','job':startup,'manifest_sha256':fields['COLLECTION_JOB_MANIFEST_SHA256'],'image':image,'schema':self.schema,'ml_rpc_calls':0})


def main()->None:
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation',choices=['prepare','case','finish'])
    parser.add_argument('--state-dir',type=Path,required=True)
    parser.add_argument('--binaries',type=Path,required=True)
    parser.add_argument('--coordinator-gate',type=Path,required=True)
    parser.add_argument('--case',choices=['A01','A02','A03','A04','A05','A06','A07','A08','A09','A10'])
    parser.add_argument('--allow-isolated-mutation',action='store_true',required=True)
    options=parser.parse_args()
    runtime=Runtime(options.state_dir,options.binaries,options.coordinator_gate)
    if options.operation=='prepare':runtime.prepare()
    elif options.operation=='finish':runtime.finish()
    else:
        if not (options.state_dir/'valid-startup-control.json').exists():raise ValueError('same frozen startup control required first')
        runtime.run_case(options.case,getattr(runtime,'case_'+options.case.lower()))


if __name__=='__main__':main()
