// Package nsec3 verifies NSEC3 denial-of-existence proofs for a
// validating resolver. It enforces resource limits so that a hostile
// zone publishing extreme iteration counts (e.g. 65535) cannot
// exhaust the resolver's CPU: responses with iterations above the
// limit are treated as insecure without performing any hashing,
// over-long salts are rejected, and the total number of iterated
// hash computations per response is capped.
package nsec3

import (
	"bytes"
	"crypto/sha1"
	"encoding/base32"
	"errors"
	"strings"
)

const (
	// HashSHA1 is the only defined NSEC3 hash algorithm (RFC 5155).
	HashSHA1 uint8 = 1
	// FlagOptOut is the NSEC3 opt-out flag (RFC 5155 section 3.1.2.1).
	FlagOptOut uint8 = 1
)

const (
	// DefaultMaxIterations bounds the accepted NSEC3 iteration count.
	DefaultMaxIterations = 100
	// DefaultMaxSaltLen bounds the accepted NSEC3 salt length in octets.
	DefaultMaxSaltLen = 8
	// DefaultMaxHashes bounds the total number of iterated hash
	// computations performed while verifying one response.
	DefaultMaxHashes = 8
)

var (
	// ErrSaltTooLong rejects a response whose NSEC3 salt exceeds the
	// configured limit.
	ErrSaltTooLong = errors.New("nsec3: salt exceeds 8 octets")
	// ErrUnsupportedHash rejects a response using an unknown NSEC3
	// hash algorithm.
	ErrUnsupportedHash = errors.New("nsec3: unsupported hash algorithm")
	// ErrBadName rejects a malformed owner or query name.
	ErrBadName = errors.New("nsec3: malformed domain name")

	errHashBudget = errors.New("nsec3: hash budget exhausted")
)

// Conclusion is the outcome of verifying an NSEC3 denial proof.
type Conclusion int

const (
	// Insecure means the response cannot prove anything (or was
	// refused by a resource limit) and must be treated as insecure.
	Insecure Conclusion = iota
	// NXDOMAIN means the query name provably does not exist.
	NXDOMAIN
	// NoData means the query name provably exists (matched an NSEC3
	// owner), so only the queried type may be absent.
	NoData
	// Wildcard means a wildcard at the closest encloser provably
	// exists, so the response is a wildcard expansion, not NXDOMAIN.
	Wildcard
)

func (c Conclusion) String() string {
	switch c {
	case NXDOMAIN:
		return "NXDOMAIN"
	case NoData:
		return "NODATA"
	case Wildcard:
		return "Wildcard"
	default:
		return "Insecure"
	}
}

// Record is a parsed NSEC3 resource record. OwnerHash and NextHashed
// hold the raw (base32hex-decoded) hash octets.
type Record struct {
	HashAlg    uint8
	Flags      uint8
	Iterations uint16
	Salt       []byte
	OwnerHash  []byte
	NextHashed []byte
}

// OptOut reports whether the record carries the opt-out flag.
func (r Record) OptOut() bool { return r.Flags&FlagOptOut != 0 }

// DecodeHash decodes a base32hex NSEC3 hash label (RFC 4648), as found
// in the owner name and the next hashed owner name of an NSEC3 record.
func DecodeHash(label string) ([]byte, error) {
	return base32.HexEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(label))
}

// Verifier checks NSEC3 denial proofs under resource limits. Create
// one Verifier per response (or call Reset between responses): the
// hash budget applies to a whole response, not to a single name.
type Verifier struct {
	// MaxIterations is the largest accepted iteration count; zero
	// selects DefaultMaxIterations. Responses exceeding it are
	// insecure and are not hashed at all.
	MaxIterations uint16
	// MaxSaltLen is the largest accepted salt length; zero selects
	// DefaultMaxSaltLen. Responses exceeding it are rejected.
	MaxSaltLen int
	// MaxHashes caps the total iterated hash computations per
	// response; zero selects DefaultMaxHashes, a negative value
	// disables the cap. Exceeding the cap yields Insecure.
	MaxHashes int
	// HashCount reports how many iterated hash computations this
	// Verifier has performed, for observability.
	HashCount int
}

// NewVerifier returns a Verifier with the default limits.
func NewVerifier() *Verifier {
	return &Verifier{
		MaxIterations: DefaultMaxIterations,
		MaxSaltLen:    DefaultMaxSaltLen,
		MaxHashes:     DefaultMaxHashes,
	}
}

// Reset clears the per-response hash counter.
func (v *Verifier) Reset() { v.HashCount = 0 }

func (v *Verifier) maxIterations() uint16 {
	if v.MaxIterations == 0 {
		return DefaultMaxIterations
	}
	return v.MaxIterations
}

func (v *Verifier) maxSaltLen() int {
	if v.MaxSaltLen == 0 {
		return DefaultMaxSaltLen
	}
	return v.MaxSaltLen
}

func (v *Verifier) maxHashes() int {
	if v.MaxHashes == 0 {
		return DefaultMaxHashes
	}
	return v.MaxHashes
}

// Verify checks the NSEC3 denial proof for qname against records and
// returns the conclusion. A non-nil error means the response was
// rejected as malformed (over-long salt, unknown algorithm, bad name);
// a nil error with Insecure means no proof could be established within
// the configured resource limits.
func (v *Verifier) Verify(qname string, records []Record) (Conclusion, error) {
	if len(records) == 0 {
		return Insecure, nil
	}
	for i := range records {
		r := &records[i]
		if r.HashAlg != HashSHA1 {
			return Insecure, ErrUnsupportedHash
		}
		if len(r.Salt) > v.maxSaltLen() {
			return Insecure, ErrSaltTooLong
		}
		if r.Iterations > v.maxIterations() {
			// CPU-exhaustion guard: treat as insecure without
			// performing any hashing.
			return Insecure, nil
		}
	}

	labels, err := splitName(qname)
	if err != nil {
		return Insecure, err
	}

	// Hashing parameters come from the first record; records with
	// different parameters live in a different hash space and are
	// skipped when matching.
	salt := records[0].Salt
	iter := records[0].Iterations
	sameSpace := func(r *Record) bool {
		return r.Iterations == iter && bytes.Equal(r.Salt, salt)
	}
	match := func(hash []byte) *Record {
		for i := range records {
			if r := &records[i]; sameSpace(r) && bytes.Equal(r.OwnerHash, hash) {
				return r
			}
		}
		return nil
	}
	covering := func(hash []byte) *Record {
		for i := range records {
			if r := &records[i]; sameSpace(r) && covers(r.OwnerHash, r.NextHashed, hash) {
				return r
			}
		}
		return nil
	}

	cache := map[string][]byte{}
	hashOf := func(name string) ([]byte, error) {
		if h, ok := cache[name]; ok {
			return h, nil
		}
		h, err := v.hashName(name, salt, iter)
		if err != nil {
			return nil, err
		}
		cache[name] = h
		return h, nil
	}

	qhash, err := hashOf(qname)
	if err != nil {
		return Insecure, nil
	}
	if match(qhash) != nil {
		return NoData, nil
	}

	// Closest encloser: the longest ancestor of qname whose hash
	// matches an NSEC3 owner name.
	ceIndex := -1
	for i := 1; i < len(labels); i++ {
		h, err := hashOf(strings.Join(labels[i:], "."))
		if err != nil {
			return Insecure, nil
		}
		if match(h) != nil {
			ceIndex = i
			break
		}
	}
	if ceIndex < 0 {
		return Insecure, nil
	}
	ceName := strings.Join(labels[ceIndex:], ".")
	nextCloser := strings.Join(labels[ceIndex-1:], ".")

	nch, err := hashOf(nextCloser)
	if err != nil {
		return Insecure, nil
	}
	nc := covering(nch)
	if nc == nil {
		return Insecure, nil
	}
	if nc.OptOut() {
		// Opt-out on the record covering the next closer name: the
		// conclusion can only be insecure, never NXDOMAIN
		// (RFC 5155 section 8.6).
		return Insecure, nil
	}

	wildHash, err := hashOf("*." + ceName)
	if err != nil {
		return Insecure, nil
	}
	if match(wildHash) != nil {
		return Wildcard, nil
	}
	if covering(wildHash) == nil {
		return Insecure, nil
	}
	return NXDOMAIN, nil
}

// hashName computes the RFC 5155 iterated SHA-1 hash of a domain name:
// IH(salt, x, 0) = SHA-1(x || salt), IH(salt, x, k) = SHA-1(IH(k-1) || salt).
// Each call consumes one unit of the per-response hash budget.
func (v *Verifier) hashName(name string, salt []byte, iterations uint16) ([]byte, error) {
	if max := v.maxHashes(); max >= 0 && v.HashCount >= max {
		return nil, errHashBudget
	}
	wire, err := canonicalWire(name)
	if err != nil {
		return nil, err
	}
	digest := sha1.Sum(append(wire, salt...))
	for i := uint16(0); i < iterations; i++ {
		digest = sha1.Sum(append(digest[:sha1.Size:sha1.Size], salt...))
	}
	v.HashCount++
	return digest[:], nil
}

// covers reports whether hash lies in the NSEC3 interval (owner, next),
// handling intervals that wrap around the end of the hash space.
func covers(owner, next, hash []byte) bool {
	n := len(owner)
	if n == 0 || len(next) != n || len(hash) != n {
		return false
	}
	switch c := bytes.Compare(owner, next); {
	case c < 0:
		return bytes.Compare(owner, hash) < 0 && bytes.Compare(hash, next) < 0
	case c > 0: // wrap-around interval
		return bytes.Compare(hash, owner) > 0 || bytes.Compare(hash, next) < 0
	default: // owner == next covers the whole space except itself
		return !bytes.Equal(hash, owner)
	}
}

// canonicalWire renders a domain name in canonical DNS wire format:
// lower-cased, length-prefixed labels, root-terminated.
func canonicalWire(name string) ([]byte, error) {
	labels, err := splitName(name)
	if err != nil {
		return nil, err
	}
	var wire []byte
	for _, l := range labels {
		wire = append(wire, byte(len(l)))
		wire = append(wire, l...)
	}
	wire = append(wire, 0)
	if len(wire) > 255 {
		return nil, ErrBadName
	}
	return wire, nil
}

func splitName(name string) ([]string, error) {
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return nil, nil
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		if len(p) == 0 || len(p) > 63 {
			return nil, ErrBadName
		}
		parts[i] = strings.ToLower(p)
	}
	return parts, nil
}
