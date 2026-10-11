package derivatives

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real SDK signing and HTTP I/O against an isolated in-process object store.
// It deliberately implements only exact-key Put/Get/Head/Delete, never List.
type s3TestStore struct {
	mu            sync.Mutex
	objects       map[string][]byte
	calls         []string
	access        string
	failPut       bool
	corrupt       bool
	redirect      string
	putWithoutACL bool
	getLength     int64
}

func newS3TestStore(t *testing.T) (*s3TestStore, *httptest.Server) {
	t.Helper()
	store := &s3TestStore{objects: map[string][]byte{}, access: "test-access", getLength: -1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store.mu.Lock()
		defer store.mu.Unlock()
		store.calls = append(store.calls, r.Method+" "+r.URL.Path)
		if !strings.Contains(r.Header.Get("Authorization"), "Credential="+store.access+"/") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>provider-secret-do-not-leak</Message></Error>`)
			return
		}
		if store.redirect != "" {
			w.Header().Set("Location", store.redirect)
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		switch r.Method {
		case http.MethodPut:
			if r.Header.Get("If-None-Match") == "*" {
				if _, exists := store.objects[r.URL.Path]; exists {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
			}
			if store.failPut {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, s3MaxObjectBytes+1))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			store.objects[r.URL.Path] = body
			store.putWithoutACL = r.Header.Get("X-Amz-Acl") == ""
			w.WriteHeader(200)
		case http.MethodGet, http.MethodHead:
			body, ok := store.objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				if r.Method != http.MethodHead {
					fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
				}
				return
			}
			if store.corrupt && r.Method == http.MethodGet {
				body = append([]byte(nil), body...)
				if len(body) > 0 {
					body[0] ^= 1
				}
			}
			length := int64(len(body))
			if store.getLength >= 0 && r.Method == http.MethodGet {
				length = store.getLength
			}
			w.Header().Set("Content-Length", fmt.Sprint(length))
			w.WriteHeader(200)
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
			}
		case http.MethodDelete:
			delete(store.objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	return store, server
}
func s3TestRequest(server *httptest.Server) S3ConfigRequest {
	return S3ConfigRequest{Enabled: true, Endpoint: server.URL, Region: "us-east-1", Bucket: "test-bucket", Prefix: "previews", PathStyle: true, AccessKey: "test-access", SecretKey: "test-secret"}
}
func s3TestBody(t *testing.T, body []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "output.webp")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	return path, hex.EncodeToString(digest[:])
}

func TestS3ClientRoundTripBothProfiles(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	target, err := m.s3Candidate(context.Background(), s3TestRequest(server))
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"thumb", "reader"} {
		body := []byte("RIFF-WEBP-" + profile)
		path, sha := s3TestBody(t, body)
		id := digest("s3-test", profile)
		key, err := s3ArtifactKey(target, id)
		if err != nil {
			t.Fatal(err)
		}
		if err = m.s3Put(context.Background(), target, key, path, int64(len(body)), sha); err != nil {
			t.Fatal(err)
		}
		got, err := m.s3Get(context.Background(), target, key, int64(len(body)), sha)
		if err != nil || string(got) != string(body) {
			t.Fatalf("get %q: %v", got, err)
		}
		if err = m.s3Head(context.Background(), target, key, int64(len(body))); err != nil {
			t.Fatal(err)
		}
		if err = m.s3Delete(context.Background(), target, key); err != nil {
			t.Fatal(err)
		}
		if _, err = m.s3Get(context.Background(), target, key, int64(len(body)), sha); !errors.Is(err, ErrS3ObjectMissing) {
			t.Fatalf("missing: %v", err)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.putWithoutACL || len(store.objects) != 0 {
		t.Fatal("public ACL or leaked objects")
	}
}

func TestS3ClientBoundsChecksumAndOwnedKeys(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	target, err := m.s3Candidate(context.Background(), s3TestRequest(server))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("bounded-output")
	path, sha := s3TestBody(t, body)
	key, _ := s3ArtifactKey(target, digest("bounded"))
	if err = m.s3Put(context.Background(), target, key, path, int64(len(body)), sha); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.corrupt = true
	store.mu.Unlock()
	if _, err = m.s3Get(context.Background(), target, key, int64(len(body)), sha); !errors.Is(err, ErrS3ChecksumMismatch) {
		t.Fatalf("checksum: %v", err)
	}
	store.mu.Lock()
	store.corrupt = false
	store.getLength = s3MaxObjectBytes + 1
	before := len(store.calls)
	store.mu.Unlock()
	if _, err = m.s3Get(context.Background(), target, key, int64(len(body)), sha); !errors.Is(err, ErrS3ReadFailed) {
		t.Fatalf("length: %v", err)
	}
	if _, err = m.s3Get(context.Background(), target, key, s3MaxObjectBytes+1, sha); !errors.Is(err, ErrS3ReadFailed) {
		t.Fatalf("bound: %v", err)
	}
	if err = m.s3Delete(context.Background(), target, "previews/not-owned"); !errors.Is(err, ErrS3DeleteFailed) {
		t.Fatalf("key: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.calls) != before+1 {
		t.Fatal("invalid input reached provider")
	}
}

func TestS3ClientRejectsRedirectsAndDoesNotRetry(t *testing.T) {
	m := coreManager(t)
	store, server := newS3TestStore(t)
	var redirected int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++; w.WriteHeader(200) }))
	defer other.Close()
	target, err := m.s3Candidate(context.Background(), s3TestRequest(server))
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.redirect = other.URL
	store.mu.Unlock()
	body := []byte("redirect")
	path, sha := s3TestBody(t, body)
	key, _ := s3ArtifactKey(target, digest("redirect"))
	if err = m.s3Put(context.Background(), target, key, path, int64(len(body)), sha); !errors.Is(err, ErrS3UploadFailed) {
		t.Fatalf("redirect: %v", err)
	}
	store.mu.Lock()
	calls := len(store.calls)
	store.mu.Unlock()
	if calls != 1 || redirected != 0 {
		t.Fatalf("calls=%d redirect=%d", calls, redirected)
	}
	client, closeClient, err := m.s3Client(target)
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	if client.Config.HTTPClient.Timeout != 20*time.Second || *client.Config.MaxRetries != 0 {
		t.Fatal("unbounded client")
	}
}

func TestS3EndpointPrefixAndAddressValidation(t *testing.T) {
	valid := S3ConfigRequest{Endpoint: "https://s3.example.com", Region: "auto", Bucket: "test-bucket", Prefix: "a/b/", PathStyle: true}
	target, err := normalizeS3Target(valid)
	if err != nil || target.Prefix != "a/b/" {
		t.Fatalf("valid: %v", err)
	}
	for _, endpoint := range []string{"http://8.8.8.8", "https://169.254.169.254", "http://[fe80::1]", "https://[fd00:ec2::254]", "https://100.100.100.200", "https://168.63.129.16", "https://metadata.google.internal", "https://user:pass@s3.example.com", "https://s3.example.com?secret=x", "https://s3.example.com#x", "https://s3.example.com/path", "file:///x"} {
		request := valid
		request.Endpoint = endpoint
		if _, err := normalizeS3Target(request); err == nil {
			t.Errorf("accepted endpoint %s", endpoint)
		}
	}
	for _, prefix := range []string{"../x", "a/../x", "a/./b", "/a", "a//b", "a\\b", "a/%2e%2e", "a\n"} {
		request := valid
		request.Prefix = prefix
		if _, err := normalizeS3Target(request); err == nil {
			t.Errorf("accepted prefix %q", prefix)
		}
	}
	for _, address := range []string{"0.0.0.0", "169.254.169.254", "224.0.0.1", "fd00:ec2::254"} {
		if s3AllowedAddress(net.ParseIP(address), false) {
			t.Errorf("allowed %s", address)
		}
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "::1", "fd00::1"} {
		if !s3AllowedAddress(net.ParseIP(address), true) {
			t.Errorf("blocked private %s", address)
		}
	}
}

func TestS3CDNValidationAndPrivacyDefault(t *testing.T) {
	for _, bad := range []string{"http://cdn.example.com", "https://u:p@cdn.example.com", "https://cdn.example.com/a/../b", "https://cdn.example.com/%2e%2e", "https://cdn.example.com/a?token=x", "https://cdn.example.com#x", "https://bad_host.example.com", "https://169.254.169.254"} {
		if _, err := normalizeS3CDN(bad); err == nil {
			t.Errorf("accepted CDN %s", bad)
		}
	}
	target := &DerivativeS3Target{Prefix: "previews/", CDNURL: "https://cdn.example.com/base"}
	key, _ := s3ArtifactKey(target, digest("cdn"))
	if s3CDNObjectURL(target, key) != "" {
		t.Fatal("CDN on without explicit opt-in")
	}
	target.PublicCDN = true
	if got := s3CDNObjectURL(target, key); got != "https://cdn.example.com/base/"+key {
		t.Fatalf("CDN URL: %s", got)
	}
}
