package iconlibrary

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/gif"
)

const maxGIFFrames = 2000
const maxGIFCanvasPixels = 160000000

// validateGIF deliberately does not render or re-encode the animation. Keeping
// the original stream preserves timing, looping, palettes, transparency and all
// disposal modes exactly, including restore-to-previous.
func validateGIF(data []byte) ([]byte, error) {
	frames, err := preflightGIF(data)
	if err != nil {
		return nil, err
	}
	animation, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || len(animation.Image) != frames {
		return nil, fault(422)
	}
	canvas := image.Rect(0, 0, animation.Config.Width, animation.Config.Height)
	for _, frame := range animation.Image {
		if frame.Bounds().Empty() || !frame.Bounds().In(canvas) {
			return nil, fault(422)
		}
	}
	return data, nil
}

// preflightGIF walks blocks without decompressing or allocating frame buffers.
// DecodeAll retains every frame, so dimensions alone do not bound its memory or
// the work needed to display an animation with many tiny update rectangles.
func preflightGIF(data []byte) (int, error) {
	return inspectGIF(data, nil)
}

// inspectGIF recognizes our marker only as a complete comment extension, never
// as bytes embedded in a palette, compressed image, or another extension.
func inspectGIF(data []byte, transparent *bool) (int, error) {
	if len(data) < 13 {
		return 0, fault(422)
	}
	width := int(binary.LittleEndian.Uint16(data[6:8]))
	height := int(binary.LittleEndian.Uint16(data[8:10]))
	if width == 0 || height == 0 {
		return 0, fault(422)
	}
	if width > 4096 || height > 4096 || width*height > 16000000 {
		return 0, fault(413)
	}
	offset := 13
	if data[10]&0x80 != 0 {
		offset += 3 * (1 << ((data[10] & 7) + 1))
	}
	frames := 0
	for offset < len(data) {
		kind := data[offset]
		offset++
		commentMatch := -1
		switch kind {
		case 0x3b: // Trailer must end the stream, not hide additional payload.
			if frames == 0 || offset != len(data) {
				return 0, fault(422)
			}
			return frames, nil
		case 0x2c: // Image descriptor, optional local palette, then LZW blocks.
			if len(data)-offset < 9 {
				return 0, fault(422)
			}
			x := int(binary.LittleEndian.Uint16(data[offset:]))
			y := int(binary.LittleEndian.Uint16(data[offset+2:]))
			w := int(binary.LittleEndian.Uint16(data[offset+4:]))
			h := int(binary.LittleEndian.Uint16(data[offset+6:]))
			flags := data[offset+8]
			if w == 0 || h == 0 || x+w > width || y+h > height || flags&0x18 != 0 {
				return 0, fault(422)
			}
			frames++
			if frames > maxGIFFrames || frames > maxGIFCanvasPixels/(width*height) {
				return 0, fault(413)
			}
			offset += 9
			if flags&0x80 != 0 {
				offset += 3 * (1 << ((flags & 7) + 1))
			}
			if offset >= len(data) || data[offset] < 2 || data[offset] > 8 {
				return 0, fault(422)
			}
			offset++
		case 0x21: // Extension; only image data contributes to the frame budget.
			if offset >= len(data) {
				return 0, fault(422)
			}
			label := data[offset]
			offset++
			switch label {
			case 0xf9:
				if len(data)-offset < 6 || data[offset] != 4 || data[offset+5] != 0 || data[offset+1]&0xe0 != 0 || (data[offset+1]>>2)&7 > 3 {
					return 0, fault(422)
				}
				offset += 6
				continue
			case 0xff, 0x01:
				if offset >= len(data) {
					return 0, fault(422)
				}
				n := int(data[offset])
				if (label == 0xff && n != 11 && n != 10) || (label == 0x01 && n != 12) || n > len(data)-offset-1 {
					return 0, fault(422)
				}
				offset += n + 1
			case 0xfe: // Comment extension consists only of sub-blocks.
				commentMatch = 0
			default:
				return 0, fault(422)
			}
		default:
			return 0, fault(422)
		}
		for {
			if offset >= len(data) {
				return 0, fault(422)
			}
			n := int(data[offset])
			offset++
			if n > len(data)-offset {
				return 0, fault(422)
			}
			if commentMatch >= 0 && n != 0 {
				if commentMatch+n > len(transparencyMarker) || string(data[offset:offset+n]) != transparencyMarker[commentMatch:commentMatch+n] {
					commentMatch = -1
				} else {
					commentMatch += n
				}
			}
			offset += n
			if n == 0 {
				if transparent != nil && commentMatch == len(transparencyMarker) {
					*transparent = true
				}
				break
			}
		}
	}
	return 0, fault(422)
}
