package iconlibrary

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"reflect"
	"testing"
)

func transparencyFixture(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.White, color.Black, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}, color.RGBA{220, 220, 220, 255}, color.RGBA{}}
	base := image.NewPaletted(image.Rect(0, 0, 9, 9), palette)
	// A closed black ring encloses a white detail that must survive.
	for y := 3; y <= 5; y++ {
		for x := 3; x <= 5; x++ {
			base.SetColorIndex(x, y, 1)
		}
	}
	base.SetColorIndex(4, 4, 0)
	base.SetColorIndex(1, 1, 4) // neutral fringe next to exterior white
	red := image.NewPaletted(image.Rect(2, 2, 3, 3), palette)
	red.SetColorIndex(2, 2, 2)
	blue := image.NewPaletted(image.Rect(6, 6, 7, 7), palette)
	blue.SetColorIndex(6, 6, 3)
	transparent := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	transparent.SetColorIndex(0, 0, 5)
	var out bytes.Buffer
	err := gif.EncodeAll(&out, &gif.GIF{Image: []*image.Paletted{base, red, blue, transparent}, Delay: []int{0, 3, 17, 50}, Disposal: []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalBackground, gif.DisposalNone}, LoopCount: 4, Config: image.Config{ColorModel: palette, Width: 9, Height: 9}})
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestTransparentGIFCompositingTimingAndEnclosedWhite(t *testing.T) {
	input := transparencyFixture(t)
	last := 0
	output, changed, err := transparentGIF(input, func(progress int) {
		if progress < last || progress > 90 {
			t.Fatalf("invalid progress %d after %d", progress, last)
		}
		last = progress
	})
	if err != nil || !changed {
		t.Fatalf("processing: changed=%v err=%v", changed, err)
	}
	animation, err := gif.DecodeAll(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	if animation.Config.Width != 9 || animation.Config.Height != 9 || len(animation.Image) != 4 || animation.LoopCount != 4 || !reflect.DeepEqual(animation.Delay, []int{0, 3, 17, 50}) {
		t.Fatalf("animation metadata changed: %+v", animation)
	}
	for i, frame := range animation.Image {
		if frame.Bounds() != image.Rect(0, 0, 9, 9) || animation.Disposal[i] != gif.DisposalBackground {
			t.Fatalf("frame %d not a cleared full canvas", i)
		}
		for _, point := range []image.Point{{0, 0}, {1, 1}, {8, 8}} {
			_, _, _, alpha := frame.At(point.X, point.Y).RGBA()
			if alpha != 0 {
				t.Fatalf("frame %d background %v still opaque", i, point)
			}
		}
		r, g, b, a := frame.At(4, 4).RGBA()
		if r != 65535 || g != 65535 || b != 65535 || a != 65535 {
			t.Fatalf("frame %d enclosed white lost", i)
		}
	}
	if r, _, _, a := animation.Image[1].At(2, 2).RGBA(); r != 65535 || a != 65535 {
		t.Fatal("red update missing")
	}
	if _, _, _, a := animation.Image[2].At(2, 2).RGBA(); a != 0 {
		t.Fatal("restore-previous left red update")
	}
	if _, _, b, a := animation.Image[2].At(6, 6).RGBA(); b != 65535 || a != 65535 {
		t.Fatal("blue update missing")
	}
	if _, _, _, a := animation.Image[3].At(6, 6).RGBA(); a != 0 {
		t.Fatal("restore-background left blue update")
	}
	if last != 90 {
		t.Fatalf("progress never completed frames: %d", last)
	}
	if _, changed, err = transparentGIF(output, nil); err != nil || changed {
		t.Fatalf("second processing should be unchanged: %v %v", changed, err)
	}
}

func TestTransparentGIFUnchangedAndLimits(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 3, 3), color.Palette{color.Black, color.White})
	frame.SetColorIndex(1, 1, 1)
	var buf bytes.Buffer
	if err := gif.Encode(&buf, frame, nil); err != nil {
		t.Fatal(err)
	}
	if output, changed, err := transparentGIF(buf.Bytes(), nil); err != nil || changed || output != nil {
		t.Fatalf("enclosed-only white changed: %v %v", changed, err)
	}
	for name, data := range map[string][]byte{
		"input":  make([]byte, MaxInputBytes+1),
		"frames": gifBlocks(1, 1, maxGIFFrames+1),
		"canvas": gifBlocks(4096, 4096, 1),
		"work":   gifBlocks(4000, 4000, 11),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := transparentGIF(data, nil)
			if f, ok := err.(Fault); !ok || f.Status != 413 {
				t.Fatalf("limit not enforced: %v", err)
			}
		})
	}
	input := transparencyFixture(t)
	if _, _, err := transparentGIF(input[:len(input)-1], nil); err == nil {
		t.Fatal("truncated GIF accepted")
	}
}

func TestTransparentGIFPaletteOverflow(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 300, 1))
	for x := range 300 {
		canvas.SetRGBA(x, 0, color.RGBA{uint8(x), uint8(x / 256), 123, 255})
	}
	mask := make([]byte, 300)
	mask[0] = 1
	frame := image.NewPaletted(canvas.Rect, nil)
	fillTransparentPalette(frame, canvas, mask)
	if len(frame.Palette) > 256 || frame.Pix[0] != 0 {
		t.Fatal("palette overflow or transparency lost")
	}
	for _, index := range frame.Pix[1:] {
		if index == 0 {
			t.Fatal("opaque pixel quantized to transparency")
		}
	}
}

func TestTransparentGIFBackgroundCanvasAndInfiniteLoop(t *testing.T) {
	palette := color.Palette{color.White, color.Black}
	frame := image.NewPaletted(image.Rect(1, 1, 2, 2), palette)
	frame.SetColorIndex(1, 1, 1)
	var input bytes.Buffer
	if err := gif.EncodeAll(&input, &gif.GIF{
		Image: []*image.Paletted{frame, frame}, Delay: []int{2, 9},
		Disposal: []byte{gif.DisposalBackground, gif.DisposalNone}, LoopCount: 0,
		Config: image.Config{Width: 3, Height: 3, ColorModel: palette},
	}); err != nil {
		t.Fatal(err)
	}
	output, changed, err := transparentGIF(input.Bytes(), nil)
	if err != nil || !changed {
		t.Fatalf("logical background: %v %v", changed, err)
	}
	animation, err := gif.DecodeAll(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	if animation.LoopCount != 0 {
		t.Fatal("infinite loop changed")
	}
	for _, frame := range animation.Image {
		if _, _, _, alpha := frame.At(0, 0).RGBA(); alpha != 0 {
			t.Fatal("logical screen background not removed")
		}
		if r, g, b, alpha := frame.At(1, 1).RGBA(); r != 0 || g != 0 || b != 0 || alpha != 65535 {
			t.Fatal("opaque foreground lost")
		}
	}
}

func TestTransparentGIFBoundedEncoding(t *testing.T) {
	buffer := &limitedGIFBuffer{}
	if _, err := buffer.Write(make([]byte, MaxInputBytes)); err != nil {
		t.Fatal(err)
	}
	if err := buffer.WriteByte(0); err == nil {
		t.Fatal("byte limit ignored")
	}
	if _, err := buffer.Write([]byte{0}); err == nil {
		t.Fatal("write limit ignored")
	}
	if buffer.Len() != MaxInputBytes {
		t.Fatal("oversized bytes retained")
	}
}

func TestTransparentGIFRejectsExpandedOutput(t *testing.T) {
	palette := make(color.Palette, 256)
	for i := range palette {
		palette[i] = color.RGBA{uint8(i), 0, 0, 255}
	}
	palette[255] = color.White
	first := image.NewPaletted(image.Rect(0, 0, 80, 80), palette)
	state := uint32(17)
	for i := range first.Pix {
		state = state*1664525 + 1013904223
		first.Pix[i] = uint8((state >> 16) % 255)
	}
	first.Pix[0] = 255
	update := image.NewPaletted(image.Rect(79, 79, 80, 80), palette)
	animation := &gif.GIF{LoopCount: -1, Config: image.Config{Width: 80, Height: 80, ColorModel: palette}}
	for i := range 1200 {
		frame := update
		if i == 0 {
			frame = first
		}
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 1)
	}
	var input bytes.Buffer
	if err := gif.EncodeAll(&input, animation); err != nil {
		t.Fatal(err)
	}
	if input.Len() > MaxInputBytes {
		t.Fatal("test input exceeds limit")
	}
	if _, _, err := transparentGIF(input.Bytes(), nil); err == nil {
		t.Fatal("expanded full-frame output accepted")
	} else if f, ok := err.(Fault); !ok || f.Status != 413 {
		t.Fatalf("unexpected output limit error: %v", err)
	}
}

func TestTransparentGIFFullColorRerunDoesNotErode(t *testing.T) {
	palette := make(color.Palette, 256)
	palette[0], palette[1], palette[2] = color.White, color.RGBA{220, 220, 220, 255}, color.Black
	for i := 3; i < len(palette); i++ {
		palette[i] = color.RGBA{uint8(i), uint8(i * 37), uint8(i * 71), 255}
	}
	frame := image.NewPaletted(image.Rect(0, 0, 32, 32), palette)
	for y := 1; y < 31; y++ {
		for x := 1; x < 31; x++ {
			index := uint8(3 + ((y-1)*30+x-1)%253)
			if x <= 2 || x >= 29 || y <= 2 || y >= 29 {
				index = 1 // Two-pixel neutral border exposes repeated fringe erosion.
			} else if x == 3 || x == 28 || y == 3 || y == 28 {
				index = 2
			}
			frame.SetColorIndex(x, y, index)
		}
	}
	secondPalette := append(color.Palette(nil), palette...)
	secondPalette[3] = color.RGBA{1, 2, 3, 255}
	update := image.NewPaletted(image.Rect(15, 15, 16, 16), secondPalette)
	update.SetColorIndex(15, 15, 3)
	var input bytes.Buffer
	if err := gif.EncodeAll(&input, &gif.GIF{
		Image: []*image.Paletted{frame, update}, Delay: []int{7, 13}, LoopCount: 0,
		Config: image.Config{Width: 32, Height: 32, ColorModel: palette},
	}); err != nil {
		t.Fatal(err)
	}
	output, changed, err := transparentGIF(input.Bytes(), nil)
	if err != nil || !changed {
		t.Fatalf("first processing: changed=%v err=%v", changed, err)
	}
	animation, err := gif.DecodeAll(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, alpha := animation.Image[0].At(1, 15).RGBA(); alpha != 0 {
		t.Fatal("first neutral fringe was not removed")
	}
	if _, _, _, alpha := animation.Image[0].At(2, 15).RGBA(); alpha == 0 {
		t.Fatal("second neutral fringe was removed on first pass")
	}
	snapshot := bytes.Clone(output)
	for range 3 {
		rerun, changed, err := transparentGIF(output, nil)
		if err != nil || changed || rerun != nil {
			t.Fatalf("processed output eroded again: changed=%v err=%v", changed, err)
		}
		if !bytes.Equal(output, snapshot) {
			t.Fatal("rerun mutated processed bytes")
		}
	}
}

func TestTransparentGIFMarkerRequiresExactCommentBlock(t *testing.T) {
	input := transparencyFixture(t)
	addExtension := func(extension []byte) []byte {
		data := bytes.Clone(input[:len(input)-1])
		data = append(data, extension...)
		return append(data, 0x3b)
	}
	comment := func(text string) []byte {
		block := []byte{0x21, 0xfe, byte(len(text))}
		block = append(block, text...)
		return append(block, 0)
	}
	for _, text := range []string{"prefix-" + transparencyMarker, transparencyMarker + "-suffix"} {
		if _, changed, err := transparentGIF(addExtension(comment(text)), nil); err != nil || !changed {
			t.Fatalf("non-exact marker skipped processing: %q %v", text, err)
		}
	}
	application := []byte{0x21, 0xff, 11}
	application = append(application, "OTHERAPP1.0"...)
	application = append(application, byte(len(transparencyMarker)))
	application = append(application, transparencyMarker...)
	application = append(application, 0)
	if _, changed, err := transparentGIF(addExtension(application), nil); err != nil || !changed {
		t.Fatalf("application payload mistaken for comment: %v", err)
	}
	// GIF comment text may span several data sub-blocks.
	split := []byte{0x21, 0xfe, 9}
	split = append(split, transparencyMarker[:9]...)
	split = append(split, byte(len(transparencyMarker)-9))
	split = append(split, transparencyMarker[9:]...)
	split = append(split, 0)
	if output, changed, err := transparentGIF(addExtension(split), nil); err != nil || changed || output != nil {
		t.Fatalf("split exact marker not recognized: %v %v", changed, err)
	}
	marked := addExtension(comment(transparencyMarker))
	if _, _, err := transparentGIF(marked[:len(marked)-1], nil); err == nil {
		t.Fatal("marker bypassed validation of truncated GIF")
	}
}
