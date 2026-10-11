package iconlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func transparentRequest(s *Service, method, id, revision string) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if method == http.MethodPost {
		body = bytes.NewReader([]byte(`{"revision":"` + revision + `"}`))
	} else {
		body = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, "/icon/api/assets/"+id+"/transparent", body)
	r.Header.Set("Authorization", "test-token")
	r.Header.Set("Origin", "https://example.test")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func waitTransparency(t *testing.T, s *Service, id string) TransparencyJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := transparentRequest(s, http.MethodGet, id, "")
		if w.Code != 200 {
			t.Fatalf("poll: %d %s", w.Code, w.Body.String())
		}
		var response struct {
			Data TransparencyJob `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Data.Status != "processing" {
			return response.Data
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("transparency worker did not finish")
	return TransparencyJob{}
}

func transparencyHooks() Hooks {
	return Hooks{
		Authorize:   func(*http.Request) (string, error) { return "1", nil },
		ReadHead:    func(*http.Request) (string, error) { return "", nil },
		CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "https://example.test" },
	}
}

func TestTransparencyHTTPLifecycleAndCache(t *testing.T) {
	s, err := New(t.TempDir(), transparencyHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := transparencyFixture(t)
	asset, err := s.store.commit("1", transparencyUploadKey, "white.gif", input, input)
	if err != nil {
		t.Fatal(err)
	}
	id := asset.Asset.ID
	if w := transparentRequest(s, http.MethodGet, id, ""); w.Code != 404 {
		t.Fatalf("missing job: %d", w.Code)
	}
	if w := transparentRequest(s, http.MethodPost, id, strings.Repeat("0", 64)); w.Code != 409 {
		t.Fatalf("stale revision: %d", w.Code)
	}
	if w := transparentRequest(s, http.MethodPost, id, asset.Asset.Revision); w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	job := waitTransparency(t, s, id)
	if job.Status != "done" || job.Progress != 100 || job.Asset == nil || job.Asset.ID != id || job.Asset.Revision == asset.Asset.Revision {
		t.Fatalf("completion: %+v", job)
	}
	for _, suffix := range []string{"", ".gif"} {
		for _, query := range []string{"", "?v=current", "?v=" + asset.Asset.Revision, "?v=" + job.Asset.Revision} {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/icon/assets/"+id+suffix+query, nil))
			etag := `"` + job.Asset.Revision + `"`
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("ETag") != etag || contentRevision(w.Body.Bytes()) != job.Asset.Revision {
				t.Fatalf("cache response: %d %v", w.Code, w.Header())
			}
			r := httptest.NewRequest(http.MethodGet, "/icon/assets/"+id+suffix+query, nil)
			r.Header.Set("If-None-Match", `W/`+etag)
			w = httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 304 || w.Body.Len() != 0 {
				t.Fatalf("revalidation: %d", w.Code)
			}
		}
	}
	if w := transparentRequest(s, http.MethodPost, id, job.Asset.Revision); w.Code != 200 {
		t.Fatalf("rerun: %d", w.Code)
	}
	unchanged := waitTransparency(t, s, id)
	if unchanged.Status != "unchanged" || unchanged.Asset == nil || *unchanged.Asset != *job.Asset {
		t.Fatalf("unchanged result: %+v", unchanged)
	}
	s.workers.Wait()
	if len(s.jobs) != 1 {
		t.Fatalf("job history leaked: %d", len(s.jobs))
	}
	png, err := s.store.commit("1", "1123456789abcdef0123456789abcdef", "static.png", []byte("original"), []byte("preview"))
	if err != nil {
		t.Fatal(err)
	}
	if w := transparentRequest(s, http.MethodPost, png.Asset.ID, png.Asset.Revision); w.Code != 415 {
		t.Fatalf("PNG accepted: %d", w.Code)
	}
}

func TestTransparencyDuplicateBusyDeletionAndAuth(t *testing.T) {
	for _, scenario := range []string{"success", "deletion", "auth"} {
		t.Run(scenario, func(t *testing.T) {
			var revoked atomic.Bool
			hooks := transparencyHooks()
			hooks.Authorize = func(r *http.Request) (string, error) {
				if err := r.Context().Err(); err != nil {
					return "", fault(401)
				}
				if revoked.Load() {
					return "", fault(403)
				}
				return "1", nil
			}
			s, err := New(t.TempDir(), hooks)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			input := transparencyFixture(t)
			asset, err := s.store.commit("1", transparencyUploadKey, "white.gif", input, input)
			if err != nil {
				t.Fatal(err)
			}
			other, err := s.store.commit("1", "1123456789abcdef0123456789abcdef", "other.gif", input, input)
			if err != nil {
				t.Fatal(err)
			}
			// Block only commit, leaving HTTP and expensive image processing usable.
			s.mutation.Lock()
			locked := true
			defer func() {
				if locked {
					s.mutation.Unlock()
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			r := httptest.NewRequest(http.MethodPost, "/icon/api/assets/"+asset.Asset.ID+"/transparent", strings.NewReader(`{"revision":"`+asset.Asset.Revision+`"}`)).WithContext(ctx)
			r.Header.Set("Authorization", "test-token")
			r.Header.Set("Origin", "https://example.test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			cancel()
			if w.Code != 200 {
				t.Fatalf("start: %d %s", w.Code, w.Body.String())
			}
			if w = transparentRequest(s, http.MethodPost, asset.Asset.ID, asset.Asset.Revision); w.Code != 200 {
				t.Fatalf("duplicate: %d", w.Code)
			}
			if w = transparentRequest(s, http.MethodPost, other.Asset.ID, other.Asset.Revision); w.Code != 429 {
				t.Fatalf("busy: %d", w.Code)
			}
			if scenario == "deletion" {
				if err = s.store.delete(asset.Asset.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "auth" {
				revoked.Store(true)
			}
			s.mutation.Unlock()
			locked = false
			s.workers.Wait()
			s.jobsMu.Lock()
			job := s.jobs[asset.Asset.ID]
			s.jobsMu.Unlock()
			if scenario == "success" {
				if job.Status != "done" {
					t.Fatalf("request cancellation aborted worker: %+v", job)
				}
			} else {
				if job.Status != "failed" || job.Message == "" || job.Asset != nil {
					t.Fatalf("failure result: %+v", job)
				}
				dir := "originals"
				if scenario == "deletion" {
					dir = "trash/originals"
				}
				data, err := os.ReadFile(s.store.path(dir, asset.Asset.ID, ".original"))
				if err != nil || !bytes.Equal(data, input) {
					t.Fatalf("failed job destroyed original: %v", err)
				}
			}
		})
	}
}

func TestTransparencyRequiresAuthorizationAndOrigin(t *testing.T) {
	hooks := transparencyHooks()
	hooks.Authorize = func(r *http.Request) (string, error) {
		if r.Header.Get("Authorization") != "test-token" {
			return "", fault(401)
		}
		return "1", nil
	}
	s, err := New(t.TempDir(), hooks)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const id = "upload-0123456789abcdef0123456789abcdef"
	s.jobs[id] = TransparencyJob{AssetID: id, Status: "unchanged", Progress: 100}
	for _, origin := range []string{"", "https://evil.test"} {
		r := httptest.NewRequest(http.MethodPost, "/icon/api/assets/"+id+"/transparent", nil)
		r.Header.Set("Authorization", "test-token")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("POST origin bypass: %d", w.Code)
		}
	}
	// Browser same-origin GET fetches do not carry Origin. Polling must still
	// authorize the native administrator before exposing an existing job.
	r := httptest.NewRequest(http.MethodGet, "/icon/api/assets/"+id+"/transparent", nil)
	r.Header.Set("Authorization", "test-token")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("authenticated GET without Origin: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Data TransparencyJob `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.AssetID != id || response.Data.Status != "unchanged" {
		t.Fatalf("poll returned unexpected job: %+v", response.Data)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, token := range []string{"", "invalid-token"} {
			r := httptest.NewRequest(method, "/icon/api/assets/"+id+"/transparent", nil)
			r.Header.Set("Authorization", token)
			if method == http.MethodPost {
				r.Header.Set("Origin", "https://example.test")
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatalf("%s authorization bypass with token %q: %d", method, token, w.Code)
			}
		}
	}
}

func TestTransparencyProcessingFailureRetainsBytes(t *testing.T) {
	s, err := New(t.TempDir(), transparencyHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := []byte("invalid GIF bytes")
	asset, err := s.store.commit("1", transparencyUploadKey, "invalid.gif", input, input)
	if err != nil {
		t.Fatal(err)
	}
	if w := transparentRequest(s, http.MethodPost, asset.Asset.ID, asset.Asset.Revision); w.Code != 200 {
		t.Fatalf("start: %d", w.Code)
	}
	job := waitTransparency(t, s, asset.Asset.ID)
	if job.Status != "failed" || job.Message == "" || job.Asset != nil {
		t.Fatalf("invalid failure result: %+v", job)
	}
	for _, path := range []string{s.store.path("originals", asset.Asset.ID, ".original"), s.store.path("previews", asset.Asset.ID, ".gif")} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, input) {
			t.Fatalf("processing failure modified bytes: %v", err)
		}
	}
	if w := transparentRequest(s, http.MethodPost, asset.Asset.ID, asset.Asset.Revision); w.Code != 200 {
		t.Fatalf("failed job cannot retry: %d", w.Code)
	}
	if job = waitTransparency(t, s, asset.Asset.ID); job.Status != "failed" {
		t.Fatalf("retry result: %+v", job)
	}
}
