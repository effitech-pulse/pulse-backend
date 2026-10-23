# Isolated collection runtime tooling

This opt-in harness uses synthetic generated inputs, exact cached image IDs and
new labelled containers/network/volumes. It does not load project `.env`, import
Data, call real sources or install packages. All evidence and private URL files
belong in an operator-selected directory outside the product checkout.

`harness.py preflight --state-dir DIR --baseline BASELINE.json` validates cached
images, eight recorded working container identities and free loopback ports.
API host port is 8094 by coordinator execution ruling: an existing process owns
8092 and must remain untouched. Fixture uses 8093; PG 55442; AMQP 5675;
management 15675. In Docker environments suppressing published ports on internal
networks, PG/AMQP/API/fixture/management use the bounded stdio forwarder below. Published ports are not
proof of effective reachability.

Provision only test infrastructure explicitly:

```sh
python3 harness.py provision --state-dir DIR --baseline BASELINE.json --allow-isolated-mutation
python3 broker.py --state-dir DIR --allow-isolated-mutation
```

Infrastructure provision checks only PG/broker ports, records exact resource IDs,
and creates PG/Rabbit on an internal network with 384 MiB cgroup limits each.
It does not migrate, seed or execute acceptance cases. Credentials are random and
written with mode0600; command errors never echo stderr or credentials.

The static fixture also implements `tunnel` and `monitor ROLE`. Copy only that
own-built binary to `/fixture` in the two new PG/Rabbit containers. Then:

```sh
python3 forward.py --state-dir DIR
python3 supervisor.py --state-dir DIR --allow-isolated-mutation
```

The forwarder binds only loopback55442/5675/8094/8093/15675, uses one persistent `docker exec -i`
to the recorded own PG ID and allows only fixed PG5432/Rabbit5672/api8000/fixture8080/Rabbit15672 destinations.
Frames are at most32768 bytes and there are at most16 concurrent connections.
Payloads and credentials are never logged. The internal network remains unchanged.
The monitor uses persistent streams to obtain real cgroup current/peak counters
once per second. PG reports database/PGDATA/WAL/temp counters; Rabbit reports own
data size. All own host process RSS contributes to aggregate memory, including actual.py,
legacy-probe and Docker children. wait4 exit peaks retain transient children
between1s samples. A supplemental64MiB limit applies per host process; original
G6 container limits and the1536MiB aggregate limit remain unchanged.

The supervisor fails closed on stale/unavailable measurements or budget breach,
checks exact IDs/labels before mutation, then stops only owned containers. Keep
all failed attempts; startup checks are not acceptance PASS. Process fault actions
must register an expected stopped container in state before killing/stopping it,
and clear that registration after observing its restart. Each lifecycle event
is checked against the owned container’s physical state. Never hide an unexplained
monitor loss as an expected fault. Monitor attachment has a3s deadline;
full-span validation rejects missing roles/metrics, stale counters, gaps across
files, early supervision loss, UNKNOWN/FAIL stops and absent child peak evidence. The full suite must create `actual-started.json`
with `unix_seconds` and update `case_started_at` to enforce45min/5min deadlines.

`build.py` requires an explicit combined source freeze (`status=FROZEN`, exact base
and per-file SHA256). It makes offline CGO0 Darwin/Linux arm64 binaries with
`timetzdata`, readonly dependencies and no package download. Old API comes from
an immutable `git archive` at the recorded base and has its own temporary GOWORK;
it never resolves the current backend source. Build outputs belong in an owned
external temporary directory, with their hashes exported as evidence. The local
Dockerfile uses only `FROM scratch` and these own files; build with
`--pull=false --network=none`. No product directories are mounted into containers.

The fixture listens on8080 and the forbidden gRPC endpoint50051. `/control` changes
only embedded synthetic response generation: revision, delay,503/reset/remove,
exact-byte padding, duplicate/raw-row overflow, hole or explicit-empty modes.
`/counters` reports source GETs and actual gRPC method counts; transport connections
are not RPCs. Full20raw fields must be described in the trusted manifest.

`scan.py --canary-file PRIVATE_FILE --surface FILE ...` checks every explicitly
captured export surface without echoing the canary or matching content. Missing
surfaces fail; an empty scan cannot establish absence of leakage. Private raw
payloads stay in the task directory. Export safe hashes/counts/statuses only.

Before acceptance, an operator must explicitly migrate15, seed own schema/guard,
accounts/wells/sources, validate a maximum valid input on the frozen binaries, and
execute A01–A11 with real process/session faults. These scripts do not turn an
unrun case into PASS. PG integration tests and actual cases require exclusive
mutation windows coordinated externally.

Teardown uses exact recorded IDs and validates labels of all resources before
removal, then rechecks the baseline working identities:

```sh
python3 harness.py teardown --state-dir DIR --allow-isolated-mutation
```

Run local helper checks with Python standard library and the existing Go toolchain:
`python3 -m unittest discover -s . -p 'test_*.py'`,
`go test ./test/runtime/collection/fixture` from the backend root.

Review regression checks are offline:

```sh
python3 -m unittest discover -s test/runtime/collection -p 'test_*.py' -v
python3 test/runtime/collection/check_config.py --go GO --gowork WORK --artifacts NEW_DIR
```

Generated C2 URLs deliberately have no query; Q1 heartbeat settings are separate.
Terminal duplicate proofs require the same event’s durable ignored disposition,
new inbox timestamp, empty ready/unacked queues and unchanged quarantine, with
a no-consumer negative control. A04 additionally requires real broker redelivery.
Rollback checks compare the whole requested primary-key set and recovered capture
values/provenance, including unexpected timestamps and preexisting rows.

The supervisor uses a bounded stdlib Unixsocket Docker Engine monitor adapter.
The existing Docker context must be local. Each metadata request filters ONLY
one recorded full64hex own ID, requires one exact ID/run-label/name/state match,
and reads at most1MiB. Only fixed `/fixture monitor ROLE` exec is permitted,
without env/stdin/privilege; stdout frames are at most32768bytes. Background
attachment keeps existing1s counters hot; all3s deadlines remain unchanged.
This avoids the observed3.003s running-container full-inspect latency. Engine
metadata uses the exact-ID-filtered summary and never exports raw Env. Planned
stop confirmation uses the same bounded snapshot.

Preparation publishes `actual-started.json` only after valid startup control and
fresh monitors for all required roles. This explicitly excludes setup from the
actual span; setup failures still permanently taint that stand. A failed stand
cannot be reused for acceptance.
