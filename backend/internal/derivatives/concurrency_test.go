package derivatives

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	appdb "github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"gorm.io/gorm"
)

// The real manager/registry/SQL path reaches a cancellable remote-Get barrier.
// No encoder, HTTP server, global driver replacement or sleep is needed.
type concurrencyEntry struct {
	ctx     context.Context
	release chan struct{}
}
type concurrencyDriver struct {
	coreFakeDriver
	files     chan concurrencyEntry
	cancelled chan struct{}
	unwind    chan struct{}
}

func (d *concurrencyDriver) Get(ctx context.Context, p string) (model.Obj, error) {
	if !strings.HasSuffix(p, ".jpg") {
		return d.coreFakeDriver.Get(ctx, p)
	}
	entry := concurrencyEntry{ctx: ctx, release: make(chan struct{})}
	select {
	case d.files <- entry:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-entry.release:
		return nil, ErrInvalidPath // Terminal, without executing the encoder.
	case <-ctx.Done():
		d.cancelled <- struct{}{}
		if d.unwind != nil {
			<-d.unwind // A canceled remote operation may still be unwinding.
		}
		return nil, ctx.Err()
	}
}

func concurrencyFixture(t *testing.T, count int) (*Manager, Job, *concurrencyDriver) {
	t.Helper()
	m := coreManager(t)
	d := &concurrencyDriver{files: make(chan concurrencyEntry, 8), cancelled: make(chan struct{}, 8)}
	oldConf := conf.Conf
	conf.Conf = &conf.Config{}
	t.Cleanup(func() { conf.Conf = oldConf })
	appdb.Init(m.db)
	op.RegisterDriver(func() driver.Driver { return d })
	s := model.Storage{ID: uint(time.Now().UnixNano() & 0x7fffffff), Driver: "CoreTest", MountPath: "/concurrency-" + digest(t.Name())[:12], Addition: "{}"}
	if e := m.db.Create(&s).Error; e != nil {
		t.Fatal(e)
	}
	if e := op.LoadStorage(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if e := m.db.First(&s, s.ID).Error; e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { op.DeleteStorageById(context.Background(), s.ID) })
	t.Cleanup(m.Close) // Stop contexts before unregistering the driver.
	j := coreJob(t, m, s)
	for n := 0; n < count; n++ {
		item := coreItem(t, m, j, filepath.Join(j.Path, fmt.Sprintf("%d.jpg", n)))
		if e := m.db.Model(&Item{}).Where("id = ?", item.ID).Updates(map[string]any{"state": "queued", "attempts": 0}).Error; e != nil {
			t.Fatal(e)
		}
	}
	m.encoderMu.Lock()
	m.encoderError = ""
	m.encoderMu.Unlock()
	return m, j, d
}
func concurrencyEntered(t *testing.T, d *concurrencyDriver) concurrencyEntry {
	t.Helper()
	select {
	case entry := <-d.files:
		return entry
	case <-time.After(3 * time.Second):
		t.Fatal("work never reached the remote-Get barrier")
		return concurrencyEntry{}
	}
}
func concurrencyWaitClaims(t *testing.T, m *Manager, count int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		m.gate.Lock()
		got := len(m.activeClaims)
		m.gate.Unlock()
		if got == count {
			return
		}
		select {
		case <-m.wake:
		case <-deadline.C:
			t.Fatalf("live claims = %d, want %d", got, count)
		}
	}
}
func concurrencyLimit(t *testing.T, m *Manager, n int) {
	t.Helper()
	if _, e := m.SaveWorkerConfiguration(context.Background(), n); e != nil {
		t.Fatal(e)
	}
}

func TestConcurrencyDispatchDynamicLimitAndDrain(t *testing.T) {
	m, j, d := concurrencyFixture(t, 6)
	if m.workerLimit != 1 || cap(m.wake) != 4 {
		t.Fatal("incorrect scheduler defaults")
	}
	if !m.dispatch() {
		t.Fatal("default worker did not claim")
	}
	entries := []concurrencyEntry{concurrencyEntered(t, d)}
	if m.dispatch() {
		t.Fatal("default concurrency exceeded one")
	}
	concurrencyLimit(t, m, 4)
	for n := 1; n < 4; n++ {
		if !m.dispatch() {
			t.Fatal("raised concurrency did not admit work")
		}
		entries = append(entries, concurrencyEntered(t, d))
	}
	concurrencyWaitClaims(t, m, 4)
	if m.dispatch() {
		t.Fatal("concurrency exceeded four")
	}
	concurrencyLimit(t, m, 1)
	for _, entry := range entries {
		if entry.ctx.Err() != nil {
			t.Fatal("lower limit interrupted active work")
		}
	}
	if m.dispatch() {
		t.Fatal("lower limit did not drain")
	}
	for _, entry := range entries[:3] {
		close(entry.release)
	}
	concurrencyWaitClaims(t, m, 1)
	current, e := m.getJob(j.ID)
	if e != nil || current.State == "completed" || current.State == "partial_failed" {
		t.Fatal("job finalized with outstanding work", current, e)
	}
	if m.dispatch() {
		t.Fatal("claim started while draining to one")
	}
	close(entries[3].release)
	concurrencyWaitClaims(t, m, 0)
	if !m.dispatch() {
		t.Fatal("drained pool did not resume")
	}
	last := concurrencyEntered(t, d)
	if m.dispatch() {
		t.Fatal("resumed pool ignored lower limit")
	}
	close(last.release)
	concurrencyWaitClaims(t, m, 0)
}

func TestConcurrencyCancelAllRejectsRetryDeleteUntilUnwound(t *testing.T) {
	m, j, d := concurrencyFixture(t, 4)
	unwind := make(chan struct{})
	d.unwind = unwind
	var once sync.Once
	release := func() { once.Do(func() { close(unwind) }) }
	defer release()
	concurrencyLimit(t, m, 4)
	entries := make([]concurrencyEntry, 0, 4)
	for n := 0; n < 4; n++ {
		if !m.dispatch() {
			t.Fatal("claim failed")
		}
		entries = append(entries, concurrencyEntered(t, d))
	}
	if e := m.Control(j.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.ctx.Err() != nil {
			t.Fatal("pause canceled active work")
		}
	}
	if m.dispatch() {
		t.Fatal("paused job admitted work")
	}
	if e := m.Control(j.ID, "retry_failed"); !errors.Is(e, ErrConflict) {
		t.Fatal("paused live claim allowed retry", e)
	}
	if e := m.Control(j.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		select {
		case <-entry.ctx.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("cancel missed a context")
		}
	}
	concurrencyWaitClaims(t, m, 4)
	for _, action := range []string{"retry", "retry_failed", "delete"} {
		if e := m.Control(j.ID, action); !errors.Is(e, ErrConflict) {
			t.Fatalf("%s allowed canceled unwinding claims: %v", action, e)
		}
	}
	// Release each remote operation explicitly, then prove control is unblocked.
	release()
	concurrencyWaitClaims(t, m, 0)
	if e := m.Control(j.ID, "retry_failed"); e != nil {
		t.Fatal(e)
	}
	if e := m.Control(j.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	if e := m.Control(j.ID, "delete"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.getJob(j.ID); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("job deletion did not persist", e)
	}
}

func TestConcurrencyCloseCancelsAndWaitsForAll(t *testing.T) {
	m, _, d := concurrencyFixture(t, 2)
	unwind := make(chan struct{})
	d.unwind = unwind
	var once sync.Once
	release := func() { once.Do(func() { close(unwind) }) }
	defer release()
	concurrencyLimit(t, m, 2)
	for n := 0; n < 2; n++ {
		if !m.dispatch() {
			t.Fatal("claim failed")
		}
		concurrencyEntered(t, d)
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	for n := 0; n < 2; n++ {
		select {
		case <-d.cancelled:
		case <-time.After(3 * time.Second):
			t.Fatal("Close missed an active context")
		}
	}
	select {
	case <-closed:
		t.Fatal("Close returned before workers unwound")
	default:
	}
	if m.dispatch() || m.step() {
		t.Fatal("shutdown admitted new work")
	}
	release()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not join all workers")
	}
	concurrencyWaitClaims(t, m, 0)
	m.Start() // Start after Close must not perform wg.Add or resurrect scheduler.
	var items []Item
	if e := m.db.Find(&items).Error; e != nil {
		t.Fatal(e)
	}
	for _, item := range items {
		if item.State != "queued" || item.Attempts != 0 || !item.LeaseUntil.IsZero() {
			t.Fatalf("shutdown lost durable retry: %+v", item)
		}
	}
}

func TestConcurrencyDiskReservesAllLiveFiles(t *testing.T) {
	m, j, d := concurrencyFixture(t, 2)
	m.cfg.BudgetBytes = workReserve*2 - 1
	concurrencyLimit(t, m, 4)
	if !m.dispatch() {
		t.Fatal("first reservation failed")
	}
	first := concurrencyEntered(t, d)
	if !m.dispatch() {
		t.Fatal("disk protection transition did not persist")
	}
	concurrencyWaitClaims(t, m, 1)
	current, e := m.getJob(j.ID)
	if e != nil || current.State != "paused" || current.PauseReason != "derivative_disk_protection" {
		t.Fatal(current, e)
	}
	var untouched Item
	if e = m.db.Where("job_id = ? AND state = ?", j.ID, "queued").First(&untouched).Error; e != nil {
		t.Fatal(e)
	}
	if untouched.Attempts != 0 || first.ctx.Err() != nil {
		t.Fatal("reservation consumed attempts or canceled active work")
	}
	close(first.release)
	concurrencyWaitClaims(t, m, 0)
	if e = m.Control(j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	if !m.dispatch() {
		t.Fatal("completed file did not release its reservation")
	}
	second := concurrencyEntered(t, d)
	close(second.release)
	concurrencyWaitClaims(t, m, 0)
}

func TestConcurrencyUnlimitedBudgetAllowsFilesBeyondFormerLimit(t *testing.T) {
	t.Setenv("DERIVATIVES_BUDGET_BYTES", "0")
	m, j, d := concurrencyFixture(t, 2)
	budgetSparseFile(t, m, "artifacts", 21<<30)
	concurrencyLimit(t, m, 4)
	if !m.dispatch() {
		t.Fatal("unlimited storage rejected first claim beyond 20 GiB")
	}
	first := concurrencyEntered(t, d)
	if !m.dispatch() {
		t.Fatal("unlimited storage rejected second claim beyond 20 GiB")
	}
	second := concurrencyEntered(t, d)
	concurrencyWaitClaims(t, m, 2)
	current, err := m.getJob(j.ID)
	if err != nil || current.State == "paused" || current.PauseReason != "" {
		t.Fatal(current, err)
	}
	close(first.release)
	close(second.release)
	concurrencyWaitClaims(t, m, 0)
}

func TestConcurrencyUnlimitedBudgetStillPausesForDiskSafety(t *testing.T) {
	t.Setenv("DERIVATIVES_BUDGET_BYTES", "0")
	m, j, _ := concurrencyFixture(t, 1)
	m.cfg.MinFreeBytes = 1 << 60
	if !m.dispatch() {
		t.Fatal("disk protection transition did not persist")
	}
	concurrencyWaitClaims(t, m, 0)
	current, err := m.getJob(j.ID)
	if err != nil || current.State != "paused" || current.PauseReason != "derivative_disk_protection" {
		t.Fatal(current, err)
	}
	var item Item
	if err = m.db.Where("job_id = ?", j.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.State != "queued" || item.Attempts != 0 {
		t.Fatal("disk protection consumed an attempt", item)
	}
}

func TestConcurrencyS3WorkIgnoresLocalFreeSpaceFloor(t *testing.T) {
	t.Setenv("DERIVATIVES_BUDGET_BYTES", "0")
	m, j, d := concurrencyFixture(t, 1)
	_, server := newS3TestStore(t)
	if _, err := m.SaveS3Configuration(context.Background(), s3TestRequest(server)); err != nil {
		t.Fatal(err)
	}
	m.cfg.MinFreeBytes = 1 << 60
	if !m.dispatch() {
		t.Fatal("S3 work rejected below local free-space floor")
	}
	entry := concurrencyEntered(t, d)
	concurrencyWaitClaims(t, m, 1)
	current, err := m.getJob(j.ID)
	if err != nil || current.State == "paused" || current.PauseReason != "" {
		t.Fatal(current, err)
	}
	close(entry.release)
	concurrencyWaitClaims(t, m, 0)
}

func TestConcurrencyRecoverySkipsOnlyLiveItems(t *testing.T) {
	m, j, orphan := leaseFixture(t)
	live := coreItem(t, m, j, j.Path+"/live.jpg")
	if e := m.db.Model(&Item{}).Where("id = ?", live.ID).Update("lease_until", time.Now().Add(-time.Hour)).Error; e != nil {
		t.Fatal(e)
	}
	other := coreItem(t, m, j, j.Path+"/other-orphan.jpg")
	if e := m.db.Model(&Item{}).Where("id = ?", other.ID).Update("lease_until", time.Now().Add(time.Hour)).Error; e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.gate.Lock()
	m.activeClaims[live.ID] = workerClaim{JobID: j.ID, Cancel: cancel, Kind: "file"}
	m.gate.Unlock()
	defer func() { cancel(); m.gate.Lock(); delete(m.activeClaims, live.ID); m.gate.Unlock() }()
	for n := 0; n < 2; n++ {
		if n == 1 {
			cancel()
		} // Canceled-but-not-returned remains live ownership.
		if e := m.recoverLeases(); e != nil {
			t.Fatal(e)
		}
		got, profiles := leaseRow(t, m, orphan.ID)
		if got.State != "queued" || got.Attempts != 2 || profiles["reader"].Attempts != 1 {
			t.Fatalf("unrelated orphan stranded or refunded twice: %+v %+v", got, profiles)
		}
		leaseAssertReady(t, profiles)
		got, _ = leaseRow(t, m, live.ID)
		if got.State != "active" || got.Attempts != 1 {
			t.Fatal("live expired context stolen", got)
		}
		got, _ = leaseRow(t, m, other.ID)
		if got.State != "queued" || got.Attempts != 0 {
			t.Fatal("future orphan was not recovered", got)
		}
	}
	if ctx.Err() == nil {
		t.Fatal("canceled ownership case not exercised")
	}
}

func TestConcurrencyOneScanGlobally(t *testing.T) {
	m := coreManager(t)
	m.cfg.ScanIntervalMillis = 0
	d := &coreFakeDriver{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s := coreRegistered(t, m, d)
	concurrencyLimit(t, m, 4)
	for _, p := range []string{s.MountPath, s.MountPath + "/other"} {
		if _, e := m.Submit(context.Background(), 1, SubmitRequest{Path: p}); e != nil {
			t.Fatal(e)
		}
	}
	if !m.dispatch() {
		t.Fatal("scan not dispatched")
	}
	select {
	case <-d.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scan never entered")
	}
	if m.dispatch() || d.calls.Load() != 1 {
		t.Fatal("concurrency setting burst parallel scans")
	}
	close(d.release)
	concurrencyWaitClaims(t, m, 0)
	if !m.dispatch() {
		t.Fatal("finished scan did not release global scan slot")
	}
	concurrencyWaitClaims(t, m, 0)
}
