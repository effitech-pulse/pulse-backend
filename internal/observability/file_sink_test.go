package observability

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileRotationBeforeAppendRetainsThreeBoundedFiles(t *testing.T) {
	dir := t.TempDir()
	raw := append(bytes.Repeat([]byte(" "), 8191), '\n')
	for range 4 {
		sink := newFileSink(dir, newTelemetryMetrics())
		for range 256 {
			if !sink.tryEmit(raw) {
				t.Fatal("unexpected queue loss")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := sink.Shutdown(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatal("rotation retained extra files", len(entries))
	}
	for _, e := range entries {
		st, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil || st.Size() != 2*1024*1024 || st.Mode().Perm() != 0640 {
			t.Fatal("bad rotation", st, err)
		}
	}
}
func TestFileIOFailureContinuesAndCountsEveryLoss(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	m := newTelemetryMetrics()
	sink := newFileSink(dir, m)
	for range 3 {
		sink.tryEmit([]byte("{}\n"))
	}
	if err := sink.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.snapshot().LogDroppedTotal != 3 {
		t.Fatal("IO failure hidden", m.snapshot())
	}
}
