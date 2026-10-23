from __future__ import annotations

import unittest
import harness


class OwnershipTests(unittest.TestCase):
    def test_refuses_unlabelled_or_unknown_resource(self):
        record = {'Id': 'abc', 'Config': {'Labels': {harness.LABEL: 'run1'}}}
        self.assertEqual(harness.owned_id(record, 'run1', ['abc']), 'abc')
        for ids, run in [([], 'run1'), (['abc'], 'other')]:
            with self.assertRaises(ValueError):
                harness.owned_id(record, run, ids)

    def test_limits_stop_at_first_breach(self):
        self.assertEqual(harness.breaches({'api_bytes': 128 * 1024**2}), [])
        self.assertEqual(harness.breaches({'api_bytes': 128 * 1024**2 + 1}), ['api_bytes'])
        with self.assertRaises(ValueError):
            harness.breaches({'unmeasured': 0})

    def test_baseline_requires_all_eight_exact_ids(self):
        baseline = ['one abc Up', 'two def Up']
        harness.verify_baseline(baseline, [{'Names': 'one', 'ID': 'abcdef'}, {'Names': 'two', 'ID': 'def123'}])
        with self.assertRaises(ValueError):
            harness.verify_baseline(baseline, [{'Names': 'one', 'ID': 'changed'}])


class CanaryTests(unittest.TestCase):
    def test_canary_scan_reports_match_without_echoing_secret(self):
        import tempfile
        from pathlib import Path
        from scan import scan
        with tempfile.TemporaryDirectory() as temporary:
            path=Path(temporary)/'safe-surface'
            path.write_bytes(b'header synthetic-canary-123456 footer')
            result=scan(b'synthetic-canary-123456',[path])
            self.assertEqual(result['status'],'FAIL')
            self.assertEqual(result['matches'],1)
            self.assertNotIn('synthetic-canary-123456',str(result))
            path.write_bytes(b'safe_code')
            self.assertEqual(scan(b'synthetic-canary-123456',[path])['status'],'PASS')


class BrokerContractTests(unittest.TestCase):
    def test_generated_collection_urls_have_exact_vhost_without_query(self):
        from broker import broker_urls
        from urllib.parse import urlparse
        for value in broker_urls('collection','synthetic','fake','pulse-q2c2-synthetic').values():
            parsed=urlparse(value)
            self.assertEqual(parsed.path,'/pulse-q2c2-synthetic')
            self.assertEqual(parsed.query,'')
        q1=broker_urls('q1_regression','synthetic','fake','q1-only')['host']
        self.assertEqual(urlparse(q1).query,'heartbeat=10')

class DeliveryProofTests(unittest.TestCase):
    def test_no_consumer_or_no_delivery_cannot_pass_terminal_proof(self):
        from proof import terminal_delivery_proven
        valid=dict(event_id='event1',observed_event='event1',state='completed',outcome='ignored',before_updated=1.,after_updated=2.,ready=0,unacked=0,quarantine_before=0,quarantine_after=0)
        self.assertTrue(terminal_delivery_proven(**valid))
        for changes in ({'after_updated':1.},{'ready':1},{'unacked':1},{'outcome':'applied'},{'quarantine_after':1},{'observed_event':'other'}):
            self.assertFalse(terminal_delivery_proven(**dict(valid,**changes)))

class PartialTeardownTests(unittest.TestCase):
    def test_cached_images_are_not_owned_cleanup_targets(self):
        import tempfile,json
        from pathlib import Path
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as temporary:
            directory=Path(temporary)
            baseline=directory/'baseline.json';baseline.write_text(json.dumps({'working_containers':['working abc Up']}))
            state={'run_id':'run','containers':[],'networks':['net-owned'],'volumes':['vol-owned'],'cached_image_metadata':harness.IMAGES,'owned_image_ids':[],'images':harness.IMAGES,'baseline':str(baseline)}
            (directory/'state.json').write_text(json.dumps(state))
            calls=[]
            def fake(*args,**kwargs):
                calls.append(args)
                if args[0] in ('network','volume') and args[1]=='inspect':return json.dumps([{'Labels':{harness.LABEL:'run'}}])
                if args[0]=='ps':return json.dumps({'Names':'working','ID':'abcdef'})
                if args[1]=='rm':return ''
                raise AssertionError('unexpected cleanup target')
            with patch.object(harness,'docker',side_effect=fake):harness.teardown(directory)
            self.assertFalse(any(call[0]=='image' for call in calls))

class ResourceProofTests(unittest.TestCase):
    def witness(self):
        from proof import GLOBAL_METRICS
        from harness import LIMITS
        roles=('postgres','rabbit','fixture','api','worker1')
        events=[dict(at=9.,role=role,id=role+'-id',state='running',observed=True) for role in roles]
        trace=[]
        for pid,role in enumerate(('actual','supervisor','forwarder'),1):
            trace.extend([dict(at=9.,kind='start',pid=pid,role=role),dict(at=13.,kind='peak',pid=pid,role=role,peak_bytes=100)])
        rows=[]
        for at in (10.,11.,12.,13.,14.):
            measured={key:0 for key in GLOBAL_METRICS};measured.update({role+'_bytes':100 for role in roles})
            rows.append(dict(at=at,measured=measured,breaches=[],cgroup_peaks={role+'_bytes':100 for role in roles},role_ids={role:role+'-id' for role in roles},source_sample_at={role:at*1e9 for role in roles}))
        return dict(samples=rows,start=10.,end=14.,lifecycle=events,trace=trace,stops=[],limits=LIMITS)

    def test_complete_resource_span_and_transient_child_peak(self):
        from proof import validate_resource_span
        valid=self.witness()
        valid['trace'].extend([dict(at=11.1,kind='start',pid=99,role='legacy-probe'),dict(at=11.2,kind='exit',pid=99,role='legacy-probe',peak_bytes=1000)])
        result=validate_resource_span(**valid)
        self.assertTrue(result['full_span_verified'])
        self.assertEqual(result['full_peak_aggregate_bytes'],1800)

    def test_missing_stale_gapped_unknown_and_peak_evidence_fail(self):
        from proof import validate_resource_span
        controls=[]
        one=self.witness();one['samples']=[{'at':0,'measured':{'api_bytes':1},'breaches':[]}];one['stops']=[{'status':'UNKNOWN'}];controls.append(one)
        one=self.witness();del one['samples'][2]['measured']['database_bytes'];controls.append(one)
        one=self.witness();del one['samples'][2]['measured']['api_bytes'];controls.append(one)
        one=self.witness();one['samples'][2]['source_sample_at']['rabbit']=1;controls.append(one)
        one=self.witness();one['samples']=one['samples'][:1];controls.append(one)
        one=self.witness();one['samples']=one['samples'][2:];controls.append(one)
        one=self.witness();one['samples']=[one['samples'][0],one['samples'][-1]];controls.append(one)
        one=self.witness();one['stops']=[{'status':'UNKNOWN'}];controls.append(one)
        one=self.witness();one['lifecycle'][1]['observed']=False;controls.append(one)
        one=self.witness();one['trace'].append(dict(at=11.1,kind='start',pid=99,role='legacy-probe'));controls.append(one)
        one=self.witness();one['trace'].extend([dict(at=11.1,kind='start',pid=99,role='legacy-probe'),dict(at=11.2,kind='exit',pid=99,role='legacy-probe',peak_bytes=65*1024**2)]);controls.append(one)
        one=self.witness();one['limits']=dict(one['limits'],aggregate_bytes=799);controls.append(one)
        one=self.witness();one['samples'][2]['cgroup_peaks']['api_bytes']=129*1024**2;controls.append(one)
        one=self.witness();one['samples'][2]['measured']['database_bytes']=float('nan');controls.append(one)
        for index,control in enumerate(controls):
            with self.subTest(index=index),self.assertRaises(ValueError):validate_resource_span(**control)

    def test_expected_stop_requires_observation_and_restart_needs_fresh_counter(self):
        from proof import validate_resource_span
        one=self.witness();one['lifecycle'].append(dict(at=11.5,role='worker1',id='worker1-id',state='stopped',observed=True,stop_deadline_at=20.))
        for row in one['samples'][2:]:
            for field in ('measured','cgroup_peaks'):row[field].pop('worker1_bytes')
            row['role_ids'].pop('worker1');row['source_sample_at'].pop('worker1')
        self.assertTrue(validate_resource_span(**one)['full_span_verified'])
        one['lifecycle'][-1]['stop_deadline_at']=12.
        with self.assertRaises(ValueError):validate_resource_span(**one)
        one['lifecycle'][-1]['stop_deadline_at']=20.
        one['lifecycle'].append(dict(at=12.5,role='worker1',id='worker1-new',state='running',observed=True))
        with self.assertRaises(ValueError):validate_resource_span(**one)

class ProcessPeakTests(unittest.TestCase):
    def test_wait4_observes_child_peak_between_samples(self):
        import tempfile,os,sys,json,time
        from pathlib import Path
        from unittest.mock import patch
        import processes
        with tempfile.TemporaryDirectory() as temporary:
            directory=Path(temporary);trace=directory/'process-trace.jsonl'
            with patch.dict(os.environ,{processes.TRACE_ENV:str(trace)}):
                before=time.time()
                child=processes.Process([sys.executable,'-c','x=bytearray(20*1024**2)'],role='legacy-probe')
                self.assertEqual(child.wait(3),0)
                records=list(processes.read_trace(trace))
                peak=[row['peak_bytes'] for row in records if row['kind']=='exit'][0]
                self.assertGreaterEqual(peak,20*1024**2)
                total,maximum,pids=processes.host_usage(directory,before)
                self.assertGreaterEqual(total,peak);self.assertGreaterEqual(maximum,peak)
                self.assertIn(str(child.pid),pids)

class WholeRequestedSetTests(unittest.TestCase):
    def test_no_effect_detects_any_timestamp_and_preexisting_change(self):
        from actual import Runtime
        from unittest.mock import Mock
        runtime=object.__new__(Runtime);runtime.query=Mock(return_value=[(0,0)])
        runtime.requested_rows=Mock(return_value={})
        runtime.no_effect('job')
        runtime.requested_rows.return_value={'well|later-timestamp':{'values':{'F_out':50}}}
        with self.assertRaises(AssertionError):runtime.no_effect('job')
        before={'well|existing':{'values':{'F_out':0},'provenance':None}}
        runtime.requested_rows.return_value=before.copy();runtime.no_effect('job',before)
        runtime.requested_rows.return_value={'well|existing':{'values':{'F_out':1},'provenance':None}}
        with self.assertRaises(AssertionError):runtime.no_effect('job',before)

    def test_captured_apply_checks_entire_key_value_and_provenance_set(self):
        from actual import Runtime
        from unittest.mock import Mock
        import json
        runtime=object.__new__(Runtime)
        point={'well_id':'pulse:1','timestamp':'2026-10-23T00:01:00Z','values':{'F_out':0,'R_izol':None}}
        provenance={'source_id':'source1','capture_sha256':'hash'}
        canonical=json.dumps({'points':[point]}).encode()
        runtime.query=Mock(side_effect=lambda sql,args: [(canonical,provenance,'hash')] if 'canonical,provenance' in sql else [('hash',1)])
        expected={'pulse:1|2026-10-23T00:01:00+00:00':dict(values=point['values'],quality='',source_id='source1',provenance=provenance)}
        runtime.requested_rows=Mock(return_value=expected)
        runtime.captured_applied('job')
        for actual in ({},dict(expected,**{'pulse:1|unexpected':expected[next(iter(expected))]}),{next(iter(expected)):dict(next(iter(expected.values())),provenance=None)}):
            runtime.requested_rows.return_value=actual
            with self.assertRaises(AssertionError):runtime.captured_applied('job')


class BoundedDigestTests(unittest.TestCase):
    def test_large_binary_hash_streams_bounded_reads_without_whole_file(self):
        import build
        class Reader:
            remaining=90*1024**2
            consumed=0
            largest=0
            def __enter__(self):return self
            def __exit__(self,*args):pass
            def read(self,size=-1):
                if not 0<size<=65536:raise AssertionError('unbounded binary read')
                self.largest=max(self.largest,size);count=min(size,self.remaining)
                self.remaining-=count;self.consumed+=count
                return b'a'*count
        class Binary:
            reader=Reader()
            def read_bytes(self):raise AssertionError('whole-file binary read exceeds host memory budget')
            def open(self,mode):
                if mode!='rb':raise AssertionError('binary read required')
                return self.reader
        binary=Binary();value=build.digest(binary)
        self.assertEqual(binary.reader.consumed,90*1024**2)
        self.assertLessEqual(binary.reader.largest,65536)
        self.assertEqual(len(value),64)

    def test_digest_preserves_known_sha256(self):
        import build,tempfile
        from pathlib import Path
        with tempfile.TemporaryDirectory() as temporary:
            path=Path(temporary)/'abc';path.write_bytes(b'abc')
            self.assertEqual(build.digest(path),'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')


if __name__ == "__main__":unittest.main()


class StdioOnlyProvisionTests(unittest.TestCase):
    def provision_commands(self):
        import tempfile,json
        from pathlib import Path
        from unittest.mock import patch
        temporary=tempfile.TemporaryDirectory()
        directory=Path(temporary.name)/'stand'
        calls=[]
        def fake(*args,**kwargs):
            calls.append(args)
            if args[:2]==('network','create'):return 'network-own'
            if args[:2]==('volume','create'):return args[-1]
            if args[0]=='run':return ('a' if args[args.index('--network-alias')+1]=='postgres' else 'b')*64
            raise AssertionError('unexpected Docker command')
        with patch.object(harness,'preflight',return_value={'cached_image_metadata':harness.IMAGES,'ports_checked':list(harness.PORTS)}),patch.object(harness,'docker',side_effect=fake),patch('builtins.print'):
            harness.provision(directory,Path(temporary.name)/'baseline.json')
        state=json.loads((directory/'state.json').read_text())
        return temporary,calls,state

    def test_infrastructure_uses_internal_network_without_docker_publications(self):
        temporary,calls,state=self.provision_commands()
        self.addCleanup(temporary.cleanup)
        runs=[row for row in calls if row[0]=='run']
        self.assertEqual(len(runs),2)
        for row in runs:
            self.assertNotIn('-p',row)
            self.assertNotIn('--publish',row)
            self.assertEqual(row[row.index('--network')+1],'network-own')
            self.assertEqual(row[row.index('--memory')+1],'384m')
        self.assertEqual(state['ports_checked'],list(harness.PORTS))

    def test_broker_restart_does_not_rebind_live_stdio_bridge_ports(self):
        temporary,calls,state=self.provision_commands()
        self.addCleanup(temporary.cleanup)
        broker=next(row for row in calls if row[0]=='run' and row[row.index('--network-alias')+1]=='rabbit')
        # Model Engine port allocation from the REAL provision argv; the real
        # failed-stand diagnostic independently confirms address-in-use behavior.
        published={broker[index+1].split(':')[1] for index,value in enumerate(broker) if value=='-p'}
        bridge_listeners={'55442','5675','15675','8093','8094'}
        def start_exact(identity):
            self.assertEqual(identity,state['rabbit_id'])
            if published & bridge_listeners:raise RuntimeError('published port conflicts with live stdio bridge')
            return identity
        self.assertEqual(start_exact(state['rabbit_id']),state['rabbit_id'])


class QueueMetricsReadinessTests(unittest.TestCase):
    def test_test_broker_enables_metrics_persistently_at_first_boot(self):
        temporary,calls,state=StdioOnlyProvisionTests().provision_commands()
        self.addCleanup(temporary.cleanup)
        from pathlib import Path
        directory=Path(temporary.name)/'stand'
        broker=next(row for row in calls if row[0]=='run' and row[row.index('--network-alias')+1]=='rabbit')
        expected='type=bind,src='+str((directory/'90-collection-test-metrics.conf').resolve())+',dst=/etc/rabbitmq/conf.d/90-collection-test-metrics.conf,readonly'
        self.assertIn(expected,broker)
        self.assertEqual((directory/'90-collection-test-metrics.conf').read_text(),'management_agent.disable_metrics_collector = false\n')
        self.assertNotIn('-rabbitmq_management_agent', (directory/'rabbit.env').read_text())

    def runtime_before_ack_loss(self,rows):
        from actual import Runtime
        runtime=Runtime.__new__(Runtime)
        runtime.control=lambda **changes:None
        runtime.input=lambda *args,**kwargs:{}
        runtime.submit=lambda request:'own-job'
        runtime.done=lambda job:None
        held=iter((0,1))
        runtime.counters=lambda:{'held_acks':next(held,1),'source_gets':0}
        runtime.snapshot=lambda:{}
        runtime.query=lambda *args:[('same-event',10.)]
        ready=list(rows)
        def management(method,path,data=None):
            if path.endswith('.quarantine'):return {'messages':0,'messages_ready':0,'messages_unacknowledged':0,'consumers':0}
            return ready.pop(0) if len(ready)>1 else ready[0]
        runtime.management=management
        return runtime

    def test_real_ack_case_waits_metadata_and_stale_stats_before_observed_unacked(self):
        class FaultReached(Exception):pass
        runtime=self.runtime_before_ack_loss([{'name':'ready'},dict(messages=0,messages_ready=0,messages_unacknowledged=0,consumers=1),dict(messages=1,messages_ready=0,messages_unacknowledged=1,consumers=1)])
        runtime.signal=lambda *args:(_ for _ in ()).throw(FaultReached())
        with self.assertRaises(FaultReached):runtime.case_a04()

    def test_real_ack_case_never_treats_missing_stats_as_zero_or_ready(self):
        runtime=self.runtime_before_ack_loss([{'name':'ready'}])
        def bounded(predicate,**kwargs):
            for _ in range(2):
                value=predicate()
                if value:return value
            raise TimeoutError('no observed metrics')
        runtime.wait=bounded
        runtime.signal=lambda *args:self.fail('fault before real queue metrics')
        with self.assertRaises(TimeoutError):runtime.case_a04()


class BrokerMetricsBootstrapTests(unittest.TestCase):
    def test_disabled_collector_cannot_report_broker_ready(self):
        import tempfile,json
        from pathlib import Path
        from unittest.mock import patch
        import broker
        with tempfile.TemporaryDirectory() as temporary:
            directory=Path(temporary)
            (directory/'state.json').write_text(json.dumps({'rabbit_id':'b'*64,'run_id':'run'}))
            calls=[]
            def fake(*args,**kwargs):
                calls.append(args)
                return '{ok,true}' if 'eval' in args else ''
            with patch.object(broker,'inspect_owned'),patch.object(broker,'docker',side_effect=fake),patch('builtins.print'),self.assertRaises(ValueError):broker.provision(directory)
            for row in calls:self.assertEqual(row[:3],('exec','--user','rabbitmq'))
            self.assertFalse(any('add_vhost' in row for row in calls))


class QueueGaugeValidationTests(unittest.TestCase):
    def test_only_complete_nonnegative_consistent_integer_gauges_are_observed(self):
        from broker import valid_queue_gauges
        valid=dict(messages=1,messages_ready=0,messages_unacknowledged=1,consumers=1)
        self.assertTrue(valid_queue_gauges(valid))
        for key in valid:
            missing=dict(valid);del missing[key]
            self.assertFalse(valid_queue_gauges(missing))
            for invalid in (-1,True,1.0,'1',None):
                self.assertFalse(valid_queue_gauges(dict(valid,**{key:invalid})))
        self.assertFalse(valid_queue_gauges(dict(valid,messages=2)))
        self.assertFalse(valid_queue_gauges([]))


class PhysicalSessionCaseTests(unittest.TestCase):
    def run_case(self,*,natural=False,term_error=False,still_running=False,oom=False,foreign=False,rows=False,global_rows=False,capture_count=1,capture_changed=False,new_fence=2,new_pid=138,refetch=False,applied_capture_changed=False):
        import contextlib
        from unittest.mock import patch
        import actual
        runtime=actual.Runtime.__new__(actual.Runtime)
        identity='c'*64
        runtime.state={'worker1_id':identity,'containers':[identity],'run_id':'own','expected_stopped':[]}
        events=[];running=True;restarted=False;new_session=False;fetches=0;captures=0
        runtime.control=lambda **kwargs:None
        runtime.submit=lambda request:'job'
        runtime.input=lambda *args,**kwargs:{}
        runtime.facts=lambda **kwargs:events.append(('facts',kwargs))
        runtime.snapshot=lambda:{'values':{'count':int(global_rows and not running),'digest':'before' if not (global_rows and not running) else 'changed'}}
        runtime.requested_rows=lambda job:({'unexpected':{}} if rows and not running else {})
        runtime.counters=lambda:{'source_gets':int(refetch and restarted)}
        def update(changes):runtime.state.update(changes);events.append(('expected',list(runtime.state['expected_stopped'])))
        runtime.update_state=update
        def inspect(state,cid):
            record={'Id':'d'*64 if foreign else cid,'Config':{'Labels':{harness.LABEL:'own'}},'State':{'Running':running,'OOMKilled':oom,'ExitCode':1}}
            harness.owned_id(record,state['run_id'],state['containers'])
            return record
        runtime.observe_role=lambda role,cid,state:events.append(('observed',state))
        def signal(role,kind):
            nonlocal running
            events.append(('signal',kind))
            running=still_running if term_error else False
            if term_error:raise RuntimeError('diagnostic TERM failed')
        runtime.signal=signal
        def restart(role):
            nonlocal running,restarted
            self.assertFalse(running);self.assertIn(('observed','stopped'),events)
            self.assertIn(('zero_effects',),events)
            running=True;restarted=True;events.append(('restart',))
            runtime.state['expected_stopped']=[]
        runtime.restart=restart
        def blocked(job):return new_pid if restarted else 137
        runtime.blocked_pid=blocked
        def query(statement,params=()):
            nonlocal running,captures,new_session
            if 'pg_terminate_backend' in statement:
                self.assertIn(identity,runtime.state['expected_stopped'],'fault must be prearmed before PG termination')
                events.append(('terminate',params[0]))
                if natural:running=False
                return [(True,)]
            if statement.startswith('SELECT fence,physical_backend_pid'):
                if restarted:
                    if new_fence>1 and new_pid!=137:new_session=True
                    return [(new_fence,new_pid)]
                return [(1,137)]
            if statement.startswith('SELECT canonical_sha256'):
                captures+=1
                return [('changed' if capture_changed and captures>1 else 'capture-sha',)]*capture_count
            if 'count(*) FROM collection_job_checkpoint' in statement:return [(0,0)]
            if 'SELECT capture_sha256' in statement:return [('changed' if applied_capture_changed else 'capture-sha',)]
            raise AssertionError('unexpected readonly proof query')
        runtime.query=query
        real_no_effect=runtime.no_effect
        def no_effect(job):
            self.assertFalse(running);real_no_effect(job);events.append(('zero_effects',))
        runtime.no_effect=no_effect
        @contextlib.contextmanager
        def barrier():
            events.append(('barrier_open',))
            try:yield
            finally:
                # On success the NEW physical session was genuinely observed
                # blocked inside the SAME barrier before releasing it.
                events.append(('barrier_close',new_session))
        runtime.checkpoint_barrier=barrier
        def bounded(predicate,**kwargs):
            for _ in range(3):
                value=predicate()
                if value:return value
            raise TimeoutError('proof barrier not reached')
        runtime.wait=bounded
        def done(job):
            self.assertTrue(new_session);self.assertIn(('barrier_close',True),events)
            return {'attempt':2}
        runtime.done=done
        runtime.captured_applied=lambda job:events.append(('exact_applied',))
        with patch.object(actual,'inspect_owned',side_effect=inspect):runtime.case_a06()
        return events

    def test_real_case_prearms_stops_then_observes_new_session_before_release(self):
        events=self.run_case()
        self.assertLess(events.index(('terminate',137)),events.index(('signal','TERM')))
        self.assertLess(events.index(('zero_effects',)),events.index(('restart',)))
        self.assertIn(('barrier_close',True),events)
        self.assertIn(('exact_applied',),events)

    def test_natural_exit_and_term_race_require_true_owned_non_oom_stop(self):
        events=self.run_case(natural=True)
        self.assertNotIn(('signal','TERM'),events)
        self.run_case(term_error=True)
        for invalid in ({'term_error':True,'still_running':True},{'natural':True,'oom':True},{'natural':True,'oom':None},{'foreign':True}):
            with self.subTest(invalid=invalid),self.assertRaises((RuntimeError,AssertionError,ValueError)):self.run_case(**invalid)

    def test_no_partial_rows_or_capture_change_before_restart(self):
        for invalid in ({'rows':True},{'global_rows':True},{'capture_count':2},{'capture_changed':True}):
            with self.subTest(invalid=invalid),self.assertRaises(AssertionError):self.run_case(**invalid)

    def test_new_fence_and_session_and_no_refetch_are_required(self):
        for invalid in ({'new_fence':1},{'new_pid':137},{'refetch':True},{'applied_capture_changed':True}):
            with self.subTest(invalid=invalid),self.assertRaises((AssertionError,TimeoutError)):self.run_case(**invalid)


class AuthorityFaultLifecycleTests(unittest.TestCase):
    def exercise(self,*,source_start_delay=0,partial_rows=False,capture_rows=0):
        import tempfile
        from pathlib import Path
        from unittest.mock import patch
        import actual
        class AuthorityComplete(Exception):pass
        runtime=actual.Runtime.__new__(actual.Runtime);identity='e'*64
        runtime.state={'worker1_id':identity,'containers':[identity],'run_id':'own','expected_stopped':[]}
        temporary=tempfile.TemporaryDirectory();self.addCleanup(temporary.cleanup);runtime.directory=Path(temporary.name)
        (runtime.directory/'session-token').write_text('synthetic-old-session')
        runtime.evidence={};runtime.session='synthetic-old-session';runtime.second_session=lambda:'synthetic-admin'
        clock=[0.];events=[];kind=[''];jobs=[0];submitted_at=[0.];observed_at=[None];running=[True]
        user={'id':1,'login':'synthetic','name':'test','role':'operator','scope':'research','active':True,'revision':1}
        def control(**changes):
            revision=changes.get('revision','')
            if revision=='cancel-a':raise AuthorityComplete()
            kind[0]=revision.removeprefix('authority-')
        runtime.control=control
        runtime.input=lambda *args,**kwargs:{}
        def submit(request):jobs[0]+=1;submitted_at[0]=clock[0];observed_at[0]=None;return 'job-'+kind[0]
        runtime.submit=submit
        def counters():
            arrived=clock[0]-submitted_at[0]>=source_start_delay
            if arrived and jobs[0]>0 and observed_at[0] is None:observed_at[0]=clock[0]
            return {'source_gets':jobs[0] if arrived else max(0,jobs[0]-1)}
        runtime.counters=counters
        def mutation(label):
            self.assertIn(identity,runtime.state['expected_stopped'],'authority fault must be prearmed')
            events.append((label,kind[0],clock[0]))
        def request(method,path,data=None,**kwargs):
            if path=='/api/v1/auth/users':return 200,[dict(user)]
            if path=='/api/v1/auth/users/1':
                mutation('mutate' if not data['active'] else 'restore');user.update(data);user['revision']+=1;return 200,{}
            if path=='/api/v1/wells/1':
                if method=='PATCH':mutation('mutate' if data['unitek_id']==-900002 else 'restore')
                return 200,{'revision':1}
            raise AssertionError('unexpected HTTP path')
        runtime.request=request
        runtime.source=lambda:{'id':'source-a','name':'source-a'}
        runtime.edit_source=lambda value:mutation('mutate' if value['name'].endswith(' changed') else 'restore')
        runtime.update_state=lambda changes:runtime.state.update(changes)
        def inspect(state,cid):
            record={'Id':cid,'Config':{'Labels':{harness.LABEL:'own'}},'State':{'Running':running[0],'OOMKilled':False,'ExitCode':0}}
            harness.owned_id(record,state['run_id'],state['containers']);return record
        runtime.observe_role=lambda role,cid,state:events.append(('observed-'+state,kind[0],clock[0]))
        def signal(role,value):
            self.assertGreaterEqual(clock[0]-observed_at[0],7.,'do not kill old worker before delayed IO window')
            running[0]=False;events.append(('stop',kind[0],clock[0]))
        runtime.signal=signal
        def restart(role):
            self.assertFalse(running[0]);self.assertTrue(any(e[0]=='restore' and e[1]==kind[0] for e in events))
            running[0]=True;runtime.state['expected_stopped']=[];events.append(('restart',kind[0],clock[0]))
        runtime.restart=restart
        runtime.requested_rows=lambda job:({'partial':{}} if partial_rows else {})
        def query(statement,params=()):
            if statement.startswith('SELECT state'):return [('revoked',)]
            if 'count(*) FROM collection_job_checkpoint' in statement:return [(0,0)]
            if 'count(*) FROM collection_job_capture' in statement:return [(capture_rows,)]
            if statement.startswith('INSERT INTO auth_session'):return []
            raise AssertionError('unexpected query')
        runtime.query=query
        real_no_effect=runtime.no_effect
        def no_effect(job):
            self.assertGreaterEqual(clock[0]-observed_at[0],7.)
            self.assertTrue(running[0],'verify late IO protection before deliberate stop')
            real_no_effect(job);events.append(('zero-effects',kind[0],clock[0]))
        runtime.no_effect=no_effect
        def bounded(predicate,*,timeout):
            end=clock[0]+timeout
            while True:
                value=predicate()
                if value:return value
                step=12. if timeout>=40 else 7.1
                if clock[0]+step>end:raise TimeoutError('observation window shorter than valid phase')
                clock[0]+=step
        runtime.wait=bounded
        with patch.object(actual,'inspect_owned',side_effect=inspect),patch.object(actual.time,'monotonic',side_effect=lambda:clock[0]),self.assertRaises(AuthorityComplete):runtime.case_a07()
        return events

    def test_each_authority_fault_keeps_late_io_then_physically_stops_restores_restarts(self):
        events=self.exercise()
        for kind in ('account','source','binding'):
            ordered=[row[0] for row in events if row[1]==kind]
            self.assertLess(ordered.index('mutate'),ordered.index('zero-effects'))
            self.assertLess(ordered.index('zero-effects'),ordered.index('stop'))
            self.assertLess(ordered.index('stop'),ordered.index('restore'))
            self.assertLess(ordered.index('restore'),ordered.index('restart'))

    def test_valid_source_start_after_eight_seconds_is_observed(self):
        self.exercise(source_start_delay=16)

    def test_late_partial_rows_or_capture_cannot_pass(self):
        for invalid in ({'partial_rows':True},{'capture_rows':1}):
            with self.subTest(invalid=invalid),self.assertRaises(AssertionError):self.exercise(**invalid)


class SourceStatusRestoreTests(unittest.TestCase):
    def exercise(self,*,intended='running',omit_status=False,patch_code=200,patch_status=None,drop_failure=False,source_id='source-a',secret=False):
        from actual import Runtime
        runtime=Runtime.__new__(Runtime);events=[];current=['running']
        def request(method,path,data=None,**kwargs):
            if method=='PUT':
                current[0]='draft';events.append(('PUT',dict(data)))
                return 200,{'id':source_id,'status':'draft'}
            self.assertEqual(method,'PATCH');self.assertEqual(path,'/api/v1/integrations/'+source_id+'/status')
            events.append(('PATCH',dict(data)))
            if patch_code==200:current[0]=patch_status or data['status']
            return patch_code,{'id':source_id,'status':current[0]}
        runtime.request=request
        def query(statement,parameters=()):
            if statement.startswith('CREATE FUNCTION'):events.append(('install-function',))
            elif statement.startswith('CREATE TRIGGER'):events.append(('install-trigger',))
            elif statement.startswith('DROP TRIGGER'):
                events.append(('drop-trigger',))
                if drop_failure:raise RuntimeError('diagnostic cleanup failure')
            elif statement.startswith('DROP FUNCTION'):events.append(('drop-function',))
            else:events.append(('operator-future-seed',parameters))
            return []
        runtime.query=query
        value={'id':source_id,'name':'synthetic','endpoint':'http://fixture:8080/v1/collection-capture','mappings':[{'field':'f0','parameter':'F_out'}]}
        if not omit_status:value['status']=intended
        if secret:value['secret']='synthetic-fake-credential'
        runtime.edit_source(value)
        return events,current[0],value

    def test_real_put_draft_is_restored_via_guarded_status_api(self):
        events,status,value=self.exercise(secret=True)
        self.assertEqual(status,'running','credential-rejection scenario must not pass solely because source stayed draft')
        order=[row[0] for row in events]
        self.assertLess(order.index('PUT'),order.index('install-trigger'))
        self.assertLess(order.index('install-trigger'),order.index('PATCH'))
        self.assertLess(order.index('PATCH'),order.index('drop-trigger'))
        self.assertLess(order.index('drop-function'),order.index('operator-future-seed'))
        self.assertEqual(events[0][1],value,'config/mapping/credential payload must retain exact intended identity')

    def test_paused_and_default_draft_are_never_autoenabled(self):
        for options,expected in (({'intended':'paused'},'paused'),({'omit_status':True},'draft')):
            events,status,_=self.exercise(**options);self.assertEqual(status,expected)
            self.assertNotIn('install-trigger',[row[0] for row in events])
            if expected=='draft':self.assertNotIn('PATCH',[row[0] for row in events])

    def test_failed_or_mismatched_status_or_cleanup_never_reports_success(self):
        for options in ({'patch_code':409},{'patch_status':'draft'},{'drop_failure':True},{'source_id':'foreign'}):
            with self.subTest(options=options),self.assertRaises((AssertionError,RuntimeError,ValueError)):self.exercise(**options)


class ManagementNamespacePermissionTests(unittest.TestCase):
    def provision(self):
        import contextlib,io,json,tempfile
        from pathlib import Path
        from unittest.mock import patch
        import broker
        temporary=tempfile.TemporaryDirectory();self.addCleanup(temporary.cleanup)
        directory=Path(temporary.name)
        (directory/'state.json').write_text(json.dumps({'run_id':'own-run','rabbit_id':'owned-rabbit'}))
        (directory/'rabbit.env').write_text('RABBITMQ_DEFAULT_USER=own_management\nRABBITMQ_DEFAULT_PASS=private_fake\n')
        calls=[]
        def docker(*args,**kwargs):
            calls.append(args)
            return '{ok,false}' if 'eval' in args else 'ok'
        with patch.object(broker,'docker',side_effect=docker),patch.object(broker,'inspect_owned'),contextlib.redirect_stdout(io.StringIO()):
            broker.provision(directory)
        return calls

    def test_real_bootstrap_grants_management_only_fixed_collection_namespace(self):
        import re
        calls=self.provision()
        grants=[row for row in calls if 'set_permissions' in row and 'own_management' in row]
        self.assertEqual(len(grants),1)
        grant=grants[0]
        self.assertEqual(grant[:6],('exec','--user','rabbitmq','owned-rabbit','rabbitmqctl','set_permissions'))
        self.assertEqual(grant[6:9],('-p','pulse-q2c2-synthetic','own_management'))
        configure,write,read=grant[9:]
        self.assertEqual(configure,'^$')
        self.assertEqual(write,r'^pulse\.collection\.v1(\..*)?$')
        self.assertEqual(read,write)
        for resource in ('pulse.collection.v1','pulse.collection.v1.ready','pulse.collection.v1.quarantine'):
            self.assertIsNone(re.fullmatch(configure,resource))
            self.assertIsNotNone(re.fullmatch(write,resource))
            self.assertIsNotNone(re.fullmatch(read,resource))
        for resource in ('pulse.file-check.v1','pulse.collection.v2','foreign','xpulse.collection.v1'):
            self.assertIsNone(re.fullmatch(write,resource))
            self.assertIsNone(re.fullmatch(read,resource))
        self.assertFalse(any('set_user_tags' in row for row in calls))
        application_grants=[row for row in calls if 'set_permissions' in row and 'own_management' not in row]
        self.assertEqual(len(application_grants),4)
        self.assertEqual(sum(row[row.index('-p')+1]=='pulse-q2c2-synthetic' for row in application_grants),2)


class RollbackDrainBindingTests(unittest.TestCase):
    def run_drain(self,*,active_lease=False):
        from actual import Runtime
        from unittest.mock import patch
        runtime=Runtime.__new__(Runtime)
        runtime.state={'api_id':'owned-api','worker1_id':'owned-worker'}
        runtime.input=lambda *a,**k:{};runtime.submit=lambda value:'pending-job'
        runtime.signal=lambda *a:None;runtime.remove_owned_container=lambda *a:None
        runtime.launch=lambda *a,**k:None
        runtime.snapshot=lambda:{key:{} for key in ('values','capture','checkpoint','coverage','log')}|{'jobs':[]}
        runtime.request=lambda method,path,*args:(503,{}) if method=='POST' else (200,{})
        runtime.evidence={};calls=[];counts=iter([1,0,1,0])
        class Cursor:
            def __enter__(self):return self
            def __exit__(self,*args):pass
            def execute(self,statement,parameters):
                calls.append((statement,parameters))
                # Model the real psycopg2 positional parser; the separate own-PG
                # diagnostic validates its exact old IndexError and bound behavior.
                if '%' in statement and not parameters:raise IndexError('unbound driver placeholder')
                self.description=None;self.rows=[]
                if statement.startswith('SELECT'):
                    self.description=True
                    if 'pg_stat_activity' in statement:
                        self.rows=[(next(counts),)]
                    elif 'SELECT id,revision' in statement:self.rows=[]
                    elif 'lease_until IS NOT NULL' in statement:self.rows=[(int(active_lease),)]
                    else:raise AssertionError('unexpected test query')
            def fetchall(self):return self.rows
        class Connection:
            def cursor(self):return Cursor()
        runtime.connection=Connection()
        with patch('actual.inspect_owned',return_value={'State':{'Running':False}}),patch('actual.time.sleep'):
            runtime.case_a10()
        return runtime,calls

    def test_real_rollback_case_binds_both_drain_queries_and_waits_active_sessions(self):
        runtime,calls=self.run_drain()
        activity=[(statement,parameters) for statement,parameters in calls if 'pg_stat_activity' in statement]
        self.assertEqual(len(activity),4)
        self.assertTrue(all(parameters==('collection-runtime-%',) for _,parameters in activity))
        self.assertTrue(all('LIKE %s' in statement for statement,_ in activity))
        self.assertTrue(all('pid<>pg_backend_pid()' in statement for statement,_ in activity[:2]))
        self.assertTrue(runtime.evidence['new_writers_drained'])

    def test_active_lease_blocks_rollback_after_real_session_drain(self):
        with self.assertRaisesRegex(AssertionError,'active lease/job'):
            self.run_drain(active_lease=True)
