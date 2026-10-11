package derivatives

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestS3ConfigurationEncryptedPersistenceRotationAndOldTargets(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	request := s3TestRequest(server)
	view, err := m.SaveS3Configuration(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Enabled || view.TargetID == 0 || !view.AccessKeyConfigured || !view.SecretKeyConfigured || view.PublicCDN {
		t.Fatalf("view: %+v", view)
	}
	firstID := view.TargetID
	var first DerivativeS3Target
	if err = m.db.First(&first, firstID).Error; err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first.Credentials, []byte(request.SecretKey)) || bytes.Contains(first.Credentials, []byte(request.AccessKey)) {
		t.Fatal("plaintext credentials in database")
	}
	serialized, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte(request.SecretKey)) || bytes.Contains(serialized, []byte(request.AccessKey)) {
		t.Fatal("credential leaked to view")
	}
	st, err := os.Stat(filepath.Join(m.root, "s3.key"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 || st.Size() != 32 {
		t.Fatal("unsafe key file")
	}
	// Blank credentials retain only the currently selected identical identity.
	request.AccessKey, request.SecretKey = "", ""
	view, err = m.SaveS3Configuration(context.Background(), request)
	if err != nil || view.TargetID != firstID {
		t.Fatalf("retain: %+v %v", view, err)
	}
	// Rotating credentials updates that target in place, not a new destination.
	request.AccessKey, request.SecretKey = "rotated-access", "rotated-secret"
	store.mu.Lock()
	store.access = request.AccessKey
	store.mu.Unlock()
	view, err = m.SaveS3Configuration(context.Background(), request)
	if err != nil || view.TargetID != firstID {
		t.Fatalf("rotate: %+v %v", view, err)
	}
	loaded, err := m.s3TargetForArtifact(Artifact{OutputTargetID: firstID})
	if err != nil {
		t.Fatal(err)
	}
	access, secret, err := m.s3Credentials(loaded)
	if err != nil || access != request.AccessKey || secret != request.SecretKey {
		t.Fatal("rotation not persisted")
	}
	request.Prefix = "other"
	request.AccessKey, request.SecretKey = "", ""
	if _, err = m.SaveS3Configuration(context.Background(), request); !errors.Is(err, ErrS3CredentialsRequired) {
		t.Fatalf("cross-target retain: %v", err)
	}
	request.AccessKey, request.SecretKey = "rotated-access", "rotated-secret"
	view, err = m.SaveS3Configuration(context.Background(), request)
	if err != nil || view.TargetID == firstID {
		t.Fatalf("new target: %+v %v", view, err)
	}
	old, err := m.s3TargetForArtifact(Artifact{OutputTargetID: firstID})
	if err != nil || old.Prefix != "previews/" {
		t.Fatal("old destination lost")
	}
	view, err = m.SaveS3Configuration(context.Background(), S3ConfigRequest{Enabled: false})
	if err != nil || view.Enabled || view.TargetID == 0 {
		t.Fatalf("disable: %+v %v", view, err)
	}
	if target, err := m.s3WriteTarget(); err != nil || target != nil {
		t.Fatalf("disabled write: %+v %v", target, err)
	}
	if _, err = m.s3TargetForArtifact(Artifact{OutputTargetID: firstID}); err != nil {
		t.Fatal("disable revoked old target")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.objects) != 0 {
		t.Fatal("probe objects leaked")
	}
}

func TestS3ConfigurationProbeCannotBeBypassedOrUseOldCredentials(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	request := s3TestRequest(server)
	if result, err := m.TestS3Configuration(context.Background(), request); err != nil || !result.Success {
		t.Fatalf("test: %+v %v", result, err)
	}
	var count int64
	if err := m.db.Model(&DerivativeS3Target{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("test saved settings")
	}
	if _, err := os.Stat(filepath.Join(m.root, "s3.key")); !os.IsNotExist(err) {
		t.Fatal("test created encryption key")
	}
	store.mu.Lock()
	store.failPut = true
	store.mu.Unlock()
	if _, err := m.SaveS3Configuration(context.Background(), request); !errors.Is(err, ErrS3ProbeFailed) {
		t.Fatalf("save bypassed own probe: %v", err)
	}
	store.mu.Lock()
	store.failPut = false
	store.mu.Unlock()
	if _, err := m.SaveS3Configuration(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.AccessKey = "wrong-request-access"
	if _, err := m.TestS3Configuration(context.Background(), request); !errors.Is(err, ErrS3ProbeFailed) {
		t.Fatalf("probe used stored credentials: %v", err)
	}
	view, err := m.S3Configuration()
	if err != nil || !view.Enabled {
		t.Fatal("failed probe changed settings")
	}
}

func TestS3ConfigurationLostOrUnsafeKeyFailsClosed(t *testing.T) {
	m := coreManager(t)
	_, server := newS3TestStore(t)
	if _, err := m.SaveS3Configuration(context.Background(), s3TestRequest(server)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.root, "s3.key")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.S3Configuration(); !errors.Is(err, ErrS3KeyUnavailable) {
		t.Fatalf("unsafe key: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.s3EncryptionKey(true); !errors.Is(err, ErrS3KeyUnavailable) {
		t.Fatalf("regenerated missing key: %v", err)
	}
	if err := m.initS3Configuration(); !errors.Is(err, ErrS3KeyUnavailable) {
		t.Fatalf("startup accepted missing key: %v", err)
	}
	if reopened, err := New(m.db, m.root); !errors.Is(err, ErrS3KeyUnavailable) {
		if reopened != nil {
			reopened.Close()
		}
		t.Fatalf("manager startup accepted missing key: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing key silently replaced")
	}
}

func TestS3ConfigurationConcurrentAtomicKeyAndCipherBinding(t *testing.T) {
	m := coreManager(t)
	var wg sync.WaitGroup
	results := make(chan []byte, 8)
	errorsCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := m.s3EncryptionKey(true)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- key
		}()
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	var first []byte
	for key := range results {
		if first == nil {
			first = key
		}
		if !bytes.Equal(first, key) {
			t.Fatal("racing creators used different keys")
		}
	}
	sealed, err := sealS3Credentials(first, "identity-a", "access", "secret")
	if err != nil {
		t.Fatal(err)
	}
	target := &DerivativeS3Target{ID: 1, IdentityHash: "identity-b", Credentials: sealed}
	if _, _, err := m.s3Credentials(target); !errors.Is(err, ErrS3KeyUnavailable) {
		t.Fatal("ciphertext reusable across targets")
	}
	target.IdentityHash = "identity-a"
	target.Credentials[len(target.Credentials)-1] ^= 1
	if _, _, err := m.s3Credentials(target); !errors.Is(err, ErrS3KeyUnavailable) {
		t.Fatal("ciphertext tampering accepted")
	}
	if !m.db.Migrator().HasTable("test_derivative_s3_targets") || !m.db.Migrator().HasTable("test_derivative_s3_settings") {
		t.Fatal("GORM table prefix ignored")
	}
}

func TestS3ConfigurationPublicCDNProbeRejectsUnverifiedDelivery(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	request := s3TestRequest(server)
	request.PublicCDN = true
	request.CDNURL = "https://" + strings.TrimPrefix(server.URL, "http://")
	// This HTTP-only loopback fixture cannot satisfy a verified HTTPS CDN GET.
	if _, err := m.SaveS3Configuration(context.Background(), request); !errors.Is(err, ErrS3CDNProbeFailed) {
		t.Fatalf("unverified CDN accepted: %v", err)
	}
	var count int64
	if err := m.db.Model(&DerivativeS3Target{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("failed CDN verification saved target")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.objects) != 0 {
		t.Fatal("failed CDN probe leaked object")
	}
}

func TestS3ConfigurationAggregateIncludesBothProfilesAndTargets(t *testing.T) {
	m := coreManager(t)
	for i, profile := range []string{"thumb", "reader"} {
		a := Artifact{ID: digest("aggregate", profile), Profile: profile, OutputTargetID: uint(i + 1), Bytes: int64(100 + i), State: "ready"}
		if err := m.db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := m.db.Create(&Artifact{ID: digest("local"), Profile: "thumb", Bytes: 999, State: "ready"}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := m.S3Configuration()
	if err != nil || view.ObjectCount != 2 || view.UsedBytes != 201 {
		t.Fatalf("usage %+v %v", view, err)
	}
	result, err := m.TestS3Configuration(context.Background(), S3ConfigRequest{})
	if err != nil || !result.Success || !result.Skipped {
		t.Fatalf("empty disabled test %+v %v", result, err)
	}
	if code, ok := S3ErrorCode(ErrS3UploadFailed); !ok || code != "s3_upload_failed" || strings.Contains(code, "secret") {
		t.Fatal("unsafe error code")
	}
}
