package iconlibrary

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/HugoSmits86/nativewebp"
)

// These hooks are deliberately isolated test fixtures, never live admin acceptance.
func fixtureHooks() Hooks {
	return Hooks{Authorize: func(r *http.Request) (string, error) {
		if r.Header.Get("Authorization") != "isolated-test-admin" {
			return "", Fault{403, "secret token must not be echoed"}
		}
		return "7", nil
	}, ReadHead: func(*http.Request) (string, error) { return "", nil }, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "https://isolated.example" }}
}
func fixture(t *testing.T, h Hooks) (*Service, *httptest.Server, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "icons")
	s, err := New(root, h)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	t.Cleanup(func() { server.Close(); s.Close() })
	return s, server, root
}
func pngData(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func request(t *testing.T, s *Service, method, path string, data []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Authorization", "isolated-test-admin")
	r.Header.Set("Origin", "https://isolated.example")
	if method == "POST" {
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Content-Length", fmt.Sprint(len(data)))
		r.Header.Set("X-Upload-Id", strings.Repeat("a", 32))
		r.Header.Set("X-Icon-Name", "%E4%B8%AD%E6%96%87.png")
	}
	for k, v := range headers {
		if v == "!omit" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if status != 200 && w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable fault")
	}
}
func uploadResult(t *testing.T, w *httptest.ResponseRecorder) saved {
	t.Helper()
	requireStatus(t, w, 200)
	var v struct {
		Code int   `json:"code"`
		Data saved `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.Data
}
func chunk(kind string, data []byte) []byte {
	b := make([]byte, len(data)+12)
	binary.BigEndian.PutUint32(b, uint32(len(data)))
	copy(b[4:], kind)
	copy(b[8:], data)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[4:len(b)-4]))
	return b
}
func statusOf(err error) int {
	if err == nil {
		return 200
	}
	if f, ok := err.(Fault); ok {
		return f.Status
	}
	return 500
}
func theme(raw string) string {
	return `<meta id="openlist-icon-config" content="` + url.PathEscape(raw) + `">`
}

func TestConvertStaticPNGAndTransparency(t *testing.T) {
	for _, size := range [][2]int{{3, 1}, {100, 100}, {464, 232}, {232, 464}, {4096, 1}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data, err := ConvertPNG(pngData(t, size[0], size[1]))
			if err != nil {
				t.Fatal(err)
			}
			if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
				t.Fatal("not real WebP")
			}
			img, err := nativewebp.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
				t.Fatal(img.Bounds())
			}
			_, _, _, alpha := img.At(0, 0).RGBA()
			if alpha != 0 {
				t.Fatal("opaque corner")
			}
			_, _, _, alpha = img.At(127, 127).RGBA()
			if alpha == 0 {
				t.Fatal("empty center")
			}
			if size[0] == 3 {
				_, _, _, alpha = img.At(126, 127).RGBA()
				if alpha != 255*257 {
					t.Fatal("original pixel changed")
				}
				_, _, _, alpha = img.At(124, 127).RGBA()
				if alpha != 0 {
					t.Fatal("small image upscaled")
				}
			}
		})
	}
}
func TestPNGRejectsMalformedAndAPNG(t *testing.T) {
	good := pngData(t, 2, 2)
	badCRC := append([]byte{}, good...)
	badCRC[len(badCRC)-1] ^= 1
	oversize := append([]byte{}, good...)
	binary.BigEndian.PutUint32(oversize[16:20], 4097)
	binary.BigEndian.PutUint32(oversize[29:33], crc32.ChecksumIEEE(oversize[12:29]))
	pixels := append([]byte{}, good...)
	binary.BigEndian.PutUint32(pixels[16:20], 4096)
	binary.BigEndian.PutUint32(pixels[20:24], 4096)
	binary.BigEndian.PutUint32(pixels[29:33], crc32.ChecksumIEEE(pixels[12:29]))
	cases := []struct {
		name   string
		data   []byte
		status int
	}{{"empty", nil, 413}, {"jpeg", []byte{255, 216, 255, 0}, 415}, {"renamed SVG", []byte("<svg/>"), 415}, {"corrupt CRC", badCRC, 422}, {"truncated", good[:len(good)-1], 422}, {"trailing", append(append([]byte{}, good...), 0), 422}, {"oversize", oversize, 413}, {"pixels", pixels, 413}, {"huge", make([]byte, MaxInputBytes+1), 413}}
	for _, kind := range []string{"acTL", "fcTL", "fdAT"} {
		data := append([]byte{}, good[:33]...)
		data = append(data, chunk(kind, []byte{0, 0, 0, 1})...)
		data = append(data, good[33:]...)
		cases = append(cases, struct {
			name   string
			data   []byte
			status int
		}{kind, data, 415})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ConvertPNG(c.data)
			if statusOf(err) != c.status {
				t.Fatalf("%v want %d", err, c.status)
			}
		})
	}
	// Valid framing and CRC, but invalid compressed pixels must fail full decode.
	data := append([]byte{}, good[:33]...)
	data = append(data, chunk("IDAT", []byte("not zlib"))...)
	data = append(data, chunk("IEND", nil)...)
	if _, err := ConvertPNG(data); statusOf(err) != 422 {
		t.Fatalf("%v", err)
	}
}
func TestHeadReferencesFailClosed(t *testing.T) {
	upload := "upload-" + strings.Repeat("1", 32)
	refs, err := HeadReferences(theme(`{"version":1,"selections":{"unknown-target":"` + upload + `","folder":"pack-folder","x":"default"}}`))
	if err != nil || !refs[upload] || !refs["pack-folder"] || !refs["default"] {
		t.Fatalf("%v %v", refs, err)
	}
	legacy := `<!-- OPENLIST-FOLDER-ICON-START --><script>window.OPENLIST_FOLDER_ICON_STYLE="smile";dangerous()</script><!-- OPENLIST-FOLDER-ICON-END -->`
	refs, err = HeadReferences(theme(`{"version":1,"selections":{}}`) + legacy)
	if err != nil || !refs["legacy-smile"] {
		t.Fatalf("%v %v", refs, err)
	}
	bad := []string{theme(`{"version":1,"selections":{},"selections":{}}`), theme(`{"version":1,"selections":{"x":"pack-no"}}`), theme(`{"version":1,"selections":{"x":false}}`), theme(`{"version":2,"selections":{}}`), theme(`{"version":true,"selections":{}}`), theme(`{"version":1.0,"selections":{}}`), theme(`{"version":1,"selections":[]}`), theme(`{"version":1,"selections":{}} {}`), `<meta id="openlist-icon-config" content="%ZZ">`, `<meta id="openlist-icon-config" content="%FF">`, `<meta id="openlist-icon-config">`, theme(`{"version":1,"selections":{}}`) + theme(`{"version":1,"selections":{}}`), `<meta id="openlist-icon-config" content="x" content="y">`, `<meta id="openlist-icon-config" id="other" content="x">`, `<!-- OPENLIST-ICON-THEME-START -->missing meta`, `<script>OPENLIST_FOLDER_ICON_STYLE="smile"</script>`, `<!-- OPENLIST-FOLDER-ICON-START -->invalid<!-- OPENLIST-FOLDER-ICON-END -->`, strings.Replace(legacy, "smile", "unknown", 1), strings.Repeat("x", 1024*1024+1)}
	for i, head := range bad {
		if _, err := HeadReferences(head); statusOf(err) != 502 {
			t.Errorf("case %d unexpectedly valid: %v", i, err)
		}
	}
	for _, head := range []string{"", `<style>.safe{color:red}</style>`} {
		if _, err := HeadReferences(head); err != nil {
			t.Fatal(err)
		}
	}
}
func TestFilenameValidation(t *testing.T) {
	for _, name := range []string{"a.png", "%E4%B8%AD%E6%96%87.PNG", url.PathEscape(strings.Repeat("😀", 78) + ".png")} {
		if _, err := decodeName(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"", "a.jpg", "a%2Fz.png", "a%5Cz.png", "%00.png", "%E2%80%AE.png", "%FF.png", "x%ZZ.png", "a png", url.PathEscape(strings.Repeat("😀", 79) + ".png")} {
		if _, err := decodeName(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}
func TestHTTPRealSocketRoundtripAndRestart(t *testing.T) {
	s, server, root := fixture(t, fixtureHooks())
	data := pngData(t, 17, 9)
	r, err := http.NewRequest("POST", server.URL+"/icon/api/upload", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "isolated-test-admin")
	r.Header.Set("Origin", "https://isolated.example")
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-Upload-Id", strings.Repeat("a", 32))
	r.Header.Set("X-Icon-Name", "socket.png")
	resp, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var value struct {
		Data saved `json:"data"`
	}
	if err = json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	id := value.Data.Asset.ID
	want := "upload-" + fmt.Sprintf("%x", sha256.Sum256([]byte("7:"+strings.Repeat("a", 32))))[:32]
	if id != want {
		t.Fatal(id, want)
	}
	resp, err = server.Client().Get(server.URL + "/icon/assets/" + id + ".webp")
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/webp" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatal(resp.StatusCode, resp.Header)
	}
	if _, err = nativewebp.Decode(bytes.NewReader(preview)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "originals"), filepath.Join(root, "previews"), filepath.Join(root, "trash")} {
		i, err := os.Stat(path)
		if err != nil || i.Mode().Perm() != 0700 {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{filepath.Join(root, "catalog.db"), filepath.Join(root, "originals", id+".original"), filepath.Join(root, "previews", id+".webp")} {
		i, err := os.Stat(path)
		if err != nil || i.Mode().Perm() != 0600 {
			t.Fatal(path, err)
		}
	}
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/"+id, nil, nil), 200)
	requireStatus(t, request(t, s, "GET", "/icon/assets/"+id+".webp", nil, nil), 404)
	server.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(root, fixtureHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	requireStatus(t, request(t, restarted, "POST", "/icon/api/upload", data, map[string]string{"X-Icon-Name": "socket.png"}), 410)
	requireStatus(t, request(t, restarted, "DELETE", "/icon/api/assets/"+id, nil, nil), 200)
	requireStatus(t, request(t, restarted, "GET", "/icon/assets/"+id+".webp", nil, nil), 404)
	original, err := os.ReadFile(filepath.Join(root, "trash", "originals", id+".original"))
	if err != nil || !bytes.Equal(original, data) {
		t.Fatal("original not recoverable", err)
	}
	var deleted string
	if err = restarted.store.db.QueryRow("SELECT deleted_at FROM assets WHERE id=?", id).Scan(&deleted); err != nil || deleted == "" {
		t.Fatal("no durable tombstone", err)
	}
}
func TestIdempotencyContentNameAndIdentity(t *testing.T) {
	s, _, _ := fixture(t, fixtureHooks())
	data := pngData(t, 10, 5)
	first := uploadResult(t, request(t, s, "POST", "/icon/api/upload", data, nil))
	if first.Reused {
		t.Fatal("first reused")
	}
	second := uploadResult(t, request(t, s, "POST", "/icon/api/upload", data, nil))
	if !second.Reused || second.Asset.ID != first.Asset.ID {
		t.Fatal(second)
	}
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", data, map[string]string{"X-Icon-Name": "different.png"}), 409)
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", pngData(t, 11, 5), nil), 409)
	another := uploadResult(t, request(t, s, "POST", "/icon/api/upload", data, map[string]string{"X-Upload-Id": strings.Repeat("b", 32)}))
	if another.Asset.ID == first.Asset.ID {
		t.Fatal("different keys deduplicated")
	}
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/"+first.Asset.ID, nil, nil), 200)
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", data, nil), 410)
}
func TestHTTPDenialsAndStrictNamespace(t *testing.T) {
	s, _, _ := fixture(t, fixtureHooks())
	data := pngData(t, 1, 1)
	for _, c := range []struct {
		header, value string
		status        int
	}{{"Authorization", "!omit", 401}, {"Authorization", "", 401}, {"Authorization", "guest", 403}, {"Origin", "!omit", 403}, {"Origin", "null", 403}, {"Origin", "https://isolated.example/", 403}, {"Origin", "http://isolated.example", 403}, {"Origin", "https://foreign.example", 403}, {"X-Upload-Id", strings.Repeat("A", 32), 400}, {"X-Icon-Name", "a.gif", 415}, {"Content-Type", "image/png", 415}, {"Content-Length", "!omit", 411}, {"Content-Length", "0", 400}, {"Content-Length", "00000000000", 400}, {"Content-Encoding", "gzip", 415}, {"Transfer-Encoding", "chunked", 400}, {"Expect", "other", 417}} {
		t.Run(c.header+c.value, func(t *testing.T) {
			w := request(t, s, "POST", "/icon/api/upload", data, map[string]string{c.header: c.value})
			requireStatus(t, w, c.status)
			if strings.Contains(w.Body.String(), "secret") {
				t.Fatal("leaked hook text")
			}
		})
	}
	for _, path := range []string{"/icon", "/icon/", "/icon/catalog.db", "/icon/originals/a", "/icon/trash/a", "/icon/api/health?x=1", "/icon/api/health?", "/icon/%61pi/health", "/icon/api/../api/health", "/icon//api/health", "/icon/assets/upload-" + strings.Repeat("A", 32) + ".webp"} {
		requireStatus(t, request(t, s, "GET", path, nil, nil), 404)
	}
	requireStatus(t, request(t, s, "HEAD", "/icon/api/health", nil, nil), 405)
	for _, key := range []string{"Authorization", "Origin", "Content-Length", "Content-Type", "X-Upload-Id", "X-Icon-Name"} {
		r := httptest.NewRequest("POST", "/icon/api/upload", bytes.NewReader(data))
		r.Header.Set(key, "a")
		r.Header.Add(key, "b")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		requireStatus(t, w, 400)
	}
	r := httptest.NewRequest("POST", "/icon/api/upload", bytes.NewReader(data))
	r.TransferEncoding = []string{"chunked"}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	requireStatus(t, w, 400)
	requireStatus(t, request(t, s, "GET", "/icon/api/catalog", nil, map[string]string{"Authorization": "!omit", "Origin": "!omit"}), 200)
}
func TestDeleteReadFailuresAndInterveningUse(t *testing.T) {
	var calls int
	h := fixtureHooks()
	h.ReadHead = func(*http.Request) (string, error) {
		calls++
		if calls == 2 {
			return theme(`{"version":1,"selections":{"x":"pack-folder"}}`), nil
		}
		return "", nil
	}
	s, _, _ := fixture(t, h)
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/pack-folder", nil, nil), 409)
	c, err := s.store.catalog()
	if err != nil || len(c.HiddenBuiltins) != 0 {
		t.Fatal(c, err)
	}
	s.hooks.ReadHead = func(*http.Request) (string, error) { return "", Fault{504, "secret-network"} }
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/pack-folder", nil, nil), 504)
	s.hooks.ReadHead = func(*http.Request) (string, error) { return `<meta id="openlist-icon-config" content="%ZZ">`, nil }
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/pack-folder", nil, nil), 502)
	s.hooks = fixtureHooks()
	authCalls := 0
	s.hooks.Authorize = func(*http.Request) (string, error) {
		authCalls++
		if authCalls > 1 {
			return "", Fault{403, "revoked"}
		}
		return "7", nil
	}
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/pack-folder", nil, nil), 403)
	c, err = s.store.catalog()
	if err != nil || len(c.HiddenBuiltins) != 0 {
		t.Fatal(c, err)
	}
}
func TestAllBuiltinTombstonesPersist(t *testing.T) {
	s, server, root := fixture(t, fixtureHooks())
	if len(builtinIDs) != 21 {
		t.Fatal(len(builtinIDs))
	}
	want := []string{}
	for id := range builtinIDs {
		requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/"+id, nil, nil), 200)
		want = append(want, id)
	}
	sort.Strings(want)
	server.Close()
	s.Close()
	s2, err := New(root, fixtureHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	c, err := s2.store.catalog()
	if err != nil || strings.Join(c.HiddenBuiltins, ",") != strings.Join(want, ",") {
		t.Fatal(c, err)
	}
}
func TestConcurrentSameKeyExactlyOneCommit(t *testing.T) {
	s, _, _ := fixture(t, fixtureHooks())
	data := pngData(t, 10, 10)
	var wg sync.WaitGroup
	var created atomic.Int64
	var succeeded atomic.Int64
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(t, s, "POST", "/icon/api/upload", data, nil)
			if w.Code == 429 {
				return
			}
			if w.Code != 200 {
				t.Errorf("%d %s", w.Code, w.Body.String())
				return
			}
			v := uploadResult(t, w)
			succeeded.Add(1)
			if !v.Reused {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 || succeeded.Load() < 1 {
		t.Fatal(created.Load(), succeeded.Load())
	}
	c, err := s.store.catalog()
	if err != nil || len(c.Assets) != 1 {
		t.Fatal(c, err)
	}
}
func TestInterruptedDeletionRecovery(t *testing.T) {
	s, server, root := fixture(t, fixtureHooks())
	asset := uploadResult(t, request(t, s, "POST", "/icon/api/upload", pngData(t, 3, 3), nil)).Asset
	// Simulate process interruption after durable tombstone but before either move.
	if _, err := s.store.db.Exec("UPDATE assets SET deleted_at='2026-01-01T00:00:00Z' WHERE id=?", asset.ID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(t, s, "GET", "/icon/assets/"+asset.ID+".webp", nil, nil), 404)
	server.Close()
	s.Close()
	s2, err := New(root, fixtureHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err = os.Stat(filepath.Join(root, "trash", "previews", asset.ID+".webp")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "previews", asset.ID+".webp")); !os.IsNotExist(err) {
		t.Fatal("still active", err)
	}
}
func TestPartialRetirementFailureStaysNonpublic(t *testing.T) {
	s, server, root := fixture(t, fixtureHooks())
	asset := uploadResult(t, request(t, s, "POST", "/icon/api/upload", pngData(t, 3, 3), nil)).Asset
	dst := filepath.Join(root, "trash", "previews", asset.ID+".webp")
	if err := os.WriteFile(dst, []byte("collision"), 0600); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/"+asset.ID, nil, nil), 503)
	requireStatus(t, request(t, s, "GET", "/icon/assets/"+asset.ID+".webp", nil, nil), 404)
	server.Close()
	s.Close()
	if err := os.Remove(dst); err != nil {
		t.Fatal(err)
	}
	s2, err := New(root, fixtureHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	requireStatus(t, request(t, s2, "DELETE", "/icon/api/assets/"+asset.ID, nil, nil), 200)
}
func TestStorageSymlinksAndUncertainSchemaFailClosed(t *testing.T) {
	for _, dir := range []string{"root", "originals", "previews", ".staging", "trash", "trash/originals", "trash/previews"} {
		t.Run(dir, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "icons")
			target := filepath.Join(base, "target")
			os.MkdirAll(target, 0700)
			path := root
			if dir != "root" {
				os.MkdirAll(root, 0700)
				path = filepath.Join(root, dir)
				os.MkdirAll(filepath.Dir(path), 0700)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if s, err := New(root, fixtureHooks()); err == nil {
				s.Close()
				t.Fatal("symlink accepted")
			}
			entries, _ := os.ReadDir(target)
			if len(entries) != 0 {
				t.Fatal("wrote through symlink")
			}
		})
	}
	root := filepath.Join(t.TempDir(), "icons")
	os.MkdirAll(filepath.Join(root, "originals"), 0700)
	orphan := filepath.Join(root, "originals", "upload-"+strings.Repeat("a", 32)+".original")
	os.WriteFile(orphan, []byte("preserve legacy data"), 0600)
	if s, err := New(root, fixtureHooks()); err == nil {
		s.Close()
		t.Fatal("data without database accepted")
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("legacy orphan removed")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE unrelated(id TEXT)")
	db.Close()
	os.Chmod(filepath.Join(root, "catalog.db"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := New(root, fixtureHooks()); err == nil {
		s.Close()
		t.Fatal("unknown schema accepted")
	}
	if _, err = os.Stat(orphan); err != nil {
		t.Fatal("unknown-schema orphan removed")
	}
}
func TestNativeRootLockAndRegisteredDamage(t *testing.T) {
	s, _, root := fixture(t, fixtureHooks())
	if s2, err := New(root, fixtureHooks()); err == nil {
		s2.Close()
		t.Fatal("concurrent root owner accepted")
	}
	data := pngData(t, 2, 2)
	asset := uploadResult(t, request(t, s, "POST", "/icon/api/upload", data, nil)).Asset
	preview := filepath.Join(root, "previews", asset.ID+".webp")
	os.Remove(preview)
	os.Symlink(filepath.Join(root, "catalog.db"), preview)
	requireStatus(t, request(t, s, "GET", "/icon/assets/"+asset.ID+".webp", nil, nil), 404)
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", data, nil), 503)
}
func TestRetainedTrashQuota(t *testing.T) {
	s, _, _ := fixture(t, fixtureHooks())
	data := pngData(t, 1, 1)
	asset := uploadResult(t, request(t, s, "POST", "/icon/api/upload", data, nil)).Asset
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/"+asset.ID, nil, nil), 200)
	// Isolated DB capacity fixture: commit must count deleted rows, not only active ones.
	if _, err := s.store.db.Exec("UPDATE assets SET original_bytes=? WHERE id=?", MaxStoredBytes, asset.ID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", data, map[string]string{"X-Upload-Id": strings.Repeat("b", 32)}), 507)
}
