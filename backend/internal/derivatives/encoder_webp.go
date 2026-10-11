package derivatives

import (
	"context"
	"encoding/binary"
	"io"
)

// encoderStripWebP compensates for the ineffective webpsave strip property in
// libvips 8.14.1. It runs ONLY on inspected local encoder output, after sRGB
// conversion. Keep pixel chunks byte-for-byte; remove every non-pixel chunk and
// clear the corresponding VP8X flags. No compressed raster enters the Go heap.
func encoderStripWebP(ctx context.Context, src io.ReaderAt, size int64, dst io.WriteSeeker) error {
	if size < 12 || size > encoderMaxOutputBytes {
		return encoderError("invalid_output")
	}
	var riff [12]byte
	if encoderReadAt(src, 0, riff[:]) != nil || string(riff[:4]) != "RIFF" || string(riff[8:]) != "WEBP" || int64(binary.LittleEndian.Uint32(riff[4:8]))+8 != size {
		return encoderError("invalid_output")
	}
	if _, err := dst.Write(riff[:]); err != nil {
		return encoderError("output_io_error")
	}
	written := int64(12)
	pixels := 0
	var head [8]byte
	for off, count := int64(12), 0; off < size; count++ {
		if ctx.Err() != nil {
			return encoderContextError(ctx.Err())
		}
		if count >= encoderMaxChunks || size-off < 8 || encoderReadAt(src, off, head[:]) != nil {
			return encoderError("invalid_output")
		}
		n := int64(binary.LittleEndian.Uint32(head[4:]))
		length := n + (n & 1)
		if length > size-off-8 {
			return encoderError("invalid_output")
		}
		switch string(head[:4]) {
		case "VP8X":
			var data [10]byte
			if n != 10 || count != 0 || encoderReadAt(src, off+8, data[:]) != nil || data[0]&2 != 0 {
				return encoderError("invalid_output")
			}
			data[0] &^= 0x2c // ICC, EXIF, XMP: alpha remains unchanged.
			if _, err := dst.Write(head[:]); err != nil {
				return encoderError("output_io_error")
			}
			if _, err := dst.Write(data[:]); err != nil {
				return encoderError("output_io_error")
			}
			written += 18
		case "VP8 ", "VP8L", "ALPH":
			if string(head[:4]) != "ALPH" {
				pixels++
			}
			if _, err := dst.Write(head[:]); err != nil {
				return encoderError("output_io_error")
			}
			got, err := io.Copy(dst, encoderContextReader{ctx, io.NewSectionReader(src, off+8, length)})
			if err != nil || got != length {
				if ctx.Err() != nil {
					return encoderContextError(ctx.Err())
				}
				return encoderError("output_io_error")
			}
			written += 8 + length
		case "ANIM", "ANMF":
			return encoderError("invalid_output")
		}
		off += 8 + length
	}
	if pixels != 1 || written > encoderMaxOutputBytes {
		return encoderError("invalid_output")
	}
	binary.LittleEndian.PutUint32(riff[4:8], uint32(written-8))
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return encoderError("output_io_error")
	}
	if _, err := dst.Write(riff[:]); err != nil {
		return encoderError("output_io_error")
	}
	return nil
}
