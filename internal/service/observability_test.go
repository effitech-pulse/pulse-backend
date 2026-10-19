package service

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

type operationLease struct {
	ready   chan context.Context
	release chan struct{}
}

func (l *operationLease) Begin(ctx context.Context, _ string) (int64, error) {
	l.ready <- ctx
	<-l.release
	return 0, domain.NewError(domain.Unavailable, "controlled")
}
func (l *operationLease) BeginCohort(ctx context.Context, s, v string) (int64, error) {
	return l.Begin(ctx, s)
}
func (*operationLease) Persist(context.Context, int64, domain.Snapshot) error { return nil }
func (*operationLease) Fail(context.Context, int64, string) error             { return nil }
func (*operationLease) Close()                                                {}

type operationStore struct {
	RunStore
	lease RunLease
}

func (s operationStore) Reserve(context.Context) (RunLease, bool, error) { return s.lease, true, nil }
func TestStartRunCapturesCorrelationBeforeDetachedWorker(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	p := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer otel.SetTracerProvider(old)
	defer p.Shutdown(context.Background())
	lease := &operationLease{ready: make(chan context.Context, 1), release: make(chan struct{})}
	ml := NewML(operationStore{lease: lease}, nil, nil, RunOptions{Timeout: time.Second})
	ctx, parent := observability.Start(observability.WithRequestID(context.Background(), "request_fixture_1234"), "HTTP parent", trace.SpanKindServer)
	ctx, cancel := context.WithCancel(ctx)
	started, err := ml.StartRun(ctx, "manual")
	if err != nil || !started {
		t.Fatal(started, err)
	}
	cancel()
	parent.End()
	var work context.Context
	select {
	case work = <-lease.ready:
	case <-time.After(time.Second):
		t.Fatal("worker missing")
	}
	if work.Err() != nil || observability.RequestID(work) != "request_fixture_1234" {
		close(lease.release)
		ml.Wait()
		t.Fatal("worker lost safe context")
	}
	close(lease.release)
	ml.Wait()
	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatal("worker span missing")
	}
	worker := spans[1]
	if worker.Name != "ml.run" || worker.Parent.IsValid() || len(worker.Links) != 1 || worker.Links[0].SpanContext.SpanID() != parent.SpanContext().SpanID() {
		t.Fatal("worker incorrectly parents expired HTTP")
	}
}
