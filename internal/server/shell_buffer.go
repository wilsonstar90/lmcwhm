package server

import "sync"

type tailBuffer struct {
	mu        sync.Mutex
	buf       []byte
	keep      int
	truncated bool
}

func newTailBuffer(keep int) *tailBuffer {
	return &tailBuffer{keep: keep, buf: make([]byte, 0, keep)}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.keep <= 0 {
		b.truncated = b.truncated || n > 0
		return n, nil
	}
	if len(p) >= b.keep {
		b.truncated = b.truncated || len(b.buf) > 0 || len(p) > b.keep
		b.buf = append(b.buf[:0], p[len(p)-b.keep:]...)
		return n, nil
	}
	if overflow := len(b.buf) + len(p) - b.keep; overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:len(b.buf)-overflow]
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.truncated {
		return "[… truncated]\n" + string(b.buf)
	}
	return string(b.buf)
}
