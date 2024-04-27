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
	endMessage string
	lastUpdate time.Time
}

func NewReader(b []byte, endMessage string) io.Reader {
	reader := &Reader{
		reader:     bytes.NewReader(b),
		total:      len(b),
		read:       0,
		endMessage: endMessage,
	}
	fmt.Println()
	reader.showProgress()
	return reader
}

func (r *Reader) Read(b []byte) (n int, err error) {
	n, err = r.reader.Read(b[:min(len(b), 4096)])
	finished := r.read+n == r.total
	if finished || time.Since(r.lastUpdate) > time.Second {
		r.showProgress()
	}
	if finished {
		fmt.Println(r.endMessage)
	}
	r.read += n
	return
}

func (r *Reader) showProgress() {
	fmt.Printf("\033[F\rUpload progress: %d/%d (%2d%%)\n", r.read, r.total, r.read*100/r.total)
	r.lastUpdate = time.Now()
}
