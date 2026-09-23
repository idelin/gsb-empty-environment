package lcc

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTripMultipleLayersAndExplicitFormat(t *testing.T) {
	raw := append(bytes.Repeat([]byte("abc"), 100), bytes.Repeat([]byte{7}, 300)...)
	limits := DefaultLimits()
	container, err := Encode(raw, 3, limits)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if string(container[:4]) != "LCC1" {
		t.Fatalf("bad magic %q", container[:4])
	}
	if got := binary.BigEndian.Uint16(container[4:6]); got != FormatVersion {
		t.Fatalf("version = %d, want %d", got, FormatVersion)
	}
	if got := binary.BigEndian.Uint16(container[6:8]); got != 3 {
		t.Fatalf("layers = %d, want 3", got)
	}
	payloadLen := binary.BigEndian.Uint32(container[8:12])
	if binary.BigEndian.Uint32(container[12:16]) != crc32.ChecksumIEEE(container[:12]) {
		t.Fatal("header checksum does not match")
	}
	wantSize := headerSize + 3*recordSize + int(payloadLen) + 4
	if len(container) != wantSize {
		t.Fatalf("container length = %d, want %d", len(container), wantSize)
	}
	table := container[headerSize : headerSize+3*recordSize]
	payload := container[headerSize+3*recordSize : len(container)-4]
	checksum := crc32.Update(0, crc32.IEEETable, table)
	checksum = crc32.Update(checksum, crc32.IEEETable, payload)
	if binary.BigEndian.Uint32(container[len(container)-4:]) != checksum {
		t.Fatal("payload checksum does not match")
	}

	decoded, err := Decode(container, limits)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("round trip mismatch")
	}
}

func TestEmptyInputRoundTrip(t *testing.T) {
	container, err := Encode(nil, 2, DefaultLimits())
	if err != nil {
		t.Fatalf("Encode empty: %v", err)
	}
	decoded, err := Decode(container, DefaultLimits())
	if err != nil {
		t.Fatalf("Decode empty: %v", err)
	}
	if len(decoded) != 0 {
		t.Fatalf("decoded %d bytes, want empty", len(decoded))
	}
}

func TestLayerBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxLayers = 2
	if _, err := Encode([]byte("aaa"), 3, limits); err == nil || !strings.Contains(err.Error(), "layer count") {
		t.Fatalf("Encode layer budget error = %v", err)
	}

	container, err := Encode([]byte("aaa"), 3, DefaultLimits())
	if err != nil {
		t.Fatalf("Encode with default limits: %v", err)
	}
	if _, err := Decode(container, limits); err == nil || !strings.Contains(err.Error(), "layer count") {
		t.Fatalf("Decode layer budget error = %v", err)
	}
}

func TestSharedInputBudgetIsCumulative(t *testing.T) {
	raw := make([]byte, 100)
	for i := range raw {
		raw[i] = byte(i)
	}
	container, err := Encode(raw, 2, DefaultLimits())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	limits := DefaultLimits()
	limits.MaxInputBytes = 150
	_, err = Decode(container, limits)
	if err == nil || !strings.Contains(err.Error(), "shared input budget") {
		t.Fatalf("cumulative input budget error = %v", err)
	}
}

func TestSharedOutputBudgetIsCumulative(t *testing.T) {
	raw := make([]byte, 100)
	for i := range raw {
		raw[i] = byte(i)
	}
	container, err := Encode(raw, 2, DefaultLimits())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	limits := DefaultLimits()
	limits.MaxOutputBytes = 150
	_, err = Decode(container, limits)
	if err == nil || !strings.Contains(err.Error(), "shared output budget") {
		t.Fatalf("cumulative output budget error = %v", err)
	}
}

func TestCumulativeExpansionBudget(t *testing.T) {
	records := []layerRecord{
		{codec: codecRLE, encodedLen: 100, decodedLen: 150},
		{codec: codecRLE, encodedLen: 150, decodedLen: 300},
	}
	container := buildTestContainer(t, records, bytes.Repeat([]byte{0}, 100))
	limits := DefaultLimits()
	limits.MaxExpansionBasisPoints = 200

	_, err := Decode(container, limits)
	if err == nil || !strings.Contains(err.Error(), "cumulative expansion") {
		t.Fatalf("cumulative expansion error = %v", err)
	}
}

func TestPerLayerExpansionBudget(t *testing.T) {
	records := []layerRecord{{codec: codecRLE, encodedLen: 10, decodedLen: 101}}
	container := buildTestContainer(t, records, bytes.Repeat([]byte{0}, 10))
	limits := DefaultLimits()
	limits.MaxExpansionBasisPoints = 1000

	_, err := Decode(container, limits)
	if err == nil || !strings.Contains(err.Error(), "layer 0 expansion") {
		t.Fatalf("per-layer expansion error = %v", err)
	}
}

func TestLegalHighCompressionRatioRequiresExplicitAllowance(t *testing.T) {
	raw := bytes.Repeat([]byte{'A'}, 10000)
	if _, err := Encode(raw, 2, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "expansion") {
		t.Fatalf("default expansion limit error = %v", err)
	}

	limits := DefaultLimits()
	limits.MaxExpansionBasisPoints = 10000
	container, err := Encode(raw, 2, limits)
	if err != nil {
		t.Fatalf("Encode with explicit high ratio: %v", err)
	}
	decoded, err := Decode(container, limits)
	if err != nil {
		t.Fatalf("Decode with explicit high ratio: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("high-ratio round trip mismatch")
	}
}

func TestOutputBudgetFailsBeforeLargeAllocation(t *testing.T) {
	records := []layerRecord{{codec: codecRLE, encodedLen: 1, decodedLen: 1 << 30}}
	container := buildTestContainer(t, records, []byte{0})
	limits := DefaultLimits()
	limits.MaxOutputBytes = 100
	limits.MaxExpansionBasisPoints = 1 << 40

	_, err := Decode(container, limits)
	if err == nil || !strings.Contains(err.Error(), "shared output budget") {
		t.Fatalf("output budget error = %v", err)
	}
}

func TestVersionChecksumAndTrailingDataAreVerified(t *testing.T) {
	container, err := Encode([]byte("hello hello hello"), 1, DefaultLimits())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	badVersion := bytes.Clone(container)
	badVersion[4] = 99
	if _, err := Decode(badVersion, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version error = %v", err)
	}

	badPayload := bytes.Clone(container)
	badPayload[len(badPayload)-5] ^= 0xff
	if _, err := Decode(badPayload, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "payload checksum") {
		t.Fatalf("payload checksum error = %v", err)
	}

	trailing := append(bytes.Clone(container), 0)
	if _, err := Decode(trailing, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing data error = %v", err)
	}
}

func TestMalformedRLEIsRejectedAfterChecksum(t *testing.T) {
	records := []layerRecord{{codec: codecRLE, encodedLen: 1, decodedLen: 1}}
	container := buildTestContainer(t, records, []byte{0})
	_, err := Decode(container, DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "truncated literal") {
		t.Fatalf("malformed RLE error = %v", err)
	}
}

func TestDecodeFileCleansTemporaryOutputOnError(t *testing.T) {
	dir := t.TempDir()
	container, err := Encode(bytes.Repeat([]byte{'A'}, 10000), 2, DefaultLimits())
	if err == nil {
		t.Fatal("Encode unexpectedly passed default expansion limit")
	}

	limits := DefaultLimits()
	limits.MaxExpansionBasisPoints = 10000
	container, err = Encode(bytes.Repeat([]byte{'A'}, 10000), 2, limits)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	containerPath := filepath.Join(dir, "input.lcc")
	if err := os.WriteFile(containerPath, container, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tight := limits
	tight.MaxOutputBytes = 10000
	before := tempOutputs(t, dir)
	err = DecodeFile(containerPath, filepath.Join(dir, "out.bin"), tight)
	if err == nil || !strings.Contains(err.Error(), "shared output budget") {
		t.Fatalf("DecodeFile error = %v", err)
	}
	after := tempOutputs(t, dir)
	if before != 0 || after != 0 {
		t.Fatalf("temporary outputs before/after = %d/%d, want 0/0", before, after)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "out.bin")); !os.IsNotExist(statErr) {
		t.Fatalf("output exists after failed decode: %v", statErr)
	}
}

func TestDecodeFileCleansTemporaryOutputOnDataError(t *testing.T) {
	dir := t.TempDir()
	records := []layerRecord{{codec: codecRLE, encodedLen: 1, decodedLen: 1}}
	container := buildTestContainer(t, records, []byte{0})
	containerPath := filepath.Join(dir, "input.lcc")
	if err := os.WriteFile(containerPath, container, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := DecodeFile(containerPath, filepath.Join(dir, "out.bin"), DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "truncated literal") {
		t.Fatalf("DecodeFile data error = %v", err)
	}
	if tempOutputs(t, dir) != 0 {
		t.Fatal("temporary output remained after data error")
	}
}

func TestDecodeFileSuccess(t *testing.T) {
	dir := t.TempDir()
	raw := []byte("file round trip")
	container, err := Encode(raw, 2, DefaultLimits())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	containerPath := filepath.Join(dir, "input.lcc")
	outputPath := filepath.Join(dir, "output.bin")
	if err := os.WriteFile(containerPath, container, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := DecodeFile(containerPath, outputPath, DefaultLimits()); err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	decoded, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("file round trip mismatch")
	}
}

func buildTestContainer(t *testing.T, records []layerRecord, payload []byte) []byte {
	t.Helper()
	header, err := makeHeader(len(records), uint64(len(payload)))
	if err != nil {
		t.Fatalf("makeHeader: %v", err)
	}
	table := marshalRecords(records)
	container := append(append(append([]byte{}, header...), table...), payload...)
	checksum := crc32.Update(0, crc32.IEEETable, table)
	checksum = crc32.Update(checksum, crc32.IEEETable, payload)
	return binary.BigEndian.AppendUint32(container, checksum)
}

func tempOutputs(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".lcc-output-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	return len(matches)
}
