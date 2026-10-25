package bootstrap

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestEquipmentPassportPurgeLifecycle(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	purge := func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if calls.Add(1) == 1 {
			return nil
		}
		entered <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return ctx.Err()
	}
	closeGC, err := startPassportPurge(context.Background(), purge, time.Millisecond)
	if err != nil || calls.Load() != 1 {
		t.Fatal("startup purge", err, calls.Load())
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("ticker did not purge")
	}
	closeGC()
	select {
	case <-stopped:
	default:
		t.Fatal("close returned before purge stopped")
	}
	closeGC() // cleanup is idempotent.
}
func TestEquipmentPassportPurgeStartupFailure(t *testing.T) {
	sentinel := errors.New("purge failed")
	closeGC, err := startPassportPurge(context.Background(), func(context.Context) error { return sentinel }, time.Hour)
	if !errors.Is(err, sentinel) || closeGC != nil {
		t.Fatal("startup must fail closed", err)
	}
}
