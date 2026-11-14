package netsec

import "io"

// counterReader is a deterministic io.Reader for reproducible Shamir tests.
type counterReader struct {
	next byte
}

func (c *counterReader) Read(p []byte) (int, error) {
	for i := range p {
		c.next++
		p[i] = c.next
	}
	return len(p), nil
}

func deterministicReader() io.Reader {
	return &counterReader{}
}
