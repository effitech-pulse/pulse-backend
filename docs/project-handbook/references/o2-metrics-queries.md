> Снимок документа 22.10.2026. Источник: `pulse-backend/deployments/observability/metrics-queries.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Pinned Collector 0.163.1 queries


Q0 metric-only alias: `MLService/UploadPinned` → `MLService/Upload`. Legacy and strict counts/durations share the same series; these metrics cannot isolate strict latency or error rate. Trace/span names and `rpc.method` remain `UploadPinned`. Bounds remain≤63 named operations + other=64 and118 named tuples +10 fallback=128; privacy, disabled exemplars, queues and O2 pins/budgets are unchanged.
Internal scrape is `otel-collector:8888`, OTLP app metrics is `:8889`.
No application instance/version/trace IDs are metric labels. Native exporter
queue HELP text still says “batches”; with configured `sizer: bytes` the actual
capacity is **4194304 bytes** for each exporter. Four 2MiB synthetic resource
records formed one batch and produced `enqueue_failed_spans=4` in isolated proof.

Actual observed native series (no `_total` suffix in this pinned exposition):

```promql
otelcol_exporter_queue_size / otelcol_exporter_queue_capacity
sum by (exporter) (rate(otelcol_exporter_send_failed_spans[5m]))
sum by (exporter) (rate(otelcol_exporter_send_failed_log_records[5m]))
sum by (exporter) (rate(otelcol_exporter_enqueue_failed_spans[5m]))
sum by (receiver) (rate(otelcol_receiver_accepted_spans[5m]))
sum by (receiver) (rate(otelcol_receiver_refused_spans[5m]))
sum by (receiver) (rate(otelcol_receiver_accepted_log_records[5m]))
sum by (receiver) (rate(otelcol_receiver_refused_log_records[5m]))
sum by (filter) (rate(otelcol_processor_filter_logs_filtered[5m]))
```

`otelcol_exporter_enqueue_failed_log_records` is the native configured log
exporter counter name but was **not observed** in this proof; do not substitute
zero for an absent series. Only spans were deliberately overflowed. Send errors
were observed for both signals. Scrape gaps mean unknown, not zero loss.

Schema filter counter excludes stanza early drops. `drop_quiet` parser/filter
errors and truncated oversized lines have no confirmed per-reason counter here.
The explicit privacy canary measurement was **6 source lines → 2 exported safe
rows**, with no private canary anywhere in Collector diagnostics or Loki output;
unknown-event processor filter measured separately. Repeat this bounded sent vs
found check after changes/restarts; an unexplained difference fails the gate.
Loss before acknowledgement, kill/OOM and offset deletion remain unknown.

Loki query_range merges structured metadata into the returned stream map. Use
`/loki/api/v1/series` to verify **indexed** label cardinality: proof contained only
`service_name` and `deployment_environment_name`. Trace/span/request/instance IDs
remain body/structured metadata. Loki discarded counters can be absent until a
real discard; save actual `/metrics` before creating alert panels around them.

Retention is a TTL ceiling, never a disk quota. Named store volumes have no hard
quota. `scripts/o2_pilot.py` is mandatory for the supervised PREP run, polls every
5 seconds plus bounded measurement overhead, checks allocated KiB converted to
bytes and host disk free, and stops only the five telemetry services. The ephemeral
measurer has only DAC_READ_SEARCH capability to traverse private store directories;
its four mounts are read-only, network is none, rootfs is read-only and resources
are 64MiB/0.2CPU. Without this capability the actual du failed closed on permissions. Stop failure
is recorded explicitly. Existing application exporters remain bounded until the
coordinator's separate controlled rollback; no application restart is performed.

Nginx rotation is rename/create/USR1 with a 1s poll. Observed managed `.1` size
2098914 bytes was **1762 bytes above 2MiB** in the synthetic rotation proof. This
is not a workload-independent maximum: the 16MiB tmpfs is the hard filesystem
bound. A stalled OS file/stdout syscall can still block Nginx. Offset tmpfs and raw
files are disposable: deleting checkpoints can replay surviving files; inode
rotation/unlinked open descriptors and crashes can lose or duplicate records.


Grafana uses the native file provider in `grafana.ini`. Its trusted root init
copies the private host0600 secret into fresh tmpfs0400/UID472, then execs `su`
to the pinned grafana user. The server PID1 has UID472, CapEff/CapPrm zero and
NoNewPrivs=1. The init only has CHOWN/DAC_OVERRIDE/SETUID/SETGID; password bytes
are never argv/environment values. `/run.sh` `GF_*__FILE` conversion is deliberately
unused because it exports plaintext to server environment. Secret files must be
32–256 bytes, outside Git; root supplies a random private secret for actual pilot.


Fix round1: each budget lock acquisition uses nonblocking flock with a 500ms
monotonic deadline and sleeps at most10ms. A busy budget is unobservable and stops
the optional profile. Each ephemeral measurer has a unique `pulse-o2-measurer-*`
name. Its finally block attempts removal of only that exact container (timeout3s)
and confirms current absence through a successful daemon lookup (timeout3s).
Cleanup failure/daemon unavailability is explicitly unconfirmed and fails the
gate. CLI SIGTERM follows cleanup and supervisor finally; cleanup defers SIGTERM
while its bounded commands execute. SIGKILL/host crash remain outside this path.
A timed-out create RPC might complete after an absence lookup: the failure keeps
the exact owned helper identity and `creation_completion_unknown`, rather than
claiming that killing the CLI canceled the daemon operation.

Pinned stanza json_parser with `parse_ints:true` represents integer literals0/1
as Int and1.5 as Double. Only the six telemetry.snapshot queue/counter fields now
require nonnegative Int; float duration_ms/duration_s remain supported. This was
checked on the actual Collector with0/1/1.5 and an unknown nested private canary.
