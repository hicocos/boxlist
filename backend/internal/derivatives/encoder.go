// Package derivatives implements bounded, local image derivative encoding.
package derivatives

import (
	"context"
	"errors"
	"math"
	"time"
)

// ProfileVersion changes whenever output encoding parameters change. Raising
// source admission limits alone preserves existing compatible v1 artifacts.
const ProfileVersion = 1

const (
	encoderMaxSourcePixels int64 = 48_000_000
	encoderMaxSourceBytes  int64 = 128 << 20
	encoderMaxOutputBytes  int64 = 64 << 20
	encoderTimeout               = 40 * time.Second
	encoderHealthTimeout         = 10 * time.Second
)

// ImageInfo describes the upright image, after applying EXIF orientation.
// InspectImage reads headers, not the entire decoded pixel raster. A successful
// inspection is not a promise that the compressed pixel data is undamaged.
type ImageInfo struct {
	Width, Height int
	Format        string
	Animated      bool
}

// Encoded describes the verified file. File is an absolute local path, not a URL.
type Encoded struct {
	Profile       string
	Width, Height int
	Bytes         int64
	SHA256        string
	File          string
}

// EncodeError exposes a stable reason, never a local filename, signed URL, or
// child-process diagnostic. Callers can use errors.As and errors.Is.
type EncodeError struct {
	Code string
	Err  error
}

func (e *EncodeError) Error() string {
	if e == nil {
		return "derivative encoding failed"
	}
	return "derivative encoding: " + e.Code
}

func (e *EncodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func encoderError(code string) error { return &EncodeError{Code: code} }

func encoderContextError(err error) error {
	code := "encode_canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "encode_timeout"
	}
	return &EncodeError{Code: code, Err: err}
}

type encoderProfile struct {
	width, height int
	pixels        int64
}

func encoderPolicy(profile string) (encoderProfile, error) {
	switch profile {
	case "thumb":
		return encoderProfile{480, 4096, 2_000_000}, nil
	case "reader":
		return encoderProfile{1600, 12000, 12_000_000}, nil
	default:
		return encoderProfile{}, encoderError("invalid_profile")
	}
}

func encoderCheckPixels(width, height int) error {
	// Division avoids overflow even for malicious integer-sized dimensions.
	if width < 1 || height < 1 || int64(width) > encoderMaxSourcePixels/int64(height) {
		return encoderError("skipped_limit")
	}
	return nil
}

func encoderDimensions(info ImageInfo, profile string) (int, int, error) {
	_, width, height, err := encoderPlan(info, profile)
	return width, height, err
}

// Keep the fit box distinct from rounded pixel dimensions. Passing two rounded
// dimensions as the fit box can over-shrink a panoramic image (480x2 might fit
// at only 381x2). Only the limiting axis should determine the uniform scale.
func encoderPlan(info ImageInfo, profile string) (encoderProfile, int, int, error) {
	p, err := encoderPolicy(profile)
	if err != nil {
		return p, 0, 0, err
	}
	if err = encoderCheckPixels(info.Width, info.Height); err != nil {
		return p, 0, 0, err
	}
	w, h := float64(info.Width), float64(info.Height)
	widthScale := math.Min(1, float64(p.width)/w)
	heightScale := math.Min(1, float64(p.height)/h)
	pixelScale := math.Min(1, math.Sqrt(float64(p.pixels)/(w*h)))
	box := p
	if pixelScale < math.Min(widthScale, heightScale) {
		box.width = max(1, int(math.Floor(w*pixelScale)))
	}
	var width, height int
	for {
		scale := math.Min(1, math.Min(float64(box.width)/w, float64(box.height)/h))
		width = max(1, int(math.Round(w*scale)))
		height = max(1, int(math.Round(h*scale)))
		if int64(width)*int64(height) <= p.pixels {
			break
		}
		box.width-- // account for the final half-pixel rounding at the cap
	}
	if profile == "reader" && heightScale < 1 && heightScale <= widthScale &&
		heightScale <= pixelScale && width < min(info.Width, 960) {
		return p, 0, 0, encoderError("long_image_requires_original")
	}
	return box, width, height, nil
}

// All invocations, including health probes, share a cancellable single-worker
// gate. The manager should also serialize downloads and processing.
var encoderGate = make(chan struct{}, 1)

func encoderAcquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return encoderContextError(err)
	}
	select {
	case encoderGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return encoderContextError(ctx.Err())
	}
}

// EncoderHealth verifies the fixed executable, resource limiter, and the
// thumbnail, WebP-save and ICC-conversion capabilities. It never installs tools.
func EncoderHealth() error {
	ctx, cancel := context.WithTimeout(context.Background(), encoderHealthTimeout)
	defer cancel()
	if err := encoderAcquire(ctx); err != nil {
		return err
	}
	defer func() { <-encoderGate }()
	return encoderHealth(ctx)
}

// InspectImage accepts a regular local file, with no symlink path components.
// GIF, APNG, animated WebP, TIFF, SVG and all non-JPEG/PNG/WebP inputs are rejected.
func InspectImage(filename string) (ImageInfo, error) {
	f, err := encoderOpenInput(filename)
	if err != nil {
		return ImageInfo{}, err
	}
	defer f.Close()
	meta, err := encoderInspectFile(context.Background(), f)
	return meta.ImageInfo, err
}

// Encode writes a new, verified local WebP file. The caller must supply private,
// controlled work paths, an immutable downloaded input and a NONEXISTENT output;
// it must not expose these arguments as an HTTP/API arbitrary-file interface.
// Existing outputs are never overwritten. Publication into the permanent object
// store and database is the manager's responsibility. The manager also enforces
// its streaming 128 MiB download limit; the encoder repeats the file-size check.
func Encode(ctx context.Context, input, output, profile string) (Encoded, error) {
	if _, err := encoderPolicy(profile); err != nil {
		return Encoded{}, err
	}
	if ctx == nil {
		return Encoded{}, encoderError("invalid_context")
	}
	ctx, cancel := context.WithTimeout(ctx, encoderTimeout)
	defer cancel()
	if err := encoderAcquire(ctx); err != nil {
		return Encoded{}, err
	}
	defer func() { <-encoderGate }()
	return encoderEncodeLocal(ctx, input, output, profile)
}
