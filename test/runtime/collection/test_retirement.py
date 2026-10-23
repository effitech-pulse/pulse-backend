"""Optional worker retirement: exact removal authority and permanent resource accounting."""
from __future__ import annotations
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock,patch
import actual
import own_engine
from proof import active_roles,validate_resource_span
import test_harness

ID='b'*64
RUN='synthetic-run'

def retirement():
    stop={'at':11.,'role':'worker2','id':ID,'state':'stopped','observed':True,'stop_deadline_at':311.}
    body={'id':ID,'role':'worker2','run_id':RUN,'run_label':RUN,'prior_stop':stop,'physical_stopped':True,'supervisor_ack_without_role':True,'supervisor_ack_at':11.1,'removed_at':11.2,'absence_verified':True,'absence_verified_at':11.3}
    event={'at':12.,'role':'worker2','id':ID,'state':'retired','observed':True,'run_id':RUN,'removal_receipt':body}
    event['removal_receipt_sha256']=hashlib.sha256(json.dumps(body,sort_keys=True,separators=(',',':')).encode()).hexdigest()
    return stop,event

class RetirementProofTests(unittest.TestCase):
    def test_optional_retirement_survives_later_cases_without_refresh(self):
        stop,event=retirement();events=[stop,event]
        self.assertEqual(active_roles(events,900.,300,RUN)['worker2']['state'],'retired')
        with self.assertRaises(ValueError):active_roles([stop],900.,300,RUN)
        witness=test_harness.ResourceProofTests().witness();witness['lifecycle']+=events;witness['run_id']=RUN
        self.assertTrue(validate_resource_span(**witness)['full_span_verified'])

    def test_wrong_authority_identity_missing_absence_or_required_role_rejected(self):
        for mutate in (lambda e:e.update(run_id='other'),lambda e:e['removal_receipt'].update(run_label='other'),lambda e:e.update(id='c'*64),lambda e:e['removal_receipt'].update(absence_verified=False),lambda e:e.update(role='worker1'),lambda e:e['removal_receipt'].update(supervisor_ack_at=10.),lambda e:e['removal_receipt'].update(prior_stop={'at':1.}),lambda e:e['removal_receipt'].update(physical_stopped=False)):
            stop,event=retirement();mutate(event)
            event['removal_receipt_sha256']=hashlib.sha256(json.dumps(event['removal_receipt'],sort_keys=True,separators=(',',':')).encode()).hexdigest()
            with self.assertRaises(ValueError):active_roles([stop,event],900.,300,RUN)
        stop,event=retirement()
        with self.assertRaises(ValueError):active_roles([stop,event],900.,300)
        with self.assertRaises(ValueError):active_roles([event],900.,300,RUN)
        with self.assertRaises(ValueError):active_roles([stop,event,dict(stop,at=13.,state='running')],900.,300,RUN)

    def test_absence_requires_positive_zero_snapshot_exact_owned_id(self):
        state={'run_id':RUN,'project':'own','containers':[ID],'worker2_id':ID}
        good={'Id':ID,'Labels':{own_engine.LABEL:RUN},'Names':['/own-worker2'],'State':'exited'}
        with patch.object(own_engine,'_snapshot',return_value=[]):self.assertTrue(own_engine.absent(state,ID,'/socket'))
        with patch.object(own_engine,'_snapshot',return_value=[good]):self.assertFalse(own_engine.absent(state,ID,'/socket'))
        for rows in ([good,good],[dict(good,Id='c'*64)],[dict(good,Labels={own_engine.LABEL:'other'})]):
            with patch.object(own_engine,'_snapshot',return_value=rows),self.assertRaises(ValueError):own_engine.absent(state,ID,'/socket')

class RuntimeRetirementTests(unittest.TestCase):
    def runtime(self,directory):
        runtime=object.__new__(actual.Runtime);runtime.directory=directory
        runtime.state={'run_id':RUN,'project':'own','containers':[ID],'worker2_id':ID,'retired_containers':[]}
        (directory/'state.json').write_text(json.dumps(runtime.state))
        stop={'at':100.,'role':'worker2','id':ID,'state':'stopped','observed':True,'stop_deadline_at':400.}
        (directory/'container-lifecycle.jsonl').write_text(json.dumps(stop)+'\n')
        (directory/'supervisor-ready.json').write_text(json.dumps({'at':101.5,'role_ids':{},'source_sample_at':{}}))
        return runtime

    def test_log_export_ack_and_absence_precede_permanent_exact_removal(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory=Path(temporary);runtime=self.runtime(directory);order=[]
            def export(identity,role):
                order.append('export');folder=directory/'logs';folder.mkdir()
                (folder/'owned-log-manifest.json').write_text(json.dumps({'run_id':RUN,'containers':{ID:{'role':'worker2','retired':False,'exports':[]}}}))
            def docker(*args):
                self.assertEqual(args,('rm',ID));self.assertEqual(order,['export']);order.append('remove')
            record={'Name':'/own-worker2','State':{'Running':False},'Config':{'Labels':{actual.LABEL:RUN}}}
            with patch.object(actual,'inspect_owned',return_value=record),patch.object(actual.time,'time',return_value=102.),patch.object(runtime,'export_owned_log',side_effect=export),patch.object(actual,'docker',side_effect=docker),patch.object(actual.own_engine,'context_socket',return_value='/socket'),patch.object(actual.own_engine,'absent',return_value=True):
                runtime.remove_owned_container(ID,'worker2',optional_retirement=True)
            self.assertEqual(runtime.state['containers'],[]);self.assertEqual(runtime.state['retired_containers'],[ID]);self.assertIsNone(runtime.state['worker2_id'])
            events=[json.loads(line) for line in (directory/'container-lifecycle.jsonl').read_text().splitlines()]
            self.assertEqual(active_roles(events,900.,300,RUN)['worker2']['state'],'retired')
            event=events[-1];receipt=directory/event['receipt_path']
            self.assertEqual(actual.digest(receipt),event['receipt_file_sha256']);self.assertEqual(json.loads(receipt.read_text()),event['removal_receipt'])
            self.assertTrue(json.loads((directory/'logs/owned-log-manifest.json').read_text())['containers'][ID]['retired'])

    def test_fresh_but_pre_stop_ack_cannot_authorize_removal(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime=self.runtime(Path(temporary));ready=runtime.directory/'supervisor-ready.json'
            ready.write_text(json.dumps({'at':99.9,'role_ids':{},'source_sample_at':{}}))
            def wait(predicate,timeout):
                self.assertFalse(predicate());raise TimeoutError
            with patch.object(actual,'inspect_owned',return_value={'State':{'Running':False}}),patch.object(actual.time,'time',return_value=100.2),patch.object(runtime,'wait',side_effect=wait),patch.object(actual,'docker') as docker:
                with self.assertRaises(TimeoutError):runtime.remove_owned_container(ID,'worker2',optional_retirement=True)
                docker.assert_not_called()

    def test_required_role_or_missing_stop_ack_cannot_remove(self):
        with tempfile.TemporaryDirectory() as temporary:
            runtime=self.runtime(Path(temporary))
            with patch.object(actual,'docker') as docker:
                with self.assertRaises(ValueError):runtime.remove_owned_container(ID,'api',optional_retirement=True)
                record={'State':{'Running':False}}
                with patch.object(actual,'inspect_owned',return_value=record),patch.object(runtime,'wait',side_effect=TimeoutError):
                    with self.assertRaises(TimeoutError):runtime.remove_owned_container(ID,'worker2',optional_retirement=True)
                docker.assert_not_called()

class StreamedOwnedLogTests(unittest.TestCase):
    def run_export(self,limit):
        import os,threading,subprocess
        payload=b'own-safe-log\n'*12000
        temporary=tempfile.TemporaryDirectory();self.addCleanup(temporary.cleanup);directory=Path(temporary.name)
        runtime=object.__new__(actual.Runtime);runtime.directory=directory;runtime.state={'run_id':RUN,'project':'own','containers':[ID],'limits':{'logs_bytes':limit}}
        class Child:
            def __init__(self,args,**kwargs):
                self.args=args;read,write=os.pipe();self.stdout=os.fdopen(read,'rb',buffering=0)
                def send():
                    try:
                        for start in range(0,len(payload),8192):os.write(write,payload[start:start+8192])
                    except BrokenPipeError:pass
                    finally:os.close(write)
                self.thread=threading.Thread(target=send);self.thread.start()
            def poll(self):return None if self.thread.is_alive() else 0
            def wait(self,timeout):
                self.thread.join(timeout)
                if self.thread.is_alive():raise subprocess.TimeoutExpired('fake log',timeout)
                return 0
            def terminate(self):self.stdout.close()
            def kill(self):self.terminate()
        sizes=[];read=os.read
        def bounded_read(fd,size):sizes.append(size);return read(fd,size)
        record={'Name':'/own-worker2','State':{'Running':False}}
        with patch.object(actual,'inspect_owned',return_value=record),patch.object(actual.processes,'Process',Child),patch.object(actual.os,'read',side_effect=bounded_read):
            try:runtime.export_owned_log(ID,'worker2')
            except ValueError:return runtime,payload,sizes,False
        return runtime,payload,sizes,True

    def test_full_log_is_private_complete_hashed_and_streamed(self):
        runtime,payload,sizes,passed=self.run_export(1024**2)
        self.assertTrue(passed);self.assertTrue(sizes);self.assertLessEqual(max(sizes),32768)
        surfaces=runtime.declared_owned_logs();self.assertEqual(len(surfaces),1)
        self.assertEqual(surfaces[0].read_bytes(),payload);self.assertEqual(surfaces[0].stat().st_mode&0o777,0o600)
        manifest=json.loads((runtime.directory/'logs/owned-log-manifest.json').read_text());export=manifest['containers'][ID]['exports'][0]
        self.assertTrue(export['complete']);self.assertEqual(export['sha256'],hashlib.sha256(payload).hexdigest())

    def test_stream_stops_at_original_log_budget_without_complete_claim(self):
        runtime,payload,sizes,passed=self.run_export(65536)
        self.assertFalse(passed);self.assertFalse((runtime.directory/'logs/owned-log-manifest.json').exists())
        self.assertLessEqual(sum(p.stat().st_size for p in (runtime.directory/'logs').glob('*.log')),65536)

if __name__=='__main__':unittest.main()
