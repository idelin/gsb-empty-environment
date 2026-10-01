package taggedunion

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

func rawItem(tag, version uint16, payload []byte) []byte {
	item := make([]byte, headerSize, headerSize+len(payload)+crcSize)
	binary.BigEndian.PutUint16(item[0:], tag)
	binary.BigEndian.PutUint16(item[2:], version)
	binary.BigEndian.PutUint32(item[4:], uint32(len(payload)))
	item = append(item, payload...)
	var tail [crcSize]byte
	binary.BigEndian.PutUint32(tail[:], crc32.ChecksumIEEE(item))
	return append(item, tail[:]...)
}

func mustDecode(t *testing.T, r *Registry, buf []byte) *Value {
	t.Helper()
	v, next, err := r.DecodeItem(buf, 0, "item")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if next != len(buf) {
		t.Fatalf("consumed %d of %d bytes", next, len(buf))
	}
	return v
}

// Round-trip matrix: every (encode version, decode version) pair.
func TestRoundTripMatrix(t *testing.T) {
	r := DemoRegistry()
	versions := []uint16{1, 2}
	for _, from := range versions {
		for _, to := range versions {
			fields := map[string]any{"name": "alice"}
			if from == 2 {
				fields["email"] = "a@example.com"
			}
			buf, err := r.Encode(&Value{Tag: TagUser, Version: from, Fields: fields})
			if err != nil {
				t.Fatalf("v%d encode: %v", from, err)
			}
			dec := mustDecode(t, r, buf)
			if dec.Opaque {
				t.Fatalf("v%d decoded as opaque", from)
			}
			mig, err := r.Migrate(dec, to)
			if err != nil {
				t.Fatalf("v%d->v%d migrate: %v", from, to, err)
			}
			if mig.Version != to || mig.Fields["name"] != "alice" {
				t.Fatalf("v%d->v%d bad result: %+v", from, to, mig)
			}
			if to == 2 && mig.Fields["email"] != fields["email"] && from == 2 {
				t.Fatalf("v%d->v%d lost email", from, to)
			}
			if _, leaked := mig.Fields["email"]; to == 1 && leaked {
				t.Fatalf("v%d->v%d leaked email into v1", from, to)
			}
			// Re-encode after migration must succeed under the target schema.
			if _, err := r.Encode(mig); err != nil {
				t.Fatalf("v%d->v%d re-encode: %v", from, to, err)
			}
		}
	}
}

func TestMigrationRefusesToGuess(t *testing.T) {
	r := NewRegistry()
	r.Register(&Schema{Tag: TagUser, Version: 1, Fields: []Field{{Name: "name", Kind: KindString}}})
	r.Register(&Schema{Tag: TagUser, Version: 2, Fields: []Field{{Name: "name", Kind: KindString}}})
	v := &Value{Tag: TagUser, Version: 1, Fields: map[string]any{"name": "x"}}
	if _, err := r.Migrate(v, 2); err == nil {
		t.Fatal("expected error: no explicit migration registered")
	}
}

func TestUnknownTagPassthrough(t *testing.T) {
	r := DemoRegistry()
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}
	raw := rawItem(777, 5, payload)
	v := mustDecode(t, r, raw)
	if !v.Opaque {
		t.Fatal("unknown tag must decode as opaque")
	}
	if v.Tag != 777 || v.Version != 5 {
		t.Fatalf("version/checksum metadata lost: tag=%d v=%d", v.Tag, v.Version)
	}
	if !bytes.Equal(v.Payload(), payload) {
		t.Fatal("payload altered")
	}
	re, err := r.Encode(v)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(re, raw) {
		t.Fatal("unknown tag re-encode not byte-identical")
	}
}

// A known tag with an unknown version must pass through verbatim and must
// NOT be interpreted by an older schema whose fields coincidentally fit.
func TestUnknownVersionNotReinterpreted(t *testing.T) {
	r := DemoRegistry()
	// User v1 payload is exactly a u16 length + string. Craft a "v9" item
	// whose payload would parse perfectly as User v1 {name:"hi"}.
	coincidental := append([]byte{0x00, 0x02}, []byte("hi")...)
	raw := rawItem(TagUser, 9, coincidental)
	v := mustDecode(t, r, raw)
	if !v.Opaque {
		t.Fatal("unknown version must be opaque, not parsed by v1 schema")
	}
	re, _ := r.Encode(v)
	if !bytes.Equal(re, raw) {
		t.Fatal("unknown version re-encode not byte-identical")
	}
	if _, err := r.Migrate(v, 1); err == nil {
		t.Fatal("opaque value must not be migratable")
	}
}

// Tag A carrying tag B's fields must be rejected.
func TestTagCarryingWrongBranchFieldsRejected(t *testing.T) {
	r := DemoRegistry()
	// Point{x:0,y:0} payload (8 zero bytes) under a User v1 header:
	// parses name="" then leaves 6 trailing bytes.
	forged := rawItem(TagUser, 1, make([]byte, 8))
	if _, _, err := r.DecodeItem(forged, 0, "forged"); err == nil {
		t.Fatal("expected rejection of User header with Point payload")
	} else if !strings.Contains(err.Error(), "trailing bytes") {
		t.Fatalf("expected trailing-bytes error, got: %v", err)
	}
	// User payload under a Point header: too short for two int32.
	userPayload := append([]byte{0x00, 0x02}, []byte("hi")...)
	forged2 := rawItem(TagPoint, 1, userPayload)
	if _, _, err := r.DecodeItem(forged2, 0, "forged"); err == nil {
		t.Fatal("expected rejection of Point header with User payload")
	}
}

func TestEmptyBranchLegal(t *testing.T) {
	r := DemoRegistry()
	buf, err := r.Encode(&Value{Tag: TagEmpty, Version: 1, Fields: map[string]any{}})
	if err != nil {
		t.Fatalf("encode empty branch: %v", err)
	}
	if want := headerSize + crcSize; len(buf) != want {
		t.Fatalf("empty branch size = %d, want %d", len(buf), want)
	}
	v := mustDecode(t, r, buf)
	if v.Opaque || len(v.Fields) != 0 {
		t.Fatalf("empty branch decoded wrong: %+v", v)
	}
}

func TestBatchAtomicWithErrorLocation(t *testing.T) {
	r := DemoRegistry()
	good, _ := r.Encode(&Value{Tag: TagUser, Version: 2, Fields: map[string]any{
		"name": "bob", "email": "b@example.com",
	}})
	batch := make([]byte, 4)
	binary.BigEndian.PutUint32(batch, 3)
	batch = append(batch, good...)
	batch = append(batch, good...)
	bad := bytes.Clone(good)
	bad[len(bad)-1] ^= 0xFF                      // corrupt crc
	crcOffset := len(batch) + len(bad) - crcSize // error points at crc field start
	batch = append(batch, bad...)

	vals, err := r.DecodeBatch(batch)
	if err == nil {
		t.Fatal("corrupt batch must fail")
	}
	if vals != nil {
		t.Fatal("atomic decode must not return partial results")
	}
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if ce.Path != "batch[2]" {
		t.Fatalf("path = %q, want batch[2]", ce.Path)
	}
	if ce.Offset != crcOffset {
		t.Fatalf("offset = %d, want %d", ce.Offset, crcOffset)
	}
}

func TestFieldErrorLocation(t *testing.T) {
	r := DemoRegistry()
	// User v2 payload: name ok, email length claims 10 but only 1 remains.
	payload := []byte{0x00, 0x01, 'x', 0x00, 0x0A, 'y'}
	buf := rawItem(TagUser, 2, payload)
	_, _, err := r.DecodeItem(buf, 0, "batch[0]")
	if err == nil {
		t.Fatal("expected field error")
	}
	ce := err.(*Error)
	if ce.Path != "batch[0].email" {
		t.Fatalf("path = %q, want batch[0].email", ce.Path)
	}
	// email length field starts at headerSize + 3.
	if ce.Offset != headerSize+3 {
		t.Fatalf("offset = %d, want %d", ce.Offset, headerSize+3)
	}
}

// Length fields must be validated before allocation.
func TestLengthValidatedBeforeAllocation(t *testing.T) {
	r := DemoRegistry()
	// Absurd payload length, tiny buffer.
	buf := make([]byte, headerSize+crcSize)
	binary.BigEndian.PutUint16(buf[0:], TagUser)
	binary.BigEndian.PutUint16(buf[2:], 1)
	binary.BigEndian.PutUint32(buf[4:], 0xFFFFFFFF)
	if _, _, err := r.DecodeItem(buf, 0, "item"); err == nil {
		t.Fatal("oversized declared length must fail")
	}
	// Absurd batch count.
	batch := make([]byte, 4)
	binary.BigEndian.PutUint32(batch, 0xFFFFFFFF)
	if _, err := r.DecodeBatch(batch); err == nil {
		t.Fatal("oversized item count must fail")
	}
}

func TestChecksumTamperRejected(t *testing.T) {
	r := DemoRegistry()
	buf, _ := r.Encode(&Value{Tag: TagUser, Version: 1, Fields: map[string]any{"name": "zed"}})
	buf[len(buf)-2] ^= 0x01
	if _, _, err := r.DecodeItem(buf, 0, "item"); err == nil {
		t.Fatal("tampered crc must fail")
	}
}

// Independent-parser check: rewrite the payload of an unknown-tagged item
// (bytes that would be meaningful fields to some other schema), fix its
// checksum, and verify the schema-aware decoder still round-trips the
// mutated bytes exactly.
func TestIndependentParserUnknownMutationPreserved(t *testing.T) {
	r := DemoRegistry()
	known, _ := r.Encode(&Value{Tag: TagUser, Version: 2, Fields: map[string]any{
		"name": "carol", "email": "c@example.com",
	}})
	unknown := rawItem(999, 1, []byte{0x00, 0x02, 'h', 'i'}) // looks like User v1 {name:"hi"}

	batch := make([]byte, 4)
	binary.BigEndian.PutUint32(batch, 2)
	batch = append(batch, known...)
	batch = append(batch, unknown...)

	// Independent structural parse: no schema knowledge involved.
	envs, err := Walk(batch)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(envs) != 2 || envs[1].Tag != 999 {
		t.Fatalf("unexpected envelopes: %+v", envs)
	}

	// Mutate the unknown item's "known-looking" fields in place and
	// repair the checksum so the envelope stays well-formed.
	mutated := bytes.Clone(batch)
	p := envs[1].Offset + headerSize
	mutated[p+2], mutated[p+3] = 'B', 'Y' // "hi" -> "BY"
	crcOff := envs[1].Offset + len(envs[1].Raw) - crcSize
	binary.BigEndian.PutUint32(mutated[crcOff:], crc32.ChecksumIEEE(mutated[envs[1].Offset:crcOff]))

	// Schema-aware decode + re-encode must preserve every byte.
	vals, err := r.DecodeBatch(mutated)
	if err != nil {
		t.Fatalf("decode mutated batch: %v", err)
	}
	if !vals[1].Opaque {
		t.Fatal("unknown tag became non-opaque after mutation")
	}
	out, err := r.EncodeBatch(vals)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(out, mutated) {
		t.Fatal("mutated unknown payload not preserved byte-for-byte")
	}
	if !bytes.Equal(vals[1].Payload(), []byte{0x00, 0x02, 'B', 'Y'}) {
		t.Fatalf("payload = %x", vals[1].Payload())
	}
}

func TestBatchRoundTripMixed(t *testing.T) {
	r := DemoRegistry()
	vals := []*Value{
		{Tag: TagUser, Version: 1, Fields: map[string]any{"name": "a"}},
		{Tag: TagUser, Version: 2, Fields: map[string]any{"name": "b", "email": "b@x"}},
		{Tag: TagPoint, Version: 1, Fields: map[string]any{"x": int32(-3), "y": int32(7)}},
		{Tag: TagEmpty, Version: 1, Fields: map[string]any{}},
	}
	buf, err := r.EncodeBatch(vals)
	if err != nil {
		t.Fatalf("encode batch: %v", err)
	}
	// Append an unknown-tag item by hand.
	unknown := rawItem(4242, 3, []byte{1, 2, 3})
	binary.BigEndian.PutUint32(buf, 5)
	buf = append(buf, unknown...)

	out, err := r.DecodeBatch(buf)
	if err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(out) != 5 || !out[4].Opaque {
		t.Fatalf("decoded %d values, opaque=%v", len(out), out[4].Opaque)
	}
	re, err := r.EncodeBatch(out)
	if err != nil {
		t.Fatalf("re-encode batch: %v", err)
	}
	if !bytes.Equal(re, buf) {
		t.Fatal("mixed batch round-trip not byte-identical")
	}
}
