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
	fmt.Println()
	return &Reader{
		reader: bytes.NewReader(b),
		total:  len(b),
		read:   0,
	}
}

func (p *Reader) Read(b []byte) (n int, err error) {
	n, err = p.reader.Read(b[:min(len(b), 4096)])
	if time.Since(p.lastUpdate) > time.Second {
		fmt.Printf("\033[F\rProgress: %d/%d (%2d%%)\n", p.read, p.total, p.read*100/p.total)
		p.lastUpdate = time.Now()
	}
	p.read += n
	return
}
