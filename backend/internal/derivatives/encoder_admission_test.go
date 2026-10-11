//go:build linux

package derivatives

import (
	"context"
	"testing"
	"time"
)

func TestEncoderSharedAdmissionWaitsAndCancelsWithoutOvercommit(t *testing.T) {
	state := func() encoderMemorySample {
		return encoderMemorySample{parentRSS: 100 << 20, containerLimit: 768 << 20, containerUsed: 100 << 20}
	}
	first, release1, e := encoderReserveWith(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer release1()
	second, release2, e := encoderReserveWith(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer release2()
	if first+second+(100<<20)+encoderMemoryReserve > encoderContainerBudget {
		t.Fatal("reservation overcommit", first, second)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, release, e := encoderReserveWith(ctx, state)
		if release != nil {
			release()
		}
		done <- e
	}()
	select {
	case e := <-done:
		t.Fatal("third admission incorrectly succeeded", e)
	case <-time.After(40 * time.Millisecond):
	}
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancel ignored")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked cancellation")
	}
	release1()
	release1()
	_, third, e := encoderReserveWith(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	third()
}
