package derivatives

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
)

func cleanupMetadataSnapshot(t *testing.T, m *Manager) string {
	t.Helper()
	var artifacts []Artifact
	var journals []DerivativeRemoteObject
	var items []Item
	var sources []DerivativeSource
	for _, query := range []*gorm.DB{
		m.db.Order("id").Find(&artifacts),
		m.db.Order("id").Find(&journals),
		m.db.Order("id").Find(&items),
		m.db.Order("source_key").Find(&sources),
	} {
		if query.Error != nil {
			t.Fatal(query.Error)
		}
	}
	data, err := json.Marshal([]any{artifacts, journals, items, sources})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func cleanupFileSnapshot(t *testing.T, m *Manager) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(m.root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := entry.Info()
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%s:%d:%d", st.Mode(), st.Size(), st.ModTime().UnixNano())
		if entry.Type().IsRegular() {
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			value += ":" + string(body)
		}
		files[p] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func rejectCleanupWrites(t *testing.T, m *Manager) func() {
	t.Helper()
	name := "test_cleanup_assessment_read_only"
	fail := func(tx *gorm.DB) { tx.AddError(errors.New("assessment attempted a database write")) }
	if err := m.db.Callback().Create().Before("gorm:create").Register(name, fail); err != nil {
		t.Fatal(err)
	}
	if err := m.db.Callback().Update().Before("gorm:update").Register(name, fail); err != nil {
		t.Fatal(err)
	}
	if err := m.db.Callback().Delete().Before("gorm:delete").Register(name, fail); err != nil {
		t.Fatal(err)
	}
	return func() {
		m.db.Callback().Create().Remove(name)
		m.db.Callback().Update().Remove(name)
		m.db.Callback().Delete().Remove(name)
	}
}

func TestCleanupAssessmentLocalReadOnlyAndMatchesDeletion(t *testing.T) {
	m := coreManager(t)
	driver := &coreFakeDriver{}
	s := coreRegistered(t, m, driver)
	j := coreJob(t, m, s)
	active := coreItem(t, m, j, s.MountPath+"/active.jpg")
	keep := coreArtifact(t, m, s, s.MountPath+"/keep.jpg", "r", "thumb")
	stale := coreArtifact(t, m, s, s.MountPath+"/stale.jpg", "r", "thumb")
	if err := m.db.Model(&stale).Update("state", "stale").Error; err != nil {
		t.Fatal(err)
	}
	missing := coreArtifact(t, m, s, s.MountPath+"/missing.jpg", "r", "thumb")
	if err := m.db.Model(&missing).Update("state", "missing").Error; err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(m.root, missing.File)); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	work := func(name string, old bool) string {
		p := filepath.Join(m.root, "work", name)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"input", "output"} {
			if err := os.WriteFile(filepath.Join(p, file), []byte("work"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if old {
			if err := os.Chtimes(p, past, past); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	oldWork := work("item-999999-expired", true)
	activeWork := work(fmt.Sprintf("item-%d-active", active.ID), true)
	freshWork := work("item-999998-fresh", false)
	orphan := func(name string, old bool) string {
		id := digest(name)
		p := filepath.Join(m.root, "artifacts", id[:2], id+".webp")
		if err := ensureDirectory(filepath.Dir(p)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		if old {
			if err := os.Chtimes(p, past, past); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	oldOrphan := orphan("old-orphan", true)
	freshOrphan := orphan("young-orphan", false)
	metadata, files := cleanupMetadataSnapshot(t, m), cleanupFileSnapshot(t, m)
	allowWrites := rejectCleanupWrites(t, m)
	assessment, err := m.AssessCleanup(context.Background())
	allowWrites()
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Temporary != (CleanupTotals{Files: 1, Bytes: 8}) || assessment.Stale != (CleanupTotals{Files: 2, Bytes: stale.Bytes + int64(len("old-orphan"))}) {
		t.Fatalf("wrong cleanup entries: %+v", assessment)
	}
	if metadata != cleanupMetadataSnapshot(t, m) || !reflect.DeepEqual(files, cleanupFileSnapshot(t, m)) || driver.calls.Load() != 0 {
		t.Fatal("assessment changed metadata/files or probed sources")
	}
	for _, scope := range []string{"temporary", "stale"} {
		value, err := m.CleanupContext(context.Background(), scope)
		if err != nil {
			t.Fatal(err)
		}
		result := value.(CleanupResult)
		want := assessment.Temporary
		if scope == "stale" {
			want = assessment.Stale
		}
		if result.Files != want.Files || result.Bytes != want.Bytes {
			t.Fatalf("%s assessment/deletion mismatch: %+v %+v", scope, want, result)
		}
	}
	for _, p := range []string{oldWork, oldOrphan, filepath.Join(m.root, stale.File)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("eligible entry retained", p, err)
		}
	}
	for _, p := range []string{activeWork, freshWork, freshOrphan, filepath.Join(m.root, keep.File)} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("protected entry removed", p, err)
		}
	}
}

func TestCleanupAssessmentRemoteReadOnlySharedKeysAndAllJournals(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	view, err := m.SaveS3Configuration(context.Background(), s3TestRequest(server))
	if err != nil {
		t.Fatal(err)
	}
	j := remoteTestJob(t, m, "processing")
	src := remoteTestSource(t, m, j, j.Path+"/one.png", "revision")
	stale := remotePublishFixture(t, m, j, src, "thumb", []byte("stale-preview"))
	keep := remotePublishFixture(t, m, j, src, "reader", []byte("keep-preview"))
	if err := m.db.Model(&stale).Update("state", "obsolete").Error; err != nil {
		t.Fatal(err)
	}
	// Two obsolete identities share one object: count/delete the exact key once.
	copyStale := stale
	copyStale.ID, copyStale.ObjectID, copyStale.State = digest("stale-copy"), stale.ID, "stale"
	if err := m.db.Create(&copyStale).Error; err != nil {
		t.Fatal(err)
	}
	copyKeep := keep
	copyKeep.ID, copyKeep.ObjectID, copyKeep.State = digest("ready-shared-copy"), keep.ID, "deleted"
	if err := m.db.Create(&copyKeep).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.journalRemote(copyKeep); err != nil {
		t.Fatal(err)
	}
	// An old journal with an absent identity still cannot delete a ready key.
	if err := m.db.Model(&DerivativeRemoteObject{}).Where("target_id = ? AND object_key = ?", keep.OutputTargetID, keep.ObjectKey).Updates(map[string]any{"artifact_id": digest("absent-shared-identity"), "created_at": time.Now().Add(-48 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	for n := range 106 {
		id := digest(fmt.Sprintf("journal-%d", n))
		a := Artifact{ID: id, Profile: "thumb", OutputTargetID: view.TargetID, ObjectKey: "previews/artifacts/" + id[:2] + "/" + id + ".webp", Bytes: 6, SHA256: id}
		if err := m.journalRemote(a); err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		store.objects["/test-bucket/"+a.ObjectKey] = []byte("orphan")
		store.mu.Unlock()
		if n < 105 {
			if err := m.db.Model(&DerivativeRemoteObject{}).Where("artifact_id = ?", id).Update("created_at", time.Now().Add(-48*time.Hour)).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	metadata, files := cleanupMetadataSnapshot(t, m), cleanupFileSnapshot(t, m)
	store.mu.Lock()
	calls := len(store.calls)
	objects := len(store.objects)
	store.mu.Unlock()
	allowWrites := rejectCleanupWrites(t, m)
	assessment, err := m.AssessCleanup(context.Background())
	allowWrites()
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Temporary != (CleanupTotals{}) || assessment.Stale != (CleanupTotals{Files: 106, Bytes: stale.Bytes + 105*6}) {
		t.Fatalf("wrong remote assessment: %+v", assessment)
	}
	if metadata != cleanupMetadataSnapshot(t, m) || !reflect.DeepEqual(files, cleanupFileSnapshot(t, m)) {
		t.Fatal("remote assessment modified local metadata/files")
	}
	store.mu.Lock()
	unchanged := len(store.calls) == calls && len(store.objects) == objects
	store.mu.Unlock()
	if !unchanged {
		t.Fatal("assessment performed remote I/O")
	}
	value, err := m.CleanupContext(context.Background(), "stale")
	if err != nil {
		t.Fatal(err)
	}
	result := value.(CleanupResult)
	if result.Files != assessment.Stale.Files || result.Bytes != assessment.Stale.Bytes {
		t.Fatal("remote assessment/deletion mismatch", assessment, result)
	}
	store.mu.Lock()
	_, kept := store.objects["/test-bucket/"+keep.ObjectKey]
	remaining := len(store.objects)
	store.mu.Unlock()
	if !kept || remaining != 2 {
		t.Fatal("ready shared key or young upload damaged", remaining)
	}
}

func TestCleanupAssessmentFailsClosed(t *testing.T) {
	for _, kind := range []string{"database", "filesystem", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			m := coreManager(t)
			ctx := context.Background()
			switch kind {
			case "database":
				if err := m.db.Migrator().DropTable(&DerivativeRemoteObject{}); err != nil {
					t.Fatal(err)
				}
			case "filesystem":
				if err := os.Remove(filepath.Join(m.root, "work")); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			assessment, err := m.AssessCleanup(ctx)
			if err == nil {
				t.Fatal("incomplete assessment masqueraded as no residue", assessment)
			}
		})
	}
}

func TestCleanupRechecksAssessmentEligibility(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	a := coreArtifact(t, m, s, s.MountPath+"/restored.jpg", "r", "thumb")
	if err := m.db.Model(&a).Update("state", "obsolete").Error; err != nil {
		t.Fatal(err)
	}
	assessment, err := m.AssessCleanup(context.Background())
	if err != nil || assessment.Stale.Files != 1 {
		t.Fatal("eligible preview not assessed", assessment, err)
	}
	if err := m.db.Model(&a).Update("state", "ready").Error; err != nil {
		t.Fatal(err)
	}
	value, err := m.CleanupContext(context.Background(), "stale")
	if err != nil {
		t.Fatal(err)
	}
	if result := value.(CleanupResult); result.Files != 0 || result.Bytes != 0 {
		t.Fatal("cleanup used outdated assessment", result)
	}
	if _, err := m.FilePath(a); err != nil {
		t.Fatal("current preview removed", err)
	}
	if err := m.db.First(&a, "id = ?", a.ID).Error; err != nil || a.State != "ready" {
		t.Fatal("current metadata removed", a.State, err)
	}
}
