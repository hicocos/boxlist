package derivatives

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestCoreAsynchronousSubmitAndWholeDirectory(t *testing.T) {
	m := coreManager(t)
	d := &coreFakeDriver{}
	for i := 0; i < 237; i++ {
		d.objects = append(d.objects, &model.Object{Name: fmt.Sprintf("%03d.jpg", i), Size: 10, Modified: time.Unix(10, 0)})
	}
	s := coreRegistered(t, m, d)
	req := SubmitRequest{Path: s.MountPath, Profiles: []string{"thumb", "reader"}, IdempotencyKey: "same-request"}
	j, e := m.Submit(context.Background(), 1, req)
	if e != nil {
		t.Fatal(e)
	}
	again, e := m.Submit(context.Background(), 1, req)
	if e != nil || again.ID != j.ID {
		t.Fatal("idempotency", again, e)
	}
	req.Force = true
	if _, e = m.Submit(context.Background(), 1, req); !errors.Is(e, ErrConflict) {
		t.Fatal("idempotency conflict", e)
	}
	if d.calls.Load() != 0 {
		t.Fatal("POST synchronously scanned source")
	}
	if !m.step() {
		t.Fatal("did not scan durable directory")
	}
	got, e := m.Job(strconv.Itoa(int(j.ID)))
	if e != nil || got.Stats.Discovered != 237 || got.Stats.Eligible != 237 || got.Stats.ScannedDirs != 1 || !got.ScanComplete {
		t.Fatalf("incomplete listing: %+v %v", got, e)
	}
	// Replay the directory checkpoint as after a crash. Unique path keys ensure
	// already committed child batches are not counted or subscribed twice.
	var dir Item
	m.db.Where("job_id = ? AND kind = ?", j.ID, "directory").First(&dir)
	m.db.Model(&Item{}).Where("id = ?", dir.ID).Update("state", "active")
	if e = m.scan(context.Background(), j, dir); e != nil {
		t.Fatal(e)
	}
	_, total, e := m.Items(strconv.Itoa(int(j.ID)), 1, 200, "")
	if e != nil || total != 238 {
		t.Fatalf("duplicate checkpoint rows: %d %v", total, e)
	}
}
func TestCoreFailedDirectoryDoesNotDeleteOfflineSource(t *testing.T) {
	m := coreManager(t)
	d := &coreFakeDriver{listErr: errors.New("403 forbidden https://secret.invalid/?token=private")}
	s := coreRegistered(t, m, d)
	a := coreArtifact(t, m, s, s.MountPath+"/keep.jpg", "r", "thumb")
	j, e := m.Submit(context.Background(), 1, SubmitRequest{Path: s.MountPath})
	if e != nil {
		t.Fatal(e)
	}
	if !m.step() {
		t.Fatal("did not scan")
	}
	got, e := m.Job(strconv.Itoa(int(j.ID)))
	if e != nil || got.State != "partial_failed" || got.Stats.FailedDirs != 1 {
		t.Fatal(got, e)
	}
	if _, e = m.Artifact(a.ID, "thumb"); e != nil {
		t.Fatal("offline listing revoked source", e)
	}
	if _, e = m.Cleanup("obsolete"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.FilePath(a); e != nil {
		t.Fatal("offline artifact deleted", e)
	}
}
func TestCoreCompleteScanMarksMissingSourcesDeleted(t *testing.T) {
	m := coreManager(t)
	d := &coreFakeDriver{}
	s := coreRegistered(t, m, d)
	missing := []Artifact{
		coreArtifact(t, m, s, s.MountPath+"/removed.jpg", "r", "thumb"),
		coreArtifact(t, m, s, s.MountPath+"/removed-directory/child.jpg", "r", "thumb"),
	}
	if _, err := m.Submit(context.Background(), 1, SubmitRequest{Path: s.MountPath}); err != nil {
		t.Fatal(err)
	}
	if !m.step() {
		t.Fatal("did not scan directory")
	}
	for _, artifact := range missing {
		var source DerivativeSource
		if err := m.db.First(&source, "source_key = ?", artifact.SourceKey).Error; err != nil || !source.Deleted {
			t.Fatal("complete listing failed to confirm absent source", source, err)
		}
		if err := m.db.First(&artifact, "id = ?", artifact.ID).Error; err != nil || artifact.State != "deleted" {
			t.Fatal("absent source preview not marked deleted", artifact.State, err)
		}
		if _, err := m.FilePath(artifact); err != nil {
			t.Fatal("scan deleted preview instead of marking it", err)
		}
	}
}
func TestCoreCancelStopsActiveScan(t *testing.T) {
	m := coreManager(t)
	d := &coreFakeDriver{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s := coreRegistered(t, m, d)
	j, e := m.Submit(context.Background(), 1, SubmitRequest{Path: s.MountPath})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { m.step(); close(done) }()
	select {
	case <-d.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scan never started")
	}
	if e = m.Control(j.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not propagate")
	}
	got, e := m.Job(strconv.Itoa(int(j.ID)))
	if e != nil || got.State != "cancelled" {
		t.Fatal(got, e)
	}
}
func TestCoreRuleEventDebouncesWithoutDropping(t *testing.T) {
	m := coreManager(t)
	last := time.Now().Add(-10 * time.Second)
	r := Rule{Name: "event", Path: "/pictures", Recursive: true, Enabled: true, LastRun: last, NextRun: time.Now().Add(6 * time.Hour)}
	if e := m.db.Create(&r).Error; e != nil {
		t.Fatal(e)
	}
	m.event("/pictures/sub")
	var after Rule
	if e := m.db.First(&after, r.ID).Error; e != nil {
		t.Fatal(e)
	}
	if !after.NextRun.Equal(last.Add(time.Minute)) {
		t.Fatal("debounced event was lost", after.NextRun)
	}
	m.event("/pictures/sub")
	m.db.First(&after, r.ID)
	if !after.NextRun.Equal(last.Add(time.Minute)) {
		t.Fatal("duplicate event postponed deadline")
	}
}

func TestCoreRulesDisabledByDefaultAndBound(t *testing.T) {
	m := coreManager(t)
	d := &coreFakeDriver{}
	s := coreRegistered(t, m, d)
	req := RuleRequest{Name: "test", Path: s.MountPath, Profiles: []string{"thumb"}, IntervalHours: 6}
	rule, e := m.SaveRule(context.Background(), 1, req)
	if e != nil || rule.Enabled || rule.IntervalMinutes != 360 {
		t.Fatal(rule, e)
	}
	req.ID = strconv.Itoa(int(rule.ID))
	req.IntervalMinutes = 14
	if _, e = m.SaveRule(context.Background(), 1, req); e == nil {
		t.Fatal("short interval accepted")
	}
	j, e := m.ScanRule(context.Background(), 1, strconv.Itoa(int(rule.ID)))
	if e != nil || j.RuleID != rule.ID {
		t.Fatal(j, e)
	}
	m.db.Model(&model.Storage{}).Where("id = ?", s.ID).Update("addition", `{"new":"root"}`)
	if _, e = m.ScanRule(context.Background(), 1, strconv.Itoa(int(rule.ID))); e == nil {
		t.Fatal("rule silently rebound")
	}
	rules, e := m.Rules()
	if e != nil || rules[0].PauseReason == "" || rules[0].Enabled {
		t.Fatal(rules, e)
	}
}
