#!/usr/bin/env python3
"""Проверяет declared export/log surfaces; не выводит canary или найденные строки."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def scan(canary: bytes, paths: list[Path]) -> dict[str, Any]:
    if len(canary)<16:
        raise ValueError('unique synthetic canary must have at least16bytes')
    matches=0
    scanned=0
    files=[]
    for path in paths:
        if path.is_symlink() or not path.is_file():
            raise ValueError('surface missing or symlink')
        count=0
        tail=b''
        with path.open('rb') as source:
            while data:=source.read(65536):
                chunk=tail+data
                count+=chunk.count(canary)
                tail=chunk[-(len(canary)-1):]
        scanned+=path.stat().st_size
        matches+=count
        files.append({'surface':path.name,'matches':count,'bytes':path.stat().st_size})
    if not files:
        raise ValueError('no declared surfaces; cannot claim zero leakage')
    return {'status':'FAIL' if matches else 'PASS','matches':matches,'scanned_bytes':scanned,'surfaces':files}


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--canary-file',type=Path,required=True)
    parser.add_argument('--surface',type=Path,action='append',required=True)
    options=parser.parse_args()
    print(json.dumps(scan(options.canary_file.read_bytes().strip(),options.surface),indent=2))
