//go:build !linux

package derivatives

import (
	"context"
	"os"
)

// The worker's descriptor-relative path handling, /proc memory monitoring and
// prlimit process isolation deliberately fail closed outside the Linux image.
func encoderOpenInput(string) (*os.File, error) {
	return nil, encoderError("encoder_unavailable")
}
func encoderHealth(context.Context) error {
	return encoderError("encoder_unavailable")
}
func encoderEncodeLocal(context.Context, string, string, string) (Encoded, error) {
	return Encoded{}, encoderError("encoder_unavailable")
}
