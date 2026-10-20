package observability

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type fileSink struct {
	mu        sync.Mutex
	queue     chan []byte
	done      chan struct{}
	closed    bool
	dir       string
	metrics   *telemetryMetrics
	pending   int64
	abandoned bool
}

func newFileSink(dir string, m *telemetryMetrics) *fileSink {
	s := &fileSink{queue: make(chan []byte, 256), done: make(chan struct{}), dir: dir, metrics: m}
	m.logCapacity.Store(256)
	go s.run()
	return s
}
func (s *fileSink) tryEmit(raw []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(raw) > 8192 {
		s.metrics.drop("logs", "oversize", 1)
		return false
	}
	if s.closed {
		s.metrics.drop("logs", "closed", 1)
		return false
	}
	select {
	case s.queue <- append([]byte(nil), raw...):
		s.pending++
		s.metrics.logQueue.Store(int64(len(s.queue)))
		return true
	default:
		s.metrics.drop("logs", "queue_full", 1)
		return false
	}
}
func (s *fileSink) run() {
	defer close(s.done)
	for raw := range s.queue {
		s.mu.Lock()
		if s.abandoned {
			s.mu.Unlock()
			return
		}
		s.metrics.logQueue.Store(int64(len(s.queue)))
		s.mu.Unlock()
		ok := s.write(raw)
		s.mu.Lock()
		if !s.abandoned {
			if !ok {
				s.metrics.drop("logs", "io_failed", 1)
			}
			s.pending--
		}
		s.mu.Unlock()
	}
}
func (s *fileSink) write(raw []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	path := filepath.Join(s.dir, "pulse.jsonl")
	st, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return false
	}
	if st != nil && st.Size()+int64(len(raw)) > 2*1024*1024 {
		if err = os.Remove(path + ".2"); err != nil && !os.IsNotExist(err) {
			return false
		}
		if err = os.Rename(path+".1", path+".2"); err != nil && !os.IsNotExist(err) {
			return false
		}
		if err = os.Rename(path, path+".1"); err != nil {
			return false
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return false
	}
	n, err := f.Write(raw)
	closeErr := f.Close()
	return err == nil && closeErr == nil && n == len(raw)
}
func (s *fileSink) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.queue)
	s.mu.Unlock()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		s.abandoned = true
		s.metrics.drop("logs", "shutdown_timeout", s.pending)
		s.pending = 0
		for range s.queue {
		}
		s.metrics.logQueue.Store(0)
		s.mu.Unlock()
		return errTelemetry
	}
}
