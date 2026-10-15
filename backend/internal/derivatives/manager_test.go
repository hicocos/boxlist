package derivatives

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func recoveryManager(t *testing.T) *Manager {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "recovery.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "test_"},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	m, err := New(database, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func recoveryCreate(t *testing.T, m *Manager, value any) {
	t.Helper()
	if err := m.db.Create(value).Error; err != nil {
		t.Fatal(err)
	}
}

func recoveryState(t *testing.T, m *Manager, id string, want string) {
	t.Helper()
	var a Artifact
	if err := m.db.First(&a, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if a.State != want {
		t.Fatalf("artifact %s: state %q, want %q", id, a.State, want)
	}
}

func TestRecoveryDoesNotScanArtifacts(t *testing.T) {
	m := recoveryManager(t)
	recoveryCreate(t, m, &Artifact{ID: fmt.Sprintf("%064x", 1), State: "ready"})
	recoveryCreate(t, m, &Item{ID: 1, State: "active"})
	recoveryCreate(t, m, &DerivativeItemProfile{ItemID: 1, Profile: "thumb", State: "active"})
	recoveryCreate(t, m, &Job{ID: 1, State: "processing"})
	key := "cancelled-job"
	recoveryCreate(t, m, &Job{ID: 2, State: "cancelling", ActiveKey: &key})

	entered := make(chan struct{})
	var once sync.Once
	if err := m.db.Callback().Query().Before("gorm:query").Register("block_recovery", func(tx *gorm.DB) {
		if tx.Statement.Table == "test_derivative_artifacts" {
			once.Do(func() { close(entered) })
			tx.AddError(errors.New("unexpected startup artifact scan"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.db.Callback().Query().Remove("block_recovery") })
	opened := make(chan *Manager, 1)
	openErr := make(chan error, 1)
	go func() {
		next, err := New(m.db, m.root)
		if err != nil {
			openErr <- err
			return
		}
		opened <- next
	}()
	var next *Manager
	select {
	case next = <-opened:
	case err := <-openErr:
		t.Fatal(err)
	case <-entered:
		t.Fatal("New scanned artifacts before returning")
	case <-time.After(5 * time.Second):
		t.Fatal("New did not finish task recovery")
	}
	t.Cleanup(next.Close)
	var item Item
	var profile DerivativeItemProfile
	var jobs []Job
	if err := m.db.First(&item, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.db.First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.db.Order("id").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if item.State != "queued" || profile.State != "queued" || len(jobs) != 2 || jobs[0].State != "queued" || jobs[1].State != "cancelled" || jobs[1].ActiveKey != nil {
		t.Fatal("interrupted task state was not recovered synchronously")
	}
	next.Start()
	next.Start()
	next.Close()
	select {
	case <-entered:
		t.Fatal("startup scanned existing artifacts")
	default:
	}
	if err := m.db.Callback().Query().Remove("block_recovery"); err != nil {
		t.Fatal(err)
	}
	recoveryState(t, m, fmt.Sprintf("%064x", 1), "ready")
}

func TestVerificationBatchesTargetsAndPreservesValidOutputs(t *testing.T) {
	m := recoveryManager(t)
	// Both retained and current targets must work, irrespective of Enabled.
	targets := []DerivativeS3Target{{ID: 1, IdentityHash: "old", Prefix: "old/"}, {ID: 2, IdentityHash: "new", Prefix: "new/"}}
	recoveryCreate(t, m, &targets)
	recoveryCreate(t, m, &DerivativeS3Setting{ID: 1, TargetID: 2, Enabled: false})
	rows := make([]Artifact, 401)
	for i := range rows {
		target := &targets[i%2]
		id := fmt.Sprintf("%064x", i+1)
		objectID := fmt.Sprintf("%064x", i+1000)
		key, err := s3ArtifactKey(target, objectID)
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = Artifact{ID: id, State: "ready", Profile: "thumb", OutputTargetID: target.ID, ObjectID: objectID, ObjectKey: key, Bytes: 32, SHA256: fmt.Sprintf("%064x", 42)}
	}
	if err := m.db.CreateInBatches(&rows, 50).Error; err != nil {
		t.Fatal(err)
	}
	queries := 0
	if err := m.db.Callback().Query().Before("gorm:query").Register("count_targets", func(tx *gorm.DB) {
		if tx.Statement.Table == "test_derivative_s3_targets" {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	result, err := m.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Checked != 401 || result.Missing != 0 {
		t.Fatalf("unexpected verification result: %+v", result)
	}
	if err := m.db.Callback().Query().Remove("count_targets"); err != nil {
		t.Fatal(err)
	}
	if queries > 3 {
		t.Fatalf("target queries grow per artifact: %d for 401 outputs", queries)
	}
	var ready int64
	if err := m.db.Model(&Artifact{}).Where("state = ?", "ready").Count(&ready).Error; err != nil {
		t.Fatal(err)
	}
	if ready != 401 {
		t.Fatalf("valid outputs lost: %d remain ready", ready)
	}

	missingTarget := rows[0]
	missingTarget.ID = fmt.Sprintf("%064x", 500)
	missingTarget.OutputTargetID = 99
	badKey := rows[1]
	badKey.ID = fmt.Sprintf("%064x", 501)
	badKey.ObjectKey = "unowned/key"
	recoveryCreate(t, m, &missingTarget)
	recoveryCreate(t, m, &badKey)
	data := []byte("local image bytes")
	for i, content := range [][]byte{data, []byte("equal length junk")} {
		id := fmt.Sprintf("%064x", 600+i)
		rel := "artifacts/" + id[:2] + "/" + id + ".webp"
		if err := os.MkdirAll(filepath.Join(m.root, "artifacts", id[:2]), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.root, rel), content, 0600); err != nil {
			t.Fatal(err)
		}
		recoveryCreate(t, m, &Artifact{ID: id, State: "ready", File: rel, Bytes: int64(len(content)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
	}
	result, err = m.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Checked != 405 || result.Missing != 3 {
		t.Fatalf("unexpected verification result: %+v", result)
	}
	recoveryState(t, m, missingTarget.ID, "missing")
	recoveryState(t, m, badKey.ID, "missing")
	recoveryState(t, m, fmt.Sprintf("%064x", 600), "ready")
	recoveryState(t, m, fmt.Sprintf("%064x", 601), "missing")
}

func TestVerificationTargetDatabaseFailurePreservesReady(t *testing.T) {
	m := recoveryManager(t)
	a := Artifact{ID: fmt.Sprintf("%064x", 1), State: "ready", OutputTargetID: 1}
	recoveryCreate(t, m, &a)
	failure := errors.New("target query failed")
	if err := m.db.Callback().Query().Before("gorm:query").Register("fail_targets", func(tx *gorm.DB) {
		if tx.Statement.Table == "test_derivative_s3_targets" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.db.Callback().Query().Remove("fail_targets") })
	if _, err := m.Verify(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("got %v, want database error", err)
	}
	recoveryState(t, m, a.ID, "ready")
}

func TestVerificationDoesNotOverwriteConcurrentChanges(t *testing.T) {
	for _, state := range []string{"obsolete", "ready"} {
		t.Run(state, func(t *testing.T) {
			m := recoveryManager(t)
			a := Artifact{ID: fmt.Sprintf("%064x", 1), State: "ready"}
			recoveryCreate(t, m, &a)
			var once sync.Once
			if err := m.db.Callback().Query().After("gorm:query").Register("replace_output", func(tx *gorm.DB) {
				if tx.Statement.Table == "test_derivative_artifacts" {
					once.Do(func() {
						err := m.db.Model(&Artifact{}).Where("id = ?", a.ID).Updates(map[string]any{"state": state, "error": "newer_output", "updated_at": a.UpdatedAt.Add(time.Second)}).Error
						if err != nil {
							tx.AddError(err)
						}
					})
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.db.Callback().Query().Remove("replace_output") })
			if _, err := m.Verify(context.Background()); err != nil {
				t.Fatal(err)
			}
			var current Artifact
			if err := m.db.First(&current, "id = ?", a.ID).Error; err != nil {
				t.Fatal(err)
			}
			if current.State != state || current.Error != "newer_output" {
				t.Fatalf("newer output overwritten: %+v", current)
			}
		})
	}
}

func TestVerificationCancellationPreservesReady(t *testing.T) {
	m := recoveryManager(t)
	a := Artifact{ID: fmt.Sprintf("%064x", 1), State: "ready"}
	recoveryCreate(t, m, &a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Verify(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	recoveryState(t, m, a.ID, "ready")
}

func TestVerificationCloseCancelsAndRejectsConcurrentChecks(t *testing.T) {
	m := recoveryManager(t)
	a := Artifact{ID: fmt.Sprintf("%064x", 1), State: "ready"}
	recoveryCreate(t, m, &a)
	entered := make(chan struct{})
	if err := m.db.Callback().Query().Before("gorm:query").Register("block_verify", func(tx *gorm.DB) {
		if tx.Statement.Table == "test_derivative_artifacts" {
			close(entered)
			<-tx.Statement.Context.Done()
			tx.AddError(tx.Statement.Context.Err())
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.db.Callback().Query().Remove("block_verify") })
	done := make(chan error, 1)
	go func() { _, err := m.Verify(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("verification did not start")
	}
	if _, err := m.Verify(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent verification: %v", err)
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel verification")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("verification after Close: %v", err)
	}
	if err := m.db.Callback().Query().Remove("block_verify"); err != nil {
		t.Fatal(err)
	}
	recoveryState(t, m, a.ID, "ready")
	if _, err := m.Verify(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("verification accepted after Close: %v", err)
	}
}
