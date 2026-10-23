# Isolated collection runtime tooling

Это opt-in synthetic-only harness для отдельного local stand: новые exact-ID/
run-labelled containers, internal egress-denied network и volumes; проектные `.env`,
Data/реальные источники/рабочая PG/ML не подключаются. Все private URL files, logs,
receipts и source/binary manifests сохраняются в новом каталоге оператора вне checkout.

Source status23.10.2026: SamplerSuccessDeadline30/H20 SOURCE APPROVED, core3 и
H01–H20 CLOSED;78Python helper controls/module gates PASS. Fresh FULLACT14
`r8e1b5066a1e3` ALL11 PASS:592.801s fullspan,569relevant/596samples,
ML0/canary0/exactteardown/working8same/free5. Reconstructed aggregate917348352B
и sampled peak956289024B — разные metrics, оба<1536MiB. Publication PASS отдельно.
FULLACT13 A01–10PASS/A11FAIL/rawbadrow остаётся wholeFAIL, не исправлен задним числом.

Tested77paths(core47+harness30 со старымREADME) зафиксированы code commit
`1a824a63c068f828a9d4dfa663b8036cd426e191`; offline binary stamp BASE7fcdirty остался прежним,
content binding compiled57/binary11 verified. Этот README обновлён после run как
**docs-only handover**, не часть прежних30byte hashes. Executable29/core47 неизменны.
Printed recipe D01/D02 guards ниже проверены24externalstub controls/syntax; whole
operator recipe после docs handover повторно live не запускался. Actual proofs,
limits/caveats и publication gates — в [runtime guide](../../../docs/q2-collection-runtime-2026-10-22.md),
HTTP/schema/coverage — в [протоколе](../../../docs/q2-collection-jobs.md).

## Offline inputs

Запускать команды ниже из **корня backend checkout**, не из каталога этого README.
Нужны macOS/arm64, локальный Docker Engine context с Unix socket, Python3.11+ с уже
установленным psycopg2, Go1.25 с cache зависимостей и exact reviewed contracts source
`1d0b2b62d2cb73d18ab69738431089edf69621f8`, candidate `v0.1.2` **UNRELEASED**.
Released tag или cached released SDK не предполагается; одна package version не
доказывает source equivalence. Пример ниже проверяет HEAD и clean SDK checkout;
для SDK directory без Git нужен независимо проверенный source manifest от этого
exact revision. Соседний contracts checkout допустим, но заданный offline SDK может
находиться в другом каталоге. Нет зависимости от private tasks/source-freeze.py.

Для old API нужен Git object `7fc37f6e737c252b37d6deef644ff444ba6f89a1`: build создаёт
immutable `git archive` и отдельный old GOWORK, не использует current backend source.
Local cache должен содержать точные image IDs:

- PG16 `sha256:ca0bd484cb98bf4b24eb1010e73fb3fcbd6714d240fbc1a10eea5b7dbecb641d`.
- Rabbit4.3.6 `sha256:3e5cc23235b0eb850f604e8ace10d9a9b6d05be3e36730e53522ee9ead707a9e`.

Отсутствующий image/cache/SDK/base object — prerequisite failure. Harness не
выполняет pull/package install и не предполагает remote release. Binary builds
CGO0/timetzdata/trimpath для Darwin+Linux arm64 используют readonly modules,
GOPROXY/GOSUMDB=off. Own images — FROM scratch, `--pull=false --network=none`.

## Isolation, budgets и fault semantics

Five free host loopback ports: API8094, fixture8093, PG55442, AMQP5675, management15675.
Рабочий Data8092 сохраняется. **Единственный host transport — fixed stdio forwarder**,
не Docker `-p`; internal network не получает egress. Одна persistent own PG exec
bridge разрешает только PG5432/Rabbit5672/API8000/fixture8080/management15672,
≤16connections и≤32768-byte frames, без payload logging/arbitrary targets.

Supervisor получает real cgroup current/peak и PG database/PGDATA/WAL/temp counters
1s, с freshness/attachment≤3s и full-span validation. Exact-owned local Engine adapter
допускает лишь full64hex ID/run-label/name/physical-state metadata≤1MiB и fixed
`/fixture monitor ROLE` exec. Unknown/stale/missing role/gap/stop receipt fail-closed;
неизвестная метрика не превращается в0. R22 candidate имеет один fixed cutoff,
exact current ID/latest running epoch/genuine counters и stable lifecycle/ledger;
old same-ID counter не переиспользуется. Earliest3s guard действует перед selector,
на всех deferrals/success/serialization и непосредственно prewrite; slow work не
публикует late successful row. Original1s/fresh3s/proof/caps не меняются. Optional
administrative saved-prefix inspection не заменяет canonical A11, не фильтрует rows
и не является prerequisite этого standalone recipe.

Original actual budgets: API128MiB, each worker256MiB, fixture64MiB, PG/Rabbit384MiB;
aggregate1536MiB включает **все** host helpers/Docker children/transient wait4 peaks.
Дополнительный64MiB guard действует **на каждый host process**, не на сумму helpers.
Database256MiB, PGDATA768MiB, WALdelta256MiB/physicalpg_wal512MiB, tempdelta16MiB,
own files2GiB/logs64MiB/evidence64MiB; case≤300s, suite≤2700s. MODULE profile с
большими test-only caps не используется для actual acceptance. Cap/counter/timestamp
не обновляются ради PASS; failed stand навсегда tainted и требует нового run.

Worker Consume имеет finite lifetime8 reconnect attempts/backoff; process exit
после exhaustion допустим и требует operator/orchestrator restart. Бесконечный
autoreconnect/same-OS-process survival не обещается. A06 заранее combined exact PG
claim-session loss внутри Apply + guarded stop/restart same container/new process:
whole PK/effects0/capture1/hashsame/CP0/coverage0 проверяются physically stopped под
barrier; затем true lease expiry/new fence+PG PID до release и exact apply/no Fetch.
Нет pool fallback старого owner, ACK shortcut или reset durable retry budget.

A07 сохраняет old process в real≥7s late-I/O window, source-start observation≤40s
внутри300s case; independent authority faults используют guarded stop/restore/restart.
Optional worker2 после A05 действительно removed: prior physical stop, fresh sampler
ACK removal monitor/counters, complete log export, exact-ID/label removal, Engine
absence и immutable current-run receipt. Это resource retirement, не API job state;
required PG/Rabbit/fixture/API/worker1 не исключаются.

## Automatic helpers

`provision` создаёт own readonly90-collection-test-metrics.conf до Rabbit boot,
collector=false сохраняется при restart. `broker.py` через `--user rabbitmq` включает
bundled management, требует `{ok,false}`, создаёт own vhosts/users и existing own
management-user получает configure^$/anchored collection-v1 read/write **только C2vhost**.
Никаких ручных broad permission grants или image/config edits не требуется.
Prepare и A04 требуют real ready/quarantine gauges: four nonnegative type-int поля,
messages=ready+unacked; missing/bool/float/string не0, A04 observedunacked1 до KILL.

Source PUT всегда draft; helper явно PATCH-ит intended status. Own temporary
schema-local exact-source/running-config trigger атомарно ставит future next_poll
при PATCH, который иначе сбрасывает его now; trigger/function удаляются finally,
cleanup error fails. Other IDs/paused/draft/direct due-update не затронуты. Это не
production migration/globalpause/API/job/provenance change. Эти helpers автоматические
и не требуют SQL/tasks из предыдущей сессии.

До любого worker/API removal сохраняются complete private0600/O_EXCL stdout/stderr
exports≤32768-byte blocks/20s. Final manifest связывает current∪retired exact IDs с
run/role/path/bytes/SHA/complete. Finish проверяет continuous **и** full logs, HTTP
status/errors, queue bodies и safe DB/export text; missing/changed/partial surface
блокирует scan. Safe errors сохраняют≤32innermost basename/function/positive-line
frames и direct HTTP100..599, без message/source/locals/URL/header/body. Canary и
raw matching content никогда не экспортируются в report.

## Operator bootstrap и полный run

Это автономный operator recipe на scripts/CLI acceptedACT14. Post-run shell/readiness
guards проверены external stubs, без новогоwholelive recipe replay.
Пути ниже заменяются operator-owned absolute paths; RUN_ROOT новый, BIN_DIR до build
не существует. Во всех отдельных terminals повторите тот же набор export variables.
Source acceptance, SDK/source identity и exact binary manifest проверяются до
отдельного recorded actual authorization; JSON gate сам по себе approval не создаёт.

```sh
set -eu
export REPO="$PWD"
export GO_BIN=/absolute/path/to/go
export PYTHON_BIN=/absolute/path/to/python-with-psycopg2
export CONTRACTS_DIR=/absolute/path/to/exact-reviewed/pulse-contracts-checkout
export RUN_ROOT=/absolute/new/own/runtime-run
export BIN_DIR="$RUN_ROOT/binaries"
export STAND_DIR="$RUN_ROOT/stand"
test ! -e "$RUN_ROOT"
mkdir -p "$STAND_DIR"
# Для SDK checkout: exact reviewed source, не предположение о released tag.
test "$(git -C "$CONTRACTS_DIR" rev-parse HEAD)" = 1d0b2b62d2cb73d18ab69738431089edf69621f8
test -z "$(git -C "$CONTRACTS_DIR" status --porcelain --untracked-files=all)"
```

До создания stand снимите собственный baseline всех текущих рабочих container IDs; предыдущий run защищал8. Имена/ID предыдущей задачи не требуются; cached images повторно проверяет preflight.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import json,os,subprocess
from pathlib import Path
root=Path(os.environ['RUN_ROOT'])
lines=subprocess.run(['docker','ps','--no-trunc','--format','{{.Names}} {{.ID}} {{.Status}}'],check=True,capture_output=True,text=True).stdout.splitlines()
(root/'baseline.json').write_text(json.dumps({'working_containers':lines},indent=2)+'\n')
PYCODE
```

Создайте source freeze из самого checkout; development untracked implementation files также включаются. Build сверяет bytes до/после compilation. Hash не заменяет independent source approval; SDK revision фиксируется отдельно.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import os,json,hashlib,subprocess
from pathlib import Path
repo=Path(os.environ['REPO']);root=Path(os.environ['RUN_ROOT'])
paths=subprocess.run(['rg','--files','-g','*.go','-g','*.sql','-g','go.mod','-g','go.sum','-g','test/runtime/collection/*.py','-g','test/runtime/collection/Dockerfile','-g','test/runtime/collection/README.md'],cwd=repo,check=True,capture_output=True,text=True).stdout.splitlines()
files={name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in sorted(paths)}
(root/'source-freeze.json').write_text(json.dumps({'status':'FROZEN','base':'7fc37f6e737c252b37d6deef644ff444ba6f89a1','files':files},indent=2)+'\n')
PYCODE
"$PYTHON_BIN" test/runtime/collection/build.py --repo "$REPO" --output "$BIN_DIR" --go "$GO_BIN" --contracts "$CONTRACTS_DIR" --freeze "$RUN_ROOT/source-freeze.json"
```

Build использует CGO0/Darwin+Linux arm64/timetzdata/trimpath, GOPROXY/GOSUMDB=off и readonly modules; old API собирается из собственного immutable Git archive/workspace. До runtime authorization нужен успешный build и проверенные source/SDK/binary-freeze bindings.

```sh
set -eu
"$PYTHON_BIN" test/runtime/collection/harness.py preflight --state-dir "$STAND_DIR" --baseline "$RUN_ROOT/baseline.json"
"$PYTHON_BIN" test/runtime/collection/harness.py provision --state-dir "$STAND_DIR" --baseline "$RUN_ROOT/baseline.json" --allow-isolated-mutation
"$PYTHON_BIN" test/runtime/collection/manifest.py --state-dir "$STAND_DIR"
"$PYTHON_BIN" - <<'PYCODE'
import os,json,sys
from pathlib import Path
sys.path.insert(0,'test/runtime/collection');import harness
p=Path(os.environ['STAND_DIR']);b=Path(os.environ['BIN_DIR']);s=json.loads((p/'state.json').read_text())
for role in ('postgres','rabbit'):
    harness.inspect_owned(s,s[role+'_id'])
    harness.docker('cp',str(b/'linux-fixture'),s[role+'_id']+':/fixture')
s['helper_copies_bytes']=2*(b/'linux-fixture').stat().st_size
(p/'state.json').write_text(json.dumps(s,indent=2)+'\n')
PYCODE
```

Provision асинхронен. **До supervisor и первого broker.py bootstrap** дождитесь
read-only readiness обоих exact-owned containers. Проверка ниже имеет monotonic60s
readiness deadline, polls≤0.5s и child timeouts≤min(3s,remaining); bounded child cleanup
выполняется source-owned Process wrapper. Она не запускает services, не меняет
профили/метрики и не повторяет mutating add_vhost/add_user. Helper/children учитываются
через processes tracking; raw stdout/stderr probes не выводятся. Actual span и его
G6/case limits ещё не начались, monitor attachment/freshness3s остаются прежними.
Rabbit probe ждёт `check_running`, то есть запуска RabbitMQ application;
`ping` подтверждает лишь Erlang runtime/authentication и недостаточен для cold boot.
Семантика — [официальная RabbitMQ4.3 manual](https://www.rabbitmq.com/docs/4.3/man/rabbitmq-diagnostics.8).
Исходный broker.py bootstrap не меняется и вызывается один раз после этой готовности.


```sh
set -eu
if ! "$PYTHON_BIN" - <<'PYCODE'
import json,os,re,subprocess,sys,time
from pathlib import Path
sys.path.insert(0,'test/runtime/collection')
import harness,processes

def wait_owned_infrastructure(directory):
    state=json.loads((directory/'state.json').read_text())
    deadline=time.monotonic()+60
    def readonly(args):
        remaining=deadline-time.monotonic()
        if remaining<=0:raise TimeoutError('owned infrastructure readiness deadline')
        try:
            return processes.run(args,capture_output=True,text=True,timeout=min(3,remaining))
        except subprocess.TimeoutExpired:
            return None
    while time.monotonic()<deadline:
        ready=True
        for role in ('postgres','rabbit'):
            identity=state[role+'_id']
            if not re.fullmatch(r'[0-9a-f]{64}',identity):raise ValueError('invalid owned readiness ID')
            inspected=readonly(['docker','inspect',identity])
            if inspected is None or inspected.returncode!=0:
                ready=False;continue
            records=json.loads(inspected.stdout)
            if not isinstance(records,list) or len(records)!=1:raise ValueError('owned readiness metadata mismatch')
            record=records[0]
            harness.owned_id(record,state['run_id'],state['containers'])
            if record['Id']!=identity or record['Name']!='/'+state['project']+'-'+role:
                raise ValueError('owned readiness identity mismatch')
            if not record['State']['Running']:
                ready=False;continue
            args=(['docker','exec',identity,'pg_isready','-U','collection_test','-d','collection_test']
                  if role=='postgres' else
                  ['docker','exec','--user','rabbitmq',identity,'rabbitmq-diagnostics','-q','check_running'])
            probe=readonly(args)
            if probe is None or probe.returncode!=0:ready=False
        if ready and time.monotonic()<=deadline:return
        remaining=deadline-time.monotonic()
        if remaining<=0:break
        time.sleep(min(.5,remaining))
    raise TimeoutError('owned infrastructure readiness deadline')

directory=Path(os.environ['STAND_DIR'])
processes.track_self(directory,'infra-readiness')
try:
    wait_owned_infrastructure(directory)
except Exception:
    raise SystemExit('owned PG/Rabbit readiness FAILED; preserve state and run guarded teardown') from None
finally:
    processes.self_peak('infra-readiness')
print('owned PG/Rabbit readiness READY')
PYCODE
then
  "$PYTHON_BIN" test/runtime/collection/harness.py teardown --state-dir "$STAND_DIR" --allow-isolated-mutation
  exit 1
fi
```

Продолжать можно только после `owned PG/Rabbit readiness READY`. Timeout/foreign-ID/
ownership mismatch вызывает guarded exact-owned teardown и nonzero exit до broker
mutations; state/receipts сохраняются. Не повторяйте provision или mutating broker
bootstrap на частично созданном stand. `broker.py` вызывается ровно один раз после
readiness; его ошибка также требует preserve+guarded teardown+fresh stand, не retry
add_vhost/add_user. Readiness helper сам выполняет только inspect/pg_isready/check_running;
cleanup — отдельная failure branch shell-команды выше.

Запустите supervised processes в отдельных terminals, сохраняя outputs и accounting wrappers. Только loopback55442/5675/8094/8093/15675 и собственная internal network; существующий Data8092 сохраняется.

```sh
set -eu
"$PYTHON_BIN" test/runtime/collection/supervisor.py --state-dir "$STAND_DIR" --allow-isolated-mutation
"$PYTHON_BIN" test/runtime/collection/forward.py --state-dir "$STAND_DIR"
"$PYTHON_BIN" test/runtime/collection/broker.py --state-dir "$STAND_DIR" --allow-isolated-mutation
```

Provision уже создал own readonly90 collector config до Rabbit boot; redundant Docker
`-p` отсутствуют, host exposure только forward.py. Broker включает поставленный
rabbitmq_management через `--user rabbitmq`, проверяет collector `{ok,false}` и
создаёт own restricted vhosts/users. Install/restart отсутствует. До prepare нужны
READY broker/supervisor receipts; prepare дополнительно проверяет обе queue gauges.
Все current helpers автоматические: readonly90 config/collector readiness, exact
C2 management permissions, source activation futurepoll guard/finally cleanup,
safe fault frames и bound drain queries уже внутри поставленного harness. Не нужны
ручные task-specific SQL files, permission grants, source snapshots из tasks или
редактирование job/lease/retry counters.

На macOS до prepare запустите bounded tracked caffeinate wrapper в отдельном terminal. Его собственный/wait4 child peak учитываются в aggregate; настройки OS не меняются. Stop file создаётся после per-case PASS до finish либо при retirement failed stand.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import os,sys,time,signal
from pathlib import Path
sys.path.insert(0,'test/runtime/collection');import processes
p=Path(os.environ['STAND_DIR']);processes.track_self(p,'suite-wrapper');running=True
def stop(_sig,_frame):
    global running
    running=False
signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
child=processes.Process(['/usr/bin/caffeinate','-dimsu','-t','2820'],role='caffeinate')
try:
    deadline=time.monotonic()+2820
    while running and time.monotonic()<deadline and not (p/'caffeinate-stop-request').exists() and child.poll() is None:time.sleep(.2)
finally:child.terminate();child.wait(3)
PYCODE
```

После source/binary review и explicit actual authorization привяжите существующее решение к новому run ID и exact binary manifest. Поле authority ниже заменяется реальной записью authorization; создание JSON не является independent approval.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import os,json,hashlib
from pathlib import Path
p=Path(os.environ['STAND_DIR']);b=Path(os.environ['BIN_DIR']);s=json.loads((p/'state.json').read_text())
gate={'actual_authorized':True,'run_id':s['run_id'],'binary_freeze_sha256':hashlib.sha256((b/'binary-freeze.json').read_bytes()).hexdigest(),'authority':'REPLACE with recorded source/binary/actual authorization reference'}
(p/'coordinator-gate.json').write_text(json.dumps(gate,indent=2)+'\n')
PYCODE
"$PYTHON_BIN" test/runtime/collection/actual.py prepare --state-dir "$STAND_DIR" --binaries "$BIN_DIR" --coordinator-gate "$STAND_DIR/coordinator-gate.json" --allow-isolated-mutation
```

Prepare создаёт own FROMscratch images offline, явно migrate/seed, проверяет valid startup и fresh monitors до actual-started. UNKNOWN/FAIL навсегда taint-ит stand: сохранить receipts, exact teardown, новая попытка без reset/reuse/cap change. Нужен новый binary output либо per-run copy approved11 binaries+identical manifest; старые image contexts сохраняются с failed attempt.

```sh
set -eu
(
  set -e
  for CASE in A01 A02 A03 A04 A05 A06 A07 A08 A09 A10; do
    "$PYTHON_BIN" test/runtime/collection/actual.py case --case "$CASE" --state-dir "$STAND_DIR" --binaries "$BIN_DIR" --coordinator-gate "$STAND_DIR/coordinator-gate.json" --allow-isolated-mutation
  done
  touch "$STAND_DIR/caffeinate-stop-request"
  "$PYTHON_BIN" test/runtime/collection/actual.py finish --state-dir "$STAND_DIR" --binaries "$BIN_DIR" --coordinator-gate "$STAND_DIR/coordinator-gate.json" --allow-isolated-mutation
)
```

Finish запускается только после10per-case PASS: declared surfaces scan, full-span/resources/canary/queue validation, затем exact own teardown. Tracked wrapper завершается до finish для wait4 peak evidence; RAM не исключается из aggregate. При failure — guarded harness teardown с сохранением истории:

```sh
set -eu
"$PYTHON_BIN" test/runtime/collection/harness.py teardown --state-dir "$STAND_DIR" --allow-isolated-mutation
```


`finish` выполняется только после всех десяти per-case PASS и является A11:
full-span resources/canary/ML method counter0, queue/log surfaces, exact own teardown
и baseline working-ID comparison. Не объединять partial/diagnostic runs в full PASS.
A10 new API modeoff сохраняет authorized GET200/POST503; old BASE API допускается
лишь после всех writers/transactions/connections drained, все sources paused, PG
readonly. Не обещается C2 protection после unrestricted old-binary write re-enable.

## Reuse approved binaries и failure cleanup

Вместо rebuild можно использовать уже отдельно одобренные11 binaries и **тот же**
`binary-freeze.json`, только если independently verified source/SDK bindings и каждый
streaming SHA совпадают. Скопируйте exact11 binary entries+manifest в новый BIN_DIR;
никакие старые image-context/state/receipts туда не переносятся. Никогда не заменяйте
binary по совпавшему имени/version без SHA/approval. Fresh build выше — автономный
default; reuse cache не отменяет new source approval/own run authorization.

После любого FAIL/UNKNOWN сохранить primary/secondary receipts, stop tracked
caffeinate wrapper, exact guarded teardown по recorded state; не docker prune.
Нельзя управлять baseline working containers или объявлять неизвестный/неатрибутированный
volume удалённым. General teardown не превращает failed run в accepted. Состояние и
история сохраняются вне checkout, следующая попытка — новый RUN_ROOT/state/image
context. Для partial setup exact ledger также определяет допустимые owned resources.

## Source checks

```sh
set -eu
python3 -m unittest discover -s test/runtime/collection -p 'test_*.py' -v
```

Recorded SamplerSuccessDeadline30 suite78PASS относится к source controls; новый запуск
сверяется по собственному output, не по этой строке. Production mandatory gates:
`make check`, isolated `TEST_DATABASE_URL=... make integration`, `make build`.
Никакие unit/PG/module PASS не заменяют fresh actual A01–A11.
