package iconlibrary

import (
	"bytes"
	"encoding/binary"
)

// EXIF is untrusted metadata: only IFD0's inline SHORT orientation is consulted.
// Malformed/unknown metadata is ignored, never followed as a pointer beyond the chunk.
func exifOrientation(data []byte) int {
	data = bytes.TrimPrefix(data, []byte("Exif\x00\x00"))
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	offset := uint64(order.Uint32(data[4:8]))
	if offset < 8 || offset+2 > uint64(len(data)) {
		return 1
	}
	n := uint64(order.Uint16(data[offset : offset+2]))
	if n > 4096 || offset+2+n*12 > uint64(len(data)) {
		return 1
	}
	for i := uint64(0); i < n; i++ {
		p := offset + 2 + i*12
		if order.Uint16(data[p:p+2]) == 274 && order.Uint16(data[p+2:p+4]) == 3 && order.Uint32(data[p+4:p+8]) == 1 {
			v := int(order.Uint16(data[p+8 : p+10]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
