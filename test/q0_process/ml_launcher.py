"""Test-only transparent observation around accepted installed runtime."""
from __future__ import annotations
import os,json,time,threading,signal
from pathlib import Path
os.setpgrp()
root=Path(os.environ['Q0_ROOT'])
lock=threading.Lock()
def event(name,**extra):
 with lock:
  with (root/'evidence/ml-calls.jsonl').open('a') as f:f.write(json.dumps({'event':name,'pid':os.getpid(),'utc_ns':time.time_ns(),**extra})+'\n')
def observe(obj,name,label):
 original=getattr(obj,name)
 def wrapper(*args,**kwargs):
  event(label)
  result=original(*args,**kwargs)
  if label=='features': event('feature_result',windows=len(result))
  return result
 setattr(obj,name,wrapper)
import q0_synthetic
import pulse_ml.ml_upload as upload
import pulse_ml.engine as engine
observe(upload,'build_feature_matrix','features')
observe(q0_synthetic.FixedModel,'predict_proba','predict')
observe(engine,'get_upload_context','legacy_loader')
observe(engine,'get_context','default_loader')
from pulse_ml.server import create_server
from pulse_ml.observability import Observer
from pulse_ml.observability_export import ExportConfig
# Export off through the supported configuration; actual service implementation unchanged.
server=create_server(observer=Observer('pulse-ml-service'))
port=server.add_insecure_port('127.0.0.1:0')
assert port
server.start()
ready=Path(os.environ['Q0_READY']);tmp=ready.with_suffix('.tmp');tmp.write_text(json.dumps({'addr':f'127.0.0.1:{port}','pid':os.getpid(),'utc_ns':time.time_ns()}));tmp.replace(ready)
event('started')
done=threading.Event()
def stop(*args):done.set()
signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
done.wait();server.stop(0).wait();event('stopped')
