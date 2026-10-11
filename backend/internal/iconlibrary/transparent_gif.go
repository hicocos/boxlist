package iconlibrary

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
)

const transparencyMarker = "openlist:gif-white-background-removed:v1"

// transparentGIF retains decoded indexed frames, but only one composited canvas
// and one restore-to-previous buffer. Encoded frames are streamed into the bounded
// result rather than retaining another animation's worth of image buffers.
func transparentGIF(data []byte, progress func(int)) ([]byte, bool, error) {
	if len(data) == 0 || len(data) > MaxInputBytes {
		return nil, false, fault(413)
	}
	processed := false
	if _, err := inspectGIF(data, &processed); err != nil {
		return nil, false, err
	}
	animation, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, false, fault(422)
	}
	// The optional fringe is intentionally one pixel wide. Reapplying it to
	// newly transparent edges would erode another pixel, so owned output is
	// idempotent. Decode first: a marker never bypasses complete GIF validation.
	if processed {
		return nil, false, nil
	}
	bounds := image.Rect(0, 0, animation.Config.Width, animation.Config.Height)
	canvas := image.NewRGBA(bounds)
	background := color.RGBA{}
	if palette, ok := animation.Config.ColorModel.(color.Palette); ok && int(animation.BackgroundIndex) < len(palette) {
		background = color.RGBAModel.Convert(palette[animation.BackgroundIndex]).(color.RGBA)
	}
	// A transparent first frame represents a transparent logical background.
	if frameHasTransparency(animation.Image[0]) {
		background = color.RGBA{}
	}
	draw.Draw(canvas, bounds, image.NewUniform(background), image.Point{}, draw.Src)
	var previous *image.RGBA
	mask := make([]byte, bounds.Dx()*bounds.Dy())
	queue := make([]uint32, 0, len(mask))
	var result bytes.Buffer
	result.WriteString("GIF89a")
	var screen [7]byte
	binary.LittleEndian.PutUint16(screen[0:2], uint16(bounds.Dx()))
	binary.LittleEndian.PutUint16(screen[2:4], uint16(bounds.Dy()))
	result.Write(screen[:]) // All frames have local palettes, index zero is transparent.
	result.Write([]byte{0x21, 0xfe, byte(len(transparencyMarker))})
	result.WriteString(transparencyMarker)
	result.WriteByte(0)
	if animation.LoopCount >= 0 {
		result.Write([]byte{0x21, 0xff, 11})
		result.WriteString("NETSCAPE2.0")
		result.Write([]byte{3, 1, byte(animation.LoopCount), byte(animation.LoopCount >> 8), 0})
	}
	removed := false
	encoded := &limitedGIFBuffer{}
	overflow := false
	paletted := image.NewPaletted(bounds, nil)
	for i, frame := range animation.Image {
		if frame.Bounds().Empty() || !frame.Bounds().In(bounds) {
			return nil, false, fault(422)
		}
		disposal := byte(0)
		if i < len(animation.Disposal) {
			disposal = animation.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			if previous == nil {
				previous = image.NewRGBA(bounds)
			}
			copy(previous.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		var changed bool
		queue, changed = exteriorWhite(canvas, mask, queue[:0])
		removed = removed || changed
		if !overflow {
			fillTransparentPalette(paletted, canvas, mask)
			encoded.Reset()
			if err := gif.EncodeAll(encoded, &gif.GIF{
				Image: []*image.Paletted{paletted}, Delay: []int{animation.Delay[i]},
				Disposal: []byte{gif.DisposalBackground}, LoopCount: -1,
				Config: image.Config{Width: bounds.Dx(), Height: bounds.Dy()},
			}); err != nil {
				if f, ok := err.(Fault); !ok || f.Status != 413 {
					return nil, false, err
				}
				overflow = true
			} else {
				// No global palette: 13-byte header, then frame blocks and trailer.
				block := encoded.Bytes()
				overflow = result.Len()+len(block)-14+1 > MaxInputBytes
				if !overflow {
					result.Write(block[13 : len(block)-1])
				}
			}
		}
		if overflow && removed {
			return nil, false, fault(413)
		}
		switch disposal {
		case gif.DisposalPrevious:
			copy(canvas.Pix, previous.Pix)
		case gif.DisposalBackground:
			clearColor := background
			if frameHasTransparency(frame) {
				clearColor = color.RGBA{}
			}
			draw.Draw(canvas, frame.Bounds(), image.NewUniform(clearColor), image.Point{}, draw.Src)
		}
		if progress != nil {
			progress(5 + (i+1)*85/len(animation.Image))
		}
	}
	if !removed {
		return nil, false, nil
	}
	result.WriteByte(0x3b)
	if _, err := validateGIF(result.Bytes()); err != nil {
		return nil, false, err
	}
	return result.Bytes(), true, nil
}

type limitedGIFBuffer struct{ bytes.Buffer }

func (b *limitedGIFBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxInputBytes-b.Len() {
		return 0, fault(413)
	}
	return b.Buffer.Write(p)
}
func (b *limitedGIFBuffer) WriteByte(c byte) error {
	if b.Len() == MaxInputBytes {
		return fault(413)
	}
	return b.Buffer.WriteByte(c)
}

func frameHasTransparency(frame *image.Paletted) bool {
	for _, c := range frame.Palette {
		_, _, _, alpha := c.RGBA()
		if alpha == 0 {
			return true
		}
	}
	return false
}

// mask=1 is edge-connected white/transparent; mask=2 is its one-pixel neutral
// fringe. Fringe pixels never seed flood fill, preserving enclosed white detail.
func exteriorWhite(canvas *image.RGBA, mask []byte, queue []uint32) ([]uint32, bool) {
	clear(mask)
	width, height := canvas.Rect.Dx(), canvas.Rect.Dy()
	neutral := func(index, threshold, spread int) bool {
		p := canvas.Pix[index*4 : index*4+4]
		lo, hi := min(p[0], p[1], p[2]), max(p[0], p[1], p[2])
		return p[3] == 0 || int(lo) >= threshold && int(hi-lo) <= spread
	}
	push := func(index int) {
		if mask[index] == 0 && neutral(index, 240, 15) {
			mask[index] = 1
			queue = append(queue, uint32(index))
		}
	}
	for x := range width {
		push(x)
		push((height-1)*width + x)
	}
	for y := 1; y < height-1; y++ {
		push(y * width)
		push(y*width + width - 1)
	}
	neighbors := func(index int, visit func(int)) {
		if index%width != 0 {
			visit(index - 1)
		}
		if index%width != width-1 {
			visit(index + 1)
		}
		if index >= width {
			visit(index - width)
		}
		if index < len(mask)-width {
			visit(index + width)
		}
	}
	// The queue grows during traversal; a range would capture its initial length.
	for i := 0; i < len(queue); i++ {
		neighbors(int(queue[i]), push)
	}
	for _, index := range queue {
		neighbors(int(index), func(n int) {
			if mask[n] == 0 && neutral(n, 205, 24) {
				mask[n] = 2
			}
		})
	}
	removed := false
	for index, value := range mask {
		if value != 0 && canvas.Pix[index*4+3] != 0 {
			removed = true
		}
	}
	return queue, removed
}

func fillTransparentPalette(frame *image.Paletted, canvas *image.RGBA, mask []byte) {
	palette := make(color.Palette, 1, 256)
	palette[0] = color.RGBA{}
	indices := make(map[color.RGBA]uint8, 255)
	clear(frame.Pix)
	for i := range mask {
		p := canvas.Pix[i*4 : i*4+4]
		if mask[i] != 0 || p[3] == 0 {
			continue
		}
		c := color.RGBA{p[0], p[1], p[2], 255}
		index, ok := indices[c]
		if !ok {
			if len(palette) == 256 {
				// Composited local palettes can exceed GIF's 255 opaque colors.
				// A fixed 6x7x6 palette gives bounded, constant-time quantization.
				palette = palette[:1]
				for r := range 6 {
					for g := range 7 {
						for b := range 6 {
							palette = append(palette, color.RGBA{uint8(r * 255 / 5), uint8(g * 255 / 6), uint8(b * 255 / 5), 255})
						}
					}
				}
				for j := range mask {
					p := canvas.Pix[j*4 : j*4+4]
					if mask[j] != 0 || p[3] == 0 {
						frame.Pix[j] = 0
						continue
					}
					r, g, b := (int(p[0])*5+127)/255, (int(p[1])*6+127)/255, (int(p[2])*5+127)/255
					frame.Pix[j] = uint8(1 + r*42 + g*6 + b)
				}
				frame.Palette = palette
				return
			}
			index = uint8(len(palette))
			indices[c] = index
			palette = append(palette, c)
		}
		frame.Pix[i] = index
	}
	frame.Palette = palette
}
