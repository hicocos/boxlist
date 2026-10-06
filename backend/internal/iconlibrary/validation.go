// Package iconlibrary implements the private native icon store and its strict HTTP API.
package iconlibrary

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/draw"
	"image/png"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/HugoSmits86/nativewebp"
	"github.com/disintegration/imaging"
)

const MaxInputBytes = 5 * 1024 * 1024
const MaxAssets = 200
const MaxStoredBytes = 256 * 1024 * 1024

// Fault is the only error type whose status may cross the HTTP boundary.
// Handler responses use fixed safe messages, never raw hook or filesystem errors.
type Fault struct {
	Status  int
	Message string
}

func (f Fault) Error() string { return f.Message }
func fault(status int) error  { return Fault{status, safeMessage(status)} }

var requestPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var assetPattern = regexp.MustCompile(`^upload-[0-9a-f]{32}$`)
var identityPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var builtinIDs = map[string]bool{
	"pack-cab": true, "pack-doc": true, "pack-docx": true, "pack-mp3": true, "pack-pdf": true, "pack-ppt": true, "pack-pptx": true,
	"pack-psd": true, "pack-rar": true, "pack-txt": true, "pack-wav": true, "pack-wma": true, "pack-xlsx": true, "pack-zip": true,
	"pack-download": true, "pack-image": true, "pack-folder": true, "pack-document": true, "pack-video": true, "pack-music": true, "legacy-smile": true,
}

func validAsset(id string) bool { return assetPattern.MatchString(id) || builtinIDs[id] }
func decodeName(raw string) (string, error) {
	if raw == "" || len(raw) > 3072 {
		return "", fault(400)
	}
	for _, c := range raw {
		if c < 33 || c > 126 {
			return "", fault(400)
		}
	}
	name, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || name == "." || name == ".." || len(name) > 1024 || len(utf16.Encode([]rune(name))) > 160 || strings.ContainsAny(name, "/\\") {
		return "", fault(400)
	}
	for _, c := range name {
		if unicode.Is(unicode.C, c) {
			return "", fault(400)
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".png") {
		return "", fault(415)
	}
	return name, nil
}

// ConvertPNG fully verifies a single static PNG before producing a lossless WebP.
// It strips metadata, preserves alpha and never scales a small image up.
func ConvertPNG(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxInputBytes {
		return nil, fault(413)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, fault(415)
	}
	if len(data) < 33 || !bytes.Equal(data[8:16], []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R'}) {
		return nil, fault(422)
	}
	w, h := uint64(binary.BigEndian.Uint32(data[16:20])), uint64(binary.BigEndian.Uint32(data[20:24]))
	if w == 0 || h == 0 {
		return nil, fault(422)
	}
	if w > 4096 || h > 4096 || w*h > 16000000 {
		return nil, fault(413)
	}
	ended, idat := false, false
	orientation := 1
	for offset := 8; offset+12 <= len(data); {
		n := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		if n > uint64(len(data)-offset-12) {
			return nil, fault(422)
		}
		end := offset + 8 + int(n)
		kind := string(data[offset+4 : offset+8])
		if kind == "IHDR" && offset != 8 {
			return nil, fault(422)
		}
		if crc32.ChecksumIEEE(data[offset+4:end]) != binary.BigEndian.Uint32(data[end:end+4]) {
			return nil, fault(422)
		}
		if kind == "acTL" || kind == "fcTL" || kind == "fdAT" {
			return nil, fault(415)
		}
		if kind == "eXIf" {
			orientation = exifOrientation(data[offset+8 : end])
		}
		if kind == "IDAT" {
			idat = true
		}
		offset = end + 4
		if kind == "IEND" {
			ended = n == 0 && offset == len(data)
			break
		}
	}
	if !ended || !idat {
		return nil, fault(422)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fault(422)
	}
	// Honor the bounded TIFF orientation tag, then strip all metadata.
	switch orientation {
	case 2:
		img = imaging.FlipH(img)
	case 3:
		img = imaging.Rotate180(img)
	case 4:
		img = imaging.FlipV(img)
	case 5:
		img = imaging.Transpose(img)
	case 6:
		img = imaging.Rotate270(img)
	case 7:
		img = imaging.Transverse(img)
	case 8:
		img = imaging.Rotate90(img)
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	if width > 232 || height > 232 {
		img = imaging.Fit(img, 232, 232, imaging.Lanczos)
		width, height = img.Bounds().Dx(), img.Bounds().Dy()
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(canvas, image.Rect((256-width)/2, (256-height)/2, (256-width)/2+width, (256-height)/2+height), img, img.Bounds().Min, draw.Src)
	var out bytes.Buffer
	if err = nativewebp.Encode(&out, canvas, &nativewebp.Options{CompressionLevel: nativewebp.DefaultCompression}); err != nil {
		return nil, fault(422)
	}
	return out.Bytes(), nil
}
