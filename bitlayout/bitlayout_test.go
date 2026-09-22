package bitlayout

import (
	"bytes"
	"reflect"
	"testing"
)

func mustCompile(t *testing.T, s *Schema) *Plan {
	t.Helper()
	p, err := Compile(s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return p
}

// tagValueSchema is the acceptance starting point: a 3-bit tag followed by
// a 10-bit signed value.
func tagValueSchema(bitOrder, byteOrder string) *Schema {
	return &Schema{
		Name:      "tv",
		BitOrder:  bitOrder,
		ByteOrder: byteOrder,
		Fields: []FieldSpec{
			{Name: "tag", Type: "uint", Bits: 3},
			{Name: "value", Type: "int", Bits: 10},
		},
	}
}

// telemetrySchema is the full acceptance layout: 3-bit tag, 10-bit signed
// value, two union branches of different lengths, a cross-byte array and a
// trailing checksum.
func telemetrySchema(bitOrder, byteOrder string) *Schema {
	return &Schema{
		Name:      "telemetry",
		BitOrder:  bitOrder,
		ByteOrder: byteOrder,
		Fields: []FieldSpec{
			{Name: "tag", Type: "uint", Bits: 3, Enum: []uint64{0, 1}},
			{Name: "value", Type: "int", Bits: 10},
			{Name: "body", Type: "union", TagFrom: "tag", Cases: map[string]*StructSpec{
				"0": {Fields: []FieldSpec{{Name: "code", Type: "uint", Bits: 5}}},
				"1": {Fields: []FieldSpec{{Name: "reading", Type: "uint", Bits: 12}}},
			}},
			{Name: "n", Type: "uint", Bits: 4},
			{Name: "samples", Type: "array", LengthFrom: "n", Element: &StructSpec{
				Fields: []FieldSpec{{Name: "v", Type: "uint", Bits: 6}},
			}},
			{Name: "pad", Type: "align", Multiple: 8},
			{Name: "crc", Type: "checksum", Bits: 8, From: "tag", To: "pad"},
		},
	}
}

func telemetryObjects() []map[string]any {
	return []map[string]any{
		{
			"tag": uint64(0), "value": int64(-512),
			"body": map[string]any{"code": uint64(31)},
			"n":    uint64(0), "samples": []any{},
		},
		{
			"tag": uint64(1), "value": int64(511),
			"body": map[string]any{"reading": uint64(4095)},
			"n":    uint64(3),
			"samples": []any{
				map[string]any{"v": uint64(1)},
				map[string]any{"v": uint64(63)},
				map[string]any{"v": uint64(17)},
			},
		},
		{
			"tag": uint64(1), "value": int64(-37),
			"body": map[string]any{"reading": uint64(2345)},
			"n":    uint64(7),
			"samples": []any{
				map[string]any{"v": uint64(0)}, map[string]any{"v": uint64(5)},
				map[string]any{"v": uint64(62)}, map[string]any{"v": uint64(33)},
				map[string]any{"v": uint64(9)}, map[string]any{"v": uint64(41)},
				map[string]any{"v": uint64(27)},
			},
		},
	}
}

// TestChunking feeds records in every possible byte-chunking and at
// arbitrary bit stops; every split must deliver exactly the one-shot result.
func TestChunking(t *testing.T) {
	for _, c := range orderCombos {
		p := mustCompile(t, telemetrySchema(c.bit, c.byte))
		for _, obj := range telemetryObjects() {
			data, nbits, err := p.EncodeBits(obj)
			if err != nil {
				t.Fatalf("EncodeBits: %v", err)
			}
			want, wantUsed, err := decodeWhole(t, p, data, nbits)
			if err != nil {
				t.Fatalf("one-shot decode: %v", err)
			}
			nbytes := len(data)
			// All 2^(nbytes-1) byte chunkings.
			for mask := 0; mask < 1<<uint(nbytes-1); mask++ {
				s := p.NewSession(Quotas{})
				start := 0
				var runErr error
				for i := 0; i < nbytes; i++ {
					if i == nbytes-1 || mask&(1<<uint(i)) != 0 {
						s.Feed(data[start : i+1])
						runErr = s.Run()
						start = i + 1
					}
				}
				if runErr != nil {
					t.Fatalf("%s/%s mask=%b: %v", c.bit, c.byte, mask, runErr)
				}
				got, used, ok := s.Result()
				if !ok || used != wantUsed || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s/%s mask=%b: got %v@%d want %v@%d",
						c.bit, c.byte, mask, got, used, want, wantUsed)
				}
			}
			// Stop at every individual bit boundary.
			for stop := uint64(0); stop <= uint64(nbits); stop++ {
				s := p.NewSession(Quotas{})
				whole := int(stop / 8)
				s.Feed(data[:whole])
				if rem := int(stop % 8); rem > 0 {
					if err := s.FeedBits(data[whole:whole+1], rem); err != nil {
						t.Fatalf("FeedBits: %v", err)
					}
				}
				err := s.Run()
				if stop < uint64(nbits) {
					if !IsNeedInput(err) {
						t.Fatalf("%s/%s stop=%d: want need-input, got %v", c.bit, c.byte, stop, err)
					}
					if stop%8 != 0 {
						continue // mid-byte stops cannot be resumed by byte feeds
					}
					// Feed the rest and finish.
					s.Feed(data[stop/8:])
					if err := s.Run(); err != nil {
						t.Fatalf("resume after stop=%d: %v", stop, err)
					}
					got, _, ok := s.Result()
					if !ok || !reflect.DeepEqual(got, want) {
						t.Fatalf("resume after stop=%d: got %v want %v", stop, got, want)
					}
				} else if err != nil {
					t.Fatalf("stop=%d (full input): %v", stop, err)
				}
			}
		}
	}
}

// TestEncodeAtomicity: a record whose last array element is out of range
// must fail validation before a single bit is written.
func TestEncodeAtomicity(t *testing.T) {
	p := mustCompile(t, arraySchema("msb", "big"))
	obj := map[string]any{
		"n": uint64(2),
		"items": []any{
			map[string]any{"v": uint64(7)},
			map[string]any{"v": uint64(32)}, // 32 does not fit 5 bits
		},
	}
	dst := bytes.Repeat([]byte{0xA5}, 16)
	before := append([]byte(nil), dst...)
	if _, err := p.Encode(obj, dst); err == nil {
		t.Fatalf("expected error for out-of-range last element")
	} else if e, ok := err.(*Error); !ok || e.Kind != ErrIllegalValue {
		t.Fatalf("want ErrIllegalValue, got %v", err)
	}
	if !bytes.Equal(dst, before) {
		t.Fatalf("destination buffer modified on failed encode: %x", dst)
	}

	// Capacity: a valid record that does not fit must not be written.
	good := map[string]any{
		"n": uint64(2),
		"items": []any{
			map[string]any{"v": uint64(7)},
			map[string]any{"v": uint64(9)},
		},
	}
	small := bytes.Repeat([]byte{0x3C}, 1) // record needs 13 bits
	if _, err := p.Encode(good, small); err == nil {
		t.Fatalf("expected capacity error")
	} else if e, ok := err.(*Error); !ok || e.Kind != ErrCapacity {
		t.Fatalf("want ErrCapacity, got %v", err)
	}
	if !bytes.Equal(small, bytes.Repeat([]byte{0x3C}, 1)) {
		t.Fatalf("destination buffer modified on capacity failure: %x", small)
	}
}

// TestChecksumAtomicity: a corrupted trailing checksum must fail the whole
// record; the session must not deliver a partial result.
func TestChecksumAtomicity(t *testing.T) {
	p := mustCompile(t, telemetrySchema("msb", "big"))
	obj := telemetryObjects()[1]
	data, nbits, err := p.EncodeBits(obj)
	if err != nil {
		t.Fatalf("EncodeBits: %v", err)
	}
	// Flip every bit of the checksum field (the final 8 bits).
	for i := 0; i < 8; i++ {
		bad := append([]byte(nil), data...)
		bit := uint64(nbits) - 1 - uint64(i)
		bad[bit/8] ^= 1 << (7 - bit%8)
		s := p.NewSession(Quotas{})
		s.Feed(bad)
		err := s.Run()
		e, ok := err.(*Error)
		if !ok || e.Kind != ErrIllegalValue {
			t.Fatalf("flip %d: want ErrIllegalValue, got %v", i, err)
		}
		if _, _, ok := s.Result(); ok {
			t.Fatalf("flip %d: session delivered a result after checksum failure", i)
		}
		// The error carries the schema path and bit offset of the checksum.
		if e.Path != "telemetry.crc" || e.Bit != uint64(nbits)-8 {
			t.Fatalf("flip %d: error at %s bit %d, want telemetry.crc bit %d",
				i, e.Path, e.Bit, nbits-8)
		}
	}
}

// TestReservedBits: non-zero alignment padding is reported distinctly.
func TestReservedBits(t *testing.T) {
	s := &Schema{
		Name: "padrec",
		Fields: []FieldSpec{
			{Name: "a", Type: "uint", Bits: 3},
			{Name: "pad", Type: "align", Multiple: 8},
			{Name: "b", Type: "uint", Bits: 4},
		},
	}
	p := mustCompile(t, s)
	good := map[string]any{"a": uint64(5), "b": uint64(9)}
	data, nbits, err := p.EncodeBits(good)
	if err != nil {
		t.Fatalf("EncodeBits: %v", err)
	}
	if nbits != 12 {
		t.Fatalf("nbits = %d, want 12", nbits)
	}
	// Set one padding bit (bit 3 of the stream).
	data[0] |= 1 << 4
	sess := p.NewSession(Quotas{})
	sess.Feed(data)
	err = sess.Run()
	e, ok := err.(*Error)
	if !ok || e.Kind != ErrReservedBits {
		t.Fatalf("want ErrReservedBits, got %v", err)
	}
	if e.Bit != 3 {
		t.Fatalf("reserved bit error at bit %d, want 3", e.Bit)
	}
}

// TestCompileErrors verifies that invalid schemas are rejected at compile
// time.
func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name   string
		schema *Schema
		want   string
	}{
		{
			name: "forward length reference",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "items", Type: "array", LengthFrom: "n", Element: &StructSpec{
					Fields: []FieldSpec{{Name: "v", Type: "uint", Bits: 5}}}},
				{Name: "n", Type: "uint", Bits: 3},
			}},
			want: "not a previously defined field",
		},
		{
			name: "duplicate field name",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "a", Type: "uint", Bits: 3},
				{Name: "a", Type: "uint", Bits: 4},
			}},
			want: "duplicate field name",
		},
		{
			name: "undefined tag",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "tag", Type: "uint", Bits: 3, Enum: []uint64{0, 1}},
				{Name: "u", Type: "union", TagFrom: "tag", Cases: map[string]*StructSpec{
					"0": {Fields: []FieldSpec{{Name: "x", Type: "uint", Bits: 5}}},
					"1": {Fields: []FieldSpec{{Name: "y", Type: "uint", Bits: 5}}},
					"2": {Fields: []FieldSpec{{Name: "y", Type: "uint", Bits: 5}}},
				}},
			}},
			want: "undefined tag",
		},
		{
			name: "missing case for declared tag",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "tag", Type: "uint", Bits: 3, Enum: []uint64{0, 1}},
				{Name: "u", Type: "union", TagFrom: "tag", Cases: map[string]*StructSpec{
					"0": {Fields: []FieldSpec{{Name: "x", Type: "uint", Bits: 5}}},
				}},
			}},
			want: "missing case",
		},
		{
			name: "unbounded allocation",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "n", Type: "uint", Bits: 64},
				{Name: "items", Type: "array", LengthFrom: "n", Element: &StructSpec{
					Fields: []FieldSpec{{Name: "v", Type: "uint", Bits: 8}}}},
			}},
			want: "unbounded allocation",
		},
		{
			name: "checksum unknown field",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "a", Type: "uint", Bits: 3},
				{Name: "crc", Type: "checksum", Bits: 8, From: "a", To: "nope"},
			}},
			want: "not a previously defined field",
		},
		{
			name: "checksum inverted range",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "a", Type: "uint", Bits: 3},
				{Name: "b", Type: "uint", Bits: 3},
				{Name: "crc", Type: "checksum", Bits: 8, From: "b", To: "a"},
			}},
			want: "empty or inverted",
		},
		{
			name:   "excessive depth",
			schema: nestedArraySchema(MaxSchemaDepth + 1),
			want:   "nesting depth",
		},
		{
			name: "tag from non-uint",
			schema: &Schema{Fields: []FieldSpec{
				{Name: "tag", Type: "int", Bits: 3},
				{Name: "u", Type: "union", TagFrom: "tag", Cases: map[string]*StructSpec{
					"0": {Fields: []FieldSpec{{Name: "x", Type: "uint", Bits: 5}}},
				}},
			}},
			want: "not a uint field",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.schema)
			if err == nil {
				t.Fatalf("expected compile error containing %q", tc.want)
			}
			e, ok := err.(*Error)
			if !ok || e.Kind != ErrSchema {
				t.Fatalf("want ErrSchema, got %v", err)
			}
			if !bytes.Contains([]byte(e.Msg), []byte(tc.want)) {
				t.Fatalf("error %q does not contain %q", e.Msg, tc.want)
			}
		})
	}
}

func nestedArraySchema(depth int) *Schema {
	inner := &StructSpec{Fields: []FieldSpec{{Name: "v", Type: "uint", Bits: 1}}}
	for i := 1; i < depth; i++ {
		inner = &StructSpec{Fields: []FieldSpec{
			{Name: "k", Type: "uint", Bits: 1},
			{Name: "a", Type: "array", LengthFrom: "k", Element: inner},
		}}
	}
	return &Schema{Fields: []FieldSpec{
		{Name: "k", Type: "uint", Bits: 1},
		{Name: "a", Type: "array", LengthFrom: "k", Element: inner},
	}}
}

// TestQuotas exercises the per-session element, bit and depth budgets.
func TestQuotas(t *testing.T) {
	p := mustCompile(t, arraySchema("msb", "big"))
	obj := map[string]any{
		"n": uint64(3),
		"items": []any{
			map[string]any{"v": uint64(1)},
			map[string]any{"v": uint64(2)},
			map[string]any{"v": uint64(3)},
		},
	}
	data, nbits, err := p.EncodeBits(obj)
	if err != nil {
		t.Fatalf("EncodeBits: %v", err)
	}

	// Element budget.
	s := p.NewSession(Quotas{MaxElements: 2, MaxBits: 1 << 20, MaxDepth: 8})
	s.Feed(data)
	if e, ok := s.Run().(*Error); !ok || e.Kind != ErrQuota {
		t.Fatalf("element quota: got %v", s.Run())
	} else if e.Path != "arr.items" {
		t.Fatalf("element quota path %q, want arr.items", e.Path)
	}

	// Bit budget.
	s = p.NewSession(Quotas{MaxElements: 100, MaxBits: 5, MaxDepth: 8})
	s.Feed(data)
	if e, ok := s.Run().(*Error); !ok || e.Kind != ErrQuota {
		t.Fatalf("bit quota: got %v", s.Run())
	}

	// Depth budget on a nested schema.
	np := mustCompile(t, nestedArraySchema(3))
	nobj := map[string]any{
		"k": uint64(1),
		"a": []any{map[string]any{
			"k": uint64(1),
			"a": []any{map[string]any{
				"k": uint64(1),
				"a": []any{map[string]any{"v": uint64(1)}},
			}},
		}},
	}
	ndata, _, err := np.EncodeBits(nobj)
	if err != nil {
		t.Fatalf("EncodeBits nested: %v", err)
	}
	s = np.NewSession(Quotas{MaxElements: 100, MaxBits: 1 << 20, MaxDepth: 1})
	s.Feed(ndata)
	if e, ok := s.Run().(*Error); !ok || e.Kind != ErrQuota {
		t.Fatalf("depth quota: got %v", s.Run())
	}

	// Generous quotas decode fine.
	s = p.NewSession(Quotas{MaxElements: 3, MaxBits: uint64(nbits), MaxDepth: 8})
	s.Feed(data)
	if err := s.Run(); err != nil {
		t.Fatalf("generous quotas: %v", err)
	}
	if _, _, ok := s.Result(); !ok {
		t.Fatalf("generous quotas: no result")
	}
}

// TestConcurrentUse shares one plan across many encoders and decoder
// sessions. Run with -race to detect data races.
func TestConcurrentUse(t *testing.T) {
	p := mustCompile(t, telemetrySchema("msb", "big"))
	objs := telemetryObjects()
	encoded := make([][]byte, len(objs))
	bits := make([]int, len(objs))
	want := make([]map[string]any, len(objs))
	for i, obj := range objs {
		data, n, err := p.EncodeBits(obj)
		if err != nil {
			t.Fatalf("EncodeBits: %v", err)
		}
		encoded[i], bits[i] = data, n
		w, _, rerr := refDecode(p, data, uint64(n))
		if rerr != nil {
			t.Fatalf("refDecode: %v", rerr)
		}
		want[i] = w
	}
	done := make(chan struct{})
	for g := 0; g < 16; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			for iter := 0; iter < 200; iter++ {
				i := (g + iter) % len(objs)
				buf := make([]byte, len(encoded[i]))
				if _, err := p.Encode(objs[i], buf); err != nil {
					t.Errorf("Encode: %v", err)
					return
				}
				s := p.NewSession(Quotas{})
				// Feed in small chunks to exercise incremental state.
				for off := 0; off < len(encoded[i]); off++ {
					s.Feed(encoded[i][off : off+1])
					if err := s.Run(); err != nil && !IsNeedInput(err) {
						t.Errorf("Run: %v", err)
						return
					}
				}
				rec, used, ok := s.Result()
				if !ok || used != uint64(bits[i]) {
					t.Errorf("bad result: ok=%v used=%d", ok, used)
					return
				}
				if !reflect.DeepEqual(rec, want[i]) {
					t.Errorf("round trip mismatch: %v vs %v", rec, want[i])
					return
				}
			}
		}(g)
	}
	for g := 0; g < 16; g++ {
		<-done
	}
}

func decodeWhole(t *testing.T, p *Plan, data []byte, nbits int) (map[string]any, uint64, error) {
	t.Helper()
	s := p.NewSession(Quotas{})
	if err := s.FeedBits(data, nbits); err != nil {
		t.Fatalf("FeedBits: %v", err)
	}
	if err := s.Run(); err != nil {
		return nil, 0, err
	}
	rec, used, ok := s.Result()
	if !ok {
		t.Fatalf("session done but no result")
	}
	return rec, used, nil
}

var orderCombos = []struct{ bit, byte string }{
	{"msb", "big"},
	{"msb", "little"},
	{"lsb", "big"},
	{"lsb", "little"},
}

// TestExhaustiveTagValue encodes and decodes every one of the 8192 records
// of the 13-bit tag+value layout, under all four bit/byte order
// combinations, comparing against the independent reference.
func TestExhaustiveTagValue(t *testing.T) {
	for _, c := range orderCombos {
		p := mustCompile(t, tagValueSchema(c.bit, c.byte))
		for raw := uint64(0); raw < 8192; raw++ {
			obj := map[string]any{
				"tag":   raw >> 10,
				"value": signExtend(raw&1023, 10),
			}
			wantBytes, wantBits, err := refEncode(p, obj)
			if err != nil {
				t.Fatalf("refEncode: %v", err)
			}
			got := make([]byte, len(wantBytes))
			n, err := p.Encode(obj, got)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if uint64(n) != wantBits || !bytes.Equal(got, wantBytes) {
				t.Fatalf("%s/%s raw=%d: encode mismatch got %x (%d bits), want %x (%d bits)",
					c.bit, c.byte, raw, got, n, wantBytes, wantBits)
			}
			// Decode production vs reference.
			rec, used, err := decodeWhole(t, p, got, n)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			refObj, refUsed, rerr := refDecode(p, got, uint64(n))
			if rerr != nil {
				t.Fatalf("refDecode: %v", rerr)
			}
			if used != refUsed || !reflect.DeepEqual(rec, refObj) {
				t.Fatalf("%s/%s raw=%d: decode mismatch got %v@%d, want %v@%d",
					c.bit, c.byte, raw, rec, used, refObj, refUsed)
			}
			if !reflect.DeepEqual(rec, obj) {
				t.Fatalf("%s/%s raw=%d: round trip got %v want %v", c.bit, c.byte, raw, rec, obj)
			}
		}
	}
}

// unionSchema adds two union branches of different lengths selected by the
// 3-bit tag.
func unionSchema(bitOrder, byteOrder string) *Schema {
	return &Schema{
		Name:      "un",
		BitOrder:  bitOrder,
		ByteOrder: byteOrder,
		Fields: []FieldSpec{
			{Name: "tag", Type: "uint", Bits: 3, Enum: []uint64{0, 1}},
			{Name: "body", Type: "union", TagFrom: "tag", Cases: map[string]*StructSpec{
				"0": {Fields: []FieldSpec{{Name: "x", Type: "uint", Bits: 5}}},
				"1": {Fields: []FieldSpec{{Name: "y", Type: "uint", Bits: 12}}},
			}},
		},
	}
}

// TestExhaustiveUnion decodes every 16-bit input pattern (both branches and
// every illegal tag) with production and reference decoders.
func TestExhaustiveUnion(t *testing.T) {
	for _, c := range orderCombos {
		p := mustCompile(t, unionSchema(c.bit, c.byte))
		for raw := uint64(0); raw < 65536; raw++ {
			input := []byte{byte(raw >> 8), byte(raw)}
			s := p.NewSession(Quotas{})
			s.Feed(input)
			err := s.Run()
			refObj, _, rerr := refDecode(p, input, 16)
			if (err == nil) != (rerr == nil) {
				t.Fatalf("%s/%s raw=%d: prod err %v, ref err %v", c.bit, c.byte, raw, err, rerr)
			}
			if err != nil {
				pe, re := err.(*Error), rerr
				if pe.Kind != re.Kind {
					t.Fatalf("%s/%s raw=%d: prod kind %v, ref kind %v", c.bit, c.byte, raw, pe.Kind, re.Kind)
				}
				continue
			}
			rec, _, ok := s.Result()
			if !ok {
				t.Fatalf("%s/%s raw=%d: no result", c.bit, c.byte, raw)
			}
			if !reflect.DeepEqual(rec, refObj) {
				t.Fatalf("%s/%s raw=%d: got %v want %v", c.bit, c.byte, raw, rec, refObj)
			}
		}
	}
}

// arraySchema is a cross-byte array: a 3-bit count followed by that many
// 5-bit elements, so elements routinely straddle byte boundaries.
func arraySchema(bitOrder, byteOrder string) *Schema {
	return &Schema{
		Name:      "arr",
		BitOrder:  bitOrder,
		ByteOrder: byteOrder,
		Fields: []FieldSpec{
			{Name: "n", Type: "uint", Bits: 3},
			{Name: "items", Type: "array", LengthFrom: "n", Element: &StructSpec{
				Fields: []FieldSpec{{Name: "v", Type: "uint", Bits: 5}},
			}},
		},
	}
}

// TestCrossByteArray exhaustively covers counts 0..2 (up to 2^13 records)
// and randomly samples larger counts, under all order combinations.
func TestCrossByteArray(t *testing.T) {
	for _, c := range orderCombos {
		p := mustCompile(t, arraySchema(c.bit, c.byte))
		check := func(obj map[string]any) {
			t.Helper()
			wantBytes, _, err := refEncode(p, obj)
			if err != nil {
				t.Fatalf("refEncode: %v", err)
			}
			got := make([]byte, len(wantBytes))
			n, err := p.Encode(obj, got)
			if err != nil {
				t.Fatalf("Encode %v: %v", obj, err)
			}
			if !bytes.Equal(got, wantBytes) {
				t.Fatalf("%s/%s %v: got %x want %x", c.bit, c.byte, obj, got, wantBytes)
			}
			rec, _, err := decodeWhole(t, p, got, n)
			if err != nil {
				t.Fatalf("decode %x: %v", got, err)
			}
			if !reflect.DeepEqual(rec, obj) {
				t.Fatalf("%s/%s %v: round trip got %v", c.bit, c.byte, obj, rec)
			}
		}
		for raw := uint64(0); raw < 8192; raw++ {
			n := raw >> 10
			bits := raw & 1023
			items := make([]any, 0, n)
			for i := uint64(0); i < n; i++ {
				items = append(items, map[string]any{"v": (bits >> (5 * i)) & 31})
			}
			check(map[string]any{"n": n, "items": items})
		}
		// Larger counts, deterministic pseudo-random values.
		state := uint64(0x12345678)
		next := func() uint64 {
			state ^= state << 13
			state ^= state >> 7
			state ^= state << 17
			return state
		}
		for trial := 0; trial < 200; trial++ {
			n := 3 + next()%5
			items := make([]any, 0, n)
			for i := uint64(0); i < n; i++ {
				items = append(items, map[string]any{"v": next() & 31})
			}
			check(map[string]any{"n": n, "items": items})
		}
	}
}
