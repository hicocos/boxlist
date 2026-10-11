package iconlibrary

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/url"
	"reflect"
	"testing"

	"golang.org/x/image/webp"
)

func gifFixture(t *testing.T, loop int) []byte {
	t.Helper()
	palette := color.Palette{color.NRGBA{}, color.NRGBA{R: 255, A: 255}, color.NRGBA{G: 255, A: 255}, color.NRGBA{B: 255, A: 255}}
	frames := []*image.Paletted{
		image.NewPaletted(image.Rect(0, 0, 4, 3), palette),
		image.NewPaletted(image.Rect(1, 0, 3, 2), palette),
		image.NewPaletted(image.Rect(2, 1, 4, 3), palette),
		image.NewPaletted(image.Rect(0, 0, 1, 1), palette),
	}
	frames[0].SetColorIndex(0, 0, 1)
	frames[0].SetColorIndex(3, 2, 1)
	frames[1].SetColorIndex(1, 0, 2)
	frames[2].SetColorIndex(2, 1, 3)
	frames[3].SetColorIndex(0, 0, 2)
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{
		Image: frames, Delay: []int{0, 7, 13, 65535}, LoopCount: loop,
		Disposal: []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalBackground, gif.DisposalNone},
		Config:   image.Config{ColorModel: palette, Width: 4, Height: 3},
	}); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 30, B: 40, A: 128})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func requireFault(t *testing.T, err error, status int) {
	t.Helper()
	if f, ok := err.(Fault); !ok || f.Status != status {
		t.Fatalf("want status %d, got %v", status, err)
	}
}

func TestGIFOriginalAnimationPreserved(t *testing.T) {
	for _, loop := range []int{-1, 0, 2, 65535} {
		original := gifFixture(t, loop)
		output, err := ConvertImage("动画.GiF", original)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, output) {
			t.Fatal("GIF was transcoded or metadata changed")
		}
		decoded, err := gif.DecodeAll(bytes.NewReader(output))
		if err != nil {
			t.Fatal(err)
		}
		if decoded.LoopCount != loop || !reflect.DeepEqual(decoded.Delay, []int{0, 7, 13, 65535}) || !reflect.DeepEqual(decoded.Disposal, []byte{1, 3, 2, 1}) {
			t.Fatalf("animation metadata changed: %+v", decoded)
		}
		if len(decoded.Image) != 4 || decoded.Image[1].Bounds() != image.Rect(1, 0, 3, 2) || decoded.Config.Width != 4 || decoded.Config.Height != 3 {
			t.Fatal("animation frame geometry changed")
		}
		for _, check := range []struct {
			frame, x, y int
			want        color.NRGBA
		}{
			{0, 0, 0, color.NRGBA{R: 255, A: 255}},
			{0, 1, 0, color.NRGBA{}},
			{1, 1, 0, color.NRGBA{G: 255, A: 255}},
			{1, 2, 0, color.NRGBA{}},
			{2, 2, 1, color.NRGBA{B: 255, A: 255}},
		} {
			got := color.NRGBAModel.Convert(decoded.Image[check.frame].At(check.x, check.y)).(color.NRGBA)
			if got != check.want {
				t.Fatalf("frame %d pixel (%d,%d): got %v want %v", check.frame, check.x, check.y, got, check.want)
			}
		}
	}
}

func TestPNGCompatibilityAndSignatures(t *testing.T) {
	pngData := pngFixture(t, 2, 2)
	gifData := gifFixture(t, 0)
	output, err := ConvertImage("still.PNG", pngData)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := webp.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != image.Rect(0, 0, 256, 256) {
		t.Fatal("PNG preview dimensions changed")
	}
	if got := color.NRGBAModel.Convert(decoded.At(127, 127)).(color.NRGBA); got != (color.NRGBA{R: 200, G: 30, B: 40, A: 128}) {
		t.Fatalf("PNG alpha/color changed: %v", got)
	}
	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha != 0 {
		t.Fatal("PNG padding lost transparency")
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"wrong.gif", pngData}, {"wrong.png", gifData}, {"wrong.webp", output},
		{"bad.gif", []byte("GIFnotreally")}, {"bad.png", []byte("not PNG")},
	} {
		_, err := ConvertImage(test.name, test.data)
		requireFault(t, err, 415)
	}
	for _, name := range []string{"静态.PNG", "动态.GiF"} {
		got, err := decodeName(url.PathEscape(name))
		if err != nil || got != name {
			t.Fatalf("name rejected: %q, %v", got, err)
		}
	}
	_, err = decodeName("bad.jpg")
	requireFault(t, err, 415)
	for _, data := range [][]byte{nil, make([]byte, MaxInputBytes+1)} {
		_, err := ConvertImage("too-big.gif", data)
		requireFault(t, err, 413)
	}
	// Inject a CRC-valid APNG animation control chunk ahead of IDAT.
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk, 8)
	copy(chunk[4:], "acTL")
	binary.BigEndian.PutUint32(chunk[8:], 1)
	binary.BigEndian.PutUint32(chunk[16:], crc32.ChecksumIEEE(chunk[4:16]))
	apng := append(append(append([]byte{}, pngData[:33]...), chunk...), pngData[33:]...)
	_, err = ConvertImage("animated.png", apng)
	requireFault(t, err, 415)
	for _, size := range []struct{ width, height uint32 }{{4097, 1}, {1, 4097}, {4096, 4096}} {
		large := append([]byte(nil), pngData...)
		binary.BigEndian.PutUint32(large[16:], size.width)
		binary.BigEndian.PutUint32(large[20:], size.height)
		_, err := ConvertImage("large.png", large)
		requireFault(t, err, 413)
	}
	_, err = ConvertImage("truncated.png", pngData[:len(pngData)-1])
	requireFault(t, err, 422)
}

// Minimal image blocks make limit tests cheap; preflight must reject their
// metadata before DecodeAll has any opportunity to allocate full canvases.
func gifBlocks(width, height, frames int) []byte {
	data := []byte{'G', 'I', 'F', '8', '9', 'a', 0, 0, 0, 0, 0x80, 0, 0, 0, 0, 0, 255, 255, 255}
	binary.LittleEndian.PutUint16(data[6:], uint16(width))
	binary.LittleEndian.PutUint16(data[8:], uint16(height))
	for range frames {
		data = append(data, 0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 0x44, 0x01, 0)
	}
	return append(data, 0x3b)
}

func TestLongGIFPreservesEveryFrame(t *testing.T) {
	data := gifBlocks(256, 256, 2000)
	result, err := ConvertImage("long.gif", data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, data) {
		t.Fatal("long GIF was modified")
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Image) != 2000 {
		t.Fatalf("lost frames: %d", len(decoded.Image))
	}
}

func TestGIFPreflightBoundsAndCorruption(t *testing.T) {
	for _, test := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"wide", gifBlocks(4097, 1, 1), 413},
		{"tall", gifBlocks(1, 4097, 1), 413},
		{"pixels", gifBlocks(4096, 4096, 1), 413},
		{"frames", gifBlocks(1, 1, 2001), 413},
		{"canvas-work", gifBlocks(4000, 4000, 11), 413},
		{"zero-canvas", gifBlocks(0, 1, 1), 422},
		{"empty", gifBlocks(1, 1, 0), 422},
		{"truncated-header", []byte("GIF89a"), 422},
		{"trailing-data", append(gifBlocks(1, 1, 1), 1), 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ConvertImage("test.gif", test.data)
			requireFault(t, err, test.status)
		})
	}
	for _, test := range []struct{ width, height, frames int }{{1, 1, 2000}, {4000, 4000, 10}, {256, 256, 2000}, {4096, 1, 1}} {
		if _, err := preflightGIF(gifBlocks(test.width, test.height, test.frames)); err != nil {
			t.Fatalf("boundary rejected: %+v, %v", test, err)
		}
	}
	original := gifFixture(t, 0)
	for end := 6; end < len(original); end++ {
		_, err := ConvertImage("truncated.gif", original[:end])
		requireFault(t, err, 422)
	}
	for _, mutate := range []func([]byte){
		func(data []byte) { data[20] = 1 },    // left+width outside logical screen
		func(data []byte) { data[24] = 0 },    // zero frame width
		func(data []byte) { data[29] = 1 },    // invalid LZW code size
		func(data []byte) { data[31] = 0xff }, // corrupt compressed codes
	} {
		data := gifBlocks(1, 1, 1)
		mutate(data)
		_, err := ConvertImage("broken.gif", data)
		requireFault(t, err, 422)
	}
	for _, extension := range [][]byte{
		{0x21, 0xf9, 4, 0x10, 0, 0, 0, 0}, // undefined disposal
		{0x21, 0xf9, 4, 0, 0, 0, 0, 1},    // missing extension terminator
		{0x21, 0xff, 11, 'x'},             // truncated application header
		{0x21, 0xfe, 255},                 // truncated comment block
		{0x21, 0x02, 0},                   // unknown extension
	} {
		base := gifBlocks(1, 1, 1)
		data := append(append(append([]byte{}, base[:19]...), extension...), base[19:]...)
		_, err := ConvertImage("broken.gif", data)
		requireFault(t, err, 422)
	}
	// GIF87a and single-frame images are accepted without upgrading their header.
	static := gifBlocks(1, 1, 1)
	copy(static[:6], "GIF87a")
	got, err := ConvertImage("single.gif", static)
	if err != nil || !bytes.Equal(got, static) {
		t.Fatalf("single-frame GIF changed: %v", err)
	}
}
