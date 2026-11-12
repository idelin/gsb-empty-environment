package netsec

import (
	"bytes"
	"compress/flate"
	"io"
)

// Decompressor is the compression sink the receiver delegates to. It is an
// interface so tests can inject a recorder instead of a real DEFLATE stream.
//
// Decompress inflates one complete compressed message payload. The input is
// the concatenated fragments of the message with the RFC 7692 sync-flush
// trailer already appended.
//
// Reset forgets every byte that fed the dictionary. It must be called between
// messages when context takeover was not negotiated, and after any failed
// inflation so that a corrupt stream can never poison later messages.
type Decompressor interface {
	Decompress(data []byte) ([]byte, error)
	Reset()
	io.Closer
}

// flateMaxWindow is the DEFLATE sliding-window size (RFC 1951) and therefore
// the largest history a back-reference can reach.
const flateMaxWindow = 1 << 15

// flateDecompressor adapts compress/flate to Decompressor.
//
// compress/flate drops its history on every Reset, so to honour context
// takeover the implementation keeps the trailing 32KiB of emitted output
// itself and reinstalls it as a preset dictionary before each message.
type flateDecompressor struct {
	takeover bool
	reader   io.ReadCloser
	history  []byte
}

// NewFlateDecompressor builds the RFC 7692 decompressor. When takeover is
// true the dictionary is reused across messages; when false it is cleared
// after every message.
func NewFlateDecompressor(takeover bool) Decompressor {
	return &flateDecompressor{takeover: takeover}
}

func (d *flateDecompressor) Decompress(data []byte) ([]byte, error) {
	src := bytes.NewReader(data)
	if d.reader == nil {
		d.reader = flate.NewReaderDict(src, d.history)
	} else if resetter, ok := d.reader.(flate.Resetter); ok {
		if err := resetter.Reset(src, d.history); err != nil {
			return nil, err
		}
	}

	out, err := io.ReadAll(d.reader)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	// RFC 7692: the appended 0x0000ffff block is an unterminated stream,
	// so io.ErrUnexpectedEOF is the expected success case. io.EOF means
	// the stream was self-terminated; both are acceptable.
	d.retainHistory(out)
	return out, nil
}

func (d *flateDecompressor) retainHistory(out []byte) {
	if !d.takeover {
		d.history = d.history[:0]
		return
	}
	if len(out) >= flateMaxWindow {
		// Copy: out may be reused by the caller and the history must be stable.
		d.history = append(d.history[:0], out[len(out)-flateMaxWindow:]...)
		return
	}
	kept := len(d.history) + len(out)
	if kept > flateMaxWindow {
		drop := kept - flateMaxWindow
		d.history = append(d.history[:0], d.history[drop:]...)
	}
	d.history = append(d.history, out...)
}

func (d *flateDecompressor) Reset() {
	d.history = d.history[:0]
	if d.reader != nil {
		_ = d.reader.Close()
		d.reader = nil
	}
}

func (d *flateDecompressor) Close() error {
	if d.reader != nil {
		// RFC 7692 streams are unterminated by design, so Close reports
		// ErrUnexpectedEOF even when every payload byte was valid.
		_ = d.reader.Close()
		d.reader = nil
	}
	return nil
}
