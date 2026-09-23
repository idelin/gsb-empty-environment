// Package gen generates structured valid PKV1 messages and single-point
// mutations. The generator knows the format grammar and the dependency
// graph between constraints (length prefixes, quoting, checksum): every
// mutation breaks exactly one labeled constraint while repairing the
// dependent ones (e.g. the CRC32 trailer is recomputed unless the checksum
// itself is the target), so all other premises stay valid.
package gen

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand"

	"pkv/format"
)

const keyChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-"

// valuePool favors characters that require escaping so quoting is exercised.
var valuePool = []byte("abcXYZ 019._-\"\\\"\\\n\n\t\t=:*")

// ValidMessage returns a random in-scope message, deterministic for a given
// rng state.
func ValidMessage(r *rand.Rand) *format.Message {
	n := 1 + r.Intn(8)
	recs := make([]format.Record, n)
	for i := range recs {
		klen := 1 + r.Intn(12)
		key := make([]byte, klen)
		for j := range key {
			key[j] = keyChars[r.Intn(len(keyChars))]
		}
		vlen := r.Intn(40)
		val := make([]byte, vlen)
		for j := range val {
			val[j] = valuePool[r.Intn(len(valuePool))]
		}
		recs[i] = format.Record{Key: string(key), Value: string(val)}
	}
	return &format.Message{Records: recs}
}

// Mutant is one concrete mutated byte string plus what the spec says about
// it. Expect is the constraint class a correct decoder must reject it with;
// Expect == format.ClassNone means the mutant is still valid and a correct
// decoder must accept it and decode it to Want.
type Mutant struct {
	Name   string
	Data   []byte
	Expect format.Class
	Want   *format.Message
}

// Mutation is a single-point mutation rule. Apply derives the mutant from a
// message and its canonical encoding, so the same rule can be re-applied to
// shrunk messages during reduction.
type Mutation struct {
	Name   string
	Expect format.Class
	Apply  func(m *format.Message, canonical []byte) Mutant
}

// recordOffsets locates every field boundary inside the canonical encoding.
type recordOffsets struct {
	klen, key, vlen, qval, next int
}

func offsets(m *format.Message) []recordOffsets {
	offs := make([]recordOffsets, len(m.Records))
	cur := 6
	for i, rec := range m.Records {
		vlen := format.EscapedLen(rec.Value)
		offs[i] = recordOffsets{
			klen: cur,
			key:  cur + 2,
			vlen: cur + 2 + len(rec.Key),
			qval: cur + 2 + len(rec.Key) + 2,
			next: cur + 2 + len(rec.Key) + 2 + vlen,
		}
		cur = offs[i].next
	}
	return offs
}

// mutate copies canon, applies fn, then repairs the CRC32 trailer so the
// checksum constraint stays valid and only the targeted constraint is broken.
func mutate(canon []byte, fn func(b []byte)) []byte {
	b := make([]byte, len(canon))
	copy(b, canon)
	fn(b)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[:len(b)-4]))
	return b
}

func cloneMessage(m *format.Message) *format.Message {
	recs := make([]format.Record, len(m.Records))
	copy(recs, m.Records)
	return &format.Message{Records: recs}
}

// Mutations returns the full set of single-point mutation rules, one per
// constraint, plus one mutation that stays valid on purpose.
func Mutations() []Mutation {
	return []Mutation{
		{
			Name:   "magic",
			Expect: format.ClassMagic,
			Apply: func(m *format.Message, canon []byte) Mutant {
				b := mutate(canon, func(b []byte) { b[0] ^= 0xFF }) // breaks C1 magic
				return Mutant{Name: "magic", Data: b, Expect: format.ClassMagic}
			},
		},
		{
			Name:   "count",
			Expect: format.ClassCount,
			Apply: func(m *format.Message, canon []byte) Mutant {
				b := mutate(canon, func(b []byte) {
					binary.BigEndian.PutUint16(b[4:6], uint16(len(m.Records)+1)) // breaks C2
				})
				return Mutant{Name: "count", Data: b, Expect: format.ClassCount}
			},
		},
		{
			Name:   "key_length",
			Expect: format.ClassKeyLen,
			Apply: func(m *format.Message, canon []byte) Mutant {
				b := mutate(canon, func(b []byte) {
					binary.BigEndian.PutUint16(b[offsets(m)[0].klen:], format.MaxKeyLen+1) // breaks C3
				})
				return Mutant{Name: "key_length", Data: b, Expect: format.ClassKeyLen}
			},
		},
		{
			Name:   "key_charset",
			Expect: format.ClassKeyCharset,
			Apply: func(m *format.Message, canon []byte) Mutant {
				b := mutate(canon, func(b []byte) { b[offsets(m)[0].key] = '=' }) // breaks C4
				return Mutant{Name: "key_charset", Data: b, Expect: format.ClassKeyCharset}
			},
		},
		{
			Name:   "value_length",
			Expect: format.ClassValueLen,
			Apply: func(m *format.Message, canon []byte) Mutant {
				b := mutate(canon, func(b []byte) {
					off := offsets(m)[0].vlen
					vlen := binary.BigEndian.Uint16(b[off:])
					binary.BigEndian.PutUint16(b[off:], vlen+1) // breaks C5: declared != actual
				})
				return Mutant{Name: "value_length", Data: b, Expect: format.ClassValueLen}
			},
		},
		{
			Name:   "quoting",
			Expect: format.ClassQuoting,
			Apply: func(m *format.Message, canon []byte) Mutant {
				offs := offsets(m)
				b := mutate(canon, func(b []byte) {
					// Prefer corrupting an existing escape sequence; otherwise
					// corrupt the opening quote of the first value.
					for i := range m.Records {
						q := b[offs[i].qval:offs[i].next]
						for j := 0; j+1 < len(q); j++ {
							if q[j] == '\\' {
								b[offs[i].qval+j+1] = 'x' // breaks C6: invalid escape
								return
							}
						}
					}
					b[offs[0].qval] = 'x' // breaks C6: missing opening quote
				})
				return Mutant{Name: "quoting", Data: b, Expect: format.ClassQuoting}
			},
		},
		{
			Name:   "checksum",
			Expect: format.ClassChecksum,
			Apply: func(m *format.Message, canon []byte) Mutant {
				b := make([]byte, len(canon))
				copy(b, canon)
				b[len(b)-1] ^= 0xFF // breaks C7; trailer deliberately NOT repaired
				return Mutant{Name: "checksum", Data: b, Expect: format.ClassChecksum}
			},
		},
		{
			Name:   "truncated",
			Expect: format.ClassTruncated,
			Apply: func(m *format.Message, canon []byte) Mutant {
				return Mutant{Name: "truncated", Data: canon[:9], Expect: format.ClassTruncated}
			},
		},
		{
			Name:   "valid_key_substitution",
			Expect: format.ClassNone, // still valid: must NOT be treated as a parse error
			Apply: func(m *format.Message, canon []byte) Mutant {
				replacement := byte('A')
				if m.Records[0].Key[0] == 'A' {
					replacement = 'B'
				}
				b := mutate(canon, func(b []byte) { b[offsets(m)[0].key] = replacement })
				want := cloneMessage(m)
				k := []byte(want.Records[0].Key)
				k[0] = replacement
				want.Records[0].Key = string(k)
				return Mutant{Name: "valid_key_substitution", Data: b, Expect: format.ClassNone, Want: want}
			},
		},
	}
}
