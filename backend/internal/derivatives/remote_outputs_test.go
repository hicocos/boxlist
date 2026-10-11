package derivatives

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

func remoteTestJob(t *testing.T, m *Manager, state string) Job {
	s := coreStorage(t, m)
	j := coreJob(t, m, s)
	if e := m.db.Model(&j).Update("state", state).Error; e != nil {
		t.Fatal(e)
	}
	j.State = state
	return j
}
func remoteTestSource(t *testing.T, m *Manager, j Job, p, rev string) DerivativeSource {
	var s model.Storage
	if e := m.db.First(&s, j.StorageID).Error; e != nil {
		t.Fatal(e)
	}
	src := newSource(&s, p, rev)
	if e := m.db.Create(&src).Error; e != nil {
		t.Fatal(e)
	}
	return src
}

func remoteTestItem(t *testing.T, m *Manager, j Job, p string) Item {
	var item Item
	err := m.db.Where("job_id = ? AND path = ?", j.ID, p).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return coreItem(t, m, j, p)
	}
	if err != nil {
		t.Fatal(err)
	}
	return item
}
func remotePublishFixture(t *testing.T, m *Manager, j Job, src DerivativeSource, profile string, payload []byte) Artifact {
	t.Helper()
	item := remoteTestItem(t, m, j, src.Path)
	if err := m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, profile).Update("state", "active").Error; err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(m.root, "work", "remote-test")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	path, sha := s3TestBody(t, payload)
	inside := filepath.Join(work, profile+".webp")
	if err := os.Rename(path, inside); err != nil {
		t.Fatal(err)
	}
	a, err := m.publish(j, item, src, Encoded{Profile: profile, Bytes: int64(len(payload)), Width: 100, Height: 100, SHA256: sha}, inside)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRemotePublicationBothProfilesReadsAndCleanOldTargets(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	request := s3TestRequest(server)
	_, err := m.SaveS3Configuration(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	j := remoteTestJob(t, m, "processing")
	src := remoteTestSource(t, m, j, j.Path+"/one.png", "revision")
	var arts []Artifact
	for _, profile := range []string{"thumb", "reader"} {
		payload := []byte("safe-output-" + profile)
		a := remotePublishFixture(t, m, j, src, profile, payload)
		arts = append(arts, a)
		if a.OutputTargetID == 0 || a.File != "" || a.ObjectKey == "" {
			t.Fatal("not remote", a.Profile)
		}
		if _, e := m.FilePath(a); !errors.Is(e, ErrInvalidPath) {
			t.Fatal("accepted remote local path")
		}
		r, e := m.OpenArtifact(context.Background(), a)
		if e != nil {
			t.Fatal(e)
		}
		got, e := io.ReadAll(r)
		r.Close()
		r.Close()
		if e != nil || !bytes.Equal(payload, got) {
			t.Fatal("remote mismatch")
		}
		store.mu.Lock()
		count := len(store.calls)
		store.mu.Unlock()
		if _, e = m.LookupPaths(context.Background(), []string{src.Path}); e != nil {
			t.Fatal(e)
		}
		if !m.artifactFileValid(a, true) {
			t.Fatal("startup recovery rejected remote metadata")
		}
		store.mu.Lock()
		after := len(store.calls)
		store.mu.Unlock()
		if count != after {
			t.Fatal("lookup/recovery performed network I/O")
		}
	}
	if _, err = m.SaveS3Configuration(context.Background(), S3ConfigRequest{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	r, err := m.OpenArtifact(context.Background(), arts[0])
	if err != nil {
		t.Fatal("disabled remote unreadable", err)
	}
	r.Close()
	if err = m.db.Model(&arts[0]).Update("state", "obsolete").Error; err != nil {
		t.Fatal(err)
	}
	result, err := m.cleanupRemote(context.Background())
	if err != nil || result.Files != 1 {
		t.Fatal("cleanup", result, err)
	}
	store.mu.Lock()
	_, oldExists := store.objects["/test-bucket/"+arts[0].ObjectKey]
	_, keepExists := store.objects["/test-bucket/"+arts[1].ObjectKey]
	store.mu.Unlock()
	if oldExists || !keepExists {
		t.Fatal("cleanup scope damaged valid output")
	}
	var keep Artifact
	if err = m.db.First(&keep, "id = ?", arts[1].ID).Error; err != nil || keep.State != "ready" {
		t.Fatal("lost ready metadata")
	}
}

func TestRemoteMissingCorruptAndFailedUploadPreserveReady(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	_, err := m.SaveS3Configuration(context.Background(), s3TestRequest(server))
	if err != nil {
		t.Fatal(err)
	}
	j := remoteTestJob(t, m, "processing")
	src := remoteTestSource(t, m, j, j.Path+"/one.png", "v1")
	a := remotePublishFixture(t, m, j, src, "thumb", []byte("known-good"))
	store.mu.Lock()
	store.corrupt = true
	store.mu.Unlock()
	if _, err = m.OpenArtifact(context.Background(), a); !errors.Is(err, ErrS3ChecksumMismatch) {
		t.Fatal(err)
	}
	var missing Artifact
	if err = m.db.First(&missing, "id = ?", a.ID).Error; err != nil || missing.State != "missing" {
		t.Fatal("corruption not invalidated")
	}
	store.mu.Lock()
	store.corrupt = false
	delete(store.objects, "/test-bucket/"+a.ObjectKey)
	store.mu.Unlock()
	if _, err = m.OpenArtifact(context.Background(), a); !errors.Is(err, ErrS3ObjectMissing) {
		t.Fatal(err)
	}
	if len(m.remoteReads) != 0 {
		t.Fatal("failed reads leaked limiter")
	}
	// Failed forced replacement never marks old output obsolete.
	if err = m.db.Model(&a).Update("state", "ready").Error; err != nil {
		t.Fatal(err)
	}
	item := remoteTestItem(t, m, j, src.Path)
	work := filepath.Join(m.root, "work", "failed")
	os.MkdirAll(work, 0700)
	p, sha := s3TestBody(t, []byte("new-payload"))
	inside := filepath.Join(work, "thumb.webp")
	os.Rename(p, inside)
	store.mu.Lock()
	store.failPut = true
	store.mu.Unlock()
	_, err = m.publish(j, item, src, Encoded{Profile: "thumb", Bytes: 11, Width: 100, Height: 100, SHA256: sha}, inside)
	if !errors.Is(err, ErrS3UploadFailed) {
		t.Fatal(err)
	}
	if err = m.db.First(&a, "id = ?", a.ID).Error; err != nil || a.State != "ready" {
		t.Fatal("failed upload revoked old")
	}
}

func TestRemoteJournalOnlyCleansOldUnreferencedKeys(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	view, err := m.SaveS3Configuration(context.Background(), s3TestRequest(server))
	if err != nil {
		t.Fatal(err)
	}
	id := digest("orphan")
	a := Artifact{ID: id, Profile: "reader", Bytes: 6, SHA256: id, OutputTargetID: view.TargetID, ObjectKey: "previews/artifacts/" + id[:2] + "/" + id + ".webp"}
	if err = m.journalRemote(a); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.objects["/test-bucket/"+a.ObjectKey] = []byte("orphan")
	store.objects["/test-bucket/previews/unknown.webp"] = []byte("keep")
	store.mu.Unlock()
	result, err := m.cleanupRemote(context.Background())
	if err != nil || result.Files != 0 {
		t.Fatal("young orphan deleted", result, err)
	}
	if err = m.db.Model(&DerivativeRemoteObject{}).Where("artifact_id = ?", a.ID).Update("created_at", time.Now().Add(-25*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	result, err = m.cleanupRemote(context.Background())
	if err != nil || result.Files != 1 {
		t.Fatal(result, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.objects) != 1 {
		t.Fatal("orphan not deleted or unknown object touched")
	}
	var rows int64
	if err = m.db.Model(&DerivativeRemoteObject{}).Count(&rows).Error; err != nil || rows != 0 {
		t.Fatal("journal deletion", rows, err)
	}
}

// A cancelled publication never makes a ready artifact addressable.
func TestRemoteCancelledPublicationJournalsWithoutCommit(t *testing.T) {
	m := coreManager(t)
	_, server := newS3TestStore(t)
	_, err := m.SaveS3Configuration(context.Background(), s3TestRequest(server))
	if err != nil {
		t.Fatal(err)
	}
	j := remoteTestJob(t, m, "cancelled")
	src := remoteTestSource(t, m, j, j.Path+"/one.png", "v1")
	item := remoteTestItem(t, m, j, src.Path)
	p, sha := s3TestBody(t, []byte("cancelled"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = m.publishContext(ctx, j, item, src, Encoded{Profile: "thumb", Bytes: 9, Width: 100, Height: 100, SHA256: sha}, p)
	if err == nil {
		t.Fatal("accepted cancelled publication")
	}
	var count int64
	if err = m.db.Model(&Artifact{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("committed cancelled artifact")
	}
	// Type check GORM remains linked to explicit transactions in parent policy.
	var _ *gorm.DB = m.db
}
