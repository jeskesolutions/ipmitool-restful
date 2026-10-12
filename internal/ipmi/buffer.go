package ipmi

import "bytes"

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}

	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }
