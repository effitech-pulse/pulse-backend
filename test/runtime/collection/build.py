#!/usr/bin/env python3
"""Offline static builds from explicit frozen file hashes; immutable old git archive."""
from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import time
from typing import Any

from harness import command, save

BASE = '7fc37f6e737c252b37d6deef644ff444ba6f89a1'


def digest(path: Path) -> str:
    digest=hashlib.sha256()
    with path.open('rb') as stream:
        while chunk:=stream.read(65536):digest.update(chunk)
    return digest.hexdigest()


def verify_freeze(repo: Path, freeze: dict[str, Any]) -> None:
    for name, expected in freeze['files'].items():
        path = (repo / name).resolve()
        expected=expected['sha256'] if isinstance(expected,dict) else expected
        if not path.is_relative_to(repo.resolve()) or digest(path) != expected:
            raise ValueError('source changed after freeze: ' + name)


def build(repo: Path, output: Path, go: Path, contracts: Path, freeze_path: Path) -> None:
    """Не читает .env, не устанавливает зависимости, oldAPI не использует текущий backend."""
    freeze = json.loads(freeze_path.read_text())
    if freeze.get('base') != BASE or freeze.get('status') != 'FROZEN':
        raise ValueError('explicit combined FROZEN source required')
    verify_freeze(repo, freeze)
    output.mkdir(parents=True, exist_ok=False)
    workspace = output / 'build.work'
    workspace.write_text('go 1.25.0\n\nuse '+str(repo.resolve())+'\n\nreplace github.com/effitech-pulse/pulse-contracts => '+str(contracts.resolve())+'\n')
    environment = dict(os.environ, CGO_ENABLED='0', GOPROXY='off', GOSUMDB='off', GOFLAGS='-mod=readonly',
                       GOTOOLCHAIN='local', GOCACHE='/private/tmp/pulse-go-cache', GOWORK=str(workspace.resolve()))
    result: dict[str, Any] = {'base': BASE, 'freeze_sha256': digest(freeze_path), 'binaries': {}}
    for platform in ('darwin', 'linux'):
        for name, package in [('api', './cmd/api'), ('collection-worker', './cmd/collection-worker'),
                              ('fixture', './test/runtime/collection/fixture'), ('seed','./test/runtime/collection/seed'), ('legacy-probe','./test/runtime/collection/legacy-probe')]:
            target = output / (platform + '-' + name)
            env = dict(environment, GOOS=platform, GOARCH='arm64')
            invocation = [str(go), 'build', '-tags=timetzdata', '-trimpath', '-ldflags=-s -w', '-o', str(target.resolve()), package]
            completed = subprocess.run(invocation, cwd=repo, env=env, capture_output=True, timeout=180)
            if completed.returncode:
                save(output / ('build-failure-'+str(time.time_ns())+'.json'), {'platform':platform,'binary':name,'exit_code':completed.returncode})
                raise RuntimeError('offline build failed: '+platform+' '+name)
            result['binaries'][platform+'-'+name]={'sha256':digest(target),'bytes':target.stat().st_size}
    archive = subprocess.run(['git','archive','--format=tar',BASE],cwd=repo,capture_output=True,check=True).stdout
    result['old_source_archive_sha256']=hashlib.sha256(archive).hexdigest()
    old = output / 'immutable-old'
    old.mkdir()
    with tarfile.open(fileobj=io.BytesIO(archive)) as source:
        source.extractall(old, filter='data')
    old_work = output / 'old.work'
    old_work.write_text('go 1.25.0\n\nuse '+str(old.resolve())+'\n\nreplace github.com/effitech-pulse/pulse-contracts => '+str(contracts.resolve())+'\n')
    target = output / 'linux-old-api'
    subprocess.run([str(go),'build','-tags=timetzdata','-trimpath','-ldflags=-s -w','-o',str(target.resolve()),'./cmd/api'],
                   cwd=old,env=dict(environment,GOWORK=str(old_work.resolve()),GOOS='linux',GOARCH='arm64'),check=True,capture_output=True,timeout=180)
    result['binaries']['linux-old-api']={'sha256':digest(target),'bytes':target.stat().st_size,'source':BASE,'workspace':str(old_work)}
    verify_freeze(repo, freeze)
    save(output / 'binary-freeze.json', result)
    print(json.dumps({'status':'BUILT','manifest':str(output/'binary-freeze.json')}))


def main() -> None:
    parser=argparse.ArgumentParser(description=__doc__)
    for name in ('repo','output','go','contracts','freeze'):
        parser.add_argument('--'+name,type=Path,required=True)
    options=parser.parse_args()
    build(options.repo,options.output,options.go,options.contracts,options.freeze)


if __name__=='__main__':
    main()
