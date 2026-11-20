package bootpolicy

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Wire format (big-endian, deterministic):
//
//   package  = magic(4) version(1) entryCount(u16) entry* certFlag(1) cert? sha256(32)
//   entry    = kind(1)
//              str SignerID; str LabelPrefix; u32 GenLo; u32 GenHi; str Location
//              parentFlag(1) [str ScopeSignerID; str ScopeLabel]
//              exemptFlag(1) [str RootID; str DigestAlg; bytes DigestHash; u64 NotAfter]
//   str      = u16 length; bytes
//   bytes    = u32 length; bytes
//
// The trailing SHA-256 covers every byte preceding it.

const (
	wireMagic   = "GBCP"
	wireVersion = byte(1)
	maxString   = 1 << 16
	maxBytes    = 1 << 20
)

type writer struct {
	buf []byte
}

func (w *writer) byte(v byte)  { w.buf = append(w.buf, v) }
func (w *writer) u16(v uint16) { w.buf = append(w.buf, byte(v>>8), byte(v)) }
func (w *writer) u32(v uint32) { w.buf = append(w.buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) }
func (w *writer) u64(v uint64) {
	w.buf = append(w.buf, byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *writer) str(v string) error {
	if len(v) > maxString-1 {
		return ErrPackage
	}
	w.u16(uint16(len(v)))
	w.buf = append(w.buf, v...)
	return nil
}

func (w *writer) raw(v []byte) error {
	if len(v) > maxBytes-1 {
		return ErrPackage
	}
	w.u32(uint32(len(v)))
	w.buf = append(w.buf, v...)
	return nil
}

type reader struct {
	buf []byte
	pos int
}

func (r *reader) byteValue() (byte, error) {
	if r.pos+1 > len(r.buf) {
		return 0, ErrPackage
	}
	v := r.buf[r.pos]
	r.pos++
	return v, nil
}

func (r *reader) u16Value() (uint16, error) {
	if r.pos+2 > len(r.buf) {
		return 0, ErrPackage
	}
	v := binary.BigEndian.Uint16(r.buf[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *reader) u32Value() (uint32, error) {
	if r.pos+4 > len(r.buf) {
		return 0, ErrPackage
	}
	v := binary.BigEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *reader) u64Value() (uint64, error) {
	if r.pos+8 > len(r.buf) {
		return 0, ErrPackage
	}
	v := binary.BigEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

func (r *reader) strValue() (string, error) {
	n, err := r.u16Value()
	if err != nil {
		return "", err
	}
	if r.pos+int(n) > len(r.buf) {
		return "", ErrPackage
	}
	v := string(r.buf[r.pos : r.pos+int(n)])
	r.pos += int(n)
	return v, nil
}

func (r *reader) rawValue() ([]byte, error) {
	n, err := r.u32Value()
	if err != nil {
		return nil, err
	}
	if r.pos+int(n) > len(r.buf) {
		return nil, ErrPackage
	}
	v := append([]byte(nil), r.buf[r.pos:r.pos+int(n)]...)
	r.pos += int(n)
	return v, nil
}

// EntryWireSize returns the encoded byte size of one entry.
func EntryWireSize(e Entry) int {
	w := &writer{}
	_ = writeEntry(w, e)
	return len(w.buf)
}

func writeEntry(w *writer, e Entry) error {
	w.byte(byte(e.Kind))
	if err := w.str(e.SignerID); err != nil {
		return err
	}
	if err := w.str(e.LabelPrefix); err != nil {
		return err
	}
	w.u32(e.GenLo)
	w.u32(e.GenHi)
	if err := w.str(e.Location); err != nil {
		return err
	}
	if e.Parent == nil {
		w.byte(0)
	} else {
		w.byte(1)
		if err := w.str(e.Parent.SignerID); err != nil {
			return err
		}
		if err := w.str(e.Parent.Label); err != nil {
			return err
		}
	}
	if e.Kind == KindExempt {
		w.byte(1)
		if err := w.str(e.RootID); err != nil {
			return err
		}
		if err := w.str(e.Digest.Algorithm); err != nil {
			return err
		}
		if err := w.raw(e.Digest.Hash); err != nil {
			return err
		}
		w.u64(e.NotAfter)
	} else {
		w.byte(0)
	}
	return nil
}

func readEntry(r *reader) (Entry, error) {
	var e Entry
	kind, err := r.byteValue()
	if err != nil {
		return e, err
	}
	e.Kind = EntryKind(kind)
	if e.SignerID, err = r.strValue(); err != nil {
		return e, err
	}
	if e.LabelPrefix, err = r.strValue(); err != nil {
		return e, err
	}
	if e.GenLo, err = r.u32Value(); err != nil {
		return e, err
	}
	if e.GenHi, err = r.u32Value(); err != nil {
		return e, err
	}
	if e.Location, err = r.strValue(); err != nil {
		return e, err
	}
	parentFlag, err := r.byteValue()
	if err != nil {
		return e, err
	}
	if parentFlag == 1 {
		e.Parent = &ParentScope{}
		if e.Parent.SignerID, err = r.strValue(); err != nil {
			return e, err
		}
		if e.Parent.Label, err = r.strValue(); err != nil {
			return e, err
		}
	} else if parentFlag != 0 {
		return e, ErrPackage
	}
	exemptFlag, err := r.byteValue()
	if err != nil {
		return e, err
	}
	if exemptFlag == 1 {
		if e.Kind != KindExempt {
			return e, ErrPackage
		}
		if e.RootID, err = r.strValue(); err != nil {
			return e, err
		}
		if e.Digest.Algorithm, err = r.strValue(); err != nil {
			return e, err
		}
		if e.Digest.Hash, err = r.rawValue(); err != nil {
			return e, err
		}
		if e.NotAfter, err = r.u64Value(); err != nil {
			return e, err
		}
	} else if exemptFlag != 0 {
		return e, ErrPackage
	}
	if err := e.Validate(); err != nil {
		return e, errors.Join(ErrPackage, err)
	}
	return e, nil
}

func writePolicy(w *writer, p *Policy) error {
	if len(p.Entries) > 1<<16-1 {
		return ErrPackage
	}
	w.u16(uint16(len(p.Entries)))
	for i := range p.Entries {
		if err := writeEntry(w, p.Entries[i]); err != nil {
			return err
		}
	}
	return nil
}

func readPolicy(r *reader) (*Policy, error) {
	n, err := r.u16Value()
	if err != nil {
		return nil, err
	}
	p := &Policy{Entries: make([]Entry, 0, n)}
	for i := 0; i < int(n); i++ {
		e, err := readEntry(r)
		if err != nil {
			return nil, err
		}
		p.Entries = append(p.Entries, e)
	}
	return p, nil
}

// EncodePackage serializes a compressed policy together with its equivalence
// certificate into an importable, checksummed package.
func EncodePackage(p *Policy, cert Certificate) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	w := &writer{}
	w.buf = append(w.buf, wireMagic...)
	w.byte(wireVersion)
	if err := writePolicy(w, p); err != nil {
		return nil, err
	}
	if err := writeCertificate(w, cert); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(w.buf)
	w.buf = append(w.buf, sum[:]...)
	return w.buf, nil
}

// ImportPackage parses a package, verifies the checksum and independently
// re-checks the equivalence certificate against original. The returned policy
// is safe to install only when err is nil.
func ImportPackage(blob []byte, original *Policy) (*Policy, Certificate, error) {
	if err := original.Validate(); err != nil {
		return nil, Certificate{}, err
	}
	if len(blob) < sha256.Size+len(wireMagic)+1 {
		return nil, Certificate{}, ErrPackage
	}
	sum := blob[len(blob)-sha256.Size:]
	body := blob[:len(blob)-sha256.Size]
	got := sha256.Sum256(body)
	if !bytesEqual(got[:], sum) {
		return nil, Certificate{}, ErrPackage
	}
	r := &reader{buf: body}
	magic := make([]byte, 4)
	if r.pos+4 > len(r.buf) {
		return nil, Certificate{}, ErrPackage
	}
	copy(magic, r.buf[:4])
	r.pos = 4
	if string(magic) != wireMagic {
		return nil, Certificate{}, ErrPackage
	}
	version, err := r.byteValue()
	if err != nil || version != wireVersion {
		return nil, Certificate{}, ErrPackage
	}
	p, err := readPolicy(r)
	if err != nil {
		return nil, Certificate{}, err
	}
	cert, err := readCertificate(r)
	if err != nil {
		return nil, Certificate{}, err
	}
	if r.pos != len(r.buf) {
		return nil, Certificate{}, ErrPackage
	}
	if err := VerifyCertificate(original, p, cert); err != nil {
		return nil, Certificate{}, err
	}
	return p, cert, nil
}
