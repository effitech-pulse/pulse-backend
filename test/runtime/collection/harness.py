#!/usr/bin/env python3
"""Изолированный opt-in стенд C2; результаты и секреты остаются вне checkout."""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import time
import processes
from typing import Any

LABEL = 'pulse.q2c2.run_id'
MIB = 1024**2
LIMITS = {'api_bytes': 128*MIB, 'worker1_bytes': 256*MIB, 'worker2_bytes': 256*MIB,
          'fixture_bytes': 64*MIB, 'postgres_bytes': 384*MIB, 'rabbit_bytes': 384*MIB,
          'harness_max_process_bytes': 64*MIB, 'aggregate_bytes': 1536*MIB, 'database_bytes': 256*MIB, 'pgdata_bytes': 768*MIB,
          'wal_delta_bytes': 256*MIB, 'wal_bytes': 512*MIB, 'temp_delta_bytes': 16*MIB,
          'stand_bytes': 2048*MIB, 'fixture_files_bytes': 16*MIB, 'transient_bytes': 32*MIB,
          'logs_bytes': 64*MIB, 'evidence_bytes': 64*MIB, 'suite_seconds': 2700, 'case_seconds': 300}
MODULE_LIMITS = dict(LIMITS, postgres_bytes=512*MIB, database_bytes=512*MIB, pgdata_bytes=2048*MIB, wal_delta_bytes=1024*MIB, stand_bytes=4096*MIB, case_seconds=600, suite_seconds=600)
IMAGES = {'postgres': 'sha256:ca0bd484cb98bf4b24eb1010e73fb3fcbd6714d240fbc1a10eea5b7dbecb641d',
          'rabbit': 'sha256:3e5cc23235b0eb850f604e8ace10d9a9b6d05be3e36730e53522ee9ead707a9e'}
PORTS = (8094, 8093, 55442, 5675, 15675)  # coordinator execution ruling: occupied8092 preserved


def owned_id(record: dict[str, Any], run: str, ids: list[str]) -> str:
    """Запрещает управление чужим/неучтённым контейнером даже при совпадении имени."""
    identity = record['Id']
    if identity not in ids or record.get('Config', {}).get('Labels', {}).get(LABEL) != run:
        raise ValueError('unknown or unowned container')
    return identity


def breaches(measured: dict[str, int | float], limits: dict[str, int] | None = None) -> list[str]:
    """Неизвестные метрики не считаются успешно измеренными."""
    limits = LIMITS if limits is None else limits
    if set(measured) - limits.keys():
        raise ValueError('unknown resource measurement')
    return [key for key, value in measured.items() if value > limits[key]]


def verify_baseline(baseline: list[str], current: list[dict[str, str]]) -> None:
    actual = {row['Names']: row['ID'] for row in current}
    for entry in baseline:
        name, prefix, *_ = entry.split()
        if not actual.get(name, '').startswith(prefix):
            raise ValueError('working container identity changed: ' + name)


def command(args: list[str], *, data: str | None = None, timeout: int = 30) -> str:
    """Не выводит stderr команды, способный содержать секреты."""
    result = processes.run(args, input=data, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError('command failed: ' + args[0] + ' ' + args[1] + f' (exit {result.returncode})')
    return result.stdout.strip()


def docker(*args: str, data: str | None = None) -> str:
    return command(['docker', *args], data=data)


def save(path: Path, value: Any, *, private: bool = False) -> None:
    """Создаёт новый файл, сохраняя failed attempts для последующего разбора."""
    mode = 0o600 if private else 0o644
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    with os.fdopen(fd, 'w') as stream:
        if isinstance(value, str):
            stream.write(value)
        else:
            json.dump(value, stream, indent=2)
            stream.write('\n')


def inventory() -> list[dict[str, str]]:
    return [json.loads(line) for line in docker('ps', '--no-trunc', '--format', '{{json .}}').splitlines()]


def inspect_owned(state: dict[str, Any], identity: str) -> dict[str, Any]:
    record = json.loads(docker('inspect', identity))[0]
    owned_id(record, state['run_id'], state['containers'])
    return record


def preflight(baseline_path: Path, *, infrastructure_only: bool = False) -> dict[str, Any]:
    baseline = json.loads(baseline_path.read_text())
    current = inventory()
    verify_baseline(baseline['working_containers'], current)
    for image in IMAGES.values():
        if docker('image', 'inspect', '--format', '{{.Id}}', image) != image:
            raise ValueError('cached image missing or changed')
    ports = (55442, 5675, 15675) if infrastructure_only else PORTS
    for port in ports:
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', port))
    return {'working_before': current, 'cached_image_metadata': IMAGES, 'ports_checked': list(ports), 'infrastructure_only': infrastructure_only}


def provision(directory: Path, baseline: Path, *, profile: str = "actual") -> None:
    directory.mkdir(parents=True, exist_ok=True)
    if (directory / 'state.json').exists():
        raise ValueError('state exists; refusing duplicate provision')
    checks = preflight(baseline, infrastructure_only=True)
    run = 'r' + secrets.token_hex(6)
    project = 'pulse-q2c2-' + run
    password = secrets.token_hex(24)
    save(directory / 'postgres.env', f'POSTGRES_USER=collection_test\nPOSTGRES_PASSWORD={password}\nPOSTGRES_DB=collection_test\n', private=True)
    save(directory / '90-collection-test-metrics.conf', 'management_agent.disable_metrics_collector = false\n')
    save(directory / 'rabbit.env', f'RABBITMQ_DEFAULT_USER=collection_test\nRABBITMQ_DEFAULT_PASS={password}\nRABBITMQ_SERVER_ADDITIONAL_ERL_ARGS=+S 2:2 +A 4\n', private=True)
    save(directory / 'test-database-url', f'postgres://collection_test:{password}@127.0.0.1:55442/collection_test?sslmode=disable\n', private=True)
    state: dict[str, Any] = {'run_id': run, 'project': project, 'containers': [], 'volumes': [],
                             'networks': [], 'owned_image_ids': [], 'created_at': dt.datetime.now(dt.timezone.utc).isoformat(),
                             'baseline': str(baseline.resolve()), 'profile': profile, 'limits': MODULE_LIMITS if profile=='module' else LIMITS, **checks}
    def checkpoint() -> None:
        # Atomic manifest publication; attempts and samples are append-only separately.
        temporary = directory / '.state.tmp'
        temporary.write_text(json.dumps(state, indent=2) + '\n')
        os.replace(temporary, directory / 'state.json')
    checkpoint()
    try:
        network = docker('network', 'create', '--internal', '--label', f'{LABEL}={run}', project)
        state['networks'].append(network)
        checkpoint()
        for role in ('postgres', 'rabbit'):
            volume = project + '-' + role
            docker('volume', 'create', '--label', f'{LABEL}={run}', volume)
            state['volumes'].append(volume)
            checkpoint()
            common = ['run', '-d', '--pull=never', '--name', project + '-' + role,
                      '--label', f'{LABEL}={run}', '--network', network, '--network-alias', role,
                      '--memory', ('512m' if profile=='module' and role=='postgres' else '384m'), '--memory-swap', ('512m' if profile=='module' and role=='postgres' else '384m'), '--cpus', '1', '--pids-limit', '256',
                      '--log-driver', 'local', '--log-opt', 'max-size=8m', '--log-opt', 'max-file=2',
                      '--env-file', str((directory / (role + '.env')).resolve())]
            if role == 'postgres':
                args = ['-v', volume + ':/var/lib/postgresql/data',
                        IMAGES[role], '-c', 'shared_buffers=32MB', '-c', 'max_connections=40',
                        '-c', 'temp_file_limit=16384', '-c', 'max_wal_size=128MB', '-c', 'min_wal_size=32MB']
            else:
                args = ['--mount', 'type=bind,src='+str((directory/'90-collection-test-metrics.conf').resolve())+',dst=/etc/rabbitmq/conf.d/90-collection-test-metrics.conf,readonly',
                        '-v', volume + ':/var/lib/rabbitmq', IMAGES[role]]
            identity = docker(*common, *args)
            state['containers'].append(identity)
            state[role + '_id'] = identity
            checkpoint()
        save(directory / 'provision-result.json', {'status': 'CREATED', 'run_id': run, 'containers': state['containers'],
                                                   'acceptance': 'NOT RUN', 'database_url_file': str(directory / 'test-database-url')})
        print(json.dumps({'status': 'CREATED', 'run_id': run, 'state': str(directory / 'state.json'),
                          'database_url_file': str(directory / 'test-database-url'), 'containers': state['containers']}))
    except Exception as error:
        save(directory / ('failure-' + str(time.time_ns()) + '.json'), {'phase': 'provision', 'error_type': type(error).__name__})
        raise


def teardown(directory: Path) -> None:
    state = json.loads((directory / 'state.json').read_text())
    # Graceful supervisor exit is a declared lifecycle event, not missing metrics.
    (directory/'supervisor-stop-request').touch(exist_ok=True)
    for identity in state['containers']:
        inspect_owned(state, identity)
    for kind, key in [('network', 'networks'), ('volume', 'volumes')]:
        for identity in state[key]:
            record = json.loads(docker(kind, 'inspect', identity))[0]
            if record.get('Labels', {}).get(LABEL) != state['run_id']:
                raise ValueError('unowned ' + kind)
    for identity in state.get('owned_image_ids',[]):
        record=json.loads(docker('image','inspect',identity))[0]
        if record.get('Config',{}).get('Labels',{}).get(LABEL)!=state['run_id']:raise ValueError('unowned image')
    for identity in state['containers']:
        docker('rm', '-f', identity)
    for identity in state['networks']:
        docker('network', 'rm', identity)
    for identity in state['volumes']:
        docker('volume', 'rm', identity)
    for identity in state.get('owned_image_ids',[]):
        docker('image','rm',identity)
    current = inventory()
    verify_baseline(json.loads(Path(state['baseline']).read_text())['working_containers'], current)
    save(directory / ('teardown-' + str(time.time_ns()) + '.json'), {'status': 'REMOVED', 'working_after': current})


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['provision', 'teardown', 'preflight'])
    parser.add_argument('--state-dir', type=Path, required=True)
    parser.add_argument('--baseline', type=Path)
    parser.add_argument('--profile', choices=['actual','module'], default='actual')
    parser.add_argument('--allow-isolated-mutation', action='store_true')
    options = parser.parse_args()
    processes.track_self(options.state_dir,'harness-control')
    if options.operation == 'preflight':
        print(json.dumps(preflight(options.baseline), indent=2))
    elif not options.allow_isolated_mutation:
        parser.error('explicit --allow-isolated-mutation required')
    elif options.operation == 'provision':
        provision(options.state_dir, options.baseline, profile=options.profile)
    else:
        teardown(options.state_dir)


if __name__ == '__main__':
    main()
