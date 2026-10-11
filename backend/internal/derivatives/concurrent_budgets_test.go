package derivatives

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestConcurrentSourcePacingKeepsAggregateRate(t *testing.T) {
	m := coreManager(t)
	m.cfg.DownloadBytesPerSecond = 64 << 10
	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := io.Copy(io.Discard, m.pacedSource(context.Background(), bytes.NewReader(make([]byte, 8<<10))))
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 450*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("aggregate rate not preserved", elapsed)
	}
}
