package netsec

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

type decodedField struct {
	name      string
	value     string
	sensitive bool
}

func TestDefaultSensitiveFieldsAreLiteralAndNeverIndexed(t *testing.T) {
	t.Parallel()

	encoder := NewDefaultEncoder()
	fields := []HeaderField{
		{Name: "Cookie", Value: "session=abc"},
		{Name: "authorization", Value: "Bearer token"},
		{Name: "x-public", Value: "visible"},
	}
	block, err := encoder.Encode(fields)
	if err != nil {
		t.Fatal(err)
	}

	decoded := decodeFieldSection(t, block)
	if len(decoded) != 3 {
		t.Fatalf("decoded %d fields, want 3", len(decoded))
	}
	if !decoded[0].sensitive || decoded[0].name != "cookie" || decoded[0].value != "session=abc" {
		t.Fatalf("unexpected Cookie field: %+v", decoded[0])
	}
	if !decoded[1].sensitive || decoded[1].name != "authorization" || decoded[1].value != "Bearer token" {
		t.Fatalf("unexpected Authorization field: %+v", decoded[1])
	}
	if decoded[2].sensitive || decoded[2].name != "x-public" || decoded[2].value != "visible" {
		t.Fatalf("unexpected public field: %+v", decoded[2])
	}
	if encoder.DynamicTableEntries() != 0 {
		t.Fatalf("dynamic table has %d entries, want 0", encoder.DynamicTableEntries())
	}
}

func TestConfiguredAndDownstreamNBitArePreserved(t *testing.T) {
	t.Parallel()

	encoder, err := NewEncoder(Config{
		SensitiveHeaders:        []string{"X-Api-Key", "x-api-key"},
		MaxDynamicTableCapacity: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !encoder.Policy().IsSensitive("X-API-KEY") {
		t.Fatal("configured header lookup is not case-insensitive")
	}

	incoming := []HeaderField{
		{Name: "X-Api-Key", Value: "k1"},
		{Name: "x-trace", Value: "t1", Sensitive: true},
		{Name: "x-request-id", Value: "r1"},
	}
	marked := encoder.Policy().Mark(incoming)
	if !marked[0].Sensitive || !marked[1].Sensitive || marked[2].Sensitive {
		t.Fatalf("unexpected marked fields: %+v", marked)
	}
	if incoming[0].Sensitive {
		t.Fatal("Mark mutated its input")
	}

	block, err := encoder.Encode(marked)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeFieldSection(t, block)
	if !decoded[0].sensitive || decoded[0].name != "x-api-key" {
		t.Fatalf("configured field lost N bit: %+v", decoded[0])
	}
	if !decoded[1].sensitive || decoded[1].name != "x-trace" {
		t.Fatalf("downstream N bit lost: %+v", decoded[1])
	}
	if decoded[2].sensitive {
		t.Fatalf("non-sensitive field gained N bit: %+v", decoded[2])
	}
}

func TestGuessLengthsAreEqualAfterCandidateInjection(t *testing.T) {
	t.Parallel()

	const victim = "session=SECRET"
	const rightGuess = "session=SECRET"
	const wrongGuess = "session=WRONG!"
	if len(rightGuess) != len(wrongGuess) || len(victim) != len(rightGuess) {
		t.Fatal("test guesses must have the same length")
	}

	encodeRun := func(candidate string) (int, []decodedField) {
		encoder, err := NewEncoder(Config{MaxDynamicTableCapacity: 4096})
		if err != nil {
			t.Fatal(err)
		}

		// Attacker-controlled request goes through the shared encoder first.
		_, err = encoder.Encode([]HeaderField{{Name: "cookie", Value: candidate}})
		if err != nil {
			t.Fatal(err)
		}
		if encoder.DynamicTableEntries() != 0 {
			t.Fatalf("candidate insertion added %d dynamic entries", encoder.DynamicTableEntries())
		}

		block, err := encoder.Encode([]HeaderField{
			{Name: "cookie", Value: victim},
			{Name: "x-tenant", Value: "tenant-a"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return len(block), decodeFieldSection(t, block)
	}

	rightLen, rightFields := encodeRun(rightGuess)
	wrongLen, wrongFields := encodeRun(wrongGuess)
	if rightLen != wrongLen {
		t.Fatalf("right guess encoded to %d bytes, wrong guess to %d bytes", rightLen, wrongLen)
	}
	if len(rightFields) != 2 || !rightFields[0].sensitive || rightFields[0].value != victim {
		t.Fatalf("unexpected victim field: %+v", rightFields)
	}
	if !wrongFields[0].sensitive || wrongFields[0].value != victim {
		t.Fatalf("unexpected wrong-guess victim field: %+v", wrongFields)
	}
}

func TestEmptyValuesAndDuplicateCookies(t *testing.T) {
	t.Parallel()

	encoder := NewDefaultEncoder()
	fields := []HeaderField{
		{Name: "cookie", Value: ""},
		{Name: "cookie", Value: "a=1"},
		{Name: "cookie", Value: "a=1"},
		{Name: "authorization", Value: ""},
	}
	block, err := encoder.Encode(fields)
	if err != nil {
		t.Fatal(err)
	}

	decoded := decodeFieldSection(t, block)
	if len(decoded) != len(fields) {
		t.Fatalf("decoded %d fields, want %d (duplicates must not be merged)", len(decoded), len(fields))
	}
	for i, field := range decoded {
		if !field.sensitive {
			t.Fatalf("field %d lost N bit: %+v", i, field)
		}
		if field.name != fields[i].Name || field.value != fields[i].Value {
			t.Fatalf("field %d = %+v, want %+v", i, field, fields[i])
		}
	}
}

func TestZeroDynamicTableCapacity(t *testing.T) {
	t.Parallel()

	encoder, err := NewEncoder(Config{MaxDynamicTableCapacity: 0})
	if err != nil {
		t.Fatal(err)
	}
	if encoder.DynamicTableCapacity() != 0 || encoder.DynamicTableEntries() != 0 {
		t.Fatalf("capacity=%d entries=%d, want both zero", encoder.DynamicTableCapacity(), encoder.DynamicTableEntries())
	}

	block, err := encoder.Encode([]HeaderField{
		{Name: "cookie", Value: "a=b"},
		{Name: "authorization", Value: "Bearer x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeFieldSection(t, block)
	if len(decoded) != 2 || !decoded[0].sensitive || !decoded[1].sensitive {
		t.Fatalf("unexpected zero-capacity output: %+v", decoded)
	}
}

func TestCapacityDoesNotChangeEncoding(t *testing.T) {
	t.Parallel()

	fields := []HeaderField{
		{Name: "cookie", Value: "session=SECRET"},
		{Name: "authorization", Value: "Bearer SECRET"},
		{Name: "x-api-key", Value: "SECRET"},
	}
	zero, err := NewEncoder(Config{
		SensitiveHeaders:        []string{"x-api-key"},
		MaxDynamicTableCapacity: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	large, err := NewEncoder(Config{
		SensitiveHeaders:        []string{"x-api-key"},
		MaxDynamicTableCapacity: 16384,
	})
	if err != nil {
		t.Fatal(err)
	}

	zeroBlock, err := zero.Encode(fields)
	if err != nil {
		t.Fatal(err)
	}
	largeBlock, err := large.Encode(fields)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(zeroBlock, largeBlock) {
		t.Fatalf("capacity changed encoding:\nzero: % x\nlarge: % x", zeroBlock, largeBlock)
	}
}

func TestRepeatedSensitiveFieldsNeverEnterDynamicTable(t *testing.T) {
	t.Parallel()

	encoder, err := NewEncoder(Config{MaxDynamicTableCapacity: 8192})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		block, err := encoder.Encode([]HeaderField{
			{Name: "cookie", Value: "session=SECRET"},
			{Name: "authorization", Value: "Bearer SECRET"},
		})
		if err != nil {
			t.Fatal(err)
		}
		decoded := decodeFieldSection(t, block)
		for _, field := range decoded {
			if !field.sensitive {
				t.Fatalf("iteration %d lost N bit: %+v", i, field)
			}
		}
	}
	if encoder.DynamicTableEntries() != 0 {
		t.Fatalf("dynamic table has %d entries, want 0", encoder.DynamicTableEntries())
	}
}

func TestLongNamesAndValues(t *testing.T) {
	t.Parallel()

	encoder := NewDefaultEncoder()
	name := "x-" + strings.Repeat("a", 140)
	value := strings.Repeat("v", 260)
	block, err := encoder.Encode([]HeaderField{{Name: name, Value: value, Sensitive: true}})
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeFieldSection(t, block)
	if len(decoded) != 1 || decoded[0].name != name || decoded[0].value != value || !decoded[0].sensitive {
		t.Fatalf("long field round trip failed: %+v", decoded)
	}
}

func TestRejectsInvalidNamesAndCapacity(t *testing.T) {
	t.Parallel()

	if _, err := NewEncoder(Config{MaxDynamicTableCapacity: -1}); err == nil {
		t.Fatal("negative capacity was accepted")
	}
	encoder := NewDefaultEncoder()
	for _, name := range []string{"", "bad name", "bad\nname"} {
		if _, err := encoder.Encode([]HeaderField{{Name: name, Value: "v"}}); err == nil {
			t.Fatalf("invalid name %q was accepted", name)
		}
	}
	if err := encoder.SetMaxDynamicTableCapacity(-1); err == nil {
		t.Fatal("negative capacity update was accepted")
	}
}

func TestExactCookieEncoding(t *testing.T) {
	t.Parallel()

	encoder := NewDefaultEncoder()
	block, err := encoder.Encode([]HeaderField{{Name: "cookie", Value: "a=b"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x00, 0x00, // Required Insert Count, Delta Base
		0x36, 'c', 'o', 'o', 'k', 'i', 'e', // 001N=1, H=0, name length 6
		0x03, 'a', '=', 'b',
	}
	if !bytes.Equal(block, want) {
		t.Fatalf("encoded\n% x\nwant\n% x", block, want)
	}
}

func decodeFieldSection(t *testing.T, block []byte) []decodedField {
	t.Helper()
	if len(block) < 2 {
		t.Fatalf("field section is too short: % x", block)
	}
	// This encoder always emits RIC=0 and delta base=0.
	if block[0] != 0 || block[1] != 0 {
		t.Fatalf("unexpected field section prefix: % x", block[:2])
	}

	var fields []decodedField
	pos := 2
	for pos < len(block) {
		flag := block[pos]
		if flag&0xe0 != 0x20 {
			t.Fatalf("field line at %d is not a literal-name line: 0x%02x", pos, flag)
		}
		sensitive := flag&0x10 != 0
		if flag&0x08 != 0 {
			t.Fatalf("unexpected Huffman bit for name at %d", pos)
		}
		nameLen, next, err := readPrefixedInt(block, pos, 3)
		if err != nil {
			t.Fatal(err)
		}
		pos = next
		if nameLen > len(block)-pos {
			t.Fatalf("name length %d exceeds remaining block", nameLen)
		}
		name := string(block[pos : pos+nameLen])
		pos += nameLen

		if pos >= len(block) {
			t.Fatalf("missing value length at %d", pos)
		}
		if block[pos]&0x80 != 0 {
			t.Fatalf("unexpected Huffman bit for value at %d", pos)
		}
		valueLen, next, err := readPrefixedInt(block, pos, 7)
		if err != nil {
			t.Fatal(err)
		}
		pos = next
		if valueLen > len(block)-pos {
			t.Fatalf("value length %d exceeds remaining block", valueLen)
		}
		value := string(block[pos : pos+valueLen])
		pos += valueLen
		fields = append(fields, decodedField{name: name, value: value, sensitive: sensitive})
	}
	return fields
}

func readPrefixedInt(data []byte, pos int, prefix uint) (int, int, error) {
	if pos >= len(data) {
		return 0, pos, fmt.Errorf("missing integer at %d", pos)
	}
	max := 1<<prefix - 1
	value := int(data[pos] & byte(max))
	pos++
	if value < max {
		return value, pos, nil
	}

	shift := uint(0)
	for {
		if pos >= len(data) {
			return 0, pos, fmt.Errorf("truncated integer at %d", pos)
		}
		b := data[pos]
		pos++
		value += int(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, pos, nil
		}
		shift += 7
		if shift > 28 {
			return 0, pos, fmt.Errorf("integer at %d is too large", pos)
		}
	}
}
