package threshold

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
)

// Point is an opaque group element. Concrete Group implementations may use
// their own representation; persistent messages always use Encode/Decode.
type Point any

// Group is the injectable prime-order group interface used by DKG and FROST.
//
// All scalar arguments are reduced modulo Order by implementations. Point
// values returned by one Group must not be passed to another Group.
type Group interface {
	Name() string
	Order() *big.Int
	Generator() Point
	ScalarBaseMult(s *big.Int) Point
	ScalarMult(p Point, s *big.Int) Point
	Add(a, b Point) Point
	Neg(a Point) Point
	Equal(a, b Point) bool
	Encode(p Point) []byte
	Decode(b []byte) (Point, error)
	RandomScalar(r io.Reader) (*big.Int, error)
	HashToScalar(domain string, chunks ...[]byte) *big.Int
}

// ID is a stable application-level participant identifier.
type ID string

func (id ID) String() string { return string(id) }

// Identity binds a human/application-visible ID to the nonzero scalar index
// used by Feldman evaluation and Lagrange interpolation.
type Identity struct {
	ID    ID  `json:"id"`
	Index int `json:"index"`
}

func scalarByteLen(g Group) int {
	return (g.Order().BitLen() + 7) / 8
}

func appendFrame(dst, b []byte) []byte {
	var length [4]byte
	if uint64(len(b)) > uint64(^uint32(0)) {
		panic("threshold: frame too large")
	}
	binary.BigEndian.PutUint32(length[:], uint32(len(b)))
	dst = append(dst, length[:]...)
	return append(dst, b...)
}

func appendUint64(dst []byte, x uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], x)
	return append(dst, b[:]...)
}

func fixedBigIntBytes(x *big.Int, size int) []byte {
	if x == nil || x.Sign() < 0 {
		panic("threshold: cannot encode negative or nil scalar")
	}
	b := x.Bytes()
	if len(b) > size {
		panic("threshold: scalar exceeds fixed length")
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

func encodeScalar(g Group, x *big.Int) []byte {
	return fixedBigIntBytes(new(big.Int).Mod(new(big.Int).Set(x), g.Order()), scalarByteLen(g))
}

func parseScalar(g Group, b []byte) (*big.Int, error) {
	size := scalarByteLen(g)
	if len(b) != size {
		return nil, fmt.Errorf("%w: scalar length %d, want %d", ErrInvalidConfig, len(b), size)
	}
	s := new(big.Int).SetBytes(b)
	if s.Sign() < 0 || s.Cmp(g.Order()) >= 0 {
		return nil, fmt.Errorf("%w: scalar outside field", ErrInvalidConfig)
	}
	return s, nil
}

func mustParseScalar(g Group, b []byte) *big.Int {
	s, err := parseScalar(g, b)
	if err != nil {
		panic(err)
	}
	return s
}

func decodePointBytes(g Group, b []byte) (Point, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: empty point", ErrInvalidCommitment)
	}
	p, err := g.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCommitment, err)
	}
	return p, nil
}

func hashToScalarModN(order *big.Int, domain string, chunks ...[]byte) *big.Int {
	var transcript []byte
	transcript = appendFrame(transcript, []byte(domain))
	for _, chunk := range chunks {
		transcript = appendFrame(transcript, chunk)
	}
	digest := sha512.Sum512(transcript)
	return new(big.Int).Mod(new(big.Int).SetBytes(digest[:]), order)
}

func randomNonzeroScalar(order *big.Int, r io.Reader) (*big.Int, error) {
	if r == nil {
		r = rand.Reader
	}
	for {
		s, err := rand.Int(r, order)
		if err != nil {
			return nil, err
		}
		if s.Sign() != 0 {
			return s, nil
		}
	}
}

func cloneBytesMap(m map[ID][]byte) map[ID][]byte {
	if m == nil {
		return nil
	}
	out := make(map[ID][]byte, len(m))
	for k, v := range m {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

func cloneByteSlices(in [][]byte) [][]byte {
	if in == nil {
		return nil
	}
	out := make([][]byte, len(in))
	for i, b := range in {
		out[i] = append([]byte(nil), b...)
	}
	return out
}
