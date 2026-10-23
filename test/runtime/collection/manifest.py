#!/usr/bin/env python3
"""Генерирует только synthetic descriptors и безопасную конфигурацию собственного run."""
from __future__ import annotations

import argparse
import copy
import hashlib
import json
from pathlib import Path

from harness import save

SENSORS=['F_out','Ia','R_izol','Ib','Ic','Ped_load','CosF','Motor_winding_temperature',
         'Pump_intake_pressure_kgs','Annulus_pressure','Vibration_X','Vibration_Y','Vibration_Z',
         'Flowrate for current day','energy_Input_energy_reactive_total_MW','energy_Input_energy_reactive_total_kW']
SETPOINTS=['set_Annular_pressure','set_Auto_adaptation_mode','set_Next_mode_frequency','set_Next_mode']


def policy(run:str,schema:str)->dict:
    channels=[]
    mappings=[]
    for index,name in enumerate(SENSORS+SETPOINTS):
        is_set=index>=16
        field=('s'+str(index-16)) if is_set else ('f'+str(index))
        markers=[9999] if name=='R_izol' else ([9999,999.9] if name.startswith('energy_') else [])
        channels.append({'name':name,'aliases':[name,field],'unit':'','is_set':is_set,'minimum':0,'maximum':1e12,'missing_markers':markers})
        mappings.append({'field':field,'parameter':name,'unit':'','scale':1,'offset':0,'missing':'','is_set':is_set})
    dense={'id':'dense-v1','protocol':'collection-fixture-v1','horizon':'2026-10-20T00:00:00Z','retention_start':'2026-09-19T00:00:00Z','empty':False,'channels':channels,'mappings':mappings}
    empty=copy.deepcopy(dense);empty['id']='empty-v1';empty['empty']=True
    return {'run_id':run,'test_schema':schema,'manifest_sha256':'','fixture_origin':'http://fixture:8080',
            'allowed_account_ids':[1,2],'bindings':[{'source_id':'source-a','well_id':1,'external_id':'-900000','identity_kind':'unitek'},
             {'source_id':'source-b','well_id':2,'external_id':'-900001','identity_kind':'unitek'}],'fixtures':[dense,empty]}


def main()->None:
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-dir',type=Path,required=True)
    options=parser.parse_args();directory=options.state_dir
    state=json.loads((directory/'state.json').read_text())
    target=directory/'fixture';target.mkdir(exist_ok=True)
    manifest=policy(state['run_id'],'q2c2_'+state['run_id'])
    raw=json.dumps(manifest,separators=(',',':'),ensure_ascii=False)+'\n'
    save(target/'manifest.json',raw)
    save(target/'parameters.json',{name:[name] for name in SENSORS})
    save(target/'manifest-metadata.json',{'sha256':hashlib.sha256(raw.encode()).hexdigest(),'schema':manifest['test_schema'],'sensor_channels':16,'raw_fields':20,'source_count':2,'accounts':2,'fixtures':2})
    print('own synthetic manifest prepared; no DB mutation')


if __name__=='__main__':main()
