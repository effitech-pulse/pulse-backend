package observability

import (
	"context"
	"errors"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var errTelemetry = errors.New("telemetry export incomplete")

type countingProcessor struct {
	mu                            sync.Mutex
	exporter                      sdktrace.SpanExporter
	metrics                       *telemetryMetrics
	queue                         []sdktrace.ReadOnlySpan
	accepted, completed, failedAt uint64
	closed, abandoned             bool
	wake                          chan struct{}
	changed                       chan struct{}
	done                          chan struct{}
}

func newCountingProcessor(e sdktrace.SpanExporter, m *telemetryMetrics) *countingProcessor {
	p := &countingProcessor{exporter: e, metrics: m, wake: make(chan struct{}, 1), changed: make(chan struct{}), done: make(chan struct{})}
	go p.run()
	return p
}
func (*countingProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (p *countingProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	if !s.SpanContext().IsSampled() {
		return
	}
	p.metrics.operation(s)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		p.metrics.drop("traces", "closed", 1)
		return
	}
	if len(p.queue) == 256 {
		p.metrics.drop("traces", "queue_full", 1)
		return
	}
	p.queue = append(p.queue, s)
	p.accepted++
	p.metrics.traceQueue.Store(int64(len(p.queue)))
	if len(p.queue) >= 64 {
		p.notify()
	}
}
func (p *countingProcessor) notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *countingProcessor) signal() { close(p.changed); p.changed = make(chan struct{}) }
func (p *countingProcessor) run() {
	defer close(p.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.wake:
		case <-ticker.C:
		}
		for {
			p.mu.Lock()
			if p.abandoned {
				p.mu.Unlock()
				return
			}
			n := min(len(p.queue), 64)
			if n == 0 {
				closed := p.closed
				p.mu.Unlock()
				if closed {
					p.shutdownExporter()
					return
				}
				break
			}
			batch := append([]sdktrace.ReadOnlySpan(nil), p.queue[:n]...)
			p.queue = p.queue[n:]
			p.metrics.traceQueue.Store(int64(len(p.queue)))
			end := p.completed + uint64(n)
			p.mu.Unlock()
			reason := p.export(batch)
			p.mu.Lock()
			if !p.abandoned {
				if reason != "" {
					p.metrics.drop("traces", "export_failed", int64(n))
					p.metrics.exportFailure("traces", reason)
					if p.failedAt == 0 {
						p.failedAt = end - uint64(n) + 1
					}
				}
				p.completed = end
				p.signal()
			}
			stop := p.abandoned
			p.mu.Unlock()
			if stop {
				return
			}
		}
	}
}
func (p *countingProcessor) export(batch []sdktrace.ReadOnlySpan) (reason string) {
	defer func() {
		if recover() != nil {
			reason = "internal"
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := p.exporter.ExportSpans(ctx, batch); err != nil {
		if ctx.Err() != nil {
			return "timeout"
		}
		return "unavailable"
	}
	return ""
}
func (p *countingProcessor) shutdownExporter() {
	defer func() { _ = recover() }()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = p.exporter.Shutdown(ctx)
}
func (p *countingProcessor) ForceFlush(ctx context.Context) error {
	p.mu.Lock()
	target := p.accepted
	p.notify()
	for p.completed < target && !p.abandoned {
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return errTelemetry
		}
		p.mu.Lock()
	}
	failed := p.abandoned || (p.failedAt != 0 && p.failedAt <= target)
	p.mu.Unlock()
	if failed {
		return errTelemetry
	}
	return nil
}
func (p *countingProcessor) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.notify()
	p.mu.Unlock()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.mu.Lock()
		p.abandoned = true
		p.metrics.drop("traces", "shutdown_timeout", int64(p.accepted-p.completed))
		p.queue = nil
		p.metrics.traceQueue.Store(0)
		p.signal()
		p.mu.Unlock()
		return errTelemetry
	}
}
