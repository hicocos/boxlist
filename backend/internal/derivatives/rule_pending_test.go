package derivatives

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

// Use explicit scheduler time and barriers, not sleeps or a running worker loop.
// The real registry/SQLite fixtures are intentionally not parallel.
func pendingRuleFixture(t *testing.T) (*Manager, *coreFakeDriver, Rule, time.Time) {
	t.Helper()
	m := coreManager(t)
	m.cfg.ScanIntervalMillis = 0
	d := &coreFakeDriver{}
	s := coreRegistered(t, m, d)
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	r := Rule{Name: "pending", Path: s.MountPath, Recursive: true, Profiles: "thumb,reader", Enabled: true, IntervalMinutes: 360, Creator: 1, StorageID: s.ID, Binding: Binding(&s), NextRun: now}
	if e := m.db.Create(&r).Error; e != nil {
		t.Fatal(e)
	}
	return m, d, r, now
}

func pendingRuleRead(t *testing.T, m *Manager, id uint) Rule {
	t.Helper()
	var r Rule
	if e := m.db.First(&r, id).Error; e != nil {
		t.Fatal(e)
	}
	return r
}

func pendingRuleJobs(t *testing.T, m *Manager, id uint, want int) []Job {
	t.Helper()
	var jobs []Job
	if e := m.db.Where("rule_id = ?", id).Order("id").Find(&jobs).Error; e != nil {
		t.Fatal(e)
	}
	if len(jobs) != want {
		t.Fatalf("rule has %d jobs, want %d: %+v", len(jobs), want, jobs)
	}
	return jobs
}

func pendingRuleFinish(t *testing.T, m *Manager, j Job) {
	t.Helper()
	m.gate.Lock()
	defer m.gate.Unlock()
	// Complete the fixture's files without downloading/encoding anything.
	if e := m.db.Model(&Item{}).Where("job_id = ?", j.ID).Update("state", "succeeded").Error; e != nil {
		t.Fatal(e)
	}
	m.finalize(j)
	got, e := m.getJob(j.ID)
	if e != nil || got.State != "completed" || got.ActiveKey != nil {
		t.Fatalf("job did not release its active key: %+v, %v", got, e)
	}
}

func pendingRuleWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rule synchronization did not finish")
	}
}

func TestRulePendingEventAfterDirectoryScanSurvivesActiveJobAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		delay time.Duration
	}{
		{"debounced", 10 * time.Second},
		{"outside_debounce", 2 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, d, r, start := pendingRuleFixture(t)
			d.objects = []model.Obj{&model.Object{Name: "old.jpg", Size: 10, Modified: start}}
			j, e := m.scanRuleAt(context.Background(), r.ID, true, start)
			if e != nil {
				t.Fatal(e)
			}
			if !m.step() {
				t.Fatal("root directory was not scanned")
			}
			got, e := m.getJob(j.ID)
			if e != nil || !got.ScanComplete || got.ActiveKey == nil {
				t.Fatalf("expected completed directories but unfinished files: %+v, %v", got, e)
			}
			if e = m.db.Model(&Job{}).Where("id = ?", j.ID).Update("state", "processing").Error; e != nil {
				t.Fatal(e)
			}
			if e = m.db.Model(&Item{}).Where("job_id = ? AND kind = ?", j.ID, "file").Update("state", "active").Error; e != nil {
				t.Fatal(e)
			}
			// This file was absent when the old job listed the directory.
			d.objects = append(d.objects, &model.Object{Name: "new.jpg", Size: 20, Modified: start})
			eventTime := start.Add(tc.delay)
			m.eventAt(r.Path, eventTime)
			m.eventAt(r.Path, eventTime)                    // duplicate notification, same deadline
			m.eventAt(r.Path, eventTime.Add(5*time.Second)) // a stream must not postpone it
			after := pendingRuleRead(t, m, r.ID)
			wantDue := start.Add(time.Minute)
			if tc.delay >= time.Minute {
				wantDue = eventTime.Add(time.Minute)
			}
			if !after.LastRun.Equal(start) || !after.NextRun.Equal(wantDue) {
				t.Fatalf("event was consumed by old job: last=%v next=%v, want %v / %v", after.LastRun, after.NextRun, start, wantDue)
			}
			pendingRuleJobs(t, m, r.ID, 1)
			m.scheduleAt(wantDue)
			after = pendingRuleRead(t, m, r.ID)
			if !after.LastRun.Equal(start) || !after.NextRun.Equal(wantDue.Add(time.Minute)) {
				t.Fatalf("dedup reset last run or swallowed follow-up: %+v", after)
			}
			pendingRuleJobs(t, m, r.ID, 1)
			m.Close()
			restarted, e := New(m.db, m.root)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(restarted.Close)
			restarted.cfg.ScanIntervalMillis = 0
			m = restarted
			after = pendingRuleRead(t, m, r.ID)
			if !after.NextRun.Equal(wantDue.Add(time.Minute)) {
				t.Fatal("restart lost persisted follow-up", after)
			}
			// A second dedup after restart must still not consume the event.
			m.scheduleAt(after.NextRun)
			after = pendingRuleRead(t, m, r.ID)
			if !after.LastRun.Equal(start) || !after.NextRun.Equal(wantDue.Add(2*time.Minute)) {
				t.Fatal("recovered active job swallowed follow-up", after)
			}
			pendingRuleJobs(t, m, r.ID, 1)
			pendingRuleFinish(t, m, j)
			followUp := after.NextRun
			m.scheduleAt(followUp)
			jobs := pendingRuleJobs(t, m, r.ID, 2)
			after = pendingRuleRead(t, m, r.ID)
			if !after.LastRun.Equal(followUp) || !after.NextRun.Equal(followUp.Add(6*time.Hour)) {
				t.Fatal("new job did not consume exactly one pending scan", after)
			}
			if !m.step() {
				t.Fatal("follow-up directory was not scanned")
			}
			stats, e := m.stats(jobs[1].ID)
			if e != nil || stats.Discovered != 2 || d.calls.Load() != 2 {
				t.Fatalf("follow-up missed newly added image: %+v, lists=%d, %v", stats, d.calls.Load(), e)
			}
			pendingRuleFinish(t, m, jobs[1])
			m.scheduleAt(followUp.Add(time.Minute))
			pendingRuleJobs(t, m, r.ID, 2)
		})
	}
}

func TestRulePendingEventDuringEnqueueIsNotOverwritten(t *testing.T) {
	m, _, r, start := pendingRuleFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	const callback = "test:pending_rule_enqueue"
	if e := m.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "DerivativeRule" || !ok || updates["last_run"] == nil {
			return
		}
		close(entered)
		<-release
	}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.db.Callback().Update().Remove(callback) })
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	scanDone := make(chan struct{})
	var scanErr error
	go func() {
		_, scanErr = m.scanRuleAt(context.Background(), r.ID, true, start)
		close(scanDone)
	}()
	pendingRuleWait(t, entered)
	// At the point where the original code overwrote NextRun, event/rule edits
	// must be excluded. This assertion avoids relying on goroutine scheduling.
	if m.gate.TryLock() {
		m.gate.Unlock()
		t.Fatal("rule deadline is updated without excluding concurrent events")
	}
	eventStarted, eventDone := make(chan struct{}), make(chan struct{})
	go func() {
		close(eventStarted)
		m.eventAt(r.Path, start.Add(10*time.Second))
		close(eventDone)
	}()
	pendingRuleWait(t, eventStarted)
	close(release)
	released = true
	pendingRuleWait(t, scanDone)
	pendingRuleWait(t, eventDone)
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	after := pendingRuleRead(t, m, r.ID)
	if !after.LastRun.Equal(start) || !after.NextRun.Equal(start.Add(time.Minute)) {
		t.Fatal("enqueue completion overwrote the concurrent event", after)
	}
	pendingRuleJobs(t, m, r.ID, 1)
}

func TestRulePendingEventDuringDirectoryIO(t *testing.T) {
	m, d, r, start := pendingRuleFixture(t)
	d.entered, d.release = make(chan struct{}, 1), make(chan struct{})
	defer close(d.release)
	j, e := m.scanRuleAt(context.Background(), r.ID, true, start)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { m.step(); close(done) }()
	pendingRuleWait(t, d.entered)
	m.eventAt(r.Path, start.Add(10*time.Second))
	after := pendingRuleRead(t, m, r.ID)
	if !after.NextRun.Equal(start.Add(time.Minute)) {
		t.Fatal("event during directory IO was not persisted", after)
	}
	// Cancellation stops the blocked fake list, without a sleep or encoder.
	if e = m.Control(j.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	pendingRuleWait(t, done)
	m.scheduleAt(after.NextRun)
	pendingRuleJobs(t, m, r.ID, 2)
}

func TestRulePendingDisableStopsAutomaticButAllowsExplicitScan(t *testing.T) {
	m, _, r, start := pendingRuleFixture(t)
	j, e := m.scanRuleAt(context.Background(), r.ID, true, start)
	if e != nil {
		t.Fatal(e)
	}
	m.eventAt(r.Path, start.Add(10*time.Second))
	ruleID := strconv.FormatUint(uint64(r.ID), 10)
	r, e = m.SaveRule(context.Background(), 1, RuleRequest{ID: ruleID, Name: r.Name, Path: r.Path, Recursive: r.Recursive, Enabled: false, Profiles: []string{"thumb", "reader"}, IntervalMinutes: r.IntervalMinutes})
	if e != nil || r.Enabled || !r.NextRun.IsZero() {
		t.Fatalf("disable did not clear automatic schedule: %+v, %v", r, e)
	}
	pendingRuleFinish(t, m, j)
	m.eventAt(r.Path, start.Add(time.Minute))
	m.scheduleAt(start.Add(24 * time.Hour))
	if _, e = m.scanRuleAt(context.Background(), r.ID, true, start.Add(time.Minute)); !errors.Is(e, ErrDisabled) {
		t.Fatal("stale scheduler candidate ignored disabled rule", e)
	}
	pendingRuleJobs(t, m, r.ID, 1)
	manual, e := m.ScanRule(context.Background(), 1, ruleID)
	if e != nil || manual.ID == j.ID {
		t.Fatalf("disabled rule cannot run explicitly: %+v, %v", manual, e)
	}
	before := pendingRuleRead(t, m, r.ID)
	again, e := m.ScanRule(context.Background(), 1, ruleID)
	after := pendingRuleRead(t, m, r.ID)
	if e != nil || again.ID != manual.ID || after.Enabled || !after.NextRun.IsZero() || !after.LastRun.Equal(before.LastRun) {
		t.Fatalf("explicit duplicate rearmed disabled rule or consumed a scan: %+v, %+v, %v", again, after, e)
	}
	pendingRuleFinish(t, m, manual)
	m.scheduleAt(after.LastRun.Add(24 * time.Hour))
	pendingRuleJobs(t, m, r.ID, 2)
}

func TestRulePendingSharesSubmitIdentityAcrossActiveStates(t *testing.T) {
	for _, state := range activeStates {
		t.Run(state, func(t *testing.T) {
			m, _, r, now := pendingRuleFixture(t)
			last := now.Add(-2 * time.Hour)
			if e := m.db.Model(&Rule{}).Where("id = ?", r.ID).Updates(map[string]any{"last_run": last, "excludes": "secret/**"}).Error; e != nil {
				t.Fatal(e)
			}
			r = pendingRuleRead(t, m, r.ID)
			j, e := m.submit(context.Background(), JobRequest{Path: r.Path, Recursive: r.Recursive, Profiles: r.Profiles, Creator: r.Creator, RuleID: r.ID}, r.Excludes)
			if e != nil {
				t.Fatal(e)
			}
			m.cfg.MaxJobs = 1 // an existing active key still dedups at the limit
			if e = m.db.Model(&Job{}).Where("id = ?", j.ID).Update("state", state).Error; e != nil {
				t.Fatal(e)
			}
			again, e := m.scanRuleAt(context.Background(), r.ID, true, now)
			after := pendingRuleRead(t, m, r.ID)
			if e != nil || again.ID != j.ID || !after.LastRun.Equal(last) || !after.NextRun.Equal(now.Add(time.Minute)) {
				t.Fatalf("active %s consumed pending scan: %+v, %+v, %v", state, again, after, e)
			}
			// Explicit scans dedup too, but must not postpone the pending deadline.
			again, e = m.scanRuleAt(context.Background(), r.ID, false, now.Add(10*time.Second))
			after = pendingRuleRead(t, m, r.ID)
			if e != nil || again.ID != j.ID || !after.LastRun.Equal(last) || !after.NextRun.Equal(now.Add(time.Minute)) {
				t.Fatalf("explicit duplicate postponed follow-up: %+v, %+v, %v", again, after, e)
			}
			pendingRuleJobs(t, m, r.ID, 1)
		})
	}
}

func TestRulePendingEnqueueAndDeadlineAreAtomic(t *testing.T) {
	m, _, r, start := pendingRuleFixture(t)
	failure := errors.New("injected rule deadline failure")
	const callback = "test:pending_rule_rollback"
	if e := m.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "DerivativeRule" && ok && updates["last_run"] != nil {
			tx.AddError(failure)
		}
	}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.db.Callback().Update().Remove(callback) })
	if _, e := m.scanRuleAt(context.Background(), r.ID, true, start); !errors.Is(e, failure) {
		t.Fatal("expected injected rule update error", e)
	}
	pendingRuleJobs(t, m, r.ID, 0)
	var items int64
	if e := m.db.Model(&Item{}).Count(&items).Error; e != nil || items != 0 {
		t.Fatalf("rolled-back job left directory checkpoint: %d, %v", items, e)
	}
	after := pendingRuleRead(t, m, r.ID)
	if !after.LastRun.IsZero() || !after.NextRun.Equal(start.Add(15*time.Minute)) {
		t.Fatal("failed enqueue consumed last run or lost retry", after)
	}
	if e := m.db.Callback().Update().Remove(callback); e != nil {
		t.Fatal(e)
	}
	m.scheduleAt(after.NextRun)
	pendingRuleJobs(t, m, r.ID, 1)
}
