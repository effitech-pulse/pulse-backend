"""Own-only Engine monitoring regression controls; no Docker/socket/PG used."""
from __future__ import annotations
import io
import json
import struct
import threading
import unittest
from unittest.mock import patch
import own_engine

PG='a'*64
WORKER='b'*64
EXEC='c'*64
STATE={'run_id':'run','project':'own','containers':[PG,WORKER],'postgres_id':PG,'worker2_id':WORKER}

class Response(io.BytesIO):
    def __init__(self,data:bytes,status:int):super().__init__(data);self.status=status

class FakeConnection:
    requests=[]
    role='postgres'
    label='run'
    def __init__(self,*args,**kwargs):self.sock=None;self.current=None
    def request(self,method,path,body=None,headers=None):
        self.requests.append((method,path,body))
        if method=='GET':self.current=Response(json.dumps([{'Id':PG,'Labels':{own_engine.LABEL:self.label},'Names':['/own-postgres'],'State':'running'}]).encode(),200)
        elif path.endswith('/exec'):
            request=json.loads(body);self.role=request['Cmd'][-1];self.current=Response(json.dumps({'Id':EXEC}).encode(),201)
        elif path.endswith('/start'):
            body=json.dumps({'role':self.role}).encode()+b'\n';self.current=Response(struct.pack('!BBBBI',1,0,0,0,len(body))+body,200)
        else:raise AssertionError('arbitrary endpoint')
    def getresponse(self):return self.current
    def close(self):pass

class EngineMonitoringTests(unittest.TestCase):
    def setUp(self):FakeConnection.requests=[];FakeConnection.label='run'

    def test_slow_role_attachment_cannot_block_existing_monitor_or_caller(self):
        entered=threading.Event();release=threading.Event()
        def inspect(state,identity,path,**kwargs):
            if identity==WORKER:entered.set();release.wait(2)
            return {'Name':'/own-'+('worker2' if identity==WORKER else 'postgres'),'State':{'Running':True}}
        with patch.object(own_engine,'Connection',FakeConnection),patch.object(own_engine,'inspect',side_effect=inspect):
            pg=own_engine.Monitor(STATE,PG,'postgres','/own/socket')
            worker=own_engine.Monitor(STATE,WORKER,'worker2','/own/socket')
            try:
                self.assertTrue(entered.wait(1))
                self.assertTrue(worker.thread.is_alive())
                self.assertEqual(json.loads(pg.stdout.readline())['role'],'postgres')
                self.assertIsNone(worker.poll())
                release.set()
                self.assertEqual(json.loads(worker.stdout.readline())['role'],'worker2')
                self.assertEqual(pg.wait(1),0);self.assertEqual(worker.wait(1),0)
            finally:
                release.set();pg.terminate();worker.terminate();pg.wait(1);worker.wait(1);pg.stdout.close();worker.stdout.close()
        for method,path,body in FakeConnection.requests:
            self.assertEqual(method,'POST')
            value=json.loads(body)
            if path.endswith('/exec'):
                self.assertEqual(set(value),{'AttachStdout','AttachStderr','AttachStdin','Tty','Privileged','Cmd'})
                self.assertEqual(value['Cmd'][:2],['/fixture','monitor']);self.assertFalse(value['AttachStdin']);self.assertFalse(value['Privileged'])

    def test_unowned_ids_roles_or_labels_never_create_exec(self):
        with patch.object(own_engine,'Connection',FakeConnection):
            for identity,role in [('d'*64,'postgres'),('short','postgres'),(PG,'arbitrary')]:
                with self.assertRaises(ValueError):own_engine.Monitor(STATE,identity,role,'/own/socket')
            FakeConnection.label='other'
            with self.assertRaises(ValueError):own_engine.inspect(STATE,PG,'/own/socket')
        self.assertTrue(all(method=='GET' for method,_,_ in FakeConnection.requests))

    def test_monitor_transport_is_bounded_and_rejects_stderr_or_bad_frames(self):
        header=lambda kind,size:struct.pack('!BBBBI',kind,0,0,0,size)
        for raw in (b'x',header(1,32769),header(2,1)+b'x',header(1,2)+b'x',header(1,0)):
            with self.subTest(raw=raw[:8]),self.assertRaises(ValueError):own_engine._frames(io.BytesIO(raw),lambda value:None)
        output=[];own_engine._frames(io.BytesIO(header(1,3)+b'one'+header(1,3)+b'two'),output.append)
        self.assertEqual(output,[b'one',b'two'])

    def test_exact_snapshot_rejects_missing_multi_mismatched_or_unknown_state(self):
        good={'Id':PG,'Labels':{own_engine.LABEL:'run'},'Names':['/own-postgres'],'State':'running'}
        for payload in ([],[good,good],[dict(good,Id=WORKER)],[dict(good,State='mystery')],[dict(good,Names=[])],[dict(good,Names=['/wrong-postgres'])],[dict(good,Labels={own_engine.LABEL:'foreign'})]):
            class Bad(FakeConnection):
                def request(self,*args,**kwargs):
                    super().request(*args,**kwargs);self.current=Response(json.dumps(payload).encode(),200)
            with self.subTest(payload=payload),patch.object(own_engine,'Connection',Bad),self.assertRaises(ValueError):own_engine.inspect(STATE,PG,'/own/socket')
        for state,expected in [('running',True),('paused',True),('exited',False),('dead',False),('created',False)]:
            class Valid(FakeConnection):
                def request(self,*args,**kwargs):
                    super().request(*args,**kwargs);self.current=Response(json.dumps([dict(good,State=state)]).encode(),200)
            with patch.object(own_engine,'Connection',Valid):self.assertEqual(own_engine.inspect(STATE,PG,'/own/socket')['State']['Running'],expected)

    def test_remote_context_is_rejected(self):
        with patch.object(own_engine,'command',return_value='tcp://remote:2375'):
            with self.assertRaises(ValueError):own_engine.context_socket()


class SupervisorHotLoopTests(unittest.TestCase):
    def run_stop_transition(self,physical_states,*,steps=3,declare_at_eof=False,declared=True,rapid_restart=False,historical_stop=False,restart_step=2,unobserved_restart=False,restart_during_measurement=False,running_during_measurement=False,running_after_cutoff=False,withhold_restart_counter=False,running_during_select=False,running_identity=None,precise_deadline_clock=False,measurement_delay=0.,publication_delay=0.):
        import os,tempfile
        from pathlib import Path
        from types import SimpleNamespace
        import supervisor
        observations=iter(physical_states);clock=[100.];pipes=[];rabbit_count=[0];failure_times=[];attachment_times=[];delayed_measurement=[False];delayed_publication=[False];state=dict(STATE,project='own',postgres_id=PG,rabbit_id=WORKER,expected_stopped=[],limits=supervisor.LIMITS);state.pop('worker2_id')
        with tempfile.TemporaryDirectory() as temporary:
            directory=Path(temporary);state_file=directory/'state.json';state_file.write_text(json.dumps(state))
            class Process:
                def __init__(self,*args,**kwargs):
                    read,write=os.pipe();self.stdout=os.fdopen(read,'rb',buffering=0);self.stdin=None;self.write=write;pipes.append(self)
                    self.role=args[2] if len(args)>2 and isinstance(args[2],str) and args[2] in own_engine.ROLES else None
                    self.generation=0
                    if self.role=='rabbit':rabbit_count[0]+=1;self.generation=rabbit_count[0]
                    if self.role is not None:attachment_times.append({'role':self.role,'generation':self.generation,'at':clock[0]})
                    if self.role is None:os.close(write);self.write=None
                def poll(self):return 0
                def wait(self,timeout=None):return 0
                def terminate(self):pass
                def kill(self):pass
            class Selector:
                def __init__(self):self.keys=[];self.step=0
                def register(self,fileobj,events,data):self.keys.append(SimpleNamespace(fileobj=fileobj,data=data))
                def unregister(self,fileobj):self.keys=[key for key in self.keys if key.fileobj is not fileobj]
                def select(self,timeout):
                    self.step+=1;clock[0]+=min(1,max(0,timeout)) if precise_deadline_clock else 1
                    for process in pipes:
                        if process.write is None:continue
                        if process.role=='rabbit' and process.generation==1 and self.step>1:continue
                        if withhold_restart_counter and process.role=='rabbit' and process.generation>1:continue
                        row={'at':int(clock[0]*1e9),'role':process.role,'current':1,'peak':17 if process.role=='rabbit' and process.generation>1 else 1,'database':1,'pgdata':1,'wal':1,'wal_lsn':100,'temp':0,'disk':1}
                        os.write(process.write,json.dumps(row).encode()+b'\n')
                        if process.role=='rabbit' and process.generation==1:os.close(process.write);process.write=None
                    if running_during_select and self.step==1:
                        clock[0]+=.1
                        (directory/'container-lifecycle.jsonl').write_text(json.dumps({'at':clock[0],'role':'postgres','id':PG,'state':'running','observed':True})+'\n')
                    if declared and self.step==(2 if declare_at_eof else 1):
                        state['expected_stopped']=[WORKER];state_file.write_text(json.dumps(state))
                    if rapid_restart and self.step==restart_step:
                        state['expected_stopped']=[];state_file.write_text(json.dumps(state))
                        at=99. if historical_stop else 99.5+restart_step
                        events=[{'at':at,'role':'rabbit','id':WORKER,'state':'stopped','observed':not unobserved_restart,'stop_deadline_at':at+300}, {'at':at+.2,'role':'rabbit','id':WORKER,'state':'running','observed':not unobserved_restart}]
                        (directory/'container-lifecycle.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in events))
                    if self.step==steps:(directory/'supervisor-stop-request').touch()
                    withheld={process.stdout for process in pipes if withhold_restart_counter and process.role=='rabbit' and process.generation>1}
                    return [(key,1) for key in list(self.keys) if key.fileobj not in withheld]
                def close(self):pass
            restarted_during_measurement=[False]
            def append_running(role,identity):
                clock[0]+=.1
                with (directory/'container-lifecycle.jsonl').open('a') as output:output.write(json.dumps({'at':clock[0],'role':role,'id':identity,'state':'running','observed':True})+'\n')
            def host_usage(*args):
                if measurement_delay and clock[0]>=102 and not delayed_measurement[0]:
                    clock[0]+=measurement_delay;delayed_measurement[0]=True
                if running_during_measurement and not restarted_during_measurement[0]:
                    append_running('postgres',running_identity or PG);restarted_during_measurement[0]=True
                if restart_during_measurement and not restarted_during_measurement[0]:
                    event_path=directory/'container-lifecycle.jsonl'
                    events=[json.loads(line) for line in event_path.read_text().splitlines()] if event_path.exists() else []
                    if events and events[-1]['state']=='stopped':
                        clock[0]+=.1
                        state['expected_stopped']=[];state_file.write_text(json.dumps(state))
                        with event_path.open('a') as output:output.write(json.dumps({'at':clock[0],'role':'rabbit','id':WORKER,'state':'running','observed':True})+'\n')
                        restarted_during_measurement[0]=True
                return (0,0,{})
            import proof
            actual_active_roles=proof.active_roles
            def active_roles_at_cutoff(*args,**kwargs):
                value=actual_active_roles(*args,**kwargs)
                if running_after_cutoff and not restarted_during_measurement[0]:
                    append_running('postgres',running_identity or PG);restarted_during_measurement[0]=True
                return value
            real_dumps=json.dumps
            def publication_dumps(value,*args,**kwargs):
                if publication_delay and isinstance(value,dict) and 'measured' in value and clock[0]>=102 and not delayed_publication[0]:
                    clock[0]+=publication_delay;delayed_publication[0]=True
                return real_dumps(value,*args,**kwargs)
            try:
                with patch.object(supervisor.sys if hasattr(supervisor,'sys') else __import__('sys'),'argv',['supervisor.py','--state-dir',str(directory),'--allow-isolated-mutation']), \
                     patch.object(supervisor.time,'time',side_effect=lambda:clock[0]),patch.object(supervisor.time,'monotonic',side_effect=lambda:clock[0]), \
                     patch.object(supervisor.own_engine,'context_socket',return_value='/own/socket'),patch.object(supervisor.own_engine,'Monitor',Process), \
                     patch.object(supervisor.own_engine,'inspect',side_effect=lambda *args,**kwargs:{'State':{'Running':next(observations,physical_states[-1])}}) as engine_inspect, \
                     patch.object(supervisor,'inspect_owned',side_effect=AssertionError('blocking CLI inspect in hot loop')), \
                     patch.object(supervisor.selectors,'DefaultSelector',Selector),patch.object(supervisor.processes,'Process',Process), \
                     patch.object(supervisor.processes,'track_self'),patch.object(supervisor.processes,'self_peak'),patch.object(supervisor.processes,'host_usage',side_effect=host_usage), \
                     patch.object(supervisor,'tree_bytes',return_value=1),patch.object(supervisor.json,'dumps',side_effect=publication_dumps),patch.object(proof,'active_roles',side_effect=active_roles_at_cutoff),patch.object(supervisor,'stop_owned',side_effect=lambda *_:failure_times.append(clock[0])) as stopped:
                    supervisor.main()
                samples=[json.loads(line) for path in directory.glob('resource-samples-*.jsonl') for line in path.read_text().splitlines()]
                event_path=directory/'container-lifecycle.jsonl'
                events=[json.loads(line) for line in event_path.read_text().splitlines()] if event_path.exists() else []
                stops=[json.loads(path.read_text()) for path in directory.glob('resource-stop-*.json')]
                self.failure_times=failure_times;self.attachment_times=attachment_times
                return samples,events,stops,engine_inspect.call_count,stopped.call_count
            finally:
                for process in pipes:
                    if process.write is not None:os.close(process.write)
                    process.stdout.close()

    def assert_live_pg_samples(self,samples):
        self.assertGreaterEqual(len(samples),2)
        self.assertEqual(samples[-1]['measured']['postgres_bytes'],1)
        self.assertTrue(all(right['at']-left['at']<=1 for left,right in zip(samples,samples[1:])))

    def test_planned_stop_uses_bounded_engine_and_keeps_live_samples(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False])
        self.assertEqual((stops,polls,halt),([],1,0));self.assert_live_pg_samples(samples)
        self.assertEqual(events[0]['state'],'stopped');self.assertTrue(events[0]['observed'])

    def test_running_to_stopped_transition_keeps_original_counter_times(self):
        samples,events,stops,polls,halt=self.run_stop_transition([True,False])
        self.assertEqual((stops,polls,halt),([],2,0));self.assert_live_pg_samples(samples)
        pending=[row for row in samples if row['at']==102.][0]
        self.assertEqual(pending['source_sample_at']['rabbit'],101.*1e9)
        self.assertEqual(events[0]['at'],103.)

    def test_expected_stop_written_during_select_is_read_before_eof_classification(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],declare_at_eof=True)
        self.assertEqual((stops,polls,halt),([],1,0));self.assert_live_pg_samples(samples)

    def test_never_stopped_or_unexpected_eof_fails_closed(self):
        for kwargs in ({'physical_states':[True]*8,'steps':7},{'physical_states':[False],'declared':False}):
            samples,events,stops,polls,halt=self.run_stop_transition(**kwargs)
            self.assertEqual(halt,1);self.assertEqual(stops[0]['status'],'UNKNOWN');self.assertEqual(events,[])
            self.assertLessEqual(samples[-1]['at'] if samples else 100.,104.)

    def test_observed_rapid_restart_rearms_only_after_this_attachment(self):
        samples,events,stops,polls,halt=self.run_stop_transition([True],steps=4,rapid_restart=True)
        self.assertEqual((stops,halt),([],0));self.assertGreaterEqual(len(samples),2)
        self.assertTrue(all(right['at']-left['at']<=3 for left,right in zip(samples,samples[1:])))
        self.assertEqual(samples[-1]['source_sample_at']['rabbit'],104.*1e9)
        self.assertEqual([row['state'] for row in events],['stopped','running'])

    def test_historical_stop_cannot_excuse_unexpected_eof(self):
        samples,events,stops,polls,halt=self.run_stop_transition([True],steps=4,rapid_restart=True,historical_stop=True)
        self.assertEqual(halt,1);self.assertEqual(stops[0]['safe_code'],'active monitor lost')

    def test_pending_stop_then_observed_restart_rearms_with_fresh_counters(self):
        samples,events,stops,polls,halt=self.run_stop_transition([True],steps=5,rapid_restart=True,restart_step=3)
        self.assertEqual((stops,halt),([],0));self.assertGreaterEqual(len(samples),3)
        self.assertTrue(all(right['at']-left['at']<=3 for left,right in zip(samples,samples[1:])))
        self.assertEqual(samples[-1]['source_sample_at']['rabbit'],105.*1e9)

    def test_observed_restart_during_measurement_never_publishes_missing_running_role(self):
        from proof import active_roles
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,restart_during_measurement=True)
        self.assertEqual((stops,halt),([],0))
        self.assertTrue(any(event['state']=='running' for event in events))
        self.assertGreaterEqual(len(samples),2)
        for sample in samples:
            active=active_roles(events,sample['at'],300)
            for role,event in active.items():
                if event['state']=='running':
                    self.assertIn(role+'_bytes',sample['measured'])
                    self.assertIn(role+'_bytes',sample['cgroup_peaks'])
                    self.assertEqual(sample['role_ids'][role],event['id'])
        self.assertTrue(all(right['at']-left['at']<=3 for left,right in zip(samples,samples[1:])))

    def assert_published_lifecycle_coherence(self,samples,events):
        from proof import active_roles
        for sample in samples:
            for role,event in active_roles(events,sample['at'],300).items():
                if event['state']=='running':
                    self.assertIn(role+'_bytes',sample['measured'])
                    self.assertIn(role+'_bytes',sample['cgroup_peaks'])
                    self.assertEqual(sample['role_ids'][role],event['id'])
                    self.assertGreaterEqual(sample['source_sample_at'][role]/1e9,event['at'])
        self.assertTrue(all(right['at']-left['at']<=3 for left,right in zip(samples,samples[1:])))

    def test_lifecycle_changed_during_select_invalidates_cached_counter_epoch(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,running_during_select=True)
        self.assertEqual((stops,halt),([],0));self.assert_published_lifecycle_coherence(samples,events)

    def test_same_id_counter_before_new_running_epoch_cannot_fill_sample(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,running_during_measurement=True)
        self.assertEqual((stops,halt),([],0));self.assert_published_lifecycle_coherence(samples,events)

    def test_event_after_cutoff_invalidates_candidate_without_timestamp_rewrite(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,running_after_cutoff=True)
        self.assertEqual((stops,halt),([],0));self.assert_published_lifecycle_coherence(samples,events)
        running=next(event for event in events if event['role']=='postgres' and event['state']=='running')
        self.assertNotIn(running['at'],[sample['at'] for sample in samples])
        post=[sample for sample in samples if sample['at']>=running['at']]
        self.assertTrue(post);self.assertGreaterEqual(post[0]['source_sample_at']['postgres']/1e9,running['at'])

    def test_running_lifecycle_wrong_owned_id_cannot_borrow_other_counter(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,running_during_measurement=True,running_identity=WORKER)
        self.assertEqual(halt,1);self.assertEqual(stops[0]['status'],'UNKNOWN')
        self.assertFalse(samples)

    def test_restart_without_first_counter_fails_closed_in_original_startup_bound(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=8,restart_during_measurement=True,withhold_restart_counter=True)
        self.assertEqual(halt,1);self.assertEqual(stops[0]['status'],'UNKNOWN')
        self.assert_published_lifecycle_coherence(samples,events)
        self.assertTrue(all('rabbit_bytes' not in row['measured'] or row['source_sample_at'].get('rabbit',0)/1e9<102. for row in samples))

    def test_missing_first_counter_stops_at_earliest_gap_or_epoch_deadline(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=60,restart_during_measurement=True,withhold_restart_counter=True,precise_deadline_clock=True)
        self.assertEqual(halt,1);self.assertEqual(stops[0]['status'],'UNKNOWN')
        restart=next(event['at'] for event in events if event['state']=='running')
        attachment=next(row['at'] for row in self.attachment_times if row['role']=='rabbit' and row['generation']==2)
        earliest=min(samples[-1]['at']+3,restart+3,attachment+3)
        self.assertLessEqual(self.failure_times[0],earliest)
        self.assert_published_lifecycle_coherence(samples,events)

    def test_coherent_fresh_candidate_delayed_during_measurement_fails_before_publish(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,measurement_delay=2.1,precise_deadline_clock=True)
        self.assertEqual(halt,1);self.assertEqual(stops[0]['status'],'UNKNOWN')
        self.assertEqual(samples[-1]['at'],101.)
        self.assert_published_lifecycle_coherence(samples,events)

    def test_coherent_fresh_candidate_delayed_before_write_fails_before_publish(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,publication_delay=2.1,precise_deadline_clock=True)
        self.assertEqual(halt,1);self.assertEqual(stops[0]['status'],'UNKNOWN')
        self.assertEqual(samples[-1]['at'],101.)
        self.assert_published_lifecycle_coherence(samples,events)

    def test_fresh_post_restart_counter_keeps_real_startup_peak(self):
        samples,events,stops,polls,halt=self.run_stop_transition([False],steps=4,restart_during_measurement=True)
        self.assertEqual((stops,halt),([],0));self.assert_published_lifecycle_coherence(samples,events)
        post=[sample for sample in samples if sample['source_sample_at'].get('rabbit',0)/1e9>102.]
        self.assertTrue(post);self.assertEqual(post[0]['cgroup_peaks']['rabbit_bytes'],17)

    def test_pending_stop_cannot_rearm_from_historical_or_unobserved_restart(self):
        for values in ({'historical_stop':True},{'unobserved_restart':True}):
            samples,events,stops,polls,halt=self.run_stop_transition([True],steps=7,rapid_restart=True,restart_step=3,**values)
            self.assertEqual(halt,1);self.assertEqual(stops[0]['status'],'UNKNOWN')
            self.assertLessEqual(samples[-1]['at'] if samples else 100.,104.)

if __name__=="__main__":unittest.main()
