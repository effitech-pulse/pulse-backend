package observability

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type barrierExporter struct {
	entered    chan struct{}
	release    chan struct{}
	calls      atomic.Int64
	fail       bool
	panicFirst bool
}

func (e *barrierExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	n := e.calls.Add(1)
	if n == 1 {
		close(e.entered)
		<-e.release
		if e.panicFirst {
			panic("PRIVATE")
		}
		if e.fail {
			return errors.New("PRIVATE")
		}
	}
	return nil
}
func (*barrierExporter) Shutdown(context.Context) error { return nil }
func sampleSpan() sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{Name: "check.execute", SpanContext: sampleContext(), StartTime: time.Now(), EndTime: time.Now().Add(time.Millisecond)}.Snapshot()
}
func TestOnEndReturnsWhileExporterBlocked(t *testing.T) {
	e := &barrierExporter{entered: make(chan struct{}), release: make(chan struct{})}
	m := newTelemetryMetrics()
	p := newCountingProcessor(e, m)
	defer close(e.release)
	for range 64 {
		p.OnEnd(sampleSpan())
	}
	<-e.entered
	done := make(chan struct{})
	go func() {
		for range 257 {
			p.OnEnd(sampleSpan())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnEnd blocked")
	}
	if s := m.snapshot(); s.TraceQueueSize != 256 || s.TraceDroppedTotal != 1 {
		t.Fatalf("bad counts: %+v", s)
	}
	for range 1743 {
		p.OnEnd(sampleSpan())
	}
	if e.calls.Load() != 1 || m.snapshot().TraceDroppedTotal != 1744 {
		t.Fatal("2000 calls escaped single bounded worker", e.calls.Load(), m.snapshot())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if p.Shutdown(ctx) == nil {
		t.Fatal("blocked exporter shutdown should time out")
	}
	if s := m.snapshot(); s.TraceDroppedTotal != 2064 {
		t.Fatalf("pending must be counted once: %+v", s)
	}
	if p.Shutdown(ctx) != nil {
		t.Fatal("repeat shutdown not idempotent")
	}
}
func TestFailedBatchCountedOnceAndWorkerContinues(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "panic"}[panics], func(t *testing.T) {
			e := &barrierExporter{entered: make(chan struct{}), release: make(chan struct{}), fail: true, panicFirst: panics}
			m := newTelemetryMetrics()
			p := newCountingProcessor(e, m)
			for range 64 {
				p.OnEnd(sampleSpan())
			}
			<-e.entered
			close(e.release)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if p.ForceFlush(ctx) == nil {
				t.Fatal("discard hidden by flush")
			}
			if s := m.snapshot(); s.TraceDroppedTotal != 64 || s.TraceExportFailedTotal != 1 {
				t.Fatalf("bad failure counts %+v", s)
			}
			p.OnEnd(sampleSpan())
			_ = p.ForceFlush(ctx)
			if e.calls.Load() != 2 {
				t.Fatal("worker stopped after failure")
			}
			_ = p.Shutdown(ctx)
		})
	}
}
func TestShutdownDeadlineNoncooperative(t *testing.T) {
	e := &barrierExporter{entered: make(chan struct{}), release: make(chan struct{})}
	m := newTelemetryMetrics()
	p := newCountingProcessor(e, m)
	for range 64 {
		p.OnEnd(sampleSpan())
	}
	<-e.entered
	start := time.Now()
	err := p.Shutdown(context.Background())
	if err == nil || time.Since(start) > 2100*time.Millisecond {
		t.Fatal("shutdown exceeded bound", err, time.Since(start))
	}
	close(e.release)
	if m.snapshot().TraceDroppedTotal != 64 {
		t.Fatal("in-flight loss missing")
	}
}

type flushContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *flushContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type twoBarrierExporter struct {
	first, second, releaseFirst, releaseSecond chan struct{}
	calls                                      atomic.Int64
}

func (e *twoBarrierExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	switch e.calls.Add(1) {
	case 1:
		close(e.first)
		<-e.releaseFirst
	case 2:
		close(e.second)
		<-e.releaseSecond
	}
	return nil
}
func (*twoBarrierExporter) Shutdown(context.Context) error { return nil }
func TestForceFlushWatermarkDoesNotWaitForLaterSpans(t *testing.T) {
	e := &twoBarrierExporter{first: make(chan struct{}), second: make(chan struct{}), releaseFirst: make(chan struct{}), releaseSecond: make(chan struct{})}
	p := newCountingProcessor(e, newTelemetryMetrics())
	for range 64 {
		p.OnEnd(sampleSpan())
	}
	<-e.first
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tracked := &flushContext{Context: ctx, waiting: make(chan struct{})}
	flushed := make(chan error, 1)
	go func() { flushed <- p.ForceFlush(tracked) }()
	<-tracked.waiting
	for range 64 {
		p.OnEnd(sampleSpan())
	}
	close(e.releaseFirst)
	<-e.second
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("later spans held flush watermark")
	}
	close(e.releaseSecond)
	_ = p.Shutdown(ctx)
}
