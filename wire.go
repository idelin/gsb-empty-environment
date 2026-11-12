package netsec

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	flagMore    byte = 1 << 0
	flagLong    byte = 1 << 1
	flagCommand byte = 1 << 2
)

// Frame is one decoded ZMTP frame.
type Frame struct {
	// More reports whether another frame follows in the same message.
	More bool
	// Command reports whether the frame body is a ZMTP command rather than
	// application data.
	Command bool
	// Body is the frame payload. It does not include the frame header.
	Body []byte
}

// ReadFrame reads one ZMTP 3.x frame from r. Reserved flag bits are
// rejected, and maxSize bounds the body allocation to prevent a peer from
// forcing an unbounded read. A non-positive maxSize uses
// DefaultMaxFrameSize.
func ReadFrame(r io.Reader, maxSize int64) (Frame, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxFrameSize
	}

	var header [1]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, fmt.Errorf("%w: %w", ErrMalformedFrame, err)
	}
	flags := header[0]
	if flags & ^(flagMore|flagLong|flagCommand) != 0 {
		return Frame{}, fmt.Errorf("%w: reserved flag bits 0x%02x", ErrMalformedFrame, flags)
	}

	var size uint64
	if flags&flagLong != 0 {
		var encoded [8]byte
		if _, err := io.ReadFull(r, encoded[:]); err != nil {
			return Frame{}, fmt.Errorf("%w: %w", ErrMalformedFrame, err)
		}
		size = binary.BigEndian.Uint64(encoded[:])
		if size > 1<<63-1 {
			return Frame{}, fmt.Errorf("%w: size %d", ErrMalformedFrame, size)
		}
	} else {
		var encoded [1]byte
		if _, err := io.ReadFull(r, encoded[:]); err != nil {
			return Frame{}, fmt.Errorf("%w: %w", ErrMalformedFrame, err)
		}
		size = uint64(encoded[0])
	}

	if int64(size) > maxSize {
		return Frame{}, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, size, maxSize)
	}

	body := make([]byte, int(size))
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, fmt.Errorf("%w: %w", ErrMalformedFrame, err)
	}

	return Frame{
		More:    flags&flagMore != 0,
		Command: flags&flagCommand != 0,
		Body:    body,
	}, nil
}

// WriteFrame writes one ZMTP 3.x frame. Bodies longer than 255 octets use
// the eight-octet long-size encoding.
func WriteFrame(w io.Writer, frame Frame) error {
	var flags byte
	if frame.More {
		flags |= flagMore
	}
	if frame.Command {
		flags |= flagCommand
	}

	size := uint64(len(frame.Body))
	var header [9]byte
	header[0] = flags
	headerLen := 1
	if size > 255 {
		header[0] |= flagLong
		binary.BigEndian.PutUint64(header[1:], size)
		headerLen = 9
	} else {
		header[1] = byte(size)
		headerLen = 2
	}

	if err := writeFull(w, header[:headerLen]); err != nil {
		return err
	}
	return writeFull(w, frame.Body)
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
