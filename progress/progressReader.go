package progress

import (
	"bytes"
	"fmt"
	"io"
	"time"
)

type Reader struct {
	reader     io.Reader
	total      int
	read       int
	lastUpdate time.Time
}

func NewReader(b []byte) io.Reader {
	reader := &Reader{
		reader: bytes.NewReader(b),
		total:  len(b),
		read:   0,
	}
	fmt.Println()
	reader.showProgress()
	return reader
}

func (r *Reader) Read(b []byte) (n int, err error) {
	n, err = r.reader.Read(b[:min(len(b), 4096)])
	if time.Since(r.lastUpdate) > time.Second || r.read+n == r.total {
		r.showProgress()
	}
	r.read += n
	return
}

func (r *Reader) showProgress() {
	fmt.Printf("\033[F\rUpload progress: %d/%d (%2d%%)\n", r.read, r.total, r.read*100/r.total)
	r.lastUpdate = time.Now()
}
