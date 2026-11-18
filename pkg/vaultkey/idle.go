package vaultkey

import (
	"io"
	"time"
)

// idleReadDelay is the pause between empty serial reads. Some platforms return
// (0, nil) on a read timeout instead of blocking; bufio turns a streak of those
// into io.ErrNoProgress. Tests may set this to zero.
var idleReadDelay = 5 * time.Millisecond

// idleReader waits for data, an error, or client shutdown instead of letting
// an idle serial port look like a broken one.
type idleReader struct {
	reader io.Reader
	done   <-chan struct{}
}

func (r *idleReader) Read(buffer []byte) (int, error) {
	for {
		count, err := r.reader.Read(buffer)
		if count > 0 || err != nil {
			return count, err
		}
		select {
		case <-r.done:
			return 0, io.EOF
		case <-time.After(idleReadDelay):
		}
	}
}
