//go:build linux

package derivatives

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/image/webp"
	"golang.org/x/sys/unix"
)

func TestEncoderLocalFiles(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "pretends-to-be.svg")
	if err := os.WriteFile(input, encoderTestPNG(t, 7, 9), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := InspectImage(input)
	encoderTestCode(t, err, "")
	if info.Format != "png" || info.Width != 7 || info.Height != 9 {
		t.Fatalf("info = %+v", info)
	}
	for _, invalid := range []string{"", "https://example.invalid/a", "file:///etc/passwd", "../image.png", "safe/../image.png", input + "[page=0]", "x\x00y"} {
		_, err := InspectImage(invalid)
		encoderTestCode(t, err, "invalid_local_path")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(input, link); err != nil {
		t.Fatal(err)
	}
	_, err = InspectImage(link)
	encoderTestCode(t, err, "invalid_local_path")
	parent := filepath.Join(dir, "linked-dir")
	if err := os.Symlink(dir, parent); err != nil {
		t.Fatal(err)
	}
	_, err = InspectImage(filepath.Join(parent, filepath.Base(input)))
	encoderTestCode(t, err, "invalid_local_path")
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = InspectImage(fifo)
	encoderTestCode(t, err, "invalid_local_path")
	hardlink := filepath.Join(dir, "hardlink")
	if err := os.Link(input, hardlink); err != nil {
		t.Fatal(err)
	}
	_, err = InspectImage(hardlink)
	encoderTestCode(t, err, "invalid_local_path")
	f, err := os.Create(filepath.Join(dir, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(encoderMaxSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = InspectImage(f.Name())
	encoderTestCode(t, err, "skipped_limit")
}

func TestEncoderPinnedPathAndSafeStage(t *testing.T) {
	dir := t.TempDir()
	data := encoderTestPNG(t, 7, 9)
	path := filepath.Join(dir, "input")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := encoderOpenInput(path)
	encoderTestCode(t, err, "")
	defer f.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", path); err != nil {
		t.Fatal(err)
	}
	m, err := encoderInspectFile(context.Background(), f)
	encoderTestCode(t, err, "")
	if m.Width != 7 {
		t.Fatal("inspected replacement instead of pinned descriptor")
	}
	parent, err := encoderOpenDir(dir)
	encoderTestCode(t, err, "")
	defer parent.Close()
	stage, name, err := encoderStage(parent)
	encoderTestCode(t, err, "")
	stat, err := stage.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0700 {
		t.Fatalf("stage mode = %o", stat.Mode().Perm())
	}
	stage.Close()
	if err := unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR); err != nil {
		t.Fatal(err)
	}
}

func TestEncoderCommandIsFixedAndBounded(t *testing.T) {
	args := encoderThumbnailArgs(480, 4096)
	joined := strings.Join(args, " ")
	for _, required := range []string{"/proc/self/fd/3", "/proc/self/fd/4/encoded.webp[Q=90,strip,alpha_q=100,effort=4]", "--size=down", "--crop=none", "--export-profile=srgb", "--fail-on=warning"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing fixed option %q: %v", required, args)
		}
	}
	for _, forbidden := range []string{"--no-rotate", "https://", "--linear", "--lossless", "sh -c"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden option %s", forbidden)
		}
	}
	limits := strings.Join(encoderLimitArgs(encoderMaxRSS), " ")
	for _, required := range []string{"--as=", "--rss=", "--cpu=", "--fsize=", "--core=0:0", "--nofile=64:64", "/usr/bin/vips", "--vips-concurrency=1", "--vips-cache-max=0"} {
		if !strings.Contains(limits, required) {
			t.Fatalf("missing limit %s", required)
		}
	}
	t.Setenv("LD_PRELOAD", "/sensitive/evil.so")
	t.Setenv("HTTPS_PROXY", "https://secret.invalid/")
	if env := strings.Join(encoderChildEnv(2), " "); strings.Contains(env, "secret") || strings.Contains(env, "LD_PRELOAD") || !strings.Contains(env, "TMPDIR=/proc/self/fd/4") {
		t.Fatalf("unsafe env: %s", env)
	}
	var log encoderBoundedLog
	data := bytes.Repeat([]byte("x"), 64<<10)
	n, err := log.Write(data)
	if err != nil || n != len(data) || len(log.text()) != 16<<10 {
		t.Fatal("unbounded or short child log")
	}
}

func TestEncoderMemoryBudget(t *testing.T) {
	for _, tc := range []struct {
		sample encoderMemorySample
		want   int64
		code   string
	}{
		{encoderMemorySample{parentRSS: 100 << 20}, 320 << 20, ""},
		{encoderMemorySample{parentRSS: 500 << 20}, 204 << 20, ""},
		{encoderMemorySample{parentRSS: 100 << 20, containerUsed: 350 << 20, containerLimit: 512 << 20}, 0, "encode_resource_limit"},
		{encoderMemorySample{parentRSS: 600 << 20}, 0, "encode_resource_limit"},
		{encoderMemorySample{}, 0, "encode_resource_limit"},
		{encoderMemorySample{parentRSS: 200 << 20, containerUsed: 100 << 20, containerLimit: 512 << 20}, 248 << 20, ""},
	} {
		budget, err := encoderChildBudget(tc.sample)
		encoderTestCode(t, err, tc.code)
		if budget != tc.want {
			t.Fatalf("budget %d want %d for %+v", budget, tc.want, tc.sample)
		}
	}
	if encoderRSS("self") <= 0 {
		t.Fatal("actual process RSS unavailable")
	}
}

func TestEncoderGateCancellation(t *testing.T) {
	encoderTestCode(t, encoderAcquire(context.Background()), "")
	defer func() { <-encoderGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := encoderAcquire(ctx)
	encoderTestCode(t, err, "encode_timeout")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func encoderRequireVips(t *testing.T) {
	t.Helper()
	_, vipsErr := os.Stat(encoderVipsPath)
	_, limitErr := os.Stat(encoderPrlimitPath)
	if vipsErr != nil || limitErr != nil {
		t.Skip("requires image-installed /usr/bin/vips and /usr/bin/prlimit")
	}
	if err := EncoderHealth(); err != nil {
		t.Fatalf("installed encoder is not healthy: %v", err)
	}
}

func encoderAssertOutput(t *testing.T, out Encoded, width, height int) image.Image {
	t.Helper()
	if out.Width != width || out.Height != height || out.Bytes <= 0 || len(out.SHA256) != 64 {
		t.Fatalf("output = %+v", out)
	}
	data, err := os.ReadFile(out.File)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if out.Bytes != int64(len(data)) || out.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("actual bytes/hash do not match")
	}
	img, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output pixel decode: %v", err)
	}
	if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		t.Fatal("decoded dimensions differ")
	}
	m, err := encoderInspect(context.Background(), bytes.NewReader(data), int64(len(data)))
	encoderTestCode(t, err, "")
	if m.metadata || m.orientation != 1 {
		t.Fatal("metadata was not stripped")
	}
	st, err := os.Stat(out.File)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("output is not private")
	}
	return img
}

func TestEncoderIntegrationPipelineStages(t *testing.T) {
	encoderRequireVips(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "source")
	if err := os.WriteFile(input, encoderTestPNG(t, 800, 100), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := encoderOpenInput(input)
	encoderTestCode(t, err, "")
	defer f.Close()
	parent, err := encoderOpenDir(dir)
	encoderTestCode(t, err, "")
	defer parent.Close()
	stage, _, err := encoderStage(parent)
	encoderTestCode(t, err, "")
	defer stage.Close()
	if err := encoderRunVips(context.Background(), encoderThumbnailArgs(480, 4096), []*os.File{f, stage}, nil); err != nil {
		t.Fatalf("thumbnail stage: %v", err)
	}
	raw, err := encoderOpenRegularAt(stage, "encoded.webp")
	encoderTestCode(t, err, "")
	defer raw.Close()
	st, err := raw.Stat()
	if err != nil {
		t.Fatal(err)
	}
	result, err := os.CreateTemp(dir, "stripped-")
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if err := encoderStripWebP(context.Background(), raw, st.Size(), result); err != nil {
		t.Fatalf("strip stage: %v", err)
	}
	st, err = result.Stat()
	if err != nil {
		t.Fatal(err)
	}
	m, err := encoderInspect(context.Background(), result, st.Size())
	if err != nil {
		t.Fatalf("inspection stage: %v", err)
	}
	if m.metadata || m.orientation != 1 {
		t.Fatalf("metadata remains: %+v", m)
	}
	if !encoderValidOutputDimensions(ImageInfo{Width: 800, Height: 100}, m.ImageInfo, "thumb", 480, 60) {
		t.Fatalf("dimensions stage: %+v", m)
	}
	if err := encoderRunVips(context.Background(), []string{"avg", "/proc/self/fd/3[fail_on=warning]"}, []*os.File{result}, nil); err != nil {
		t.Fatalf("pixel validation stage: %v", err)
	}
}

func TestEncoderIntegrationRoundTrip(t *testing.T) {
	encoderRequireVips(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "source.bin")
	if err := os.WriteFile(input, encoderTestPNG(t, 800, 100), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := Encode(context.Background(), input, filepath.Join(dir, "thumb.tmp"), "thumb")
	encoderTestCode(t, err, "")
	img := encoderAssertOutput(t, out, 480, 60)
	_, _, _, alpha := img.At(0, 0).RGBA()
	if alpha > 1000 {
		t.Fatalf("transparent edge became opaque: %d", alpha)
	}
	_, _, _, alpha = img.At(479, 30).RGBA()
	if alpha < 64000 {
		t.Fatalf("opaque edge alpha: %d", alpha)
	}
	reader, err := Encode(context.Background(), input, filepath.Join(dir, "reader.tmp"), "reader")
	encoderTestCode(t, err, "")
	encoderAssertOutput(t, reader, 800, 100)
	// The static WebP source itself must also be decoded and re-encoded.
	webpOut, err := Encode(context.Background(), reader.File, filepath.Join(dir, "from-webp.tmp"), "thumb")
	encoderTestCode(t, err, "")
	encoderAssertOutput(t, webpOut, 480, 60)
	_, err = Encode(context.Background(), input, out.File, "thumb")
	encoderTestCode(t, err, "output_exists")
	check, err := os.ReadFile(out.File)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(check)
	if hex.EncodeToString(sum[:]) != out.SHA256 {
		t.Fatal("existing file overwritten")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".encoder-") {
			t.Fatalf("stage leak: %s", entry.Name())
		}
	}
}

func TestEncoderIntegrationOrientationAndColor(t *testing.T) {
	encoderRequireVips(t)
	dir := t.TempDir()
	// Red left and blue right must become red top and blue bottom for EXIF 6.
	img := image.NewRGBA(image.Rect(0, 0, 60, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 60; x++ {
			c := color.RGBA{B: 255, A: 255}
			if x < 30 {
				c = color.RGBA{R: 255, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "oriented")
	if err := os.WriteFile(input, encoderTestAddJPEGEXIF(jpg.Bytes(), encoderTestEXIF(6, binary.LittleEndian)), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := Encode(context.Background(), input, filepath.Join(dir, "upright"), "reader")
	encoderTestCode(t, err, "")
	decoded := encoderAssertOutput(t, out, 30, 60)
	r, _, b, _ := decoded.At(15, 10).RGBA()
	if r < b+20000 {
		t.Fatal("top not rotated red")
	}
	r, _, b, _ = decoded.At(15, 50).RGBA()
	if b < r+20000 {
		t.Fatal("bottom not rotated blue")
	}
	// Build an actual p3-tagged source with libvips; the expected color is
	// computed by libvips' explicit ICC transform, not a made-up fixture.
	colorFixture := image.NewRGBA(image.Rect(0, 0, 60, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 60; x++ {
			c := color.RGBA{R: 45, G: 140, B: 210, A: 255}
			if x < 30 {
				c = color.RGBA{R: 200, G: 90, B: 30, A: 255}
			}
			colorFixture.SetRGBA(x, y, c)
		}
	}
	var plain bytes.Buffer
	if err := png.Encode(&plain, colorFixture); err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(dir, "plain.png")
	if err := os.WriteFile(plainPath, plain.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	p3 := filepath.Join(dir, "p3.png")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := encoderRunVips(ctx, []string{"pngsave", plainPath, p3, "--profile=p3"}, nil, nil); err != nil {
		t.Fatalf("p3 fixture creation: %v", err)
	}
	expected := filepath.Join(dir, "expected.png")
	if err := encoderRunVips(ctx, []string{"icc_transform", p3, expected, "srgb", "--embedded", "--intent=relative"}, nil, nil); err != nil {
		t.Fatalf("ICC expected conversion: %v", err)
	}
	out, err = Encode(context.Background(), p3, filepath.Join(dir, "srgb"), "reader")
	encoderTestCode(t, err, "")
	decoded = encoderAssertOutput(t, out, 60, 30)
	f, err := os.Open(expected)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range []image.Point{{10, 15}, {50, 15}} {
		a := color.NRGBAModel.Convert(decoded.At(pt.X, pt.Y)).(color.NRGBA)
		b := color.NRGBAModel.Convert(want.At(pt.X, pt.Y)).(color.NRGBA)
		t.Logf("ICC %v: source=%v got=%v reference=%v", pt, colorFixture.At(pt.X, pt.Y), a, b)
		for i, pair := range [][2]byte{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}} {
			d := int(pair[0]) - int(pair[1])
			if d > 14 || d < -14 {
				t.Fatalf("ICC pixel %v channel %d: got %v want %v", pt, i, a, b)
			}
		}
	}
}

func TestEncoderIntegrationCorruptionAndLimits(t *testing.T) {
	encoderRequireVips(t)
	dir := t.TempDir()
	valid := encoderTestPNG(t, 100, 100)
	corrupt := append([]byte{}, valid[:33]...)
	corrupt = append(corrupt, encoderTestPNGChunk("IDAT", []byte("not a zlib raster"))...)
	corrupt = append(corrupt, encoderTestPNGChunk("IEND", nil)...)
	input := filepath.Join(dir, "corrupt")
	if err := os.WriteFile(input, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "must-not-exist")
	_, err := Encode(context.Background(), input, output, "thumb")
	if err == nil {
		t.Fatal("corrupt pixel stream accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed encode published output: %v", err)
	}
	// A corrupt WebP may have entirely valid dimensions. The pixel verifier
	// must reject it even though the deliberately header-only inspector passes.
	badWebP := encoderTestRIFF(encoderTestVP8L(100, 100))
	path := filepath.Join(dir, "bad.webp")
	if err := os.WriteFile(path, badWebP, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = InspectImage(path)
	encoderTestCode(t, err, "")
	f, err := encoderOpenInput(path)
	encoderTestCode(t, err, "")
	defer f.Close()
	err = encoderRunVips(context.Background(), []string{"avg", "/proc/self/fd/3[fail_on=warning]"}, []*os.File{f}, nil)
	encoderTestCode(t, err, "encode_failed")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".encoder-") {
			t.Fatalf("failed stage leak: %s", entry.Name())
		}
	}
}

func TestEncoderIntegrationPanorama(t *testing.T) {
	encoderRequireVips(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "panorama")
	if err := os.WriteFile(input, encoderTestPNG(t, 9528, 50), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := Encode(context.Background(), input, filepath.Join(dir, "small"), "thumb")
	encoderTestCode(t, err, "")
	encoderAssertOutput(t, out, 480, 3)
}

// A real self-exec process tree tests the SAME group-kill callback without a
// shell, external network, or a large image. The waiter keeps a live leader so
// there is no PID-reuse window while the descendant is being checked.
func TestEncoderProcessHelper(t *testing.T) {
	mode := os.Getenv("ENCODER_PROCESS_HELPER")
	if mode == "" {
		return
	}
	if mode == "leaf" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestEncoderProcessHelper$")
	child.Env = append(os.Environ(), "ENCODER_PROCESS_HELPER=leaf")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	_, _ = io.WriteString(os.Stdout, strconv.Itoa(child.Process.Pid)+"\n")
	_ = child.Wait()
	os.Exit(0)
}

func TestEncoderProcessGroupCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEncoderProcessHelper$")
	cmd.Env = append(os.Environ(), "ENCODER_PROCESS_HELPER=parent")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return encoderKillGroup(cmd) }
	cmd.WaitDelay = time.Second
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer encoderKillGroup(cmd)
	var line []byte
	var one [1]byte
	for len(line) < 32 {
		if _, err := pipe.Read(one[:]); err != nil {
			t.Fatal(err)
		}
		if one[0] == '\n' {
			break
		}
		line = append(line, one[0])
	}
	pid, err := strconv.Atoi(string(line))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled command succeeded")
	}
	// Descendants can briefly be zombies until the runtime/container init
	// reaps them, but none may remain running after group cancellation.
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		end := strings.LastIndex(string(data), ") ")
		if end < 0 || !strings.HasPrefix(string(data[end+2:]), "Z ") {
			t.Fatalf("descendant still alive: %s", data)
		}
	}
}
