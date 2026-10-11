package derivatives

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"strings"
	"testing"
)

func encoderTestCode(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if !encoderIsCode(err, code) {
		t.Fatalf("error = %v; want %s", err, code)
	}
}

func encoderTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: uint8(255 * x / max(1, width-1))})
		}
	}
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encoderTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, width, height)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encoderTestPNGChunk(kind string, data []byte) []byte {
	b := make([]byte, 12+len(data))
	binary.BigEndian.PutUint32(b[:4], uint32(len(data)))
	copy(b[4:8], kind)
	copy(b[8:], data)
	binary.BigEndian.PutUint32(b[8+len(data):], crc32.ChecksumIEEE(b[4:8+len(data)]))
	return b
}

func encoderTestRIFFChunk(kind string, data []byte) []byte {
	b := make([]byte, 8+len(data)+len(data)%2)
	copy(b[:4], kind)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(data)))
	copy(b[8:], data)
	return b
}

func encoderTestRIFF(chunks ...[]byte) []byte {
	payload := bytes.Join(chunks, nil)
	b := make([]byte, 12+len(payload))
	copy(b[:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], uint32(4+len(payload)))
	copy(b[8:12], "WEBP")
	copy(b[12:], payload)
	return b
}

// Minimal headers only, intentionally NOT an encoded image. Inspect must not
// decode these pixels; the integration suite proves corrupt pixels cannot pass
// Encode's independent, limited output decoder.
func encoderTestVP8L(width, height int) []byte {
	b := make([]byte, 5)
	b[0] = 0x2f
	binary.LittleEndian.PutUint32(b[1:], uint32(width-1)|uint32(height-1)<<14)
	return encoderTestRIFFChunk("VP8L", b)
}
func encoderTestVP8X(width, height int, flags byte) []byte {
	b := make([]byte, 10)
	b[0] = flags
	for i := 0; i < 3; i++ {
		b[4+i] = byte((width - 1) >> (8 * i))
		b[7+i] = byte((height - 1) >> (8 * i))
	}
	return encoderTestRIFFChunk("VP8X", b)
}

func encoderTestEXIF(orientation int, order binary.ByteOrder) []byte {
	b := make([]byte, 26)
	if order == binary.LittleEndian {
		copy(b[:2], "II")
	} else {
		copy(b[:2], "MM")
	}
	order.PutUint16(b[2:4], 42)
	order.PutUint32(b[4:8], 8)
	order.PutUint16(b[8:10], 1)
	order.PutUint16(b[10:12], 0x0112)
	order.PutUint16(b[12:14], 3)
	order.PutUint32(b[14:18], 1)
	order.PutUint16(b[18:20], uint16(orientation))
	return b
}

func encoderTestAddJPEGEXIF(data, exif []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), exif...)
	segment := make([]byte, 4+len(payload))
	segment[0], segment[1] = 0xff, 0xe1
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	copy(segment[4:], payload)
	out := append([]byte{}, data[:2]...)
	out = append(out, segment...)
	return append(out, data[2:]...)
}

func TestEncoderInspectSupportedHeaders(t *testing.T) {
	for _, tc := range []struct {
		format string
		data   []byte
	}{
		{"png", encoderTestPNG(t, 24, 17)},
		{"jpeg", encoderTestJPEG(t, 24, 17)},
		{"webp", encoderTestRIFF(encoderTestVP8L(24, 17))},
		{"webp", encoderTestRIFF(encoderTestVP8X(24, 17, 0), encoderTestVP8L(24, 17))},
	} {
		t.Run(tc.format, func(t *testing.T) {
			info, err := encoderInspect(context.Background(), bytes.NewReader(tc.data), int64(len(tc.data)))
			encoderTestCode(t, err, "")
			if info.Width != 24 || info.Height != 17 || info.Format != tc.format || info.Animated {
				t.Fatalf("info = %+v", info)
			}
		})
	}
}

func TestEncoderRejectUnsupportedAndMalformed(t *testing.T) {
	pngImage := encoderTestPNG(t, 20, 30)
	insert := func(data []byte, at int, chunk []byte) []byte {
		out := append([]byte{}, data[:at]...)
		out = append(out, chunk...)
		return append(out, data[at:]...)
	}
	cases := []struct {
		name     string
		data     []byte
		code     string
		animated bool
	}{
		{"empty", nil, "invalid_image", false},
		{"html", []byte("<html>not an image</html>"), "unsupported_format", false},
		{"gif87", []byte("GIF87a"), "unsupported_gif", false},
		{"gif89", []byte("GIF89a"), "unsupported_gif", false},
		{"tiff-le", []byte("II\x2a\x00bad"), "unsupported_tiff", false},
		{"tiff-be", []byte("MM\x00\x2abad"), "unsupported_tiff", false},
		{"bigtiff", []byte("II\x2b\x00bad"), "unsupported_tiff", false},
		{"svg", []byte("\xef\xbb\xbf \n<svg xmlns='http://www.w3.org/2000/svg'/>"), "unsupported_svg", false},
		{"xml", []byte("<?xml version='1.0'?><svg/>"), "unsupported_svg", false},
		{"apng-before-data", insert(pngImage, 33, encoderTestPNGChunk("acTL", make([]byte, 8))), "unsupported_animation", true},
		{"apng-after-data", insert(pngImage, len(pngImage)-12, encoderTestPNGChunk("fcTL", make([]byte, 26))), "unsupported_animation", true},
		{"webp-animation-bit", encoderTestRIFF(encoderTestVP8X(20, 30, 2), encoderTestVP8L(20, 30)), "unsupported_animation", true},
		{"webp-late-anim", encoderTestRIFF(encoderTestVP8L(20, 30), encoderTestRIFFChunk("ANIM", make([]byte, 6))), "unsupported_animation", true},
		{"webp-hidden-anmf", encoderTestRIFF(encoderTestVP8L(20, 30), encoderTestRIFFChunk("ANMF", nil)), "unsupported_animation", true},
		{"webp-outer-inner-mismatch", encoderTestRIFF(encoderTestVP8X(20, 30, 0), encoderTestVP8L(30, 20)), "invalid_image", false},
		{"webp-oversized-inner", encoderTestRIFF(encoderTestVP8X(20, 30, 0), encoderTestVP8L(10000, 10000)), "skipped_limit", false},
		{"webp-missing-pixels", encoderTestRIFF(encoderTestVP8X(20, 30, 0)), "invalid_image", false},
		{"webp-duplicate-frame", encoderTestRIFF(encoderTestVP8L(20, 30), encoderTestVP8L(20, 30)), "invalid_image", false},
		{"png-truncated", pngImage[:len(pngImage)-1], "invalid_image", false},
		{"png-trailing-data", append(append([]byte{}, pngImage...), 0), "invalid_image", false},
		{"jpeg-truncated", []byte("\xff\xd8\xff\xe1\x00\x10short"), "invalid_image", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := encoderInspect(context.Background(), bytes.NewReader(tc.data), int64(len(tc.data)))
			encoderTestCode(t, err, tc.code)
			if info.Animated != tc.animated {
				t.Fatalf("animated = %v", info.Animated)
			}
		})
	}
	for _, mutate := range []func([]byte){
		func(b []byte) { binary.BigEndian.PutUint32(b[8:12], math.MaxUint32) },
		func(b []byte) { b[29] ^= 1 }, // IHDR CRC
	} {
		b := append([]byte{}, pngImage...)
		mutate(b)
		_, err := encoderInspect(context.Background(), bytes.NewReader(b), int64(len(b)))
		encoderTestCode(t, err, "invalid_image")
	}
}

func TestEncoderOrientation(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for orientation := 1; orientation <= 8; orientation++ {
			exif := encoderTestEXIF(orientation, order)
			jpegImage := encoderTestAddJPEGEXIF(encoderTestJPEG(t, 40, 30), exif)
			pngImage := encoderTestPNG(t, 40, 30)
			pngImage = append(append(append([]byte{}, pngImage[:33]...), encoderTestPNGChunk("eXIf", exif)...), pngImage[33:]...)
			webpImage := encoderTestRIFF(encoderTestVP8X(40, 30, 8), encoderTestVP8L(40, 30), encoderTestRIFFChunk("EXIF", exif))
			for _, data := range [][]byte{jpegImage, pngImage, webpImage} {
				m, err := encoderInspect(context.Background(), bytes.NewReader(data), int64(len(data)))
				encoderTestCode(t, err, "")
				w, h := 40, 30
				if orientation >= 5 {
					w, h = h, w
				}
				if m.Width != w || m.Height != h || m.orientation != orientation {
					t.Fatalf("orientation %d: %+v", orientation, m)
				}
			}
		}
	}
	for _, data := range [][]byte{nil, []byte("Exif\x00\x00"), encoderTestEXIF(0, binary.LittleEndian), encoderTestEXIF(9, binary.BigEndian)} {
		_, err := encoderEXIFOrientation(data)
		encoderTestCode(t, err, "invalid_image")
	}
	exif := encoderTestEXIF(6, binary.LittleEndian)
	binary.LittleEndian.PutUint32(exif[4:8], math.MaxUint32)
	_, err := encoderEXIFOrientation(exif)
	encoderTestCode(t, err, "invalid_image")
}

type encoderCountingReaderAt struct {
	r            io.ReaderAt
	reads, bytes int
}

func (r *encoderCountingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.reads++
	r.bytes += len(p)
	return r.r.ReadAt(p, off)
}

func TestEncoderInspectionIsBounded(t *testing.T) {
	pngImage := encoderTestPNG(t, 10, 10)
	// Pixel bytes are deliberately junk; inspection should skip them rather
	// than decoding them. Actual Encode still rejects the corrupt raster.
	data := append([]byte{}, pngImage[:33]...)
	data = append(data, encoderTestPNGChunk("IDAT", make([]byte, 2<<20))...)
	data = append(data, encoderTestPNGChunk("IEND", nil)...)
	r := &encoderCountingReaderAt{r: bytes.NewReader(data)}
	_, err := encoderInspect(context.Background(), r, int64(len(data)))
	encoderTestCode(t, err, "")
	if r.bytes > 32<<10 {
		t.Fatalf("header inspection read %d bytes", r.bytes)
	}

	chunks := make([][]byte, 0, encoderMaxChunks+2)
	chunks = append(chunks, encoderTestVP8L(10, 10))
	for i := 0; i < encoderMaxChunks; i++ {
		chunks = append(chunks, encoderTestRIFFChunk("JUNK", nil))
	}
	data = encoderTestRIFF(chunks...)
	_, err = encoderInspect(context.Background(), bytes.NewReader(data), int64(len(data)))
	encoderTestCode(t, err, "skipped_limit")
	_, err = encoderInspect(context.Background(), bytes.NewReader(nil), encoderMaxSourceBytes+1)
	encoderTestCode(t, err, "skipped_limit")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = encoderInspect(ctx, bytes.NewReader(pngImage), int64(len(pngImage)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestEncoderDimensions(t *testing.T) {
	cases := []struct {
		profile                     string
		width, height, wantW, wantH int
		code                        string
	}{
		{"thumb", 100, 80, 100, 80, ""},
		{"reader", 100, 80, 100, 80, ""},
		{"thumb", 4000, 3000, 480, 360, ""},
		{"reader", 4000, 3000, 1600, 1200, ""},
		{"thumb", 800, 20000, 164, 4096, ""},
		{"reader", 800, 20000, 0, 0, "long_image_requires_original"},
		{"reader", 1600, 10000, 1385, 8656, ""},
		{"thumb", 9528, 50, 480, 3, ""},
		{"thumb", 1, 16000000, 1, 4096, ""},
		{"reader", 0, 100, 0, 0, "skipped_limit"},
		{"reader", 4001, 4000, 1600, 1600, ""},
		{"thumb", 8000, 6000, 480, 360, ""},
		{"reader", 8000, 6000, 1600, 1200, ""},
		{"reader", 8001, 6000, 0, 0, "skipped_limit"},
		{"reader", math.MaxInt, 2, 0, 0, "skipped_limit"},
		{"arbitrary;quality=1", 10, 10, 0, 0, "invalid_profile"},
	}
	for _, tc := range cases {
		w, h, err := encoderDimensions(ImageInfo{Width: tc.width, Height: tc.height}, tc.profile)
		encoderTestCode(t, err, tc.code)
		if w != tc.wantW || h != tc.wantH {
			t.Errorf("%s %dx%d: got %dx%d, want %dx%d", tc.profile, tc.width, tc.height, w, h, tc.wantW, tc.wantH)
		}
	}
	for w := 1; w <= 6000; w += 53 {
		for h := 1; h <= 24000; h += 257 {
			for _, profile := range []string{"thumb", "reader"} {
				outW, outH, err := encoderDimensions(ImageInfo{Width: w, Height: h}, profile)
				if err != nil {
					continue
				}
				p, _ := encoderPolicy(profile)
				if outW < 1 || outH < 1 || outW > w || outH > h || outW > p.width || outH > p.height || int64(outW)*int64(outH) > p.pixels {
					t.Fatalf("invalid result %dx%d from %dx%d", outW, outH, w, h)
				}
			}
		}
	}
}

func TestEncoderErrorsNeverLeakDetails(t *testing.T) {
	secret := "/sensitive/path https://storage.invalid/a?token=secret"
	err := &EncodeError{Code: "encode_failed", Err: errors.New(secret)}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "https") {
		t.Fatal(err)
	}
	if !errors.Is(err, err.Err) {
		t.Fatal("Unwrap failed")
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if !errors.Is(encoderContextError(cause), cause) {
			t.Fatal("lost context cause")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, encodeErr := Encode(ctx, "unused", "unused", "thumb")
	encoderTestCode(t, encodeErr, "encode_canceled")
	_, encodeErr = Encode(context.Background(), "unused", "unused", "reader[Q=1]")
	encoderTestCode(t, encodeErr, "invalid_profile")
}
