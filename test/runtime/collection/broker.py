#!/usr/bin/env python3
"""Создаёт отдельные ограниченные C2 и Q1-regression vhosts только на own Rabbit."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import secrets
import time

from harness import docker, inspect_owned, save


def broker_urls(prefix:str,user:str,password:str,vhost:str)->dict[str,str]:
    suffix='' if prefix=='collection' else '?heartbeat=10'
    values={'host':f'amqp://{user}:{password}@127.0.0.1:5675/{vhost}{suffix}'}
    if prefix=='collection':values['internal']=f'amqp://{user}:{password}@rabbit:5672/{vhost}{suffix}'
    return values


def valid_queue_gauges(value: object) -> bool:
    if not isinstance(value,dict):return False
    keys=('messages','messages_ready','messages_unacknowledged','consumers')
    return all(type(value.get(key)) is int and value[key]>=0 for key in keys) and value['messages']==value['messages_ready']+value['messages_unacknowledged']


def provision(directory: Path) -> None:
    state=json.loads((directory/'state.json').read_text())
    identity=state['rabbit_id']
    inspect_owned(state,identity)
    ready=docker('exec','--user','rabbitmq',identity,'rabbitmq-diagnostics','-q','ping')
    docker('exec','--user','rabbitmq',identity,'rabbitmq-plugins','enable','rabbitmq_management')
    collector=docker('exec','--user','rabbitmq',identity,'rabbitmqctl','eval','application:get_env(rabbitmq_management_agent, disable_metrics_collector).')
    if collector!='{ok,false}':raise ValueError('test broker metrics collector must be enabled')
    results=[]
    for prefix,namespace in [('q1_regression','file-check'),('collection','collection')]:
        vhost='pulse-q2c2-synthetic' if prefix=='collection' else 'pulse-'+prefix+'-'+state['run_id']
        docker('exec','--user','rabbitmq',identity,'rabbitmqctl','add_vhost',vhost)
        pattern=r'^pulse\.'+namespace+r'\.v1(\..*)?$'
        if prefix=='collection':
            credentials=dict(line.split('=',1) for line in (directory/'rabbit.env').read_text().splitlines() if '=' in line)
            docker('exec','--user','rabbitmq',identity,'rabbitmqctl','set_permissions','-p',vhost,credentials['RABBITMQ_DEFAULT_USER'],'^$',pattern,pattern)
        for kind in ('producer','worker'):
            user=('c2' if prefix=='collection' else prefix)+'_'+kind+'_'+state['run_id']
            password=secrets.token_hex(24)
            docker('exec','--user','rabbitmq',identity,'rabbitmqctl','add_user',user,password)
            docker('exec','--user','rabbitmq',identity,'rabbitmqctl','set_permissions','-p',vhost,user,pattern,pattern,pattern)
            urls=broker_urls(prefix,user,password,vhost)
            save(directory/(prefix+'_'+kind+'_amqp_url'),urls['host']+'\n',private=True)
            if prefix=='collection':
                save(directory/(prefix+'_'+kind+'_internal_amqp_url'),urls['internal']+'\n',private=True)
        results.append({'purpose':prefix,'vhost':vhost,'permission_pattern':pattern})
    save(directory/('broker-provision-'+str(time.time_ns())+'.json'),{'rabbit_id':identity,'ready':ready,'metrics_collector_disabled':False,'vhosts':results})
    print(json.dumps({'status':'READY','private_files':[str(directory/(prefix+'_'+kind+'_amqp_url')) for prefix in ('q1_regression','collection') for kind in ('producer','worker')]}))


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-dir',type=Path,required=True)
    parser.add_argument('--allow-isolated-mutation',action='store_true',required=True)
    options=parser.parse_args()
    provision(options.state_dir)
