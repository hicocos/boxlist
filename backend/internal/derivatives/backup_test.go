package derivatives

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func backupFixture(t *testing.T) (*Manager, *s3TestStore, S3PreviewBackup, Artifact) {
	t.Helper()
	m := coreManager(t)
	store, server := newS3TestStore(t)
	req := s3TestRequest(server)
	if _, err := m.SaveS3Configuration(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	j := remoteTestJob(t, m, "processing")
	src := remoteTestSource(t, m, j, j.Path+"/remote.png", digest("revision"))
	a := remotePublishFixture(t, m, j, src, "thumb", bytes.Repeat([]byte("portable-preview"), 8192))
	// A second historical target is retained even while the active target is disabled.
	req.Prefix = "historical"
	if _, err := m.SaveS3Configuration(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveS3Configuration(context.Background(), S3ConfigRequest{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	var storage model.Storage
	if err := m.db.First(&storage, j.StorageID).Error; err != nil {
		t.Fatal(err)
	}
	coreArtifact(t, m, storage, j.Path+"/local.png", digest("local-revision"), "thumb")
	store.mu.Lock()
	store.calls = nil
	store.mu.Unlock()
	b, err := m.BackupS3Previews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.calls) != 0 {
		t.Fatal("backup performed object I/O", store.calls)
	}
	return m, store, b, a
}

func roundtripBackup(t *testing.T, b S3PreviewBackup) S3PreviewBackup {
	t.Helper()
	encoded, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var result S3PreviewBackup
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestS3PreviewBackupPortableRoundtripAndLocalExclusion(t *testing.T) {
	original, store, b, a := backupFixture(t)
	if len(b.Previews) != 1 || len(b.Targets) != 2 || b.Enabled {
		t.Fatal("local preview included or historical target missing")
	}
	encoded, err := json.Marshal(b)
	if err != nil || b.Version != 2 || bytes.Contains(encoded, []byte(`"data"`)) {
		t.Fatal("backup is not metadata-only", err)
	}
	b = roundtripBackup(t, b)
	fresh := coreManager(t)
	if err := fresh.db.Create(&model.Storage{ID: 42, MountPath: "/pictures", Driver: "CoreTest", Addition: `{"root":"one"}`}).Error; err != nil {
		t.Fatal(err)
	}
	offset := b.Targets[0].Config
	offset.Prefix = "destination-offset/"
	if _, err := fresh.SaveS3Configuration(context.Background(), offset); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	expected := append([]byte(nil), store.objects["/test-bucket/"+a.ObjectKey]...)
	store.objects["/test-bucket/unrelated"] = []byte("keep")
	store.calls = nil
	store.mu.Unlock()
	if err := fresh.RestoreS3Previews(context.Background(), b, false); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	if len(store.calls) != 0 {
		t.Fatal("restore performed object I/O", store.calls)
	}
	store.mu.Unlock()
	views, err := fresh.LookupPaths(context.Background(), []string{a.Path, "/pictures/local.png"})
	if err != nil || len(views) != 1 {
		t.Fatal("portable lookup failed", err)
	}
	restored := views[a.Path]["thumb"]
	if restored.ID == a.ID || restored.StorageID != 42 || restored.Binding == a.Binding || restored.OutputTargetID == a.OutputTargetID || restored.File != "" || restored.ObjectKey != a.ObjectKey || restored.ObjectID != a.ID {
		t.Fatal("identity not remapped")
	}
	if _, err := fresh.Artifact(restored.ID, "thumb"); err != nil {
		t.Fatal("artifact not immediately addressable", err)
	}
	r, err := fresh.OpenArtifact(context.Background(), restored)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(data, expected) {
		t.Fatal("restored bytes differ")
	}
	reexported, err := fresh.BackupS3Previews(context.Background())
	if err != nil || reexported.Previews[0].Artifact.ID != a.ID || reexported.Previews[0].ObjectKey != a.ObjectKey {
		t.Fatal("retained key cannot be exported again", err)
	}
	oldKey, err := os.ReadFile(filepath.Join(original.root, "s3.key"))
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := os.ReadFile(filepath.Join(fresh.root, "s3.key"))
	if err != nil || bytes.Equal(oldKey, newKey) {
		t.Fatal("installation key copied")
	}
	var targets []DerivativeS3Target
	if err := fresh.db.Find(&targets).Error; err != nil || len(targets) != 3 {
		t.Fatal("historical targets missing", err)
	}
	for _, target := range targets {
		access, secret, err := fresh.s3Credentials(&target)
		if err != nil || access != "test-access" || secret != "test-secret" || bytes.Contains(target.Credentials, []byte(secret)) {
			t.Fatal("credentials not locally sealed")
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if string(store.objects["/test-bucket/unrelated"]) != "keep" {
		t.Fatal("unrelated object modified")
	}
	for _, call := range store.calls {
		if strings.HasPrefix(call, "DELETE ") && !strings.Contains(call, ".boxlist-probes/") {
			t.Fatal("restore deleted object")
		}
	}
}

func TestS3PreviewBackupIntegrityValidationBeforeMutation(t *testing.T) {
	original, store, b, a := backupFixture(t)
	fresh := coreManager(t)
	coreStorage(t, fresh)
	b = roundtripBackup(t, b)
	b.Previews[0].Artifact.Width++
	if err := fresh.RestoreS3Previews(context.Background(), b, true); !errors.Is(err, ErrBackupIntegrity) {
		t.Fatal(err)
	}
	b = roundtripBackup(t, b)
	b.Previews[0].ObjectKey = "previews/artifacts/00/" + digest("foreign-key") + ".webp"
	b.SHA256 = backupChecksum(b)
	if err := fresh.RestoreS3Previews(context.Background(), b, true); !errors.Is(err, ErrBackupInvalid) {
		t.Fatal("object key identity not checked", err)
	}
	var count int64
	if err := fresh.db.Model(&DerivativeS3Target{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("invalid backup persisted targets")
	}
	store.mu.Lock()
	delete(store.objects, "/test-bucket/"+a.ObjectKey)
	store.mu.Unlock()
	if _, err := original.BackupS3Previews(context.Background()); err != nil {
		t.Fatal("offline/missing bucket prevented index export", err)
	}
	m, _, complete, _ := backupFixture(t)
	complete.Previews[0].ObjectKey = "unrelated"
	complete.SHA256 = backupChecksum(complete)
	if err := fresh.RestoreS3Previews(context.Background(), complete, true); !errors.Is(err, ErrBackupInvalid) {
		t.Fatal("accepted arbitrary object key", err)
	}
	if code := ErrorCode(errors.New("provider-secret-do-not-leak")); strings.Contains(code, "secret") {
		t.Fatal("error leakage")
	}
	var target DerivativeS3Target
	if err := m.db.First(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.db.Model(&target).Update("credentials", []byte("broken-provider-secret")).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := m.BackupS3Previews(context.Background()); !errors.Is(err, ErrS3KeyUnavailable) {
		t.Fatal("bad credentials silently exported", err)
	}
}

func TestS3PreviewBackupMergeAndOverride(t *testing.T) {
	_, _, b, _ := backupFixture(t)
	fresh := coreManager(t)
	s := coreStorage(t, fresh)
	local := coreArtifact(t, fresh, s, "/pictures/remote.png", digest("existing"), "thumb")
	unrelated := coreArtifact(t, fresh, s, "/pictures/unrelated.png", digest("unrelated"), "reader")
	if err := fresh.RestoreS3Previews(context.Background(), roundtripBackup(t, b), false); err != nil {
		t.Fatal(err)
	}
	views, err := fresh.LookupPaths(context.Background(), []string{local.Path})
	if err != nil || views[local.Path]["thumb"].ID != local.ID {
		t.Fatal("merge replaced existing preview")
	}
	if err := fresh.RestoreS3Previews(context.Background(), roundtripBackup(t, b), true); err != nil {
		t.Fatal(err)
	}
	views, err = fresh.LookupPaths(context.Background(), []string{local.Path, unrelated.Path})
	if err != nil || views[local.Path]["thumb"].OutputTargetID == 0 || views[unrelated.Path]["reader"].ID != unrelated.ID {
		t.Fatal("override failed or unrelated preview affected")
	}
}

func TestS3PreviewBackupRefusesMissingMountButDoesNotInspectObjects(t *testing.T) {
	_, store, b, a := backupFixture(t)
	fresh := coreManager(t)
	if err := fresh.RestoreS3Previews(context.Background(), b, true); !errors.Is(err, ErrBackupStorage) {
		t.Fatal("missing mount accepted", err)
	}
	coreStorage(t, fresh)
	store.mu.Lock()
	store.objects["/test-bucket/"+a.ObjectKey] = bytes.Repeat([]byte("x"), int(a.Bytes))
	store.mu.Unlock()
	if err := fresh.RestoreS3Previews(context.Background(), b, true); err != nil {
		t.Fatal("index restore inspected object", err)
	}
	store.mu.Lock()
	if len(store.calls) != 0 || !bytes.Equal(store.objects["/test-bucket/"+a.ObjectKey], bytes.Repeat([]byte("x"), int(a.Bytes))) {
		t.Fatal("restore touched objects", store.calls)
	}
	store.mu.Unlock()
	views, err := fresh.LookupPaths(context.Background(), []string{a.Path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.OpenArtifact(context.Background(), views[a.Path]["thumb"]); !errors.Is(err, ErrS3ChecksumMismatch) {
		t.Fatal("subsequent read did not validate existing object", err)
	}
}

func TestS3PreviewBackupRetainedKeyCleanupAndValidation(t *testing.T) {
	_, store, b, a := backupFixture(t)
	fresh := coreManager(t)
	if err := fresh.db.Create(&model.Storage{ID: 42, MountPath: "/pictures", Driver: "CoreTest", Addition: `{"root":"one"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fresh.RestoreS3Previews(context.Background(), b, true); err != nil {
		t.Fatal(err)
	}
	views, err := fresh.LookupPaths(context.Background(), []string{a.Path})
	if err != nil {
		t.Fatal(err)
	}
	restored := views[a.Path]["thumb"]
	bad := restored
	bad.ObjectKey = "unrelated"
	if fresh.artifactFileValid(bad, false) {
		t.Fatal("arbitrary retained key accepted")
	}
	bad.ObjectKey = b.Targets[0].Config.Prefix + ".boxlist-probes/" + digest("probe")
	if _, err := fresh.remoteTarget(bad); !errors.Is(err, ErrInvalidPath) {
		t.Fatal("probe accepted as artifact", err)
	}
	shared := restored
	shared.ID, shared.State = digest("obsolete-shared-identity"), "obsolete"
	if err := fresh.db.Create(&shared).Error; err != nil {
		t.Fatal(err)
	}
	if result, err := fresh.cleanupRemote(context.Background()); err != nil || result.Files != 0 {
		t.Fatal("cleanup removed shared retained object", result, err)
	}
	store.mu.Lock()
	if len(store.calls) != 0 {
		t.Fatal("shared cleanup performed object I/O", store.calls)
	}
	store.mu.Unlock()
	// Orphan journals retain the original identity independently of remapped IDs.
	if err := fresh.db.Delete(&restored).Error; err != nil {
		t.Fatal(err)
	}
	if err := fresh.db.Model(&DerivativeRemoteObject{}).Where("artifact_id = ?", restored.ID).Update("created_at", time.Now().Add(-48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fresh.cleanupRemote(context.Background())
	if err != nil || result.Files != 1 {
		t.Fatal("retained-key journal cleanup failed", result, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.objects["/test-bucket/"+a.ObjectKey]; exists {
		t.Fatal("cleanup did not remove original key")
	}
}
