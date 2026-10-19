"""Проверяет переносимость комплекта, спецификации и контрольные суммы."""
from __future__ import annotations
import hashlib
from html.parser import HTMLParser
import json
from pathlib import Path
import re
from urllib.parse import urlsplit,unquote
import xml.etree.ElementTree as ET
import zipfile

ROOT=Path(__file__).resolve().parents[1]
errors=[];links_checked=0
unsafe=[str(p.relative_to(ROOT)) for p in ROOT.rglob('*') if p.is_symlink()
 or any(part.startswith('.env') or part in {'.git','.venv','.aws','.ssh','workspace-data','data'} for part in p.relative_to(ROOT).parts)]
if unsafe:raise SystemExit('Forbidden package path: '+', '.join(unsafe))

# Только локальные snapshots: архив проверяется без соседних checkout и сети.
# Внешний drift отдельно: sync_sources.py --workspace <repository-split> --check.
registry=json.loads((ROOT/'inventory/snapshot-sources.json').read_text())
snapshot_count=0
for entry in registry['entries']:
 path=ROOT/entry['snapshot'];snapshot_count+=1
 try:path.resolve().relative_to(ROOT)
 except ValueError:errors.append('Outside snapshot path: '+entry['snapshot']);continue
 if not entry.get('owner') or not entry.get('source_sha256'):
  errors.append('Incomplete snapshot metadata: '+entry['snapshot'])
 if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest()!=entry.get('snapshot_sha256'):
  errors.append('Snapshot digest mismatch: '+entry['snapshot'])
class Links(HTMLParser):
 def __init__(self):super().__init__();self.links=[];self.ids=set()
 def handle_starttag(self,tag,attrs):
  attrs=dict(attrs)
  if 'id' in attrs:self.ids.add(attrs['id'])
  for a in ['href','src']:
   if a in attrs:self.links.append(attrs[a])

def check_link(source:Path,url:str):
 global links_checked
 parts=urlsplit(url)
 if parts.scheme or parts.netloc:return
 if not parts.path:
  if parts.fragment and source.suffix=='.html':
   if unquote(parts.fragment) not in html_docs[source].ids:errors.append(f'{source.relative_to(ROOT)}: missing anchor {url}')
  return
 target=(source.parent/unquote(parts.path)).resolve()
 try:target.relative_to(ROOT)
 except ValueError:errors.append(f'Outside package: {source.relative_to(ROOT)} {url}');return
 links_checked+=1
 if not target.exists():errors.append(f'Missing: {source.relative_to(ROOT)} -> {url}')
 if parts.fragment and target.suffix=='.html' and target in html_docs and unquote(parts.fragment) not in html_docs[target].ids:errors.append(f'Missing anchor: {url}')

html_docs={}
for source in ROOT.rglob('*.html'):
 # head-snippet — шаблон размещения иконок в корне сайта, не страница комплекта.
 if source.name=='head-snippet.html':continue
 p=Links();p.feed(source.read_text());html_docs[source.resolve()]=p
for source,parsed in html_docs.items():
 for url in parsed.links:check_link(source,url)
for source in ROOT.rglob('*.md'):
 text=re.sub(r'```.*?```','',source.read_text(),flags=re.S)
 for url in re.findall(r'!?\[[^\]]*\]\(([^)]+)\)',text):check_link(source,url)

api=json.loads((ROOT/'contracts/openapi.json').read_text());refs=[]
def visit(v):
 if isinstance(v,dict):
  if '$ref' in v:refs.append(v['$ref'])
  for child in v.values():visit(child)
 elif isinstance(v,list):
  for child in v:visit(child)
visit(api)
for ref in refs:
 if not ref.startswith('#/'):errors.append('Unexpected remote OpenAPI ref: '+ref);continue
 value=api
 try:
  for key in ref[2:].split('/'):value=value[key.replace('~1','/').replace('~0','~')]
 except KeyError:errors.append('Unresolved OpenAPI ref: '+ref)

page_count=0
for source in ROOT.glob('diagrams/*.drawio'):
 tree=ET.parse(source)
 for page in tree.getroot().findall('diagram'):
  page_count+=1;cells=page.findall('./mxGraphModel/root/mxCell');ids=[c.get('id') for c in cells]
  if len(ids)!=len(set(ids)):errors.append(f'Duplicate draw.io ID: {source.name}')
  for c in cells:
   if c.get('edge')=='1' and (c.get('source') not in ids or c.get('target') not in ids):errors.append(f'Broken draw.io edge: {source.name} {c.get("id")}')

checksums=0;listed=set();checksum_path=ROOT/'checksums.sha256'
if not checksum_path.is_file():errors.append('Missing checksums.sha256')
for line in checksum_path.read_text().splitlines() if checksum_path.is_file() else []:
 digest,name=line.split('  ',1);path=ROOT/name;checksums+=1
 try:path.resolve().relative_to(ROOT)
 except ValueError:errors.append('Outside checksum path: '+name);continue
 if name in listed:errors.append('Duplicate checksum path: '+name)
 listed.add(name)
 if not path.exists() or hashlib.sha256(path.read_bytes()).hexdigest()!=digest:errors.append('Checksum mismatch: '+name)

files={p.relative_to(ROOT).as_posix():p for p in ROOT.rglob('*') if p.is_file()
 and p.name not in {'pulse-project-documentation.zip','.DS_Store'}
 and '__pycache__' not in p.parts and p.suffix!='.pyc'}
if set(files)-{'checksums.sha256'}!=listed:errors.append('Checksums file set differs from folder')
archive=ROOT/'pulse-project-documentation.zip'
archive_status='extracted package validated (no nested ZIP required)'
if archive.exists():
 with zipfile.ZipFile(archive) as z:
  if z.testzip():errors.append('Corrupt ZIP entry')
  expected={str(Path('pulse-project-documentation')/name) for name in files}
  if set(z.namelist())!=expected:errors.append('ZIP content differs from folder')
  for n in z.namelist():
   rel=Path(n)
   if '..' in rel.parts or rel.is_absolute() or '\\' in n or any(p.startswith('.env') for p in rel.parts) or '.git' in rel.parts or 'workspace-data' in rel.parts or not rel.parts or rel.parts[0]!='pulse-project-documentation':
    errors.append('Forbidden ZIP path: '+n);continue
   name=rel.relative_to('pulse-project-documentation').as_posix()
   if name in files and z.read(n)!=files[name].read_bytes():errors.append('ZIP bytes differ from folder: '+name)
 archive_status='ZIP matches folder bytes'

# В главном портале нет сетевых скриптов, CDN и зависимостей от API приложения.
portal=(ROOT/'index.html').read_text()
if re.search(r'<script[^>]+src=',portal):errors.append('External script in documentation portal')
if 'fetch(' in portal or 'XMLHttpRequest' in portal:errors.append('Unexpected runtime network request in portal')
if errors:
 print('\n'.join(errors));raise SystemExit(1)
print(f'OK: {links_checked} local links, {len(refs)} OpenAPI refs, {page_count} draw.io pages, {checksums} checksums, {snapshot_count} mapped snapshots; {archive_status}')
