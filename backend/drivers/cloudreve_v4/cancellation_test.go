package cloudreve_v4

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCancellationDuringSecondPageReturnsNoPartialResult(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			replyData(w, map[string]any{"files": fixtureFiles(0, 100, 1), "pagination": map[string]any{"page": 0, "page_size": 100, "total_items": 230}})
			return
		}
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		objs, err := listTestDir(d, ctx)
		if objs != nil {
			t.Error("partial page returned after cancellation")
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second page not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second page did not cancel")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestTokenRefreshCancellation(t *testing.T) {
	started := make(chan struct{})
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/session/token/refresh" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	})
	d.RefreshToken = "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":9999999999}`)) + ".test"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := listTestDir(d, ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not cancel")
	}
}
