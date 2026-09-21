package bitlayout_test

import (
	"reflect"
	"testing"

	"bitlayout"
	"bitlayout/internal/refimpl"
)

// smallUnionSchema has variable-length records: tag(2) + union(4|6 bits)
// + pad-to-byte + 4-bit checksum. Every record is exactly 12 bits, so the
// whole input space is 4096 patterns.
const smallUnionSchema = `{
  "name": "small", "bitOrder": "msb", "byteOrder": "big",
  "fields": [
    { "kind": "int", "name": "tag", "bits": 2 },
    { "kind": "union", "name": "u", "tagFrom": "tag", "cases": {
        "0": { "fields": [ { "kind": "int", "name": "a", "bits": 4 } ] },
        "1": { "fields": [ { "kind": "int", "name": "b", "bits": 6 } ] }
    } },
    { "kind": "pad", "align": 8 },
    { "kind": "checksum", "name": "ck", "bits": 4, "algo": "sum", "from": "tag", "to": "u" }
  ]
}`

// tagValueSchema is the minimal acceptance record: 3-bit tag + 10-bit
// signed value, 13 bits total, 8192 patterns.
const tagValueSchema = `{
  "name": "tv", "bitOrder": "msb", "byteOrder": "big",
  "fields": [
    { "kind": "int", "name": "tag", "bits": 3 },
    { "kind": "int", "name": "val", "bits": 10, "signed": true }
  ]
}`

// crossByteSchema exercises arrays crossing byte boundaries plus an LSB /
// little-endian layout: len(2) + n x 5-bit signed elements + pad + crc.
const crossByteSchema = `{
  "name": "cb", "bitOrder": "lsb", "byteOrder": "little",
  "fields": [
    { "kind": "int", "name": "n", "bits": 2 },
    { "kind": "array", "name": "els", "lengthFrom": "n",
      "element": { "kind": "int", "bits": 5, "signed": true } },
    { "kind": "pad", "align": 8 },
    { "kind": "checksum", "name": "ck", "bits": 4, "algo": "xor", "from": "n", "to": "els" }
  ]
}`

// exhaustDecode decodes every pattern of nbits with both implementations
// and compares records or error kinds.
func exhaustDecode(t *testing.T, schemaJSON string, nbits int) {
	t.Helper()
	plan := mustCompile(t, schemaJSON)
	// generous quotas: the reference has no quota notion
	opts := &bitlayout.DecodeOptions{MaxDepth: 16, MaxTotalElements: 1 << 20, MaxTotalBits: 1 << 24}
	for pattern := 0; pattern < 1<<uint(nbits); pattern++ {
		bits := make([]bool, nbits)
		for i := 0; i < nbits; i++ {
			bits[i] = pattern>>uint(nbits-1-i)&1 == 1
		}
		buf := bitsToBytes(bits, plan.BitOrder)
		pRec, _, pErr := plan.Decode(buf, int64(nbits), opts)
		rRec, _, rErr := refimpl.Decode([]byte(schemaJSON), bits)
		if (pErr == nil) != (rErr == nil) {
			t.Fatalf("pattern %0*b: production err %v, reference err %v", nbits, pattern, pErr, rErr)
		}
		if pErr != nil {
			pk, rk := prodKind(pErr), refKind(rErr)
			want := map[bitlayout.ErrKind]refimpl.Kind{
				bitlayout.ErrNeedMoreInput: refimpl.NeedMore,
				bitlayout.ErrReservedBits:  refimpl.Reserved,
				bitlayout.ErrValue:         refimpl.Value,
			}[pk]
			if want == "" || rk != want {
				t.Fatalf("pattern %0*b: error kind mismatch: production %v, reference %v", nbits, pattern, pk, rk)
			}
			continue
		}
		if !reflect.DeepEqual(pRec, rRec) {
			t.Fatalf("pattern %0*b: records differ\nprod: %#v\nref:  %#v", nbits, pattern, pRec, rRec)
		}
	}
}

func TestExhaustiveTagValue(t *testing.T) {
	exhaustDecode(t, tagValueSchema, 13)
}

func TestExhaustiveSmallUnion(t *testing.T) {
	exhaustDecode(t, smallUnionSchema, 12)
}

// TestExhaustiveCrossByte covers every input for the small record sizes
// (n=0 and n=1 both produce 16-bit records) and samples the larger ones.
func TestExhaustiveCrossByte(t *testing.T) {
	exhaustDecode(t, crossByteSchema, 16) // covers n=0 and n=1 exhaustively

	// Sample the n=2 (24-bit) and n=3 (32-bit) spaces deterministically.
	plan := mustCompile(t, crossByteSchema)
	opts := &bitlayout.DecodeOptions{MaxDepth: 16, MaxTotalElements: 1 << 20, MaxTotalBits: 1 << 24}
	for _, nbits := range []int{24, 32} {
		for sample := 0; sample < 20000; sample++ {
			pattern := uint64(sample)*2654435761 + uint64(nbits)
			bits := make([]bool, nbits)
			for i := 0; i < nbits; i++ {
				bits[i] = pattern>>uint(i%31)&1 == 1
			}
			buf := bitsToBytes(bits, plan.BitOrder)
			pRec, _, pErr := plan.Decode(buf, int64(nbits), opts)
			rRec, _, rErr := refimpl.Decode([]byte(crossByteSchema), bits)
			if (pErr == nil) != (rErr == nil) {
				t.Fatalf("nbits=%d sample %d: production err %v, reference err %v", nbits, sample, pErr, rErr)
			}
			if pErr == nil && !reflect.DeepEqual(pRec, rRec) {
				t.Fatalf("nbits=%d sample %d: records differ", nbits, sample)
			}
		}
	}
}

// TestExhaustiveEncode encodes every valid record of the small schemas
// with both implementations and compares the emitted bits, then decodes
// the production bits and expects the original values back.
func TestExhaustiveEncode(t *testing.T) {
	plan := mustCompile(t, smallUnionSchema)
	for tag := uint64(0); tag < 2; tag++ {
		max := uint64(16)
		name := "a"
		if tag == 1 {
			max, name = 64, "b"
		}
		for v := uint64(0); v < max; v++ {
			rec := map[string]any{"tag": tag, "u": map[string]any{name: v}}
			nbits, err := plan.Validate(rec)
			if err != nil {
				t.Fatalf("tag %d v %d: %v", tag, v, err)
			}
			buf := make([]byte, (nbits+7)/8)
			if _, err := plan.Encode(rec, buf); err != nil {
				t.Fatalf("tag %d v %d encode: %v", tag, v, err)
			}
			refBits, err := refimpl.Encode([]byte(smallUnionSchema), rec)
			if err != nil {
				t.Fatalf("tag %d v %d ref: %v", tag, v, err)
			}
			if !reflect.DeepEqual(bytesToBits(buf, int(nbits), plan.BitOrder), refBits) {
				t.Fatalf("tag %d v %d: bit mismatch", tag, v)
			}
			got, _, err := plan.Decode(buf, nbits, nil)
			if err != nil {
				t.Fatalf("tag %d v %d decode: %v", tag, v, err)
			}
			if got["tag"] != tag || got["u"].(map[string]any)[name] != v {
				t.Fatalf("tag %d v %d: round trip gave %#v", tag, v, got)
			}
		}
	}
}

// TestExhaustiveCrossByteEncode encodes every record of the cross-byte
// schema (n up to 3, 5-bit signed elements) and compares bits.
func TestExhaustiveCrossByteEncode(t *testing.T) {
	plan := mustCompile(t, crossByteSchema)
	var rec func(n int, vals []int64) map[string]any
	rec = func(n int, vals []int64) map[string]any {
		els := make([]any, n)
		for i, v := range vals {
			els[i] = v
		}
		return map[string]any{"n": uint64(n), "els": els}
	}
	vals := []int64{-16, -9, -1, 0, 1, 7, 15}
	for n := 0; n <= 3; n++ {
		total := 1
		for i := 0; i < n; i++ {
			total *= len(vals)
		}
		for combo := 0; combo < total; combo++ {
			vs := make([]int64, n)
			for i, c := 0, combo; i < n; i, c = i+1, c/len(vals) {
				vs[i] = vals[c%len(vals)]
			}
			r := rec(n, vs)
			nbits, err := plan.Validate(r)
			if err != nil {
				t.Fatalf("n=%d %v: %v", n, vs, err)
			}
			buf := make([]byte, (nbits+7)/8)
			if _, err := plan.Encode(r, buf); err != nil {
				t.Fatalf("n=%d %v encode: %v", n, vs, err)
			}
			refBits, err := refimpl.Encode([]byte(crossByteSchema), r)
			if err != nil {
				t.Fatalf("n=%d %v ref: %v", n, vs, err)
			}
			if !reflect.DeepEqual(bytesToBits(buf, int(nbits), plan.BitOrder), refBits) {
				t.Fatalf("n=%d %v: bit mismatch", n, vs)
			}
		}
	}
}
