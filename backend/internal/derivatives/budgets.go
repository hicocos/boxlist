package derivatives

import (
	"context"
	"io"
	"time"
)

func waitDerivativePace(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Equal endpoints mean the whole day; wraparound windows are supported.
// The configured process timezone also governs the displayed rule schedule.
func (m *Manager) inRunWindow(now time.Time) bool {
	start, end, hour := int(m.cfg.RunStartHour), int(m.cfg.RunEndHour), now.Hour()
	if start == end {
		return true
	}
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}

type derivativePacedReader struct {
	manager  *Manager
	ctx      context.Context
	source   io.Reader
	rate     int64
	started  time.Time
	received int64
}

func (r *derivativePacedReader) Read(buf []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(buf) > 64<<10 {
		buf = buf[:64<<10]
	}
	n, err := r.source.Read(buf)
	r.received += int64(n)
	if r.manager != nil && n > 0 {
		r.manager.paceMu.Lock()
		now := time.Now()
		start := r.manager.paceNext
		if start.Before(now) {
			start = now
		}
		target := start.Add(time.Duration(n) * time.Second / time.Duration(r.rate))
		r.manager.paceNext = target
		r.manager.paceMu.Unlock()
		if e := waitDerivativePace(r.ctx, time.Until(target)); e != nil {
			return n, e
		}
		return n, err
	}
	// At most one small chunk per worker is read ahead. The manager ledger keeps
	// total source transfer pacing unchanged as concurrency increases.
	target := time.Duration(r.received) * time.Second / time.Duration(r.rate)
	if paceErr := waitDerivativePace(r.ctx, target-time.Since(r.started)); paceErr != nil {
		return n, paceErr
	}
	return n, err
}
func (m *Manager) pacedSource(ctx context.Context, source io.Reader) io.Reader {
	return &derivativePacedReader{manager: m, ctx: ctx, source: source, rate: m.cfg.DownloadBytesPerSecond, started: time.Now()}
}
