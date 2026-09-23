package featureneg

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
)

// Supported serialization range (self-defined limits). Anything outside
// these bounds is rejected during decoding, before allocation.
const (
	formatVersion uint32 = 1
	maxStringLen         = 1 << 12 // 4 KiB per string
	maxListLen           = 1 << 12 // 4096 entries per list
	maxFrameLen          = 1 << 20 // 1 MiB per decoded frame
)

var (
	errTruncated = errors.New("featureneg: truncated input")
	errLength    = errors.New("featureneg: declared length exceeds remaining input or configured limit")
	errTrailing  = errors.New("featureneg: trailing bytes after value")
	errFormat    = errors.New("featureneg: unsupported format version")
)

// writer appends big-endian length-prefixed values.
type writer struct{ buf []byte }

func (w *writer) u32(v uint32) {
	w.buf = binary.BigEndian.AppendUint32(w.buf, v)
}

func (w *writer) str(s string) {
	w.u32(uint32(len(s)))
	w.buf = append(w.buf, s...)
}

// reader decodes values, validating every declared length against the
// remaining input before slicing or allocating.
type reader struct {
	buf []byte
	off int
	err error
}

func (r *reader) u32() uint32 {
	if r.err != nil {
		return 0
	}
	if len(r.buf)-r.off < 4 {
		r.err = errTruncated
		return 0
	}
	v := binary.BigEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v
}

// bytes reads a length-prefixed byte slice, enforcing limit and
// remaining-input checks before touching the data.
func (r *reader) bytes(limit uint32) []byte {
	n := r.u32()
	if r.err != nil {
		return nil
	}
	if n > limit || uint64(n) > uint64(len(r.buf)-r.off) {
		r.err = errLength
		return nil
	}
	b := r.buf[r.off : r.off+int(n)]
	r.off += int(n)
	return b
}

func (r *reader) str() string {
	b := r.bytes(maxStringLen)
	if r.err != nil {
		return ""
	}
	return string(b)
}

// count reads a list length and validates it against the limit and the
// minimum bytes each remaining entry would need (4-byte length prefix),
// so a hostile count cannot trigger a huge allocation.
func (r *reader) count() int {
	n := r.u32()
	if r.err != nil {
		return 0
	}
	if n > maxListLen || uint64(n)*4 > uint64(len(r.buf)-r.off) {
		r.err = errLength
		return 0
	}
	return int(n)
}

// EncodeOffer serializes an offer deterministically (map keys sorted),
// which makes it suitable for transcript hashing.
func EncodeOffer(o *Offer) []byte {
	w := &writer{}
	w.u32(formatVersion)
	w.str(o.Party)
	w.u32(o.Protocol.Min)
	w.u32(o.Protocol.Max)
	writeStringList(w, o.Required)
	writeStringList(w, o.Optional)
	writeStringList(w, o.Preference)
	names := make([]string, 0, len(o.Features))
	for name := range o.Features {
		names = append(names, name)
	}
	sort.Strings(names)
	w.u32(uint32(len(names)))
	for _, name := range names {
		f := o.Features[name]
		w.str(f.Name)
		w.u32(f.Versions.Min)
		w.u32(f.Versions.Max)
		w.u32(uint32(len(f.Requires)))
		for _, d := range f.Requires {
			w.str(d.Name)
			w.u32(d.MinVersion)
		}
		writeStringList(w, f.Conflicts)
	}
	return w.buf
}

func writeStringList(w *writer, list []string) {
	w.u32(uint32(len(list)))
	for _, s := range list {
		w.str(s)
	}
}

// DecodeOffer parses an offer produced by EncodeOffer. It never
// executes host code and validates all lengths before allocating.
func DecodeOffer(buf []byte) (*Offer, error) {
	r := &reader{buf: buf}
	if v := r.u32(); r.err == nil && v != formatVersion {
		r.err = errFormat
	}
	o := &Offer{Features: map[string]Feature{}}
	o.Party = r.str()
	o.Protocol = Range{Min: r.u32(), Max: r.u32()}
	o.Required = readStringList(r)
	o.Optional = readStringList(r)
	o.Preference = readStringList(r)
	nf := r.count()
	for i := 0; i < nf && r.err == nil; i++ {
		var f Feature
		f.Name = r.str()
		f.Versions = Range{Min: r.u32(), Max: r.u32()}
		nd := r.count()
		for j := 0; j < nd && r.err == nil; j++ {
			f.Requires = append(f.Requires, Dep{Name: r.str(), MinVersion: r.u32()})
		}
		f.Conflicts = readStringList(r)
		o.Features[f.Name] = f
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(buf) {
		return nil, errTrailing
	}
	return o, nil
}

func readStringList(r *reader) []string {
	n := r.count()
	var out []string
	for i := 0; i < n && r.err == nil; i++ {
		out = append(out, r.str())
	}
	return out
}

// Certificate is the complete selection certificate produced by a
// successful negotiation. It is bound to the session transcript, so it
// cannot be replayed to decode data negotiated under new parameters.
type Certificate struct {
	Transcript      [32]byte
	ProtocolVersion uint32
	Selected        []Selection
}

// Summary hashes the canonical certificate encoding; it binds any
// subsequent decoding to this exact negotiation result.
func (c *Certificate) Summary() [32]byte {
	return sha256.Sum256(EncodeCertificate(c))
}

// EncodeCertificate serializes a certificate deterministically.
func EncodeCertificate(c *Certificate) []byte {
	w := writer{}
	w.u32(formatVersion)
	w.u32(uint32(len(c.Transcript)))
	w.buf = append(w.buf, c.Transcript[:]...)
	w.u32(c.ProtocolVersion)
	w.u32(uint32(len(c.Selected)))
	for _, s := range c.Selected {
		w.str(s.Name)
		w.u32(s.Version)
	}
	return w.buf
}

// DecodeCertificate parses a certificate with the same strict length
// validation as DecodeOffer.
func DecodeCertificate(buf []byte) (*Certificate, error) {
	r := &reader{buf: buf}
	if v := r.u32(); r.err == nil && v != formatVersion {
		r.err = errFormat
	}
	c := &Certificate{}
	t := r.bytes(32)
	if r.err == nil && len(t) != 32 {
		r.err = errLength
	}
	if r.err == nil {
		copy(c.Transcript[:], t)
		c.ProtocolVersion = r.u32()
		n := r.count()
		for i := 0; i < n && r.err == nil; i++ {
			c.Selected = append(c.Selected, Selection{Name: r.str(), Version: r.u32()})
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(buf) {
		return nil, errTrailing
	}
	return c, nil
}
