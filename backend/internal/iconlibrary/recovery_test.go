package iconlibrary

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HugoSmits86/nativewebp"
)

func TestEXIFOrientationAndMetadataStripping(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(2, 0, color.NRGBA{B: 255, A: 255})
	var raw bytes.Buffer
	png.Encode(&raw, img)
	data := raw.Bytes()
	exif := make([]byte, 26)
	copy(exif, "II")
	binary.LittleEndian.PutUint16(exif[2:], 42)
	binary.LittleEndian.PutUint32(exif[4:], 8)
	binary.LittleEndian.PutUint16(exif[8:], 1)
	binary.LittleEndian.PutUint16(exif[10:], 274)
	binary.LittleEndian.PutUint16(exif[12:], 3)
	binary.LittleEndian.PutUint32(exif[14:], 1)
	binary.LittleEndian.PutUint16(exif[18:], 6)
	oriented := append([]byte{}, data[:33]...)
	oriented = append(oriented, chunk("eXIf", exif)...)
	oriented = append(oriented, data[33:]...)
	preview, err := ConvertPNG(oriented)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := nativewebp.Decode(bytes.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, a := decoded.At(127, 126).RGBA()
	if r != 65535 || a != 65535 {
		t.Fatal("orientation not applied")
	}
	_, _, b, a := decoded.At(127, 128).RGBA()
	if b != 65535 || a != 65535 {
		t.Fatal("orientation pixels lost")
	}
	if bytes.Contains(preview, []byte("EXIF")) {
		t.Fatal("metadata leaked")
	}
	for _, v := range [][]byte{nil, []byte("II"), append([]byte{}, exif[:9]...), []byte("ZZ000000")} {
		if exifOrientation(v) != 1 {
			t.Fatal("malformed EXIF used")
		}
	}
}
func TestPendingDeleteRejectsRetryAndSecondDelete(t *testing.T) {
	s, _, _ := fixture(t, fixtureHooks())
	data := pngData(t, 2, 2)
	asset := uploadResult(t, request(t, s, "POST", "/icon/api/upload", data, nil)).Asset
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	s.hooks.ReadHead = func(*http.Request) (string, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
		}
		return "", nil
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(t, s, "DELETE", "/icon/api/assets/"+asset.ID, nil, nil) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not enter hook")
	}
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", data, nil), 409)
	requireStatus(t, request(t, s, "DELETE", "/icon/api/assets/pack-folder", nil, nil), 429)
	close(release)
	requireStatus(t, <-done, 200)
}
func TestUploadRevocationAndIdentityChange(t *testing.T) {
	for _, change := range []bool{false, true} {
		h := fixtureHooks()
		calls := 0
		h.Authorize = func(*http.Request) (string, error) {
			calls++
			if calls == 3 {
				if change {
					return "8", nil
				}
				return "", Fault{403, "revoked"}
			}
			return "7", nil
		}
		s, _, root := fixture(t, h)
		requireStatus(t, request(t, s, "POST", "/icon/api/upload", pngData(t, 1, 1), nil), 403)
		c, err := s.store.catalog()
		if err != nil || len(c.Assets) != 0 {
			t.Fatal(c, err)
		}
		entries, err := os.ReadDir(filepath.Join(root, "previews"))
		if err != nil || len(entries) != 0 {
			t.Fatal(entries, err)
		}
	}
}
func TestOldPythonSchemaMigrationAndCrashOrphans(t *testing.T) {
	s, server, root := fixture(t, fixtureHooks())
	data := pngData(t, 4, 4)
	asset := uploadResult(t, request(t, s, "POST", "/icon/api/upload", data, nil)).Asset
	server.Close()
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("ALTER TABLE assets DROP COLUMN deleted_at"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	// A recognized old Python catalog permits narrowly named crash remnant cleanup.
	orphan := filepath.Join(root, "previews", "upload-"+strings.Repeat("f", 32)+".webp")
	os.WriteFile(orphan, []byte("orphan"), 0600)
	stage := filepath.Join(root, ".staging", strings.Repeat("f", 32)+".tmp")
	os.WriteFile(stage, []byte("stage"), 0600)
	s2, err := New(root, fixtureHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	c, err := s2.store.catalog()
	if err != nil || len(c.Assets) != 1 || c.Assets[0].ID != asset.ID {
		t.Fatal(c, err)
	}
	v := uploadResult(t, request(t, s2, "POST", "/icon/api/upload", data, nil))
	if !v.Reused {
		t.Fatal("old row not reused")
	}
	for _, p := range []string{orphan, stage} {
		if _, err = os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("crash remnant retained", p, err)
		}
	}
}
func TestTwoHundredRetainedItemQuota(t *testing.T) {
	s, _, _ := fixture(t, fixtureHooks())
	tx, err := s.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxAssets; i++ {
		key := fmt.Sprintf("%032x", i)
		_, err = tx.Exec(`INSERT INTO assets(id,owner,request_id,content_sha,name,width,height,bytes,original_bytes,created_at,deleted_at) VALUES(?,'7',?,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','quota.png',256,256,1,1,'2026-01-01','2026-01-01')`, uploadID("7", key), key)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", pngData(t, 1, 1), nil), 507)
}
func TestNilHooksAndErrorRedaction(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "icons"), Hooks{}); err == nil {
		t.Fatal("nil hooks accepted")
	}
	for _, err := range []error{Fault{401, "secret token"}, &Fault{403, "secret filename"}, fmt.Errorf("secret filesystem path")} {
		w := httptest.NewRecorder()
		replyError(w, err)
		var data map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("secret echoed")
		}
	}
}
func TestFileInstallFailureLeavesNoPublicRow(t *testing.T) {
	s, _, root := fixture(t, fixtureHooks())
	id := uploadID("7", strings.Repeat("a", 32))
	blocked := filepath.Join(root, "previews", id+".webp")
	os.WriteFile(blocked, []byte("preexisting"), 0600)
	requireStatus(t, request(t, s, "POST", "/icon/api/upload", pngData(t, 2, 2), nil), 503)
	c, err := s.store.catalog()
	if err != nil || len(c.Assets) != 0 {
		t.Fatal(c, err)
	}
	if _, err = os.Stat(filepath.Join(root, "originals", id+".original")); !os.IsNotExist(err) {
		t.Fatal("partial install not cleaned", err)
	}
	if _, err = os.Stat(blocked); err != nil {
		t.Fatal("preexisting collision removed", err)
	}
	stages, _ := os.ReadDir(filepath.Join(root, ".staging"))
	if len(stages) != 0 {
		t.Fatal("staging leak")
	}
}
func TestAlteredCatalogSchemaPreservesOrphans(t *testing.T) {
	s, server, root := fixture(t, fixtureHooks())
	server.Close()
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("ALTER TABLE assets ADD COLUMN unknown TEXT"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	orphan := filepath.Join(root, "originals", "upload-"+strings.Repeat("e", 32)+".original")
	os.WriteFile(orphan, []byte("preserve"), 0600)
	if s2, err := New(root, fixtureHooks()); err == nil {
		s2.Close()
		t.Fatal("unknown schema accepted")
	}
	if _, err = os.Stat(orphan); err != nil {
		t.Fatal("destructive recovery on unknown schema", err)
	}
}
