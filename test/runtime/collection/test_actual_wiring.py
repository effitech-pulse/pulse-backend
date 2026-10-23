"""Проверки ACT orchestration без Docker, сети и PostgreSQL."""
from __future__ import annotations

import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from urllib.error import HTTPError
from types import SimpleNamespace
from typing import Callable
from unittest.mock import patch

import actual
import harness
from proof import GLOBAL_METRICS, validate_resource_span


class LifecycleWiringTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.runtime = object.__new__(actual.Runtime)
        self.runtime.directory = Path(self.temporary.name)
        self.runtime.state = {
            'run_id': 'synthetic-run', 'project': 'synthetic-project',
            'containers': ['postgres-owned', 'rabbit-owned', 'fixture-owned', 'api-owned'],
            'networks': ['network-owned'], 'runtime_image_id': 'image-owned',
        }
        self.physical = {identity: True for identity in self.runtime.state['containers']}
        self.commands: list[tuple[str, ...]] = []
        self.now = 9.
        self.kill_stops = True
        self.start_runs = True
        self.write_state()
        for module in (actual, harness):
            self.enterContext(patch.object(module, 'docker', side_effect=self.docker))
        self.enterContext(patch.object(actual.time, 'time', side_effect=lambda: self.now))

    def write_state(self) -> None:
        (self.runtime.directory / 'state.json').write_text(json.dumps(self.runtime.state))

    def docker(self, *args: str) -> str:
        """Заменяет только внешний CLI; ownership и lifecycle остаются настоящими."""
        self.commands.append(args)
        if args[0] == 'inspect':
            identity = args[1]
            return json.dumps([{'Id': identity,
                                'Config': {'Labels': {harness.LABEL: 'synthetic-run'}},
                                'State': {'Running': self.physical[identity]}}])
        if args[0] == 'run':
            self.physical['worker-owned'] = True
            return 'worker-owned'
        if args[:2] == ('kill', '--signal'):
            if args[2] in ('KILL', 'TERM'):
                persisted = json.loads((self.runtime.directory / 'state.json').read_text())
                self.assertIn(args[3], persisted['expected_stopped'])
                self.physical[args[3]] = not self.kill_stops
            return ''
        if args[0] == 'start':
            self.physical[args[1]] = self.start_runs
            return args[1]
        raise AssertionError('unexpected external command: ' + repr(args))

    def events(self) -> list[dict]:
        path = self.runtime.directory / 'container-lifecycle.jsonl'
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_observed_stop_records_physical_identity_and_bounded_window(self) -> None:
        """Потеря runtime LIMITS ломает первый подтверждённый stop."""
        with tempfile.TemporaryDirectory() as temporary:
            runtime = object.__new__(actual.Runtime)
            runtime.directory = Path(temporary)
            runtime.state = {'worker1_id': 'worker-owned'}
            with patch.object(actual, 'inspect_owned', return_value={'State': {'Running': False}}), \
                    patch.object(actual.time, 'time', return_value=1000.):
                runtime.observe_role('worker1', 'worker-owned', 'stopped')
            records = [json.loads(line) for line in
                       (runtime.directory / 'container-lifecycle.jsonl').read_text().splitlines()]
            self.assertEqual(records, [{'at': 1000., 'role': 'worker1', 'id': 'worker-owned',
                                        'state': 'stopped', 'observed': True,
                                        'stop_deadline_at': 1300.}])

    def test_worker_start_signal_restart_events_supply_resource_proof(self) -> None:
        """Пропуск launch/stop/restart handoff делает реальный resource span неполным."""
        for role in ('postgres', 'rabbit', 'fixture', 'api'):
            self.runtime.observe_role(role, role + '-owned', 'running')
        self.runtime.start_worker('worker1')
        self.now = 11.5
        self.runtime.signal('worker1', 'KILL')
        self.assertEqual(self.runtime.state['expected_stopped'], ['worker-owned'])
        self.now = 12.5
        self.runtime.start_worker('worker1')
        self.assertEqual(self.runtime.state['expected_stopped'], [])
        persisted = json.loads((self.runtime.directory / 'state.json').read_text())
        self.assertEqual(persisted['worker1_id'], 'worker-owned')
        self.assertEqual(persisted['containers'].count('worker-owned'), 1)
        self.assertEqual(persisted['expected_stopped'], [])
        events = self.events()
        self.assertEqual(events[-3:], [
            {'at': 9., 'role': 'worker1', 'id': 'worker-owned', 'state': 'running', 'observed': True},
            {'at': 11.5, 'role': 'worker1', 'id': 'worker-owned', 'state': 'stopped',
             'observed': True, 'stop_deadline_at': 311.5},
            {'at': 12.5, 'role': 'worker1', 'id': 'worker-owned', 'state': 'running', 'observed': True},
        ])
        launches = [command for command in self.commands if command[0] == 'run']
        self.assertEqual(len(launches), 1)
        for flag, value in (('--memory', '256m'), ('--memory-swap', '256m'),
                            ('--entrypoint', '/collection-worker')):
            self.assertEqual(launches[0][launches[0].index(flag) + 1], value)
        samples = []
        for at in (10., 11., 12., 13., 14.):
            roles = ['postgres', 'rabbit', 'fixture', 'api']
            if at != 12.:
                roles.append('worker1')
            measured = {key: 0 for key in GLOBAL_METRICS}
            measured.update({role + '_bytes': 100 for role in roles})
            samples.append({'at': at, 'measured': measured, 'breaches': [],
                            'cgroup_peaks': {role + '_bytes': 100 for role in roles},
                            'role_ids': {role: ('worker-owned' if role == 'worker1' else role + '-owned')
                                         for role in roles},
                            'source_sample_at': {role: at * 1e9 for role in roles}})
        trace = [event for pid, role in enumerate(('actual', 'supervisor', 'forwarder'), 1)
                 for event in ({'at': 9., 'kind': 'start', 'pid': pid, 'role': role},
                               {'at': 14., 'kind': 'peak', 'pid': pid, 'role': role, 'peak_bytes': 100})]
        result = validate_resource_span(samples, start=10., end=14., lifecycle=events,
                                        stops=[], trace=trace, limits=harness.LIMITS)
        self.assertTrue(result['full_span_verified'])
        self.assertEqual(result['samples'], 5)
        self.assertEqual(result['full_peak_aggregate_bytes'], 800)

    def test_unobserved_stop_never_writes_lifecycle_evidence(self) -> None:
        """Ожидаемая остановка не доказывает физическую остановку."""
        with self.assertRaisesRegex(AssertionError, 'not physically observed'):
            self.runtime.observe_role('worker1', 'api-owned', 'stopped')
        self.assertEqual(self.events(), [])

    def test_failed_restart_keeps_expected_stop_without_running_evidence(self) -> None:
        """Неудачный docker start не очищает ожидание и не создаёт running proof."""
        self.runtime.state.update(worker1_id='worker-owned', expected_stopped=['worker-owned'])
        self.runtime.state['containers'].append('worker-owned')
        self.physical['worker-owned'] = False
        self.write_state()
        self.start_runs = False
        with self.assertRaisesRegex(AssertionError, 'not physically observed'):
            self.runtime.restart('worker1')
        self.assertEqual(self.events(), [])
        persisted = json.loads((self.runtime.directory / 'state.json').read_text())
        self.assertEqual(persisted['expected_stopped'], ['worker-owned'])

    def test_pause_and_continue_do_not_claim_physical_stop(self) -> None:
        """SIGSTOP/CONT оставляют Running=true и требуют непрерывных resource counters."""
        self.runtime.state['worker1_id'] = 'api-owned'
        self.write_state()
        for signal in ('STOP', 'CONT'):
            self.runtime.signal('worker1', signal)
        self.assertEqual(self.events(), [])
        self.assertNotIn('expected_stopped', self.runtime.state)


class CaseReceiptWiringTests(unittest.TestCase):
    def lost_after_receipt(self, *, operation_fails: bool) -> dict:
        """Исполняет настоящий receipt writer при потере заключительного PG snapshot."""
        with tempfile.TemporaryDirectory() as temporary:
            runtime = object.__new__(actual.Runtime)
            runtime.directory = Path(temporary)
            runtime.state = {'run_id': 'synthetic-run', 'containers': ['api-owned']}
            before = {'values': {'count': 7, 'digest': 'before-digest'}}
            canary = 'private-db-error-canary'

            def operation() -> None:
                runtime.facts(barrier_reached=True)
                if operation_fails:
                    raise ValueError(canary)

            with patch.object(runtime, 'snapshot', side_effect=[before, OSError(canary)]), \
                    patch.object(actual, 'inspect_owned', return_value={'State': {'Running': False}}), \
                    patch.object(actual, 'docker', return_value=''), \
                    patch.object(actual.time, 'time', return_value=1000.), \
                    patch.object(actual.time, 'time_ns', return_value=1000000000000):
                with self.assertRaises((OSError, RuntimeError)):
                    runtime.run_case('A01', operation)
            receipts = list(runtime.directory.glob('A01-*.json'))
            self.assertEqual(len(receipts), 1, 'after-snapshot failure must preserve one case receipt')
            record = json.loads(receipts[0].read_text())
            self.assertEqual(record['before'], before)
            self.assertIsNone(record['after'], 'missing snapshot must not become invented state')
            self.assertEqual(record['evidence'], {'barrier_reached': True})
            self.assertEqual(record['case'], 'A01')
            self.assertEqual(record['run_id'], 'synthetic-run')
            self.assertEqual(record['snapshot_error_type'], 'OSError')
            self.assertNotIn(canary, ''.join(path.read_text() for path in runtime.directory.iterdir()))
            clock = json.loads((runtime.directory / 'actual-started.json').read_text())
            self.assertNotIn('case_started_at', clock)
            return record

    def test_missing_after_snapshot_persists_unknown_case_receipt(self) -> None:
        """Успешная операция с недоступным after не может стать PASS или потерять receipt."""
        record = self.lost_after_receipt(operation_fails=False)
        self.assertEqual(record['status'], 'UNKNOWN')
        self.assertEqual(record['reason'], 'OSError')
        self.assertIsNone(record['operation_error_type'])

    def test_missing_after_snapshot_preserves_original_operation_failure(self) -> None:
        """Ошибка snapshot не заменяет исходный FAIL и безопасный класс ошибки операции."""
        record = self.lost_after_receipt(operation_fails=True)
        self.assertEqual(record['status'], 'FAIL')
        self.assertEqual(record['reason'], 'ValueError')
        self.assertEqual(record['operation_error_type'], 'ValueError')


class FinishLogSurfaceTests(unittest.TestCase):
    def test_finish_rejects_log_manifest_that_differs_from_owned_history(self) -> None:
        """Недостающий retired export и посторонний ID запрещены до canary scan."""
        for identities in ((), ('api-retired', 'foreign-id')):
            with self.subTest(identities=identities), tempfile.TemporaryDirectory() as temporary:
                runtime = object.__new__(actual.Runtime)
                runtime.directory = Path(temporary)
                runtime.state = {'run_id': 'synthetic-run', 'containers': [],
                                 'retired_containers': ['api-retired']}
                logs = runtime.directory / 'logs'
                logs.mkdir()
                manifest = {'version': 1, 'run_id': 'synthetic-run', 'containers': {}}
                for identity in identities:
                    exported = logs / (identity + '-full-1.log')
                    exported.write_bytes(b'clean owned export\n')
                    manifest['containers'][identity] = {
                        'role': 'api', 'retired': True,
                        'exports': [{'path': 'logs/' + exported.name, 'bytes': exported.stat().st_size,
                                     'sha256': hashlib.sha256(exported.read_bytes()).hexdigest(),
                                     'complete': True}],
                    }
                (logs / 'owned-log-manifest.json').write_text(json.dumps(manifest))
                (runtime.directory / 'canary').write_bytes(b'private-manifest-canary-1234')
                with patch.object(runtime, 'snapshot', return_value={}), \
                        patch.object(runtime, 'counters', return_value={'ml_rpc_calls': 0}), \
                        patch.object(runtime, 'query', return_value=[]), \
                        patch.object(runtime, 'management', return_value=[]), \
                        patch('scan.scan', side_effect=AssertionError('incomplete ownership reached scan')) as scanner:
                    with self.assertRaises(ValueError):
                        runtime.finish()
                self.assertEqual(scanner.call_count, 0)

    def test_finish_detects_canary_in_retired_owned_container_log(self) -> None:
        """A10 retirement не может скрыть утечку в накопленном supervisor log."""
        from scan import scan as real_scan

        with tempfile.TemporaryDirectory() as temporary:
            runtime = object.__new__(actual.Runtime)
            runtime.directory = Path(temporary)
            runtime.state = {'run_id': 'synthetic-run', 'containers': ['api-current'],
                             'retired_containers': ['api-retired']}
            logs = runtime.directory / 'logs'
            logs.mkdir()
            retired_log = logs / 'api-retired.log'
            canary = b'private-retired-log-canary-1234'
            retired_log.write_bytes(b'old API log: ' + canary + b'\n')
            for name in ('api-current.log', 'api-current-full-1.log', 'api-retired-full-1.log'):
                (logs / name).write_text('clean complete owned log export\n')
            manifest = {'version': 1, 'run_id': 'synthetic-run', 'containers': {}}
            for identity in ('api-current', 'api-retired'):
                exported = logs / (identity + '-full-1.log')
                manifest['containers'][identity] = {
                    'role': 'api', 'retired': identity == 'api-retired',
                    'exports': [{'path': 'logs/' + exported.name, 'bytes': exported.stat().st_size,
                                 'sha256': hashlib.sha256(exported.read_bytes()).hexdigest(),
                                 'complete': True}],
                }
            (logs / 'owned-log-manifest.json').write_text(json.dumps(manifest))
            (runtime.directory / 'canary').write_bytes(canary + b'\n')
            scanned: list[Path] = []
            reports: list[dict] = []

            def scan_boundary(secret: bytes, paths: list[Path]) -> dict:
                scanned.extend(paths)
                report = real_scan(secret, paths)
                reports.append(report)
                if report['status'] == 'PASS':
                    # Stop before unrelated resource/teardown paths on broken code.
                    raise AssertionError('finish scan accepted a leaked retired log')
                return report

            def docker_logs(arguments: list[str], **kwargs: object) -> SimpleNamespace:
                self.assertEqual(arguments, ['docker', 'logs', '--timestamps', 'api-current'])
                return SimpleNamespace(stdout='clean current logs\n', stderr='')

            with patch.object(runtime, 'snapshot', return_value={}), \
                    patch.object(runtime, 'counters', return_value={'ml_rpc_calls': 0}), \
                    patch.object(runtime, 'query', return_value=[]), \
                    patch.object(runtime, 'management', return_value=[]), \
                    patch.object(runtime, 'export_owned_log', return_value=None, create=True), \
                    patch.object(actual, 'inspect_owned', return_value={'State': {'Running': True}}), \
                    patch.object(actual.processes, 'run', side_effect=docker_logs), \
                    patch('scan.scan', side_effect=scan_boundary):
                with self.assertRaisesRegex(AssertionError, 'canary leaked') as caught:
                    runtime.finish()
            self.assertIn(retired_log, scanned)
            self.assertEqual(len(reports), 1)
            self.assertEqual(reports[0]['status'], 'FAIL')
            self.assertGreaterEqual(reports[0]['matches'], 1)
            self.assertNotIn(canary.decode(), str(reports[0]))
            self.assertNotIn(canary.decode(), str(caught.exception))
            self.assertEqual(list(runtime.directory.glob('A11-*.json')), [])


class SafeFaultDiagnosticsTests(unittest.TestCase):
    CANARY = 'synthetic-diagnostics-secret-987654'

    def http_error(self, code: int) -> HTTPError:
        secret = self.CANARY
        return HTTPError('https://user:' + secret + '@invalid.test/private?token=' + secret,
                         code, 'private message ' + secret, {'X-Private': secret},
                         io.BytesIO(('private response ' + secret).encode()))

    def receipt(self, operation: Callable[[], None], *, after_error: Exception | None = None) -> dict:
        with tempfile.TemporaryDirectory() as temporary:
            runtime = object.__new__(actual.Runtime)
            runtime.directory = Path(temporary)
            runtime.state = {'run_id': 'synthetic-run', 'containers': []}
            snapshots = 0

            def after_snapshot_failure() -> dict:
                nonlocal snapshots
                snapshots += 1
                if snapshots > 1 and after_error is not None:
                    raise after_error
                return {'values': {'count': 7, 'digest': 'safe-digest'}}

            with patch.object(runtime, 'snapshot', side_effect=after_snapshot_failure), \
                    patch.object(actual.time, 'time', return_value=1000.), \
                    patch.object(actual.time, 'time_ns', return_value=1000000000000):
                with self.assertRaises(RuntimeError):
                    runtime.run_case('A06', operation)
            paths = list(runtime.directory.glob('A06-*.json'))
            self.assertEqual(len(paths), 1)
            serialized = paths[0].read_text()
            for forbidden in (self.CANARY, 'https://', 'invalid.test', 'private response',
                              'private message', 'X-Private'):
                self.assertFalse(forbidden in serialized, 'receipt contains private exception material')
            clock = json.loads((runtime.directory / 'actual-started.json').read_text())
            self.assertNotIn('case_started_at', clock)
            self.assertFalse((runtime.directory / '.actual-clock.tmp').exists())
            return json.loads(serialized)

    def assert_safe_frames(self, record: dict, field: str, origin: str) -> None:
        frames = record.get(field)
        self.assertIsInstance(frames, list, 'safe origin frames must be persisted')
        self.assertGreater(len(frames), 0)
        self.assertLessEqual(len(frames), 32)
        for frame in frames:
            self.assertEqual(set(frame), {'file', 'function', 'line'})
            self.assertIsInstance(frame['file'], str)
            self.assertEqual(frame['file'], Path(frame['file']).name)
            self.assertFalse('/' in frame['file'] or '\\' in frame['file'])
            self.assertIsInstance(frame['function'], str)
            self.assertTrue(frame['function'])
            self.assertIs(type(frame['line']), int)
            self.assertGreater(frame['line'], 0)
        self.assertTrue(any(frame['file'] == 'test_actual_wiring.py' and frame['function'] == origin
                            for frame in frames), 'original failing function must remain identifiable')

    def test_deep_http_operation_persists_bounded_safe_origin_and_status(self) -> None:
        error = self.http_error(503)

        def deep_http_failure(depth: int = 45) -> None:
            if depth:
                deep_http_failure(depth - 1)
            else:
                raise error

        record = self.receipt(deep_http_failure)
        self.assertEqual(record['operation_error_type'], 'HTTPError')
        self.assert_safe_frames(record, 'operation_error_frames', 'deep_http_failure')
        self.assertEqual(record.get('operation_http_status'), 503)
        self.assertIs(type(record['operation_http_status']), int)
        self.assertEqual(record.get('snapshot_error_frames'), [])
        self.assertIsNone(record.get('snapshot_http_status'))

    def test_after_snapshot_http_failure_keeps_separate_operation_origin(self) -> None:
        def original_operation_failure() -> None:
            raise ValueError(self.CANARY)

        record = self.receipt(original_operation_failure, after_error=self.http_error(502))
        self.assertEqual(record['status'], 'FAIL')
        self.assertEqual(record['reason'], 'ValueError')
        self.assertEqual(record['operation_error_type'], 'ValueError')
        self.assert_safe_frames(record, 'operation_error_frames', 'original_operation_failure')
        self.assertIsNone(record.get('operation_http_status'))
        self.assertEqual(record['snapshot_error_type'], 'HTTPError')
        self.assert_safe_frames(record, 'snapshot_error_frames', 'after_snapshot_failure')
        self.assertEqual(record.get('snapshot_http_status'), 502)
        self.assertIs(type(record['snapshot_http_status']), int)
        self.assertIsNone(record['after'])

    def test_out_of_range_http_codes_are_not_persisted_as_status(self) -> None:
        for code in (99, 600):
            with self.subTest(code=code):
                error = self.http_error(code)

                def invalid_status_failure() -> None:
                    raise error

                record = self.receipt(invalid_status_failure)
                self.assert_safe_frames(record, 'operation_error_frames', 'invalid_status_failure')
                self.assertIsNone(record.get('operation_http_status'))


if __name__ == '__main__':
    unittest.main()
