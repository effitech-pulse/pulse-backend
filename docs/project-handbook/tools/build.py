"""Собирает автономный HTML и архив документации без внешних зависимостей."""
from __future__ import annotations
import hashlib
import html
import json
import re
from pathlib import Path
from urllib.parse import urlsplit, unquote
import zipfile

ROOT=Path(__file__).resolve().parents[1]
unsafe=[str(p.relative_to(ROOT)) for p in ROOT.rglob('*') if p.is_symlink()
 or any(part.startswith('.env') or part in {'.git','.venv','.aws','.ssh','workspace-data','data'} for part in p.relative_to(ROOT).parts)]
if unsafe:raise SystemExit('Forbidden package path: '+', '.join(unsafe))
INDEX=json.loads((ROOT/'inventory/document-index.json').read_text())
ENTRIES=[{'id':'home','title':'Начало и содержание','path':'README.md'},*INDEX,
 {'id':'brand-guidelines','title':'Приложение — правила логотипа','path':'design/brand-kit/GUIDELINES.md'},
 {'id':'json-examples','title':'Приложение — JSON примеры','path':'examples/README.md'},
 {'id':'source-policy','title':'Источники документации и публикация','path':'SOURCE_POLICY.md'},
 {'id':'verification-matrix','title':'Матрица проверок и CI gates','path':'references/verification-matrix.md'},
 {'id':'model-rpc','title':'Совместимость моделей и RPC','path':'references/model-rpc-compatibility.md'},
 {'id':'frontend-readme','title':'Интерфейс — запуск и источник UI','path':'references/frontend-readme.md'},
 {'id':'frontend-design','title':'Интерфейс — текущий дизайн','path':'references/frontend-design.md'},
 {'id':'frontend-handoff','title':'Интерфейс — handoff и статусы','path':'references/frontend-handoff.md'},
 {'id':'ml-service-readme','title':'ML-сервис — запуск и ограничения','path':'references/ml-service-readme.md'},
 {'id':'causal-baseline','title':'Причинный бейзлайн и ограничения','path':'references/causal-baseline.md'},
 {'id':'runtime-installation','title':'Native runtime — установка и проверка','path':'references/runtime-installation.md'},
 {'id':'backend-configuration','title':'Backend — все параметры и профили','path':'references/backend-configuration.md'},
 {'id':'backend-operations','title':'Backend — эксплуатационная инструкция','path':'references/backend-operations.md'}]
DOCMAP={str((ROOT/e['path']).resolve()):e['id'] for e in ENTRIES}

def slug(text: str)->str:
 return re.sub(r'[^\w\- ]','',text.lower()).strip().replace(' ','-')

def rewrite(url:str,source:Path)->str:
 parts=urlsplit(html.unescape(url))
 if parts.scheme or parts.netloc:return url
 if not parts.path:return '#'+DOCMAP[str(source)]+('-'+parts.fragment if parts.fragment else '')
 target=(source.parent/unquote(parts.path)).resolve()
 if str(target) in DOCMAP:return '#'+DOCMAP[str(target)]+('-'+parts.fragment if parts.fragment else '')
 try:rel=target.relative_to(ROOT)
 except ValueError:return url
 return str(rel)+('?' + parts.query if parts.query else '')+('#'+parts.fragment if parts.fragment else '')

def inline(text:str,source:Path)->str:
 tokens=[]
 def token(value:str)->str:
  tokens.append(value);return f'\x00{len(tokens)-1}\x00'
 text=re.sub(r'`([^`]+)`',lambda m:token('<code>'+html.escape(m[1])+'</code>'),text)
 def image(m):
  return token(f'<figure><a href="{html.escape(rewrite(m[2],source),quote=True)}"><img src="{html.escape(rewrite(m[2],source),quote=True)}" alt="{html.escape(m[1],quote=True)}" loading="lazy"></a><figcaption>{html.escape(m[1])}</figcaption></figure>')
 text=re.sub(r'!\[([^\]]*)\]\(([^)]+)\)',image,text)
 text=re.sub(r'\[([^\]]+)\]\(([^)]+)\)',lambda m:token(f'<a href="{html.escape(rewrite(m[2],source),quote=True)}">{html.escape(m[1])}</a>'),text)
 text=html.escape(text)
 text=re.sub(r'\*\*([^*]+)\*\*',r'<strong>\1</strong>',text)
 for n,value in enumerate(tokens):text=text.replace(f'\x00{n}\x00',value)
 return text

def render(source:Path,id:str)->str:
 lines=source.read_text().splitlines();out=[];i=0
 while i<len(lines):
  line=lines[i]
  if not line.strip():i+=1;continue
  if line.startswith('```'):
   language=line[3:].strip();i+=1;code=[]
   while i<len(lines) and not lines[i].startswith('```'):code.append(lines[i]);i+=1
   out.append('<div class="code-block"><span class="language">'+html.escape(language)+'</span><pre><code>'+html.escape('\n'.join(code))+'</code></pre></div>');i+=1;continue
  h=re.match(r'^(#{1,6})\s+(.+)',line)
  if h:
   level=len(h[1]);out.append(f'<h{level} id="{id}-{slug(h[2])}">{inline(h[2],source)}</h{level}>');i+=1;continue
  if line.startswith('|') and i+1<len(lines) and re.match(r'^\|[ :|\-]+\|?$',lines[i+1]):
   rows=[]
   def cells(value):return [inline(c.strip(),source) for c in value.strip().strip('|').split('|')]
   header=cells(line);i+=2
   while i<len(lines) and lines[i].startswith('|'):rows.append(cells(lines[i]));i+=1
   out.append('<div class="table-scroll"><table><thead><tr>'+''.join('<th>'+c+'</th>' for c in header)+'</tr></thead><tbody>'+''.join('<tr>'+''.join('<td>'+c+'</td>' for c in row)+'</tr>' for row in rows)+'</tbody></table></div>');continue
  if re.match(r'^\s*[-*]\s+',line) or re.match(r'^\d+\.\s+',line):
   ordered=bool(re.match(r'^\d+\.',line));tag='ol' if ordered else 'ul';items=[]
   pattern=r'^\d+\.\s+' if ordered else r'^\s*[-*]\s+'
   while i<len(lines) and re.match(pattern,lines[i]):items.append('<li>'+inline(re.sub(pattern,'',lines[i]),source)+'</li>');i+=1
   out.append(f'<{tag}>'+''.join(items)+f'</{tag}>');continue
  if line.startswith('> '):out.append('<blockquote>'+inline(line[2:],source)+'</blockquote>');i+=1;continue
  paragraph=[line];i+=1
  while i<len(lines) and lines[i].strip() and not re.match(r'^(#|```|\||[-*] |\d+\. )',lines[i]):paragraph.append(lines[i]);i+=1
  out.append('<p>'+inline(' '.join(paragraph),source)+'</p>')
 return '\n'.join(out)

CSS='''
:root{--ink:#1d2b3a;--muted:#657487;--blue:#2f6fed;--border:#dce3ea;--canvas:#f3f5f7;--surface:#fff}
*{box-sizing:border-box}body{margin:0;background:var(--canvas);color:var(--ink);font:15px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",Arial,sans-serif}a{color:#2359c1;text-underline-offset:3px}a:hover{color:#123f9a}a:focus-visible,button:focus-visible,input:focus-visible{outline:3px solid #82adff;outline-offset:3px}button,input{font:inherit}button{cursor:pointer}header{height:76px;padding:14px 30px;display:flex;gap:14px;align-items:center;background:var(--surface);border-bottom:1px solid var(--border)}header img{width:36px;height:36px}header strong{display:block;font-size:17px}header small{display:block;color:var(--muted);font-size:12px}.header-actions{margin-left:auto;display:flex;gap:12px;align-items:center;font-size:13px}.header-actions a,.header-actions button{padding:7px 12px;border:1px solid var(--border);border-radius:4px;background:white;text-decoration:none;color:var(--ink)}.layout{display:grid;grid-template-columns:288px minmax(0,1fr)}aside{padding:24px 18px;position:sticky;top:0;height:calc(100vh - 1px);overflow:auto;border-right:1px solid var(--border)}label{display:block;font-size:12px;color:var(--muted);margin-bottom:6px}input{width:100%;padding:9px 11px;border:1px solid #a5b4c4;border-radius:4px;background:var(--surface);font-size:14px}#search-status{display:block;min-height:30px;padding-top:5px;font-size:12px;color:var(--muted)}nav{display:flex;flex-direction:column;gap:3px}nav a{font-size:13px;line-height:1.5;padding:9px 10px;border-radius:4px;text-decoration:none;color:var(--ink);border-left:3px solid transparent}nav a[aria-current=page]{background:#e8effd;border-left-color:var(--blue);color:#204fa8;font-weight:600}main{padding:32px 42px 80px;min-width:0}article{max-width:1160px;margin:0 auto;background:white;border:1px solid var(--border);padding:34px 40px;border-radius:4px}article[hidden],nav a[hidden]{display:none}h1{font-size:30px;line-height:1.25;font-weight:650;margin:0 0 22px}h2{font-size:22px;line-height:1.35;margin:36px 0 14px}h3{font-size:17px;line-height:1.4;margin:26px 0 12px}p{margin:12px 0}ul,ol{padding-left:24px}li{margin:7px 0}.table-scroll{overflow:auto;max-width:100%;margin:20px 0;border:1px solid var(--border);border-radius:4px}table{border-collapse:collapse;width:100%;font-size:13px;line-height:1.6}th,td{padding:11px 13px;text-align:left;border-bottom:1px solid var(--border);vertical-align:top}th{background:#f3f5f7;font-weight:600}tr:last-child td{border-bottom:0}td{min-width:110px}code{font:12px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;background:#eef2f6;padding:2px 5px;border-radius:3px;overflow-wrap:anywhere}.code-block{margin:20px 0;border:1px solid var(--border);background:#f6f8fa;border-radius:4px;overflow:hidden}.language{font-size:11px;color:var(--muted);padding:5px 14px;display:block;border-bottom:1px solid var(--border)}pre{margin:0;padding:16px;overflow:auto}pre code{padding:0;background:none;white-space:pre;overflow-wrap:normal}figure{margin:24px 0}figure img{width:100%;height:auto;border:1px solid var(--border)}figcaption{font-size:12px;color:var(--muted);margin-top:6px}blockquote{margin:18px 0;border-left:3px solid var(--blue);padding:12px 16px;background:#edf3ff}.footer{max-width:1160px;margin:20px auto 0;color:var(--muted);font-size:12px}.empty{padding:20px;color:var(--muted)}
@media(max-width:1000px){main{padding:24px}.layout{grid-template-columns:245px minmax(0,1fr)}article{padding:24px}}@media(max-width:720px){header{padding:12px 16px;height:auto;flex-wrap:wrap}.header-actions{width:100%;margin:0}.layout{display:block}aside{position:relative;height:auto;max-height:310px;padding:16px;border-bottom:1px solid var(--border)}nav{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))}main{padding:16px}h1{font-size:24px}article{padding:22px 18px}}@media print{header,aside,.footer{display:none}.layout{display:block}main{padding:0}article,article[hidden]{display:block!important;border:0;padding:0;max-width:none;break-before:page}article:first-child{break-before:auto}.table-scroll{overflow:visible}a{color:inherit;text-decoration:none}pre{white-space:pre-wrap}h2,h3{break-after:avoid}tr,figure{break-inside:avoid}}
'''
JS='''
const links=[...document.querySelectorAll('nav a')],articles=[...document.querySelectorAll('main article')],input=document.querySelector('#search');
function show(){const id=decodeURIComponent(location.hash.slice(1)||'home');const article=articles.find(a=>id===a.id||id.startsWith(a.id+'-'))||articles[0];articles.forEach(a=>a.hidden=a!==article);links.forEach(a=>{if(a.dataset.id===article.id)a.setAttribute('aria-current','page');else a.removeAttribute('aria-current')});document.title=article.dataset.title+' · Пульс скважины';if(id!==article.id){requestAnimationFrame(()=>document.getElementById(id)?.scrollIntoView())}else window.scrollTo(0,0)}
function search(){const q=input.value.toLocaleLowerCase('ru').trim();let matches=[];links.forEach(link=>{const a=document.getElementById(link.dataset.id);const hit=!q||a.textContent.toLocaleLowerCase('ru').includes(q);link.hidden=!hit;if(hit)matches.push(link)});document.querySelector('#search-status').textContent=q?'Найдено разделов: '+matches.length:'Все разделы';document.querySelector('#no-results').hidden=matches.length>0;const current=links.find(a=>a.getAttribute('aria-current')==='page');if(q&&matches.length&&current?.hidden)location.hash=matches[0].dataset.id}
window.addEventListener('hashchange',show);input.addEventListener('input',search);document.querySelector('#print').addEventListener('click',()=>window.print());show();search();
'''
nav=''.join(f'<a href="#{e["id"]}" data-id="{e["id"]}">{html.escape(e["title"])}</a>' for e in ENTRIES)
articles=''.join(f'<article id="{e["id"]}" data-title="{html.escape(e["title"],quote=True)}" hidden>{render(ROOT/e["path"],e["id"])}</article>' for e in ENTRIES)
page=f'''<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Техническая документация · Пульс скважины</title><style>{CSS}</style></head><body><header><img src="assets/pulse-symbol.svg" alt=""><div><strong>Пульс скважины</strong><small>Техническая документация проекта</small></div><div class="header-actions"><a href="https://www.figma.com/design/km4tIbp3LnMTRrV6yi6Qho">Figma и макеты ↗</a><a href="diagrams/pulse-c4.drawio">C4 · draw.io</a><button id="print" type="button">Печать всех разделов</button></div></header><div class="layout"><aside><label for="search">Поиск по содержимому</label><input id="search" type="search" placeholder="Например: RBAC, Kafka, watermark" autocomplete="off"><span id="search-status" role="status"></span><nav aria-label="Разделы документации">{nav}</nav><div id="no-results" class="empty" hidden>Совпадений нет</div></aside><main>{articles}<p class="footer">Срез рабочего дерева · статусы реализации указаны в разделе 2 · исходники и спецификации внутри комплекта</p></main></div><script>{JS}</script></body></html>'''
(ROOT/'index.html').write_text(page)
# Контрольные суммы не включают архив и сам файл контрольных сумм.
paths=sorted(p for p in ROOT.rglob('*') if p.is_file()
 and p.name not in {'checksums.sha256','pulse-project-documentation.zip','.DS_Store'}
 and '__pycache__' not in p.parts and p.suffix!='.pyc')
(ROOT/'checksums.sha256').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+str(p.relative_to(ROOT))+'\n' for p in paths))
with zipfile.ZipFile(ROOT/'pulse-project-documentation.zip','w',compression=zipfile.ZIP_DEFLATED) as z:
 for p in [*paths,ROOT/'checksums.sha256']:z.write(p,Path('pulse-project-documentation')/p.relative_to(ROOT))
print(f'HTML: {len(ENTRIES)} раздела; ZIP: {(ROOT/"pulse-project-documentation.zip").stat().st_size:,} bytes')
