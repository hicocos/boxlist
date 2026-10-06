package iconlibrary

import (
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func FuzzHeadReferences(f *testing.F) {
	for _, head := range []string{"", theme(`{"version":1,"selections":{"folder":"pack-folder"}}`), `<meta id="openlist-icon-config" content="%ZZ">`, `<!-- OPENLIST-FOLDER-ICON-START -->OPENLIST_FOLDER_ICON_STYLE='smile'<!-- OPENLIST-FOLDER-ICON-END -->`} {
		f.Add(head)
	}
	f.Fuzz(func(t *testing.T, head string) { _, _ = HeadReferences(head) })
}
func FuzzPNGContainer(f *testing.F) {
	f.Add([]byte("not png"))
	f.Add([]byte("\x89PNG\r\n\x1a\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxInputBytes {
			return
		}
		_, _ = ConvertPNG(data)
	})
}
func TestBodyLengthAndResourceLimits(t *testing.T) {
	s, _, _ := fixture(t, fixtureHooks())
	data := pngData(t, 1, 1)
	for _, c := range []struct {
		size   int64
		header string
		body   []byte
		status int
	}{{0, "0", nil, 413}, {MaxInputBytes + 1, fmt.Sprint(MaxInputBytes + 1), data, 413}, {int64(len(data)) + 1, fmt.Sprint(len(data) + 1), data, 400}, {int64(len(data)), fmt.Sprint(len(data)), make([]byte, MaxInputBytes+1), 413}} {
		r := httptest.NewRequest("POST", "/icon/api/upload", bytes.NewReader(c.body))
		r.ContentLength = c.size
		r.Header.Set("Content-Length", c.header)
		r.Header.Set("Authorization", "isolated-test-admin")
		r.Header.Set("Origin", "https://isolated.example")
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Upload-Id", strings.Repeat("a", 32))
		r.Header.Set("X-Icon-Name", "a.png")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		requireStatus(t, w, c.status)
	}
	r := httptest.NewRequest("DELETE", "/icon/api/assets/pack-folder", io.NopCloser(strings.NewReader("unknown body")))
	r.ContentLength = -1
	r.Header.Set("Authorization", "isolated-test-admin")
	r.Header.Set("Origin", "https://isolated.example")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	requireStatus(t, w, 400)
}
