package strdict

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// DecoderConfig configures a decode session.
type DecoderConfig struct {
	Registry *Registry // required
	Batch    *Batch    // optional: leases are taken inside this batch
	Version  uint32    // initial pinned version; 0 = none until a switch frame
	Budget   *Budget   // optional byte budget; nil = unlimited
}

// Decoder reads a frame stream produced by Encoder. It is a pure data
// decoder: it never executes host code, and it validates every declared
// length against MaxInlineLen and the budget before allocating.
type Decoder struct {
	cfg   DecoderConfig
	br    *bufio.Reader
	off   int64
	lease *Lease
	dict  *Dictionary
	done  bool
}

type countReader struct {
	r io.Reader
	n *int64
}

func (c countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += int64(n)
	return n, err
}

// NewDecoder starts a decode session. If cfg.Version is non-zero the
// session is pinned to that generation and charged its dictionary bytes.
func NewDecoder(r io.Reader, cfg DecoderConfig) (*Decoder, error) {
	if cfg.Registry == nil {
		return nil, errors.New("strdict: nil registry")
	}
	d := &Decoder{cfg: cfg}
	d.br = bufio.NewReader(countReader{r: r, n: &d.off})
	if cfg.Version != 0 {
		if err := d.switchVersion(cfg.Version); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *Decoder) charge(n int64) error { return d.cfg.Budget.Charge(n) }

func (d *Decoder) checkLease() error {
	if d.lease != nil && d.lease.Released() {
		return ErrLeaseReleased
	}
	return nil
}

func (d *Decoder) switchVersion(version uint32) error {
	var (
		lease *Lease
		err   error
	)
	if d.cfg.Batch != nil {
		lease, err = d.cfg.Batch.Acquire(version)
	} else {
		lease, err = d.cfg.Registry.Acquire(version)
	}
	if err != nil {
		return err
	}
	if err := d.charge(lease.Dictionary().Bytes()); err != nil {
		lease.Release()
		return err
	}
	if d.lease != nil {
		d.lease.Release()
	}
	d.lease = lease
	d.dict = lease.Dictionary()
	return nil
}

// Next returns the next decoded string, or io.EOF at the end marker or a
// clean end of input. A truncated frame yields io.ErrUnexpectedEOF.
func (d *Decoder) Next() (string, error) {
	if d.done {
		return "", io.EOF
	}
	if err := d.checkLease(); err != nil {
		return "", err
	}
	tag, err := d.br.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			d.done = true
		}
		return "", err
	}
	switch tag {
	case tagEnd:
		d.done = true
		return "", io.EOF
	case tagInline:
		return d.readInline()
	case tagRef:
		return d.readRef()
	case tagSwitch:
		v, err := binary.ReadUvarint(d.br)
		if err != nil {
			return "", frameErr(err)
		}
		if v > math.MaxUint32 {
			return "", ErrMalformed
		}
		if err := d.switchVersion(uint32(v)); err != nil {
			return "", err
		}
		return d.Next()
	default:
		return "", ErrMalformed
	}
}

func (d *Decoder) readInline() (string, error) {
	n, err := binary.ReadUvarint(d.br)
	if err != nil {
		return "", frameErr(err)
	}
	// Validate the declared length before allocating the staging buffer.
	if n > MaxInlineLen {
		return "", ErrLengthTooLarge
	}
	if err := d.charge(int64(n)); err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(d.br, buf); err != nil {
		return "", frameErr(err)
	}
	return string(buf), nil
}

func (d *Decoder) readRef() (string, error) {
	if d.dict == nil {
		return "", ErrNoDictionary
	}
	idx, err := binary.ReadUvarint(d.br)
	if err != nil {
		return "", frameErr(err)
	}
	if idx > math.MaxUint32 {
		return "", ErrMalformed
	}
	s, ok := d.dict.Lookup(uint32(idx))
	if !ok {
		return "", ErrUnknownIndex
	}
	// Charge the expansion before handing the string out.
	if err := d.charge(int64(len(s))); err != nil {
		return "", err
	}
	return s, nil
}

// Checkpoint marks a resumable position: the byte offset of the next frame
// and the dictionary version in force at that point. To recover, create a
// new Decoder over a reader positioned at Offset with Version as the
// initial version.
func (d *Decoder) Checkpoint() Checkpoint {
	var v uint32
	if d.dict != nil {
		v = d.dict.Version()
	}
	return Checkpoint{Offset: d.off - int64(d.br.Buffered()), Version: v}
}

// Checkpoint is a recovery point inside a stream.
type Checkpoint struct {
	Offset  int64
	Version uint32
}

// Close releases the session lease. It is safe to call multiple times.
func (d *Decoder) Close() error {
	if d.lease != nil {
		d.lease.Release()
		d.lease = nil
	}
	return nil
}

func frameErr(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
