package iconlibrary

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDeleteRejectsRemovedAssets(t *testing.T) {
	s := &Service{}
	for _, id := range []string{"pack-folder", "pack-pdf", "legacy-smile", "default"} {
		t.Run(id, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/icon/api/assets/"+id, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("removed asset returned %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDeleteUploadWithMigratedSelections(t *testing.T) {
	var head string
	s, err := New(t.TempDir(), Hooks{
		Authorize: func(*http.Request) (string, error) { return "1", nil },
		ReadHead:  func(*http.Request) (string, error) { return head, nil },
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") == "https://example.test"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	initial, err := s.store.catalog()
	if err != nil {
		t.Fatal(err)
	}
	asset, err := s.store.commit("1", "0123456789abcdef0123456789abcdef", "uploaded icon", []byte("original"), []byte("preview"))
	if err != nil {
		t.Fatal(err)
	}
	requestDelete := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/icon/api/assets/"+asset.Asset.ID, nil)
		r.Header.Set("Authorization", "test-token")
		r.Header.Set("Origin", "https://example.test")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	head = `<meta id="openlist-icon-config" content="` + url.PathEscape(`{"version":1,"selections":{"folder":"pack-folder","pdf":"`+asset.Asset.ID+`"}}`) + `">`
	if w := requestDelete(); w.Code != http.StatusConflict {
		t.Fatalf("referenced upload not protected: %d, %s", w.Code, w.Body.String())
	}
	head = `<meta id="openlist-icon-config" content="` + url.PathEscape(`{"version":1,"selections":{"folder":"pack-folder","pdf":"default"}}`) + `">`
	w := requestDelete()
	if w.Code != http.StatusOK {
		t.Fatalf("unreferenced upload not deleted: %d, %s", w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Removed bool   `json:"removed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ID != asset.Asset.ID || response.Data.Kind != "uploaded" || !response.Data.Removed {
		t.Fatalf("unexpected delete response: %+v", response.Data)
	}
	catalog, err := s.store.catalog()
	if err != nil || len(catalog.Assets) != len(initial.Assets) {
		t.Fatalf("deleted upload still visible: %+v, %v", catalog, err)
	}
}

func TestGIFUploadOriginalServingAndLifecycle(t *testing.T) {
	// Reopening must retain native GIF previews and retire orphan GIF files using
	// the same crash-recovery rules as existing WebP previews.
	root := t.TempDir()
	hooks := Hooks{
		Authorize:   func(*http.Request) (string, error) { return "1", nil },
		ReadHead:    func(*http.Request) (string, error) { return "", nil },
		CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "https://example.test" },
	}
	s, err := New(root, hooks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			s.Close()
		}
	})
	initial, err := s.store.catalog()
	if err != nil {
		t.Fatal(err)
	}
	original := gifFixture(t, 2)
	const key = "0123456789abcdef0123456789abcdef"
	upload := func(name, key string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/icon/api/upload", bytes.NewReader(body))
		r.Header.Set("Authorization", "test-token")
		r.Header.Set("Origin", "https://example.test")
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
		r.Header.Set("X-Upload-Id", key)
		r.Header.Set("X-Icon-Name", url.PathEscape(name))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := upload("动画.GIF", key, original)
	if w.Code != http.StatusOK {
		t.Fatalf("GIF upload: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Data saved `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	asset := response.Data.Asset
	if asset.Source != "uploaded" || asset.Format != "gif" || asset.Name != "动画.GIF" || asset.Bytes != int64(len(original)) || asset.Width != 256 || asset.Height != 256 {
		t.Fatalf("unexpected GIF asset: %+v", asset)
	}
	for _, file := range []string{filepath.Join("originals", asset.ID+".original"), filepath.Join("previews", asset.ID+".gif")} {
		got, err := os.ReadFile(filepath.Join(root, file))
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("original GIF not preserved in %s: %v", file, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "previews", "upload-ffffffffffffffffffffffffffffffff.gif")
	if err := os.WriteFile(orphan, original, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = New(root, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan GIF survived recovery: %v", err)
	}
	w = upload("动画.GIF", key, original)
	if w.Code != http.StatusOK {
		t.Fatalf("GIF retry after recovery: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !response.Data.Reused || response.Data.Asset != asset {
		t.Fatalf("GIF retry changed asset: %+v, %v", response.Data, err)
	}
	for _, suffix := range []string{"", ".gif", ".webp"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/icon/assets/"+asset.ID+suffix, nil))
		if suffix == ".webp" {
			if w.Code != http.StatusNotFound {
				t.Fatalf("GIF served with false WebP suffix: %d", w.Code)
			}
			continue
		}
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/gif" || !bytes.Equal(w.Body.Bytes(), original) {
			t.Fatalf("GIF serving changed bytes/type: %d %s", w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := upload("mismatch.png", "1123456789abcdef0123456789abcdef", original); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("mismatched upload accepted: %d", w.Code)
	}
	if w := upload("corrupt.gif", "2123456789abcdef0123456789abcdef", original[:len(original)-1]); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("truncated upload accepted: %d", w.Code)
	}
	if w := upload("complex.gif", "3123456789abcdef0123456789abcdef", gifBlocks(1, 1, 2001)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-budget animation accepted: %d", w.Code)
	}
	w = upload("still.png", "4123456789abcdef0123456789abcdef", pngFixture(t, 2, 2))
	if w.Code != http.StatusOK {
		t.Fatalf("PNG upload regressed: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Data.Asset.Format != "png" {
		t.Fatalf("PNG format missing: %+v, %v", response.Data, err)
	}
	for _, suffix := range []string{"", ".webp", ".gif"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/icon/assets/"+response.Data.Asset.ID+suffix, nil))
		if suffix == ".gif" {
			if w.Code != http.StatusNotFound {
				t.Fatalf("PNG served as GIF: %d", w.Code)
			}
		} else if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/webp" || !bytes.HasPrefix(w.Body.Bytes(), []byte("RIFF")) {
			t.Fatalf("PNG serving regressed: %d", w.Code)
		}
	}
	catalog, err := s.store.catalog()
	if err != nil || len(catalog.Assets) != len(initial.Assets)+2 {
		t.Fatalf("rejected uploads modified catalog: %+v, %v", catalog, err)
	}
	if err := s.store.delete(asset.ID); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "trash", "previews", asset.ID+".gif"))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("GIF retirement changed bytes: %v", err)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/icon/assets/"+asset.ID, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted GIF still public: %d", w.Code)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(root, hooks)
	if err != nil {
		t.Fatalf("GIF tombstone recovery: %v", err)
	}
}
