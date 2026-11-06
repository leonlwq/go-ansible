package ssh

import (
	"io"
	"strings"
)

const MaxCommandOutputBytes = 16 * 1024 * 1024 // 16MB 安全保护上限

// BoundedBuffer limits memory consumption to maxBytes, dropping excess bytes to prevent OOM.
type BoundedBuffer struct {
	buf       strings.Builder
	maxBytes  int64
	written   int64
	truncated bool
}

// NewBoundedBuffer creates a BoundedBuffer with specified maxBytes limit.
func NewBoundedBuffer(maxBytes int64) *BoundedBuffer {
	if maxBytes <= 0 {
		maxBytes = MaxCommandOutputBytes
	}
	return &BoundedBuffer{maxBytes: maxBytes}
}

func (b *BoundedBuffer) Write(p []byte) (n int, err error) {
	n = len(p)
	remaining := b.maxBytes - b.written
	if remaining <= 0 {
		b.truncated = true
		return n, nil
	}
	if int64(len(p)) > remaining {
		b.buf.Write(p[:remaining])
		b.written += remaining
		b.truncated = true
		return n, nil
	}
	b.buf.Write(p)
	b.written += int64(len(p))
	return n, nil
}

func (b *BoundedBuffer) String() string {
	str := b.buf.String()
	if b.truncated {
		str += "\n...[output truncated: exceeded maximum limit of 16MB]..."
	}
	return str
}

// ReadBoundedOutput reads up to maxBytes from r into string, truncating and background-draining if exceeded.
func ReadBoundedOutput(r io.Reader, maxBytes int64) (string, error) {
	return readBoundedOutput(r, maxBytes)
}
// it stops reading further and appends a truncation notice.
func readBoundedOutput(r io.Reader, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = MaxCommandOutputBytes
	}
	lr := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		// 达到上限后在后台持续排空管道，避免远端进程因管道缓冲写满而阻塞死锁
		go func() {
			_, _ = io.Copy(io.Discard, r)
		}()
		return string(data[:maxBytes]) + "\n...[output truncated: exceeded maximum limit of 16MB]...", nil
	}
	return string(data), nil
}

