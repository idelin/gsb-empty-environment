package nsec3

import (
	"math/big"
	"sort"
	"testing"
)

// testParams mirrors the RFC 5155 Appendix A chain parameters.
var testSalt = []byte{0xaa, 0xbb, 0xcc, 0xdd}

const (
	testAlg  = HashSHA1
	testIter = uint16(12)
)

func mustHash(t *testing.T, name string) []byte {
	t.Helper()
	h, err := HashName(testAlg, testIter, testSalt, name)
	if err != nil {
		t.Fatalf("hash %q: %v", name, err)
	}
	return h
}

// ringRecord is one element of a synthetic NSEC3 chain.
type ringRecord struct {
	name   string
	types  []uint16
	optOut bool
}

// buildRing hashes every named owner, sorts the hashes along the ring and
// links each NextHash to its successor (the last wraps to the first).
func buildRing(t *testing.T, elems []ringRecord) []*Record {
	t.Helper()
	type node struct {
		hash []byte
		rec  *Record
	}
	nodes := make([]node, 0, len(elems))
	for _, e := range elems {
		h := mustHash(t, e.name)
		flags := uint8(0)
		if e.optOut {
			flags = optOutFlag
		}
		nodes = append(nodes, node{
			hash: h,
			rec: &Record{
				Algorithm:  testAlg,
				Flags:      flags,
				Iterations: testIter,
				Salt:       append([]byte(nil), testSalt...),
				OwnerHash:  h,
				Types:      typeSet(e.types),
			},
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return compareBytes(nodes[i].hash, nodes[j].hash) < 0
	})
	for i := range nodes {
		next := nodes[(i+1)%len(nodes)].hash
		nodes[i].rec.NextHash = append([]byte(nil), next...)
	}
	out := make([]*Record, len(nodes))
	for i, n := range nodes {
		out[i] = n.rec
	}
	return out
}

func typeSet(types []uint16) map[uint16]bool {
	m := make(map[uint16]bool, len(types))
	for _, ty := range types {
		m[ty] = true
	}
	return m
}

func compareBytes(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// incrementBytes adds delta to a 160-bit hash value with ring wrap.
func incrementBytes(h []byte, delta int) []byte {
	out := append([]byte(nil), h...)
	carry := delta
	for i := len(out) - 1; i >= 0 && carry > 0; i-- {
		sum := int(out[i]) + carry
		out[i] = byte(sum & 0xFF)
		carry = sum >> 8
	}
	return out
}

// newVerifier is a test helper that fails the test on construction error.
func newVerifier(t *testing.T, recs []*Record) *Verifier {
	t.Helper()
	v, err := NewVerifier(recs)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// markCoveringOptOut sets the Opt-Out flag on whichever ring element
// covers the hash of name, mirroring how an authoritative server flags
// the single NSEC3 record that brackets an unsigned delegation.
func markCoveringOptOut(t *testing.T, recs []*Record, name string) *Record {
	t.Helper()
	h := mustHash(t, name)
	for _, r := range recs {
		if RecordCoversHash(r, h) {
			r.Flags |= optOutFlag
			return r
		}
	}
	t.Fatalf("no record covers %s", name)
	return nil
}

// clearCoveringOptOut removes the Opt-Out flag from the record covering
// name, used to build forged (flag-missing) proofs.
func clearCoveringOptOut(t *testing.T, recs []*Record, name string) {
	t.Helper()
	h := mustHash(t, name)
	for _, r := range recs {
		if RecordCoversHash(r, h) {
			r.Flags &^= optOutFlag
			return
		}
	}
	t.Fatalf("no record covers %s", name)
}

// midpoint returns (a+b)/2 as an unsigned fixed-width integer.
func midpoint(a, b []byte) []byte {
	ai := new(big.Int).SetBytes(a)
	bi := new(big.Int).SetBytes(b)
	sum := new(big.Int).Add(ai, bi)
	return sum.Rsh(sum, 1).FillBytes(make([]byte, len(a)))
}
