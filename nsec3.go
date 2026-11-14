package netsec

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"strings"
)

const base32HexAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUV"

// EncodeBase32Hex encodes bytes using RFC 4648 base32hex without padding, as
// required for NSEC3 owner-name labels.
func EncodeBase32Hex(in []byte) string {
	if len(in) == 0 {
		return ""
	}
	var out strings.Builder
	out.Grow((len(in)*8 + 4) / 5)
	var buf uint32
	var bits uint
	for _, b := range in {
		buf = buf<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out.WriteByte(base32HexAlphabet[(buf>>bits)&0x1f])
		}
	}
	if bits > 0 {
		out.WriteByte(base32HexAlphabet[(buf<<(5-bits))&0x1f])
	}
	return out.String()
}

// DecodeBase32Hex decodes unpadded RFC 4648 base32hex. It rejects padding,
// invalid characters, and non-zero leftover bits.
func DecodeBase32Hex(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty base32hex", ErrMalformed)
	}
	var out []byte
	var buf uint32
	var bits uint
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v uint32
		switch {
		case c >= '0' && c <= '9':
			v = uint32(c - '0')
		case c >= 'A' && c <= 'V':
			v = uint32(c-'A') + 10
		case c >= 'a' && c <= 'v':
			v = uint32(c-'a') + 10
		default:
			return nil, fmt.Errorf("%w: invalid base32hex character %q", ErrMalformed, c)
		}
		buf = buf<<5 | v
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(buf>>bits))
		}
	}
	if bits > 0 && buf&((1<<bits)-1) != 0 {
		return nil, fmt.Errorf("%w: non-zero base32hex padding bits", ErrMalformed)
	}
	return out, nil
}

// HashName computes the RFC 5155 SHA-1 NSEC3 hash of a canonical name using
// this exact iteration/salt parameter set.
func (p Parameters) HashName(n Name) ([]byte, error) {
	if p.Kind != ProofNSEC3 {
		return nil, fmt.Errorf("%w: not an NSEC3 parameter set", ErrMalformed)
	}
	if p.HashAlgorithm != HashAlgorithmSHA1 {
		return nil, fmt.Errorf("%w: unsupported NSEC3 hash algorithm %d", ErrMalformed, p.HashAlgorithm)
	}
	if p.Iterations > DefaultNSEC3MaxIterations {
		return nil, fmt.Errorf("%w: NSEC3 iteration count too large", ErrMalformed)
	}
	if n.IsZero() {
		return nil, fmt.Errorf("%w: zero name", ErrMalformed)
	}
	first := make([]byte, 0, len(n.Wire())+len(p.Salt))
	first = append(first, n.Wire()...)
	first = append(first, p.Salt...)
	sum := sha1.Sum(first)
	for i := uint16(0); i < p.Iterations; i++ {
		in := make([]byte, 0, len(sum)+len(p.Salt))
		in = append(in, sum[:]...)
		in = append(in, p.Salt...)
		sum = sha1.Sum(in)
	}
	out := make([]byte, len(sum))
	copy(out, sum[:])
	return out, nil
}

// NSEC3OwnerName builds the full owner name for a hashed owner under apex.
func NSEC3OwnerName(apex Name, hash []byte) (Name, error) {
	if len(hash) != sha1.Size {
		return Name{}, fmt.Errorf("%w: NSEC3 hash length must be 20", ErrMalformed)
	}
	return apex.AppendLabel(EncodeBase32Hex(hash))
}

// OwnerHash validates that owner is exactly one base32hex label below apex and
// returns the decoded hash.
func (r NSEC3Record) OwnerHash(apex Name) ([]byte, error) {
	if r.Owner.IsZero() || apex.IsZero() {
		return nil, fmt.Errorf("%w: zero NSEC3 owner or apex", ErrMalformed)
	}
	if !r.Owner.IsProperSubdomainOf(apex) || r.Owner.LabelCount() != apex.LabelCount()+1 {
		return nil, fmt.Errorf("%w: NSEC3 owner is not a direct child of apex", ErrMalformed)
	}
	labels := r.Owner.labels()
	label := labels[0]
	decoded, err := DecodeBase32Hex(string(label))
	if err != nil {
		return nil, err
	}
	if len(decoded) != sha1.Size {
		return nil, fmt.Errorf("%w: NSEC3 owner hash length %d", ErrMalformed, len(decoded))
	}
	if !strings.EqualFold(EncodeBase32Hex(decoded), string(label)) {
		return nil, fmt.Errorf("%w: non-canonical base32hex owner label", ErrMalformed)
	}
	return decoded, nil
}

// NSEC3CoveredBy reports whether targetHash lies strictly inside the circular
// hash interval ownerHash -> nextHash.
func NSEC3CoveredBy(ownerHash, nextHash, targetHash []byte) bool {
	if len(ownerHash) == 0 || len(nextHash) == 0 || len(targetHash) == 0 {
		return false
	}
	if bytes.Equal(ownerHash, targetHash) || bytes.Equal(nextHash, targetHash) {
		return false
	}
	cmpOwnerNext := bytes.Compare(ownerHash, nextHash)
	cmpTargetOwner := bytes.Compare(targetHash, ownerHash)
	cmpTargetNext := bytes.Compare(targetHash, nextHash)
	if cmpOwnerNext < 0 {
		return cmpTargetOwner > 0 && cmpTargetNext < 0
	}
	if cmpOwnerNext > 0 {
		return cmpTargetOwner > 0 || cmpTargetNext < 0
	}
	return false
}
