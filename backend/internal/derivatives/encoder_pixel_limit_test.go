//go:build linux

package derivatives

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Stream rows into a compressed fixture: do not allocate a 48MP raster in Go.
func TestEncoderIntegrationTripledPixelLimit(t *testing.T) {
	if _, err := os.Stat(encoderVipsPath); err != nil {
		t.Skip("runtime libvips required")
	}
	var pixels bytes.Buffer
	compressed := zlib.NewWriter(&pixels)
	row := make([]byte, 1+8000*3)
	for y := 0; y < 6000; y++ {
		if _, err := compressed.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:4], 8000)
	binary.BigEndian.PutUint32(header[4:8], 6000)
	header[8], header[9] = 8, 2
	data := []byte("\x89PNG\r\n\x1a\n")
	data = append(data, encoderTestPNGChunk("IHDR", header)...)
	data = append(data, encoderTestPNGChunk("IDAT", pixels.Bytes())...)
	data = append(data, encoderTestPNGChunk("IEND", nil)...)
	dir := t.TempDir()
	input := filepath.Join(dir, "source.png")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := InspectImage(input)
	encoderTestCode(t, err, "")
	if info.Width != 8000 || info.Height != 6000 {
		t.Fatal(info)
	}
	for _, profile := range []string{"thumb", "reader"} {
		result, err := Encode(context.Background(), input, filepath.Join(dir, profile+".webp"), profile)
		encoderTestCode(t, err, "")
		expected := 480
		if profile == "reader" {
			expected = 1600
		}
		if result.Width != expected || result.Height != expected*3/4 {
			t.Fatal(result)
		}
	}
	// One row-width beyond the new bound is rejected before any raster decoding.
	binary.BigEndian.PutUint32(header[0:4], 8001)
	oversized := append([]byte("\x89PNG\r\n\x1a\n"), encoderTestPNGChunk("IHDR", header)...)
	oversized = append(oversized, encoderTestPNGChunk("IEND", nil)...)
	_, err = encoderInspect(context.Background(), bytes.NewReader(oversized), int64(len(oversized)))
	encoderTestCode(t, err, "skipped_limit")
}
