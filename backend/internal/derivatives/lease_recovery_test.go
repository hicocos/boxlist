package derivatives

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func leaseFixture(t *testing.T) (*Manager, Job, Item) {
	t.Helper()
	m := coreManager(t)
	j := coreJob(t, m, coreStorage(t, m))
	j.State = "processing"
	j.Fingerprint = digest(t.Name())
	j.ActiveKey = &j.Fingerprint
	if e := m.db.Save(&j).Error; e != nil {
		t.Fatal(e)
	}
	item := coreItem(t, m, j, j.Path+"/lease.jpg")
	item.Attempts = 3
	item.LeaseUntil = time.Now().Add(time.Hour)
	item.NextAttempt = time.Now().Add(time.Hour)
	if e := m.db.Save(&item).Error; e != nil {
		t.Fatal(e)
	}
	if e := m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, "thumb").Updates(map[string]any{
		"state": "ready", "artifact_id": "kept-artifact", "attempts": 1, "bytes": 99,
	}).Error; e != nil {
		t.Fatal(e)
	}
	if e := m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, "reader").Updates(map[string]any{
		"state": "active", "attempts": 2,
	}).Error; e != nil {
		t.Fatal(e)
	}
	return m, j, item
}

// Callbacks inject failures into real GORM transactions; no sockets, busy-lock
// timing assumptions, driver replacement, or encoder process is required.
func leaseCallback(t *testing.T, m *Manager, query bool, callback func(*gorm.DB)) func() {
	t.Helper()
	name := "lease_recovery:" + t.Name()
	var e error
	if query {
		e = m.db.Callback().Query().Before("gorm:query").Register(name, callback)
	} else {
		e = m.db.Callback().Update().Before("gorm:update").Register(name, callback)
	}
	if e != nil {
		t.Fatal(e)
	}
	var once sync.Once
	remove := func() {
		once.Do(func() {
			if query {
				e = m.db.Callback().Query().Remove(name)
			} else {
				e = m.db.Callback().Update().Remove(name)
			}
			if e != nil {
				t.Error(e)
			}
		})
	}
	t.Cleanup(remove)
	return remove
}

func leaseRow(t *testing.T, m *Manager, id uint) (Item, map[string]DerivativeItemProfile) {
	t.Helper()
	var item Item
	if e := m.db.Preload("Result").First(&item, id).Error; e != nil {
		t.Fatal(e)
	}
	profiles := map[string]DerivativeItemProfile{}
	for _, p := range item.Result {
		profiles[p.Profile] = p
	}
	return item, profiles
}

func leaseAssertReady(t *testing.T, profiles map[string]DerivativeItemProfile) {
	t.Helper()
	p := profiles["thumb"]
	if p.State != "ready" || p.ArtifactID != "kept-artifact" || p.Attempts != 1 || p.Bytes != 99 {
		t.Fatalf("recovery changed a published profile: %+v", p)
	}
}

func TestLeaseFailItemRetriesEveryLocalTransition(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		query       bool
	}{
		{"job_read", "DerivativeJob", true},
		{"item_read", "DerivativeItem", true},
		{"ready_count", "DerivativeItemProfile", true},
		{"item_update", "DerivativeItem", false},
		{"profile_update", "DerivativeItemProfile", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, j, item := leaseFixture(t)
			var hits atomic.Int32
			remove := leaseCallback(t, m, tc.query, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == tc.model && hits.Add(1) <= 2 {
					tx.AddError(errors.New("database is locked (injected)"))
				}
			})
			m.gate.Lock()
			e := m.failItem(j, item, &sourceHTTPError{Status: 403})
			m.gate.Unlock()
			remove()
			if e != nil || hits.Load() != 3 {
				t.Fatalf("local transaction was not retried: calls=%d error=%v", hits.Load(), e)
			}
			got, profiles := leaseRow(t, m, item.ID)
			if got.State != "partial_failed" || got.Attempts != 3 || !got.LeaseUntil.IsZero() || profiles["reader"].State != "failed" || profiles["reader"].Attempts != 2 {
				t.Fatalf("incorrect terminal state: item=%+v profiles=%+v", got, profiles)
			}
			leaseAssertReady(t, profiles)
			// A stale completion callback must not pause the job or refund again.
			if e = m.failItem(j, item, ErrDisk); e != nil {
				t.Fatal(e)
			}
			again, _ := leaseRow(t, m, item.ID)
			current, e := m.getJob(j.ID)
			if e != nil || again.Attempts != got.Attempts || current.State != "processing" {
				t.Fatalf("terminal transition was not idempotent: %+v %+v %v", again, current, e)
			}
		})
	}
}

func TestLeaseFailItemExhaustionReportsAndRollsBack(t *testing.T) {
	for _, query := range []bool{true, false} {
		t.Run(fmt.Sprintf("query_%t", query), func(t *testing.T) {
			m, j, item := leaseFixture(t)
			busy := errors.New("SQLITE_BUSY: injected terminal state failure")
			var hits atomic.Int32
			remove := leaseCallback(t, m, query, func(tx *gorm.DB) {
				model := "DerivativeItemProfile"
				if query {
					model = "DerivativeJob"
				}
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == model {
					hits.Add(1)
					tx.AddError(busy)
				}
			})
			e := m.failItem(j, item, ErrDisk)
			remove()
			if !errors.Is(e, busy) || hits.Load() != 4 {
				t.Fatalf("SQL failure discarded or unbounded retries: %v calls=%d", e, hits.Load())
			}
			got, profiles := leaseRow(t, m, item.ID)
			current, e := m.getJob(j.ID)
			if e != nil || current.State != "processing" || got.State != "active" || got.Attempts != 3 || profiles["reader"].State != "active" || profiles["reader"].Attempts != 2 {
				t.Fatalf("partial failure transaction committed: %+v %+v %+v %v", current, got, profiles, e)
			}
			if e = m.recoverLeases(); e != nil {
				t.Fatal(e)
			}
			got, profiles = leaseRow(t, m, item.ID)
			if got.State != "queued" || got.Attempts != 2 || !got.LeaseUntil.IsZero() || profiles["reader"].State != "queued" || profiles["reader"].Attempts != 1 {
				t.Fatalf("last failed SQL write stranded future lease: %+v %+v", got, profiles)
			}
			leaseAssertReady(t, profiles)
		})
	}
}

func TestLeaseDatabaseFailureDoesNotConsumeEncodingAttempts(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprintf("shutdown_%t", shutdown), func(t *testing.T) {
			m, j, item := leaseFixture(t)
			cause := errors.New("database is locked")
			state := "retry_wait"
			if shutdown {
				m.cancel()
				cause = context.Canceled
				state = "queued"
			}
			if e := m.failItem(j, item, cause); e != nil {
				t.Fatal(e)
			}
			got, profiles := leaseRow(t, m, item.ID)
			if got.State != state || got.Attempts != 2 || !got.LeaseUntil.IsZero() || profiles["reader"].State != state || profiles["reader"].Attempts != 1 {
				t.Fatalf("local SQL/shutdown consumed attempt budget: %+v %+v", got, profiles)
			}
			if !shutdown && (got.NextAttempt.IsZero() || got.Error != "derivative_database_error") {
				t.Fatalf("database retry lacks local backoff/diagnostic: %+v", got)
			}
			leaseAssertReady(t, profiles)
			if e := m.failItem(j, item, cause); e != nil {
				t.Fatal(e)
			}
			again, ps := leaseRow(t, m, item.ID)
			if again.Attempts != got.Attempts || ps["reader"].Attempts != profiles["reader"].Attempts {
				t.Fatal("duplicate failure refunded an attempt twice")
			}
		})
	}
}

func TestLeaseDatabaseFailurePreservesCommittedProfileFailures(t *testing.T) {
	m, j, item := leaseFixture(t)
	if e := m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, "reader").Updates(map[string]any{
		"state": "retry_wait", "error": "encode_timeout", "attempts": 2,
	}).Error; e != nil {
		t.Fatal(e)
	}
	if e := m.failItem(j, item, errors.New("database is locked")); e != nil {
		t.Fatal(e)
	}
	_, profiles := leaseRow(t, m, item.ID)
	if p := profiles["reader"]; p.State != "retry_wait" || p.Error != "encode_timeout" || p.Attempts != 2 {
		t.Fatalf("database retry rewrote a committed encoding result: %+v", p)
	}
	leaseAssertReady(t, profiles)
}

func TestLeaseRecoveryFutureExpiredPausedAndCancelled(t *testing.T) {
	for _, state := range []string{"processing", "paused", "cancelled", "cancelling"} {
		for _, expiry := range []time.Duration{-time.Hour, time.Hour} {
			t.Run(fmt.Sprintf("%s_%s", state, expiry), func(t *testing.T) {
				m, j, item := leaseFixture(t)
				if e := m.db.Model(&Job{}).Where("id = ?", j.ID).Updates(map[string]any{"state": state, "pause_reason": "manual"}).Error; e != nil {
					t.Fatal(e)
				}
				if e := m.db.Model(&Item{}).Where("id = ?", item.ID).Update("lease_until", time.Now().Add(expiry)).Error; e != nil {
					t.Fatal(e)
				}
				for n := 0; n < 2; n++ {
					if e := m.recoverLeases(); e != nil {
						t.Fatal(e)
					}
				}
				got, profiles := leaseRow(t, m, item.ID)
				itemState, jobState := "queued", state
				if state == "processing" {
					jobState = "queued"
				} else if state == "cancelled" || state == "cancelling" {
					itemState, jobState = "cancelled", "cancelled"
				}
				current, e := m.getJob(j.ID)
				if e != nil || current.State != jobState || got.State != itemState || got.Stage != "recovered" || got.Attempts != 2 || !got.LeaseUntil.IsZero() || !got.NextAttempt.IsZero() || profiles["reader"].State != itemState || profiles["reader"].Attempts != 1 {
					t.Fatalf("non-idempotent or unsafe lease recovery: %+v %+v %+v %v", current, got, profiles, e)
				}
				if state == "paused" && current.PauseReason != "manual" {
					t.Fatal("manual pause was lost")
				}
				leaseAssertReady(t, profiles)
			})
		}
	}
}

func TestLeaseRecoveryTransactionRetriesWithoutDoubleRefund(t *testing.T) {
	m, j, item := leaseFixture(t)
	var hits atomic.Int32
	remove := leaseCallback(t, m, false, func(tx *gorm.DB) {
		// Fail after both profile and item writes, proving they roll back.
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "DerivativeJob" && hits.Add(1) <= 2 {
			tx.AddError(errors.New("database is locked"))
		}
	})
	if e := m.recoverLeases(); e != nil {
		t.Fatal(e)
	}
	remove()
	got, profiles := leaseRow(t, m, item.ID)
	current, e := m.getJob(j.ID)
	if e != nil || hits.Load() != 3 || got.Attempts != 2 || profiles["reader"].Attempts != 1 || current.State != "queued" || current.ActiveKey == nil || *current.ActiveKey != j.Fingerprint {
		t.Fatalf("retry refunded more than once or dropped dedup key: %+v %+v %+v hits=%d %v", got, profiles, current, hits.Load(), e)
	}
	leaseAssertReady(t, profiles)
}

func TestLeaseRecoveryFailureBlocksClaimsAndRollsBack(t *testing.T) {
	m, j, item := leaseFixture(t)
	busy := errors.New("database is locked")
	var hits atomic.Int32
	remove := leaseCallback(t, m, false, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "DerivativeJob" {
			hits.Add(1)
			tx.AddError(busy)
		}
	})
	if m.step() {
		t.Fatal("step continued claiming after failed orphan recovery")
	}
	remove()
	got, profiles := leaseRow(t, m, item.ID)
	current, e := m.getJob(j.ID)
	if e != nil || hits.Load() != 4 || current.State != "processing" || got.State != "active" || got.Attempts != 3 || profiles["reader"].State != "active" || profiles["reader"].Attempts != 2 || len(m.activeClaims) != 0 {
		t.Fatalf("failed recovery committed partial state: %+v %+v %+v calls=%d %v", current, got, profiles, hits.Load(), e)
	}
	if e = m.recoverLeases(); e != nil {
		t.Fatal(e)
	}
	got, profiles = leaseRow(t, m, item.ID)
	if got.State != "queued" || got.Attempts != 2 || profiles["reader"].Attempts != 1 {
		t.Fatalf("orphan was not recoverable after SQL recovered: %+v %+v", got, profiles)
	}
}

func TestLeaseStepRecoversLastStrandedActiveWithoutRestart(t *testing.T) {
	m := coreManager(t)
	m.cfg.ScanIntervalMillis = 0
	d := &coreFakeDriver{listErr: &sourceHTTPError{Status: 403}}
	s := coreRegistered(t, m, d)
	j, e := m.Submit(context.Background(), 1, SubmitRequest{Path: s.MountPath})
	if e != nil {
		t.Fatal(e)
	}
	var hits atomic.Int32
	remove := leaseCallback(t, m, false, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if ok && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "DerivativeItem" && updates["state"] == "failed" {
			hits.Add(1)
			tx.AddError(errors.New("database is locked"))
		}
	})
	if m.step() {
		t.Fatal("failed SQL persistence requested an immediate busy loop")
	}
	remove()
	var stranded Item
	if e = m.db.Where("job_id = ?", j.ID).First(&stranded).Error; e != nil {
		t.Fatal(e)
	}
	if hits.Load() != 4 || d.calls.Load() != 1 || stranded.State != "active" || !stranded.LeaseUntil.After(time.Now()) || len(m.activeClaims) != 0 {
		t.Fatalf("did not reproduce returned worker/future orphan: %+v hits=%d calls=%d", stranded, hits.Load(), d.calls.Load())
	}
	if !m.step() {
		t.Fatal("last active item stayed stranded until restart/lease expiry")
	}
	got, _ := leaseRow(t, m, stranded.ID)
	current, e := m.getJob(j.ID)
	if e != nil || got.State != "failed" || got.Attempts != 1 || !got.LeaseUntil.IsZero() || current.State != "partial_failed" || d.calls.Load() != 2 {
		t.Fatalf("orphan did not finish normally: %+v %+v calls=%d %v", got, current, d.calls.Load(), e)
	}
}

func TestLeaseStepBusyTerminalRetryDoesNotRepeatSourceWork(t *testing.T) {
	m := coreManager(t)
	m.cfg.ScanIntervalMillis = 0
	d := &coreFakeDriver{listErr: &sourceHTTPError{Status: 403}}
	s := coreRegistered(t, m, d)
	j, e := m.Submit(context.Background(), 1, SubmitRequest{Path: s.MountPath})
	if e != nil {
		t.Fatal(e)
	}
	var hits atomic.Int32
	remove := leaseCallback(t, m, false, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if ok && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "DerivativeItem" && updates["state"] == "failed" && hits.Add(1) <= 2 {
			tx.AddError(errors.New("database is locked"))
		}
	})
	if !m.step() {
		t.Fatal("temporary local failure did not resolve within the transaction retry")
	}
	remove()
	current, e := m.getJob(j.ID)
	if e != nil || current.State != "partial_failed" || hits.Load() != 3 || d.calls.Load() != 1 {
		t.Fatalf("SQL retry repeated source work: %+v hits=%d calls=%d %v", current, hits.Load(), d.calls.Load(), e)
	}
}

func TestLeaseRecoveryNeverStealsLiveExpiredContext(t *testing.T) {
	m := coreManager(t)
	m.cfg.ScanIntervalMillis = 0
	d := &coreFakeDriver{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s := coreRegistered(t, m, d)
	j, e := m.Submit(context.Background(), 1, SubmitRequest{Path: s.MountPath})
	if e != nil {
		t.Fatal(e)
	}
	other, e := m.Submit(context.Background(), 1, SubmitRequest{Path: s.MountPath + "/other"})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan bool, 1)
	var once sync.Once
	release := func() { once.Do(func() { close(d.release) }) }
	t.Cleanup(func() {
		release()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("live scan did not exit")
		}
	})
	go func() { done <- m.step() }()
	select {
	case <-d.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scan never entered fake driver")
	}
	if e = m.db.Model(&Item{}).Where("job_id = ? AND state = ?", j.ID, "active").Update("lease_until", time.Now().Add(-time.Hour)).Error; e != nil {
		t.Fatal(e)
	}
	if e = m.recoverLeases(); e != nil {
		t.Fatal(e)
	}
	if m.step() || d.calls.Load() != 1 {
		t.Fatal("a second step claimed work while the first context was alive")
	}
	var active, queued Item
	if e = m.db.Where("job_id = ?", j.ID).First(&active).Error; e != nil {
		t.Fatal(e)
	}
	if e = m.db.Where("job_id = ?", other.ID).First(&queued).Error; e != nil {
		t.Fatal(e)
	}
	if active.State != "active" || active.Attempts != 1 || queued.State != "queued" || queued.Attempts != 0 {
		t.Fatalf("live claim reset or duplicate claim made: %+v %+v", active, queued)
	}
	release()
}

func TestLeaseFinalizeRetriesAndCommitsRuleAtomically(t *testing.T) {
	m, j, item := leaseFixture(t)
	if e := m.failItem(j, item, &sourceHTTPError{Status: 403}); e != nil {
		t.Fatal(e)
	}
	rule := Rule{Name: "lease-finalize", LastState: "processing"}
	if e := m.db.Create(&rule).Error; e != nil {
		t.Fatal(e)
	}
	j.RuleID = rule.ID
	if e := m.db.Model(&Job{}).Where("id = ?", j.ID).Update("rule_id", rule.ID).Error; e != nil {
		t.Fatal(e)
	}
	busy := errors.New("database is locked")
	var hits atomic.Int32
	remove := leaseCallback(t, m, false, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "DerivativeRule" {
			hits.Add(1)
			tx.AddError(busy)
		}
	})
	e := m.finalize(j)
	remove()
	if !errors.Is(e, busy) || hits.Load() != 4 {
		t.Fatalf("finalization discarded SQL error: %v calls=%d", e, hits.Load())
	}
	current, e := m.getJob(j.ID)
	if e != nil || current.State != "processing" || current.ActiveKey == nil || current.ScanComplete {
		t.Fatalf("job committed without matching rule state: %+v %v", current, e)
	}
	if m.step() {
		t.Fatal("finalization-only retry unexpectedly claimed source work")
	}
	current, e = m.getJob(j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.db.First(&rule, rule.ID).Error; e != nil {
		t.Fatal(e)
	}
	if current.State != "partial_failed" || current.ActiveKey != nil || !current.ScanComplete || rule.LastState != "partial_failed" {
		t.Fatalf("finalization was not retried locally: %+v %+v", current, rule)
	}
}
