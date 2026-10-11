package op

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/pkg/singleflight"
)

type observedReadContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedReadContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func waitReadSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("read synchronization timed out")
	}
}

func TestSharedReadOwnerCancellationRetriesLiveWaiter(t *testing.T) {
	var group singleflight.Group[int]
	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	defer ownerCancel()
	started := make(chan struct{})
	ownerDone := make(chan struct{})
	go func() {
		defer close(ownerDone)
		_, err := doReadWithContext(ownerCtx, &group, "same", func() (int, error) { close(started); <-ownerCtx.Done(); return 0, ownerCtx.Err() })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("owner err=%v", err)
		}
	}()
	waitReadSignal(t, started)
	waiter := &observedReadContext{Context: context.Background(), waiting: make(chan struct{})}
	waiterDone := make(chan struct{})
	var retried atomic.Int32
	go func() {
		defer close(waiterDone)
		value, err := doReadWithContext(waiter, &group, "same", func() (int, error) { retried.Add(1); return 42, nil })
		if value != 42 || err != nil {
			t.Errorf("waiter value=%d err=%v", value, err)
		}
	}()
	waitReadSignal(t, waiter.waiting)
	ownerCancel()
	waitReadSignal(t, ownerDone)
	waitReadSignal(t, waiterDone)
	if retried.Load() != 1 {
		t.Fatalf("retry calls=%d", retried.Load())
	}
}

func TestSharedReadWaiterCancellationDoesNotStopOwner(t *testing.T) {
	var group singleflight.Group[int]
	started, release, ownerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	go func() {
		defer close(ownerDone)
		value, err := doReadWithContext(context.Background(), &group, "same", func() (int, error) { close(started); <-release; return 42, nil })
		if value != 42 || err != nil {
			t.Errorf("owner value=%d err=%v", value, err)
		}
	}()
	waitReadSignal(t, started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiter := &observedReadContext{Context: ctx, waiting: make(chan struct{})}
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		_, err := doReadWithContext(waiter, &group, "same", func() (int, error) { t.Error("waiter should not own the read"); return 0, nil })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiter err=%v", err)
		}
	}()
	waitReadSignal(t, waiter.waiting)
	cancel()
	waitReadSignal(t, waiterDone)
	select {
	case <-ownerDone:
		t.Fatal("waiter cancelled owner")
	default:
	}
	close(release)
	waitReadSignal(t, ownerDone)
}

func TestReadOwnDeadlineDoesNotRetry(t *testing.T) {
	var group singleflight.Group[int]
	calls := 0
	_, err := doReadWithContext(context.Background(), &group, "same", func() (int, error) { calls++; return 0, context.DeadlineExceeded })
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestPreCancelledSharedReadDoesNotStart(t *testing.T) {
	var group singleflight.Group[int]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := doReadWithContext(ctx, &group, "same", func() (int, error) { t.Error("cancelled read started"); return 0, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
