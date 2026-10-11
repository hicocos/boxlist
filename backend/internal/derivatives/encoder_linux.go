//go:build linux

package derivatives

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	encoderVipsPath        = "/usr/bin/vips"
	encoderPrlimitPath     = "/usr/bin/prlimit"
	encoderContainerBudget = int64(768 << 20)
	encoderMemoryReserve   = int64(64 << 20)
	encoderMaxRSS          = int64(320 << 20)
	encoderMinRSS          = int64(128 << 20)
	encoderMaxAS           = int64(512 << 20)
)

// Walk from a pinned root descriptor with O_NOFOLLOW at EVERY component. A
// preceding Lstat/EvalSymlinks check alone would have a TOCTOU race. Relative
// application data paths are supported, but URLs, dot-dot and vips option-string
// syntax are not. The child only sees fixed /proc/self/fd paths, never this text.
func encoderLocalPath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\x00[]:\\") {
		return "", encoderError("invalid_local_path")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return "", encoderError("invalid_local_path")
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", encoderError("invalid_local_path")
		}
	}
	absolute, err := filepath.Abs(name)
	if err != nil || absolute == "/" {
		return "", encoderError("invalid_local_path")
	}
	return absolute, nil
}

func encoderOpenDir(absolute string) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, encoderError("invalid_local_path")
	}
	for _, part := range strings.Split(strings.TrimPrefix(absolute, "/"), "/") {
		if part == "" {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, encoderError("invalid_local_path")
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "encoder-directory"), nil
}

func encoderOpenInput(filename string) (*os.File, error) {
	absolute, err := encoderLocalPath(filename)
	if err != nil {
		return nil, err
	}
	dir, err := encoderOpenDir(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return encoderOpenRegularAt(dir, filepath.Base(absolute))
}

func encoderOpenRegularAt(dir *os.File, name string) (*os.File, error) {
	// O_NONBLOCK prevents a FIFO from hanging before we can reject its type.
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, encoderError("invalid_local_path")
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		_ = unix.Close(fd)
		return nil, encoderError("invalid_local_path")
	}
	return os.NewFile(uintptr(fd), "encoder-image"), nil
}

func encoderHealth(ctx context.Context) error {
	for _, path := range []string{encoderVipsPath, encoderPrlimitPath} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return encoderError("encoder_unavailable")
		}
	}
	for _, operation := range []string{"thumbnail", "webpsave", "icc_transform"} {
		var out encoderBoundedLog
		if err := encoderRunVips(ctx, []string{"--list=" + operation}, nil, &out); err != nil || !strings.Contains(out.text(), operation) {
			return encoderError("encoder_unavailable")
		}
	}
	return nil
}

func encoderEncodeLocal(ctx context.Context, input, output, profile string) (Encoded, error) {
	f, err := encoderOpenInput(input)
	if err != nil {
		return Encoded{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return Encoded{}, encoderError("invalid_local_path")
	}
	meta, err := encoderInspectFile(ctx, f)
	if err != nil {
		return Encoded{}, err
	}
	box, width, height, err := encoderPlan(meta.ImageInfo, profile)
	if err != nil {
		return Encoded{}, err
	}
	absolute, err := encoderLocalPath(output)
	if err != nil {
		return Encoded{}, err
	}
	parent, err := encoderOpenDir(filepath.Dir(absolute))
	if err != nil {
		return Encoded{}, err
	}
	defer parent.Close()
	base := filepath.Base(absolute)
	var existing unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), base, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		return Encoded{}, encoderError("output_exists")
	} else if !errors.Is(err, unix.ENOENT) {
		return Encoded{}, encoderError("invalid_local_path")
	}
	stage, name, err := encoderStage(parent)
	if err != nil {
		return Encoded{}, err
	}
	defer stage.Close()
	defer func() {
		_ = unix.Unlinkat(int(stage.Fd()), "result.webp", 0)
		_ = unix.Unlinkat(int(stage.Fd()), "encoded.webp", 0)
		_ = unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
	}()
	// The private stage directory is inherited as FD 4; source is FD 3.
	// libvips thumbnail auto-orients by default, uses shrink-to-fit with no crop,
	// and transforms embedded ICC to built-in srgb BEFORE container tag stripping.
	args := encoderThumbnailArgs(box.width, box.height)
	if err := encoderRunVips(ctx, args, []*os.File{f, stage}, nil); err != nil {
		return Encoded{}, err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return Encoded{}, encoderError("source_changed")
	}
	raw, err := encoderOpenRegularAt(stage, "encoded.webp")
	if err != nil {
		return Encoded{}, encoderError("invalid_output")
	}
	defer raw.Close()
	rawStat, err := raw.Stat()
	if err != nil || rawStat.Size() <= 0 || rawStat.Size() > encoderMaxOutputBytes {
		return Encoded{}, encoderError("invalid_output")
	}
	rawMeta, err := encoderInspect(ctx, raw, rawStat.Size())
	if err != nil || rawMeta.Format != "webp" || rawMeta.orientation != 1 ||
		!encoderValidOutputDimensions(meta.ImageInfo, rawMeta.ImageInfo, profile, width, height) {
		return Encoded{}, encoderError("invalid_output")
	}
	// libvips 8.14.1 webpsave's --strip does not actually remove ICC/EXIF.
	// Color conversion has already happened. Strip container metadata without
	// decoding/re-encoding pixels, then validate the actual published bytes.
	outFD, err := unix.Openat(int(stage.Fd()), "result.webp", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return Encoded{}, encoderError("invalid_output")
	}
	result := os.NewFile(uintptr(outFD), "result.webp")
	defer result.Close()
	if err := encoderStripWebP(ctx, raw, rawStat.Size(), result); err != nil {
		return Encoded{}, err
	}
	info, err := result.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > encoderMaxOutputBytes {
		return Encoded{}, encoderError("invalid_output")
	}
	out, err := encoderInspect(ctx, result, info.Size())
	if err != nil || out.Format != "webp" || out.Animated || out.metadata || out.orientation != 1 ||
		!encoderValidOutputDimensions(meta.ImageInfo, out.ImageInfo, profile, width, height) {
		return Encoded{}, encoderError("invalid_output")
	}
	// Reading just VP8X/DecodeConfig is NOT output verification. Force a full
	// pixel evaluation in a SECOND limited subprocess (not in the Go server).
	if err := encoderRunVips(ctx, []string{"avg", "/proc/self/fd/3[fail_on=warning]"}, []*os.File{result}, nil); err != nil {
		if encoderIsCode(err, "encode_failed") {
			return Encoded{}, encoderError("invalid_output")
		}
		return Encoded{}, err
	}
	if _, err := result.Seek(0, io.SeekStart); err != nil {
		return Encoded{}, encoderError("invalid_output")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, encoderContextReader{ctx, io.LimitReader(result, encoderMaxOutputBytes+1)})
	if err != nil || n != info.Size() {
		if ctx.Err() != nil {
			return Encoded{}, encoderContextError(ctx.Err())
		}
		return Encoded{}, encoderError("invalid_output")
	}
	if err := result.Chmod(0600); err != nil || result.Sync() != nil {
		return Encoded{}, encoderError("output_io_error")
	}
	if ctx.Err() != nil {
		return Encoded{}, encoderContextError(ctx.Err())
	}
	// Atomic, no-replace move relative to pinned directory descriptors. Never
	// follow, truncate or overwrite a concurrently-created file or symlink.
	if err := unix.Renameat2(int(stage.Fd()), "result.webp", int(parent.Fd()), base, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return Encoded{}, encoderError("output_exists")
		}
		return Encoded{}, encoderError("output_io_error")
	}
	if err := parent.Sync(); err != nil {
		_ = unix.Unlinkat(int(parent.Fd()), base, 0)
		return Encoded{}, encoderError("output_io_error")
	}
	return Encoded{Profile: profile, Width: out.Width, Height: out.Height, Bytes: n, SHA256: hex.EncodeToString(hash.Sum(nil)), File: absolute}, nil
}

func encoderThumbnailArgs(width, height int) []string {
	return []string{
		"thumbnail", "/proc/self/fd/3", "/proc/self/fd/4/encoded.webp[Q=90,strip,alpha_q=100,effort=4]",
		strconv.Itoa(width), "--height=" + strconv.Itoa(height), "--size=down", "--crop=none",
		"--export-profile=srgb", "--intent=relative", "--fail-on=warning",
	}
}

func encoderValidOutputDimensions(src, out ImageInfo, profile string, width, height int) bool {
	policy, err := encoderPolicy(profile)
	if err != nil || out.Width < 1 || out.Height < 1 || out.Width > src.Width || out.Height > src.Height ||
		out.Width > policy.width || out.Height > policy.height || int64(out.Width)*int64(out.Height) > policy.pixels {
		return false
	}
	// libvips rounds pixels when fitting a bounding box. At most one pixel of
	// rounding may differ from the conservative integer box; no cropping or
	// arbitrary "valid but wrong" output can pass by dimensions alone.
	if out.Width < width-1 || out.Width > width+1 || out.Height < height-1 || out.Height > height+1 {
		return false
	}
	diff := int64(out.Width)*int64(src.Height) - int64(out.Height)*int64(src.Width)
	if diff < 0 {
		diff = -diff
	}
	return diff <= int64(max(src.Width, src.Height))
}

func encoderStage(parent *os.File) (*os.File, string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", encoderError("output_io_error")
	}
	name := ".encoder-" + hex.EncodeToString(random[:])
	if err := unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil {
		return nil, "", encoderError("output_io_error")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
		return nil, "", encoderError("output_io_error")
	}
	return os.NewFile(uintptr(fd), "encoder-stage"), name, nil
}

// Child output is never surfaced; health needs only a small capability listing.
// Return the original write length so a verbose process cannot deadlock on a
// full pipe. Stderr is discarded, not retained in errors/logs with signed URLs.
type encoderBoundedLog struct{ data []byte }

func (w *encoderBoundedLog) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.data) < 16<<10 {
		w.data = append(w.data, p[:min(len(p), (16<<10)-len(w.data))]...)
	}
	return n, nil
}
func (w *encoderBoundedLog) text() string { return string(w.data) }

type encoderMemorySample struct {
	parentRSS, containerUsed, containerLimit int64
}

func encoderChildBudget(s encoderMemorySample) (int64, error) {
	limit := encoderContainerBudget
	if s.containerLimit > 0 && s.containerLimit < limit {
		limit = s.containerLimit
	}
	used := max(s.parentRSS, s.containerUsed)
	budget := min(encoderMaxRSS, limit-used-encoderMemoryReserve)
	if s.parentRSS <= 0 || budget < encoderMinRSS {
		return 0, encoderError("encode_resource_limit")
	}
	return budget, nil
}

func encoderRSS(pid string) int64 {
	b, err := os.ReadFile("/proc/" + pid + "/statm")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(b))
	if len(parts) < 2 {
		return 0
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || n <= 0 || n > 1<<40 {
		return 0
	}
	return n * int64(os.Getpagesize())
}

func encoderCgroupValue(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 128 {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func encoderMemory() encoderMemorySample {
	s := encoderMemorySample{parentRSS: encoderRSS("self")}
	// These are the names visible in a Docker cgroup namespace. Also discover
	// nested v2/v1 membership when running without a private cgroup namespace.
	dirs := []string{"/sys/fs/cgroup", "/sys/fs/cgroup/memory"}
	if b, err := os.ReadFile("/proc/self/cgroup"); err == nil && len(b) < 64<<10 {
		for _, line := range strings.Split(string(b), "\n") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) != 3 || strings.Contains(parts[2], "..") {
				continue
			}
			root := "/sys/fs/cgroup"
			if parts[1] != "" {
				if !strings.Contains(","+parts[1]+",", ",memory,") {
					continue
				}
				root += "/memory"
			}
			for dir := filepath.Join(root, parts[2]); dir != root && strings.HasPrefix(dir, root+"/"); dir = filepath.Dir(dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	for _, dir := range dirs {
		for _, names := range [][2]string{{"memory.max", "memory.current"}, {"memory.limit_in_bytes", "memory.usage_in_bytes"}} {
			limit := encoderCgroupValue(filepath.Join(dir, names[0]))
			used := encoderCgroupValue(filepath.Join(dir, names[1]))
			if limit > 0 && (s.containerLimit == 0 || limit < s.containerLimit) {
				s.containerLimit = limit
			}
			// Only count usage from a constrained cgroup, not the host's entire
			// unlimited root cgroup (which includes unrelated services).
			if limit > 0 && used > s.containerUsed {
				s.containerUsed = used
			}
		}
	}
	return s
}

func encoderLimitArgs(rss int64) []string {
	// Linux ignores RLIMIT_RSS: the runtime RSS/container monitor below is
	// essential. RLIMIT_AS is the hard allocation backstop, not a claim about
	// resident peak memory. CPU, file size, fds, stack, memlock and core dumps
	// are independently bounded. Do not use RLIMIT_NPROC for a shared UID.
	as := min(encoderMaxAS, rss+(192<<20))
	return []string{
		"--as=" + strconv.FormatInt(as, 10), "--rss=" + strconv.FormatInt(rss, 10),
		"--cpu=30:32", "--fsize=" + strconv.FormatInt(encoderMaxOutputBytes, 10),
		"--nofile=64:64", "--core=0:0", "--memlock=0:0", "--stack=8388608:8388608", "--", encoderVipsPath,
		"--vips-concurrency=1", "--vips-cache-max=0", "--vips-cache-max-memory=16777216", "--vips-cache-max-files=4",
	}
}

func encoderChildEnv(extraFiles int) []string {
	tmp := "/tmp"
	if extraFiles >= 2 {
		tmp = "/proc/self/fd/4"
	}
	// Whitelist, never inherit LD_PRELOAD, VIPS_PATH, HTTP proxies, user plugin
	// paths, secrets or other settings that could alter decoder behaviour.
	return []string{
		"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=/nonexistent", "TMPDIR=" + tmp,
		"VIPS_CONCURRENCY=1", "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "MAGICK_THREAD_LIMIT=1",
		"MALLOC_ARENA_MAX=2", "VIPS_DISC_THRESHOLD=16777216",
	}
}

func encoderKillGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// All concurrently running decoder processes share the existing conservative
// memory budget. Reserve full child limits before launch, not only observed RSS;
// otherwise simultaneous startup can overcommit before the next RSS sample.
var encoderAdmission = struct {
	sync.Mutex
	reserved int64
	changed  chan struct{}
}{changed: make(chan struct{})}

func encoderReserve(ctx context.Context) (int64, func(), error) {
	return encoderReserveWith(ctx, encoderMemory)
}
func encoderReserveWith(ctx context.Context, snapshot func() encoderMemorySample) (int64, func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, nil, encoderContextError(err)
		}
		encoderAdmission.Lock()
		memory := snapshot()
		limit := encoderContainerBudget
		if memory.containerLimit > 0 {
			limit = min(limit, memory.containerLimit)
		}
		// Double-count reserved children against observed cgroup usage on purpose:
		// page cache and other application memory must never become unreserved.
		used := max(memory.containerUsed, memory.parentRSS) + encoderAdmission.reserved
		budget := min(encoderMaxRSS, limit-used-encoderMemoryReserve)
		if memory.parentRSS > 0 && budget >= encoderMinRSS {
			encoderAdmission.reserved += budget
			encoderAdmission.Unlock()
			var once sync.Once
			release := func() {
				once.Do(func() {
					encoderAdmission.Lock()
					encoderAdmission.reserved -= budget
					close(encoderAdmission.changed)
					encoderAdmission.changed = make(chan struct{})
					encoderAdmission.Unlock()
				})
			}
			return budget, release, nil
		}
		changed := encoderAdmission.changed
		encoderAdmission.Unlock()
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, nil, encoderContextError(ctx.Err())
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func encoderRunVips(ctx context.Context, operation []string, files []*os.File, stdout io.Writer) error {
	if err := ctx.Err(); err != nil {
		return encoderContextError(err)
	}
	rss, release, err := encoderReserve(ctx)
	if err != nil {
		return err
	}
	defer release()
	args := append(encoderLimitArgs(rss), operation...)
	cmd := exec.CommandContext(ctx, encoderPrlimitPath, args...)
	cmd.Dir = "/"
	cmd.Env = encoderChildEnv(len(files))
	cmd.ExtraFiles = files
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error { return encoderKillGroup(cmd) }
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return encoderError("encoder_unavailable")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	pid := strconv.Itoa(cmd.Process.Pid)
	for {
		select {
		case err := <-done:
			// A decoder should never fork, but clean up any remaining process
			// group members before returning, including on ordinary failures.
			_ = encoderKillGroup(cmd)
			if ctx.Err() != nil {
				return encoderContextError(ctx.Err())
			}
			if err != nil {
				return encoderError("encode_failed")
			}
			return nil
		case <-ticker.C:
			memory := encoderMemory()
			childRSS := encoderRSS(pid)
			limit := encoderContainerBudget
			if memory.containerLimit > 0 {
				limit = min(limit, memory.containerLimit)
			}
			used := max(memory.parentRSS+childRSS, memory.containerUsed)
			if childRSS > rss || used+encoderMemoryReserve > limit || memory.parentRSS <= 0 {
				_ = encoderKillGroup(cmd)
				<-done
				return encoderError("encode_resource_limit")
			}
		case <-ctx.Done():
			_ = encoderKillGroup(cmd)
			<-done
			return encoderContextError(ctx.Err())
		}
	}
}
