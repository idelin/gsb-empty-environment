package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Version is the SCT protocol version (RFC 6962 s3.2).
type Version byte

// VersionV1 is the only SCT version defined by RFC 6962.
const VersionV1 Version = 0

// HashAlgorithm identifies the hash algorithm of a digitally-signed struct.
type HashAlgorithm byte

const (
	HashNone   HashAlgorithm = 0
	HashMD5    HashAlgorithm = 1
	HashSHA1   HashAlgorithm = 2
	HashSHA224 HashAlgorithm = 3
	HashSHA256 HashAlgorithm = 4
	HashSHA384 HashAlgorithm = 5
	HashSHA512 HashAlgorithm = 6
)

// SignatureAlgorithm identifies the signature algorithm of a
// digitally-signed struct.
type SignatureAlgorithm byte

const (
	SigAnonymous SignatureAlgorithm = 0
	SigRSA       SignatureAlgorithm = 1
	SigDSA       SignatureAlgorithm = 2
	SigECDSA     SignatureAlgorithm = 3
)

// SCT is a parsed SignedCertificateTimestamp (RFC 6962 s3.2).
type SCT struct {
	Version            Version
	LogID              [32]byte
	Timestamp          uint64 // milliseconds since the Unix epoch
	Extensions         []byte
	HashAlgorithm      HashAlgorithm
	SignatureAlgorithm SignatureAlgorithm
	Signature          []byte
}

// Time returns the SCT timestamp as a time.Time.
func (s *SCT) Time() time.Time {
	return time.UnixMilli(int64(s.Timestamp)).UTC()
}

// ErrMalformedSCTList is returned when an SCT or SCT list cannot be decoded.
var ErrMalformedSCTList = errors.New("netsec: malformed SCT list")

// ParseSCTList decodes a TLS-encoded SignedCertificateTimestampList, as
// carried in the X.509 SCT extension (1.3.6.1.4.1.11129.2.4.2) or the
// signed_certificate_timestamp TLS extension.
func ParseSCTList(b []byte) ([]*SCT, error) {
	r := &cursor{data: b}
	total := r.uint16()
	if r.err != nil || int(total) != len(r.data[r.off:]) {
		return nil, fmt.Errorf("%w: list length prefix mismatch", ErrMalformedSCTList)
	}
	var scts []*SCT
	for r.off < len(r.data) {
		n := r.uint16()
		if r.err != nil {
			return nil, fmt.Errorf("%w: truncated SCT length", ErrMalformedSCTList)
		}
		raw := r.bytes(int(n))
		if r.err != nil {
			return nil, fmt.Errorf("%w: truncated SCT", ErrMalformedSCTList)
		}
		sct, err := parseSCT(raw)
		if err != nil {
			return nil, err
		}
		scts = append(scts, sct)
	}
	if len(scts) == 0 {
		return nil, fmt.Errorf("%w: empty list", ErrMalformedSCTList)
	}
	return scts, nil
}

func parseSCT(b []byte) (*SCT, error) {
	r := &cursor{data: b}
	sct := &SCT{}
	sct.Version = Version(r.byte())
	copy(sct.LogID[:], r.bytes(32))
	sct.Timestamp = r.uint64()
	extLen := r.uint16()
	sct.Extensions = r.bytes(int(extLen))
	sct.HashAlgorithm = HashAlgorithm(r.byte())
	sct.SignatureAlgorithm = SignatureAlgorithm(r.byte())
	sigLen := r.uint16()
	sct.Signature = r.bytes(int(sigLen))
	if r.err != nil || r.off != len(r.data) {
		return nil, fmt.Errorf("%w: bad SCT encoding", ErrMalformedSCTList)
	}
	return sct, nil
}

// marshalSCT encodes a single SCT without its length prefix.
func marshalSCT(s *SCT) []byte {
	out := make([]byte, 0, 48+len(s.Extensions)+len(s.Signature))
	out = append(out, byte(s.Version))
	out = append(out, s.LogID[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], s.Timestamp)
	out = append(out, ts[:]...)
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(s.Extensions)))
	out = append(out, l[:]...)
	out = append(out, s.Extensions...)
	out = append(out, byte(s.HashAlgorithm), byte(s.SignatureAlgorithm))
	binary.BigEndian.PutUint16(l[:], uint16(len(s.Signature)))
	out = append(out, l[:]...)
	out = append(out, s.Signature...)
	return out
}

// marshalSCTList encodes a SignedCertificateTimestampList.
func marshalSCTList(scts []*SCT) []byte {
	var body []byte
	var l [2]byte
	for _, s := range scts {
		raw := marshalSCT(s)
		binary.BigEndian.PutUint16(l[:], uint16(len(raw)))
		body = append(body, l[:]...)
		body = append(body, raw...)
	}
	out := make([]byte, 0, len(body)+2)
	binary.BigEndian.PutUint16(l[:], uint16(len(body)))
	out = append(out, l[:]...)
	out = append(out, body...)
	return out
}

// cursor is a bounds-checked big-endian byte reader.
type cursor struct {
	data []byte
	off  int
	err  error
}

func (c *cursor) bytes(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || len(c.data)-c.off < n {
		c.err = ErrMalformedSCTList
		return nil
	}
	b := c.data[c.off : c.off+n]
	c.off += n
	return b
}

func (c *cursor) byte() byte {
	b := c.bytes(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (c *cursor) uint16() uint16 {
	b := c.bytes(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}

func (c *cursor) uint64() uint64 {
	b := c.bytes(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
