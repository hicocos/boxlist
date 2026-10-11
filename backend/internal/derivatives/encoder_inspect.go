package derivatives

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strings"

	"golang.org/x/image/webp"
)

const (
	encoderMaxChunks     = 8192
	encoderMaxMetadata   = 4 << 20
	encoderMaxEXIF       = 1 << 20
	encoderMaxHeaderScan = 4 << 20
)

type encoderImageMeta struct {
	ImageInfo
	orientation int
	metadata    bool
}

type encoderContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r encoderContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func encoderInspectFile(ctx context.Context, f *os.File) (encoderImageMeta, error) {
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return encoderImageMeta{}, encoderError("invalid_local_path")
	}
	if stat.Size() > encoderMaxSourceBytes {
		return encoderImageMeta{}, encoderError("skipped_limit")
	}
	return encoderInspect(ctx, f, stat.Size())
}

// Inspect uses random-access chunk headers to skip compressed pixel payloads.
// No input-dependent raster allocation or full-image Decode is allowed here.
// Both chunk count and metadata size are bounded, including after image data.
func encoderInspect(ctx context.Context, r io.ReaderAt, size int64) (encoderImageMeta, error) {
	m := encoderImageMeta{orientation: 1}
	if err := ctx.Err(); err != nil {
		return m, encoderContextError(err)
	}
	if size <= 0 {
		return m, encoderError("invalid_image")
	}
	if size > encoderMaxSourceBytes {
		return m, encoderError("skipped_limit")
	}
	header := make([]byte, min(size, 512))
	if _, err := r.ReadAt(header, 0); err != nil {
		return m, encoderError("invalid_image")
	}
	var err error
	switch {
	case bytes.HasPrefix(header, []byte("\xff\xd8\xff")):
		m.Format = "jpeg"
		err = encoderInspectJPEG(ctx, r, size, &m)
	case bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")):
		m.Format = "png"
		err = encoderInspectPNG(ctx, r, size, &m)
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		m.Format = "webp"
		err = encoderInspectWebP(ctx, r, size, &m)
	case bytes.HasPrefix(header, []byte("GIF87a")), bytes.HasPrefix(header, []byte("GIF89a")):
		m.Format = "gif"
		return m, encoderError("unsupported_gif")
	case bytes.HasPrefix(header, []byte("II\x2a\x00")), bytes.HasPrefix(header, []byte("MM\x00\x2a")),
		bytes.HasPrefix(header, []byte("II\x2b\x00")), bytes.HasPrefix(header, []byte("MM\x00\x2b")):
		m.Format = "tiff"
		return m, encoderError("unsupported_tiff")
	default:
		// Do not parse XML or invoke an SVG loader. The sniff is only for a
		// clearer error; ALL other signatures also fail the allowlist.
		prefix := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(header), "\xef\xbb\xbf")))
		if strings.Contains(prefix, "<svg") || strings.HasPrefix(prefix, "<?xml") || strings.HasPrefix(prefix, "<!doctype svg") {
			m.Format = "svg"
			return m, encoderError("unsupported_svg")
		}
		return m, encoderError("unsupported_format")
	}
	if err != nil {
		if ctx.Err() != nil {
			return m, encoderContextError(ctx.Err())
		}
		return m, err
	}
	// After structural/animation checks, DecodeConfig independently verifies
	// the registered JPEG/PNG/WebP header. WebP payload dimensions were ALSO
	// checked below: DecodeConfig alone trusts the outer VP8X canvas.
	config, format, err := image.DecodeConfig(encoderContextReader{ctx, io.NewSectionReader(r, 0, size)})
	if err != nil || format != m.Format || config.Width != m.Width || config.Height != m.Height {
		if ctx.Err() != nil {
			return m, encoderContextError(ctx.Err())
		}
		return m, encoderError("invalid_image")
	}
	if err := encoderCheckPixels(config.Width, config.Height); err != nil {
		return m, err
	}
	if m.orientation >= 5 && m.orientation <= 8 {
		m.Width, m.Height = m.Height, m.Width
	}
	return m, nil
}

func encoderReadAt(r io.ReaderAt, off int64, p []byte) error {
	if _, err := r.ReadAt(p, off); err != nil {
		return encoderError("invalid_image")
	}
	return nil
}

func encoderInspectPNG(ctx context.Context, r io.ReaderAt, size int64, m *encoderImageMeta) error {
	var header [8]byte
	seenData, seenEXIF := false, false
	metadata := int64(0)
	for off, count := int64(8), 0; off < size; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count >= encoderMaxChunks {
			return encoderError("skipped_limit")
		}
		if size-off < 12 || encoderReadAt(r, off, header[:]) != nil {
			return encoderError("invalid_image")
		}
		n := int64(binary.BigEndian.Uint32(header[:4]))
		kind := string(header[4:])
		if n > size-off-12 || (count == 0 && kind != "IHDR") {
			return encoderError("invalid_image")
		}
		switch kind {
		case "acTL", "fcTL", "fdAT":
			m.Animated = true
			return encoderError("unsupported_animation")
		case "IHDR":
			if count != 0 || n != 13 {
				return encoderError("invalid_image")
			}
			var data [17]byte
			if encoderReadAt(r, off+8, data[:]) != nil {
				return encoderError("invalid_image")
			}
			crc := crc32.NewIEEE()
			_, _ = crc.Write(header[4:])
			_, _ = crc.Write(data[:13])
			if crc.Sum32() != binary.BigEndian.Uint32(data[13:]) {
				return encoderError("invalid_image")
			}
			m.Width, m.Height = int(binary.BigEndian.Uint32(data[:4])), int(binary.BigEndian.Uint32(data[4:8]))
			if err := encoderCheckPixels(m.Width, m.Height); err != nil {
				return err
			}
		case "IDAT":
			seenData = true
		case "IEND":
			if n != 0 || !seenData || off+12 != size {
				return encoderError("invalid_image")
			}
			var crc [4]byte
			if encoderReadAt(r, off+8, crc[:]) != nil || binary.BigEndian.Uint32(crc[:]) != crc32.ChecksumIEEE(header[4:]) {
				return encoderError("invalid_image")
			}
			return nil
		default:
			metadata += n
			if metadata > encoderMaxMetadata {
				return encoderError("skipped_limit")
			}
			if kind == "eXIf" {
				m.metadata = true
				if seenEXIF || n > encoderMaxEXIF {
					return encoderError("invalid_image")
				}
				seenEXIF = true
				data := make([]byte, n)
				if encoderReadAt(r, off+8, data) != nil {
					return encoderError("invalid_image")
				}
				var err error
				m.orientation, err = encoderEXIFOrientation(data)
				if err != nil {
					return err
				}
			}
		}
		off += n + 12
	}
	return encoderError("invalid_image")
}

func encoderInspectJPEG(ctx context.Context, r io.ReaderAt, size int64, m *encoderImageMeta) error {
	var b [4]byte
	seenEXIF, seenSOF := false, false
	for off, count := int64(2), 0; off < size; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count >= encoderMaxChunks || off > encoderMaxHeaderScan {
			return encoderError("skipped_limit")
		}
		if encoderReadAt(r, off, b[:2]) != nil || b[0] != 0xff {
			return encoderError("invalid_image")
		}
		if b[1] == 0xff { // bounded marker fill bytes
			off++
			continue
		}
		marker := b[1]
		if marker == 0xd9 || marker == 0xd8 || marker == 0x00 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			return encoderError("invalid_image")
		}
		if encoderReadAt(r, off+2, b[:2]) != nil {
			return encoderError("invalid_image")
		}
		n := int64(binary.BigEndian.Uint16(b[:2]))
		if n < 2 || n > size-off-2 || off+2+n > encoderMaxHeaderScan {
			return encoderError("invalid_image")
		}
		if marker == 0xda { // stop before entropy data; libvips validates it
			if !seenSOF || size < off+n+4 {
				return encoderError("invalid_image")
			}
			var end [2]byte
			if encoderReadAt(r, size-2, end[:]) != nil || end != [2]byte{0xff, 0xd9} {
				return encoderError("invalid_image")
			}
			return nil
		}
		if marker == 0xc0 || marker == 0xc1 || marker == 0xc2 {
			var sof [6]byte
			if seenSOF || n < 8 || encoderReadAt(r, off+4, sof[:]) != nil {
				return encoderError("invalid_image")
			}
			seenSOF = true
			m.Width, m.Height = int(binary.BigEndian.Uint16(sof[3:5])), int(binary.BigEndian.Uint16(sof[1:3]))
			if err := encoderCheckPixels(m.Width, m.Height); err != nil {
				return err
			}
		}
		if marker == 0xe1 && n >= 8 {
			var sig [6]byte
			if encoderReadAt(r, off+4, sig[:]) != nil {
				return encoderError("invalid_image")
			}
			if string(sig[:]) == "Exif\x00\x00" {
				if seenEXIF {
					return encoderError("invalid_image")
				}
				seenEXIF, m.metadata = true, true
				data := make([]byte, n-2)
				if encoderReadAt(r, off+4, data) != nil {
					return encoderError("invalid_image")
				}
				var err error
				m.orientation, err = encoderEXIFOrientation(data)
				if err != nil {
					return err
				}
			}
		}
		off += 2 + n
	}
	return encoderError("invalid_image")
}

func encoderInspectWebP(ctx context.Context, r io.ReaderAt, size int64, m *encoderImageMeta) error {
	var header [12]byte
	if size < 20 || encoderReadAt(r, 0, header[:]) != nil || int64(binary.LittleEndian.Uint32(header[4:8]))+8 != size {
		return encoderError("invalid_image")
	}
	seenExtended, seenImage, seenEXIF, seenAlpha := false, false, false, false
	canvasW, canvasH := 0, 0
	var flags byte
	metadata := int64(0)
	for off, count := int64(12), 0; off < size; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count >= encoderMaxChunks {
			return encoderError("skipped_limit")
		}
		if size-off < 8 || encoderReadAt(r, off, header[:8]) != nil {
			return encoderError("invalid_image")
		}
		kind := string(header[:4])
		n := int64(binary.LittleEndian.Uint32(header[4:8]))
		padded := n + n%2
		if padded > size-off-8 {
			return encoderError("invalid_image")
		}
		switch kind {
		case "ANIM", "ANMF":
			m.Animated = true
			return encoderError("unsupported_animation")
		case "VP8X":
			if count != 0 || seenExtended || n != 10 || encoderReadAt(r, off+8, header[:10]) != nil {
				return encoderError("invalid_image")
			}
			seenExtended, flags = true, header[0]
			if flags&2 != 0 {
				m.Animated = true
				return encoderError("unsupported_animation")
			}
			if flags&0xc1 != 0 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
				return encoderError("invalid_image")
			}
			canvasW = 1 + int(header[4]) + int(header[5])<<8 + int(header[6])<<16
			canvasH = 1 + int(header[7]) + int(header[8])<<8 + int(header[9])<<16
			if err := encoderCheckPixels(canvasW, canvasH); err != nil {
				return err
			}
		case "VP8 ", "VP8L":
			if seenImage || (kind == "VP8L" && seenAlpha) || n < 5 {
				return encoderError("invalid_image")
			}
			seenImage = true
			// A synthetic RIFF wrapper isolates the actual bitstream header.
			// This is header validation, never a fabricated encoded output.
			var wrapper [20]byte
			copy(wrapper[:4], "RIFF")
			binary.LittleEndian.PutUint32(wrapper[4:8], uint32(12+padded))
			copy(wrapper[8:12], "WEBP")
			copy(wrapper[12:16], kind)
			binary.LittleEndian.PutUint32(wrapper[16:20], uint32(n))
			config, err := webp.DecodeConfig(io.MultiReader(bytes.NewReader(wrapper[:]), io.NewSectionReader(r, off+8, padded)))
			if err != nil {
				return encoderError("invalid_image")
			}
			m.Width, m.Height = config.Width, config.Height
			if err := encoderCheckPixels(m.Width, m.Height); err != nil {
				return err
			}
			if seenExtended && (m.Width != canvasW || m.Height != canvasH) {
				return encoderError("invalid_image")
			}
		case "ALPH":
			if !seenExtended || flags&0x10 == 0 || seenAlpha || seenImage || n < 1 {
				return encoderError("invalid_image")
			}
			seenAlpha = true
		default:
			metadata += n
			if metadata > encoderMaxMetadata {
				return encoderError("skipped_limit")
			}
			if kind == "EXIF" || kind == "ICCP" || kind == "XMP " {
				m.metadata = true
				if !seenExtended {
					return encoderError("invalid_image")
				}
			}
			if kind == "EXIF" {
				if seenEXIF || n > encoderMaxEXIF {
					return encoderError("invalid_image")
				}
				seenEXIF = true
				data := make([]byte, n)
				if encoderReadAt(r, off+8, data) != nil {
					return encoderError("invalid_image")
				}
				var err error
				m.orientation, err = encoderEXIFOrientation(data)
				if err != nil {
					return err
				}
			}
		}
		if n%2 != 0 {
			var pad [1]byte
			if encoderReadAt(r, off+8+n, pad[:]) != nil || pad[0] != 0 {
				return encoderError("invalid_image")
			}
		}
		off += 8 + padded
	}
	if !seenImage {
		return encoderError("invalid_image")
	}
	return nil
}

// Only the first TIFF IFD can describe the primary raster orientation. Do not
// follow IFD/sub-IFD chains or allocate based on untrusted EXIF offsets/counts.
func encoderEXIFOrientation(data []byte) (int, error) {
	data = bytes.TrimPrefix(data, []byte("Exif\x00\x00"))
	if len(data) < 8 {
		return 0, encoderError("invalid_image")
	}
	var order binary.ByteOrder
	switch string(data[:4]) {
	case "II\x2a\x00":
		order = binary.LittleEndian
	case "MM\x00\x2a":
		order = binary.BigEndian
	default:
		return 0, encoderError("invalid_image")
	}
	off := int64(order.Uint32(data[4:8]))
	if off < 8 || off > int64(len(data))-2 {
		return 0, encoderError("invalid_image")
	}
	count := int64(order.Uint16(data[off : off+2]))
	if count > 1024 || off+2+count*12+4 > int64(len(data)) {
		return 0, encoderError("invalid_image")
	}
	orientation, found := 1, false
	for i := int64(0); i < count; i++ {
		entry := data[off+2+i*12 : off+2+(i+1)*12]
		if order.Uint16(entry[:2]) != 0x0112 {
			continue
		}
		if found || order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			return 0, encoderError("invalid_image")
		}
		found = true
		orientation = int(order.Uint16(entry[8:10]))
		if orientation < 1 || orientation > 8 {
			return 0, encoderError("invalid_image")
		}
	}
	return orientation, nil
}

func encoderIsCode(err error, code string) bool {
	var target *EncodeError
	return errors.As(err, &target) && target.Code == code
}
