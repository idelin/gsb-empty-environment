package strdict

import (
	"encoding/binary"
	"io"
)

const (
	tagEnd    byte = 0x00
	tagInline byte = 0x01
	tagRef    byte = 0x02
	tagSwitch byte = 0x03
)

// MaxInlineLen bounds the declared length of an inline string frame. The
// decoder rejects larger declarations before allocating anything.
const MaxInlineLen = 1 << 24 // 16 MiB

// Encoder writes a frame stream mixing references and inline strings.
type Encoder struct {
	w    io.Writer
	dict *Dictionary
	err  error
}

// NewEncoder creates an encoder whose references resolve against dict. dict
// may be nil if only inline frames are written before the first SwitchTo.
func NewEncoder(w io.Writer, dict *Dictionary) *Encoder {
	return &Encoder{w: w, dict: dict}
}

func (e *Encoder) write(p []byte) error {
	if e.err != nil {
		return e.err
	}
	_, e.err = e.w.Write(p)
	return e.err
}

// Inline writes s as a literal string frame.
func (e *Encoder) Inline(s string) error {
	if len(s) > MaxInlineLen {
		return ErrLengthTooLarge
	}
	var hdr [1 + binary.MaxVarintLen64]byte
	hdr[0] = tagInline
	n := binary.PutUvarint(hdr[1:], uint64(len(s)))
	if err := e.write(hdr[:1+n]); err != nil {
		return err
	}
	return e.write([]byte(s))
}

// Ref writes a reference to index idx of the current dictionary.
func (e *Encoder) Ref(idx uint32) error {
	if e.dict == nil {
		return ErrNoDictionary
	}
	if _, ok := e.dict.Lookup(idx); !ok {
		return ErrUnknownIndex
	}
	var hdr [1 + binary.MaxVarintLen64]byte
	hdr[0] = tagRef
	n := binary.PutUvarint(hdr[1:], uint64(idx))
	return e.write(hdr[:1+n])
}

// String writes s as a reference when it is pooled in the current
// dictionary, otherwise inline.
func (e *Encoder) String(s string) error {
	if e.dict != nil {
		if idx, ok := e.dict.Index(s); ok {
			return e.Ref(idx)
		}
	}
	return e.Inline(s)
}

// SwitchTo emits an explicit version-switch frame. References written after
// this point resolve against d; references already written keep their old
// meaning for the decoder.
func (e *Encoder) SwitchTo(d *Dictionary) error {
	if d == nil || !d.Frozen() {
		return ErrNotFrozen
	}
	var hdr [1 + binary.MaxVarintLen64]byte
	hdr[0] = tagSwitch
	n := binary.PutUvarint(hdr[1:], uint64(d.Version()))
	if err := e.write(hdr[:1+n]); err != nil {
		return err
	}
	e.dict = d
	return nil
}

// End writes the end-of-stream marker.
func (e *Encoder) End() error { return e.write([]byte{tagEnd}) }
