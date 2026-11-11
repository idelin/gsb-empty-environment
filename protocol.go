package sshchannel

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
)

// Frame types multiplexed over the session's byte stream.
const (
	frameOpen  byte = 1
	frameData  byte = 2
	frameClose byte = 3
	frameOK    byte = 4
	frameFail  byte = 5
	frameHalf  byte = 6
)

// maxFramePayload bounds a single frame payload so a hostile peer cannot
// force an unbounded allocation by advertising a huge length prefix.
const maxFramePayload = 1 << 20 // 1 MiB

var errFrameTooLarge = errors.New("sshchannel: frame payload exceeds maximum size")

// frameReader reads length-prefixed frames from a buffered stream.
type frameReader struct {
	r      io.Reader
	hdr    [5]byte
	lenBuf [4]byte
}

func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{r: r}
}

// readFrame returns the frame type and a fresh payload buffer.
func (f *frameReader) readFrame() (byte, []byte, error) {
	if _, err := io.ReadFull(f.r, f.hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(f.hdr[1:])
	if n > maxFramePayload {
		return 0, nil, errFrameTooLarge
	}
	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(f.r, payload); err != nil {
			return 0, nil, err
		}
	}
	return f.hdr[0], payload, nil
}

// frameWriter serializes writes so concurrent channels never interleave bytes.
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newFrameWriter(w io.Writer) *frameWriter {
	return &frameWriter{w: w}
}

func (f *frameWriter) write(typ byte, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := f.w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := f.w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// encodeTarget serializes host:port as a single length-less payload; the frame
// length prefix already delimits it.
func encodeTarget(host string, port uint16) []byte {
	return []byte(net.JoinHostPort(host, itoaPort(port)))
}

func decodeTarget(payload []byte) (host string, port uint16, err error) {
	host, portStr, err := net.SplitHostPort(string(payload))
	if err != nil {
		return "", 0, err
	}
	portNum, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return "", 0, err
	}
	if portNum < 0 || portNum > 65535 {
		return "", 0, errors.New("sshchannel: port out of range")
	}
	return host, uint16(portNum), nil
}

func itoaPort(port uint16) string {
	switch {
	case port == 0:
		return "0"
	default:
		var buf [5]byte
		pos := len(buf)
		for p := port; p > 0; p /= 10 {
			pos--
			buf[pos] = byte('0' + p%10)
		}
		return string(buf[pos:])
	}
}
