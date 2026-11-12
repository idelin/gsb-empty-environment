package avroocf

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"testing"
)

const longSchema = `"long"`

const recordSchema = `{
	"type":"record","name":"demo.Node","fields":[
		{"name":"value","type":"long"},
		{"name":"next","type":["null","demo.Node"]}
	]
}`

func TestFingerprintGoldenAndAllowlistRejection(t *testing.T) {
	nullSchema, err := ParseSchema([]byte(`"null"`), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := nullSchema.FingerprintSHA256(); got !=
		"f072cbec3bf8841871d4284230c5e983dc211a56837aed862487148f947d1a1f" {
		t.Fatalf("unexpected null fingerprint: %s", got)
	}

	schema, err := ParseSchema([]byte(longSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile := newContainer(t, longSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(42)}, false, false)

	reader := openContainer(t, containerFile.buffer.Bytes(), allowConfig(schema))
	values := drainAll(t, reader)
	if len(values) != 1 || values[0] != int64(42) {
		t.Fatalf("unexpected values: %#v", values)
	}

	config := allowConfig(schema)
	config.AllowedFingerprints = map[string]bool{"deadbeef": true}
	if _, err := Open(bytes.NewReader(containerFile.buffer.Bytes()), config); !errors.Is(err, ErrSchemaRejected) {
		t.Fatalf("expected ErrSchemaRejected, got %v", err)
	}
}

func TestRecursiveSchemaDepthEnforced(t *testing.T) {
	deeplyNested := buildNestedSchema(4)
	if _, err := ParseSchema([]byte(deeplyNested), 3); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("expected schema depth error, got %v", err)
	}

	schema, err := ParseSchema([]byte(recordSchema), 8)
	if err != nil {
		t.Fatal(err)
	}

	var leaf bytes.Buffer
	writeLong(&leaf, 7)
	leaf.Write(encodeLong(0))

	var middle bytes.Buffer
	writeLong(&middle, 6)
	middle.Write(encodeLong(1))
	middle.Write(leaf.Bytes())

	var root bytes.Buffer
	writeLong(&root, 5)
	root.Write(encodeLong(1))
	root.Write(middle.Bytes())

	containerFile := newContainer(t, recordSchema, "")
	containerFile.writeBlock([][]byte{root.Bytes()}, false, false)

	config := allowConfig(schema)
	config.MaxSchemaDepth = 2
	reader := openContainer(t, containerFile.buffer.Bytes(), config)
	if _, err := reader.NextBlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Decode(); !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("expected runtime depth error, got %v", err)
	}
}

func patchBlockCount(t *testing.T, data []byte, count int64) []byte {
	t.Helper()
	stream := bytes.NewReader(data[len(magic):])
	entries, err := readLong(stream)
	if err != nil || entries != 1 {
		t.Fatalf("unexpected metadata entries: %d %v", entries, err)
	}
	if _, err := readBytes(stream); err != nil {
		t.Fatal(err)
	}
	if _, err := readBytes(stream); err != nil {
		t.Fatal(err)
	}
	zero, err := readLong(stream)
	if err != nil || zero != 0 {
		t.Fatalf("missing map terminator: %d %v", zero, err)
	}
	if _, err := io.CopyN(io.Discard, stream, syncSize); err != nil {
		t.Fatal(err)
	}
	consumed := int(stream.Size()) - stream.Len()
	blockOffset := len(magic) + consumed

	originalCount := encodeLong(1)
	if count < 0 {
		originalCount = encodeLong(-1)
	}
	result := make([]byte, 0, len(data)+4)
	result = append(result, data[:blockOffset]...)
	result = append(result, encodeLong(count)...)
	result = append(result, data[blockOffset+len(originalCount):]...)
	return result
}

func TestInflatedBlockCountRejected(t *testing.T) {
	schema, err := ParseSchema([]byte(longSchema), 4)
	if err != nil {
		t.Fatal(err)
	}

	containerFile := newContainer(t, longSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(1)}, false, false)
	patched := patchBlockCount(t, containerFile.buffer.Bytes(), 50)
	reader := openContainer(t, patched, allowConfig(schema))
	if _, err := reader.NextBlock(); !errors.Is(err, ErrBlockCount) {
		t.Fatalf("expected inflated count rejection, got %v", err)
	}

	containerFile = newContainer(t, longSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(1)}, true, false)
	patched = patchBlockCount(t, containerFile.buffer.Bytes(), -50)
	reader = openContainer(t, patched, allowConfig(schema))
	if _, err := reader.NextBlock(); !errors.Is(err, ErrBlockCount) {
		t.Fatalf("expected negative-count inflation rejection, got %v", err)
	}
}

func TestDeclaredBlockBytesMismatchRejected(t *testing.T) {
	schema, err := ParseSchema([]byte(longSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile := newContainer(t, longSchema, "")
	// Two encoded longs, negative count, then tamper the declared byte size
	// so it claims more bytes than the stream can provide.
	containerFile.writeBlock([][]byte{encodeLong(1), encodeLong(2)}, true, false)
	data := containerFile.buffer.Bytes()

	// Locate the block's size varint, which sits directly after count = -2.
	stream := bytes.NewReader(data[len(magic):])
	if _, err := readMapHeader(&countedReader{source: stream, remaining: int64(len(data)) - 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, stream, syncSize); err != nil {
		t.Fatal(err)
	}
	consumed := int(stream.Size()) - stream.Len()
	blockOffset := len(magic) + consumed
	countBytes := encodeLong(-2)
	sizeOffset := blockOffset + len(countBytes)

	tampered := append([]byte(nil), data...)
	oversize := encodeLong(int64(len(data)) * 4)
	tampered = append(tampered[:sizeOffset], append(oversize, tampered[sizeOffset+1:]...)...)

	reader := openContainer(t, tampered, allowConfig(schema))
	if _, err := reader.NextBlock(); !errors.Is(err, ErrBlockCount) {
		t.Fatalf("expected declared-size mismatch rejection, got %v", err)
	}
}

func TestTamperedSyncMarkerRejected(t *testing.T) {
	schema, err := ParseSchema([]byte(longSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile := newContainer(t, longSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(9), encodeLong(10)}, true, false)
	data := append([]byte(nil), containerFile.buffer.Bytes()...)
	data[len(data)-1] ^= 0xff

	reader := openContainer(t, data, allowConfig(schema))
	if _, err := reader.NextBlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Decode(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Decode(); err != nil {
		t.Fatal(err)
	}
	if err := reader.finishBlock(); !errors.Is(err, ErrSyncMarker) {
		t.Fatalf("expected sync marker error, got %v", err)
	}
}

func TestEnumAndUnionOutOfRangeRejected(t *testing.T) {
	enumSchema := `{"type":"enum","name":"e.Color","symbols":["RED","GREEN"]}`
	schema, err := ParseSchema([]byte(enumSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile := newContainer(t, enumSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(2)}, false, false)
	reader := openContainer(t, containerFile.buffer.Bytes(), allowConfig(schema))
	if _, err := reader.NextBlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Decode(); !errors.Is(err, ErrEnumOutOfRange) {
		t.Fatalf("expected enum range error, got %v", err)
	}

	unionSchema := `["null","long","string"]`
	schema, err = ParseSchema([]byte(unionSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile = newContainer(t, unionSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(9)}, false, false)
	reader = openContainer(t, containerFile.buffer.Bytes(), allowConfig(schema))
	if _, err := reader.NextBlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Decode(); !errors.Is(err, ErrUnionOutOfRange) {
		t.Fatalf("expected union range error, got %v", err)
	}
}

func TestRoundTripNegativeCountAndSkip(t *testing.T) {
	schema, err := ParseSchema([]byte(longSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile := newContainer(t, longSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(100), encodeLong(200)}, true, false)
	containerFile.writeBlock([][]byte{encodeLong(300)}, true, false)

	reader := openContainer(t, containerFile.buffer.Bytes(), allowConfig(schema))
	count, err := reader.NextBlock()
	if err != nil || count != 2 {
		t.Fatalf("block count = %d, err = %v", count, err)
	}
	if err := reader.Skip(); err != nil {
		t.Fatal(err)
	}
	value, err := reader.Decode()
	if err != nil || value != int64(200) {
		t.Fatalf("value = %#v, err = %v", value, err)
	}
	values := drainAll(t, reader)
	if len(values) != 1 || values[0] != int64(300) {
		t.Fatalf("unexpected tail values: %#v", values)
	}
}

func TestBlockLimitAndTruncation(t *testing.T) {
	schema, err := ParseSchema([]byte(longSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile := newContainer(t, longSchema, "")
	containerFile.writeBlock([][]byte{encodeLong(1), encodeLong(2)}, false, false)

	config := allowConfig(schema)
	config.MaxBlockObjects = 1
	reader := openContainer(t, containerFile.buffer.Bytes(), config)
	if _, err := reader.NextBlock(); !errors.Is(err, ErrBlockCount) {
		t.Fatalf("expected object limit error, got %v", err)
	}

	data := containerFile.buffer.Bytes()
	truncated := append([]byte(nil), data[:len(data)-8]...)
	reader = openContainer(t, truncated, allowConfig(schema))
	if _, err := reader.NextBlock(); err == nil {
		t.Fatal("expected truncated block to fail reading")
	}

	if _, err := Open(bytes.NewReader([]byte("not an avro file")), allowConfig(schema)); !errors.Is(err, ErrNotContainer) {
		t.Fatalf("expected ErrNotContainer, got %v", err)
	}
}

func TestDeflateBombAndDisabledCodec(t *testing.T) {
	schema, err := ParseSchema([]byte(longSchema), 4)
	if err != nil {
		t.Fatal(err)
	}
	containerFile := newContainer(t, longSchema, "deflate")
	containerFile.writeBlock([][]byte{encodeLong(1)}, true, true)

	if _, err := Open(bytes.NewReader(containerFile.buffer.Bytes()), allowConfig(schema)); !errors.Is(err, ErrCodecUnsupported) {
		t.Fatalf("expected disabled codec error, got %v", err)
	}

	config := allowConfig(schema)
	config.AllowDeflate = true
	config.MaxDecompressedBlockBytes = 1
	reader := openContainer(t, containerFile.buffer.Bytes(), config)
	var bombErr error
	if _, err := reader.NextBlock(); err != nil {
		bombErr = err
	} else if _, err := reader.Decode(); err != nil {
		bombErr = err
	}
	if bombErr == nil {
		t.Fatal("expected decompression limit error")
	} else if !errors.Is(bombErr, ErrBlockCount) {
		t.Fatalf("expected ErrBlockCount for bomb, got %v", bombErr)
	}
}

func TestCanonicalFormStable(t *testing.T) {
	schemaJSON := `{
		"type":"record","name":"demo.User","namespace":"demo",
		"extra":"ignored",
		"fields":[
			{"name":"id","type":"long","doc":"x"},
			{"name":"tags","type":{"type":"array","items":"string"}}
		]}`
	first, err := ParseSchema([]byte(schemaJSON), 8)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseSchema(first.Canonical(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Canonical(), second.Canonical()) {
		t.Fatalf("canonical form not stable: %s vs %s", first.Canonical(), second.Canonical())
	}
	if got := first.FingerprintCRC64(); got != second.FingerprintCRC64() {
		t.Fatalf("crc fingerprints differ: %d != %d", got, second.FingerprintCRC64())
	}
	if first.FingerprintSHA256() == fmt.Sprintf("%x", 0) {
		t.Fatal("sha256 fingerprint unexpectedly zero-like")
	}
}

func TestCRC64MatchesStandardAvroTable(t *testing.T) {
	// The Avro fingerprint uses the reflected RFC 3720 polynomial with zero
	// initial value and no final xor, matching the MakeTable-based CRC.
	table := crc64.MakeTable(0xC96C5795D7870F42)
	payload := []byte("avro parsing canonical form payload")
	expected := crc64.Checksum(payload, table)
	if got := crc64Avro(payload); got != expected {
		t.Fatalf("crc64 mismatch: got %d want %d", got, expected)
	}
}
