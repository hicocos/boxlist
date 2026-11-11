package hybrid_cache

import (
	"fmt"
	"io"
	"sort"
)

type BufferStore struct {
	blocks [][]byte
	ends   []int64
	size   int64
}

func (m *BufferStore) Size() int64 {
	return m.size
}

// 用于存储不复用的[]byte
func (m *BufferStore) Append(buf []byte) {
	m.size += int64(len(buf))
	m.blocks = append(m.blocks, buf)
	m.ends = append(m.ends, m.size)
}

func (m *BufferStore) Close() error {
	if len(m.blocks) > 0 {
		clear(m.blocks)
		m.blocks = m.blocks[:0]
		m.ends = m.ends[:0]
		m.size = 0
	}
	return nil
}

func (m *BufferStore) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 || off >= m.size {
		return 0, io.EOF
	}

	i := sort.Search(len(m.ends), func(i int) bool { return m.ends[i] > off })
	if i > 0 {
		off -= m.ends[i-1]
	}
	var n int
	for _, buf := range m.blocks[i:] {
		nn := copy(p[n:], buf[off:])
		n += nn
		if n == len(p) {
			return n, nil
		}
		off = 0
	}

	return n, io.EOF
}

func (m *BufferStore) WriteAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 || off >= m.size {
		return 0, io.ErrShortWrite
	}

	i := sort.Search(len(m.ends), func(i int) bool { return m.ends[i] > off })
	if i > 0 {
		off -= m.ends[i-1]
	}
	var n int
	for _, b := range m.blocks[i:] {
		nn := copy(b[off:], p[n:])
		n += nn
		if n == len(p) {
			return n, nil
		}
		off = 0
	}

	return n, io.ErrShortWrite
}

func (m *BufferStore) GrowTo(size int64) (err error) {
	if size <= m.size {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered in %v", r)
		}
	}()
	m.Append(make([]byte, size-m.size))
	return nil
}

var _ BackingStore = (*BufferStore)(nil)
