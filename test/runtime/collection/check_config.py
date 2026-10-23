#!/usr/bin/env python3
"""Проверяет generatedC2 URLs реальным frozen config.Load без изменения Go sources/PG."""
from __future__ import annotations
import argparse
import json
import os
from pathlib import Path
import subprocess

from broker import broker_urls
from harness import save

GO_TEST=r'''package collection_test
import (
 "encoding/json"
 "os"
 "strings"
 "testing"
 "github.com/effitech-pulse/pulse-backend/internal/config"
)
func TestGeneratedCollectionBrokerURLsAcceptedByFrozenConfig(t *testing.T){
 raw,err:=os.ReadFile(os.Getenv("Q2C2_GENERATED_URLS"));if err!=nil{t.Fatal(err)}
 var urls map[string]string;if json.Unmarshal(raw,&urls)!=nil{t.Fatal("fixture invalid")}
 // Do not read any product/shared .env, and do not connect to PG/broker.
 if err=os.Chdir(t.TempDir());err!=nil{t.Fatal(err)}
 defaults:=map[string]string{"APP_SCOPE":"research","AUTH_MODE":"local","ML_AUTORUN_ENABLED":"false","DB_POOL_SIZE":"5","COHORT_JOB_MODE":"off","FILE_CHECK_MODE":"off","FILE_CHECK_ALLOW_REAL":"false","COHORT_JOB_ALLOW_REAL":"false","COLLECTION_JOB_MODE":"synthetic","COLLECTION_JOB_MANIFEST":"/own/manifest.json","COLLECTION_JOB_MANIFEST_SHA256":strings.Repeat("a",64),"COLLECTION_JOB_RUN_ID":"synthetic-test","COLLECTION_JOB_TEST_SCHEMA":"q2c2_synthetic"}
 for key,value:=range defaults{t.Setenv(key,value)}
 for _,raw:=range urls{
  t.Setenv("COLLECTION_JOB_AMQP_URL",raw);t.Setenv("COLLECTION_JOB_PRODUCER_AMQP_URL",raw)
  if _,err=config.Load();err!=nil{t.Fatal("generated C2 URL rejected",err)}
 }
}'''


def main()->None:
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go',type=Path,required=True)
    parser.add_argument('--gowork',type=Path,required=True)
    parser.add_argument('--artifacts',type=Path,required=True)
    options=parser.parse_args()
    directory=options.artifacts.resolve();directory.mkdir(parents=True,exist_ok=False)
    repo=Path(__file__).resolve().parents[3]
    generated=directory/'generated-urls.json'
    values={}
    for kind in ('producer','worker'):
        values.update({kind+'_'+key:value for key,value in broker_urls('collection','synthetic_'+kind,'fake','pulse-q2c2-synthetic').items()})
    save(generated,values)
    source=directory/'generated_contract_test.go';save(source,GO_TEST)
    overlay=directory/'overlay.json'
    virtual=repo/'test/runtime/collection/generated_contract_test.go'
    save(overlay,{'Replace':{str(virtual):str(source)}})
    environment=dict(os.environ,GOPROXY='off',GOSUMDB='off',GOFLAGS='-mod=readonly',GOCACHE='/private/tmp/pulse-go-cache',GOWORK=str(options.gowork.resolve()),Q2C2_GENERATED_URLS=str(generated))
    with (directory/'go-config-contract.log').open('x') as output:
        result=subprocess.run([str(options.go),'-C',str(repo),'test','-count=1','-overlay',str(overlay),'./test/runtime/collection','-run','TestGeneratedCollectionBrokerURLsAcceptedByFrozenConfig'],env=environment,stdout=output,stderr=subprocess.STDOUT,timeout=60)
    if result.returncode:raise RuntimeError('generated C2 URLs rejected by frozen config; see own contract log')
    print('generated C2 host/internal producer/worker URLs accepted by frozen core config; no PG/source mutation')


if __name__=='__main__':main()
