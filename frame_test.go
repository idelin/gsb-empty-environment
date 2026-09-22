package safeframe

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math/rand"
	"strings"
	"testing"
)

func sampleFrame() *Frame {
	return &Frame{
		Version:       Version1,
		Sequence:      42,
		TimestampNano: 9876543210,
		Root: Root{
			{Name: "account", Type: TypeMessage, Root: Root{
				{Name: "id", Type: TypeString, Text: "acct-7"},
				{Name: "token", Type: TypeString, Text: "super-secret-token"},
			}},
			{Name: "note", Type: TypeBytes, Bytes: []byte("keep-me")},
			{Name: "vendorX", Type: 99, Extra: []byte{0xde, 0xad}},
		},
	}
}

func TestRoundTripAndUnknownExtension(t *testing.T) {
	original := sampleFrame()
	wire, err := original.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if int(binary.BigEndian.Uint32(wire[8:12])) != len(wire)-HeaderLength {
		t.Fatal("payload length mismatch")
	}

	decoded, err := DecodeFrame(wire)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Sequence != original.Sequence || decoded.TimestampNano != original.TimestampNano {
		t.Fatal("timing fields were not preserved")
	}
	extension := decoded.Root[2]
	if extension.Type != 99 || !bytes.Equal(extension.Extra, []byte{0xde, 0xad}) {
		t.Fatalf("unknown extension was not preserved: %#v", extension)
	}
}

func TestRedactNestedAndRecomputesFrame(t *testing.T) {
	original := sampleFrame()
	original.Root[0].Root = append(original.Root[0].Root,
		&Field{Name: "token", Type: TypeBytes, Bytes: []byte("token-bytes")},
	)
	originalWire, err := original.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	originalLen := len(originalWire)

	redacted, redactedWire, err := Redact(original, []Path{
		ParsePathOrFail(t, "account.token"),
	}, RedactOptions{
		Replacement: []byte("REDACTED-LONGER-VALUE"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(redactedWire) == originalLen {
		t.Fatal("replacement changed length but wire length did not")
	}
	if int(binary.BigEndian.Uint32(redactedWire[8:12])) != len(redactedWire)-HeaderLength {
		t.Fatal("payload length was not recomputed")
	}
	if _, err := DecodeFrame(redactedWire); err != nil {
		t.Fatalf("redacted frame is not parseable: %v", err)
	}
	if redacted.Flags&FlagRedacted == 0 {
		t.Fatal("redacted flag was not set")
	}
	if redacted.Sequence != original.Sequence || redacted.TimestampNano != original.TimestampNano {
		t.Fatal("timing fields changed")
	}
	for _, secret := range []string{"super-secret-token", "token-bytes"} {
		if bytes.Contains(redactedWire, []byte(secret)) {
			t.Fatalf("secret %q leaked into output", secret)
		}
	}
	if bytes.Contains(originalWire, []byte("REDACTED-LONGER-VALUE")) {
		t.Fatal("source frame was mutated")
	}

	values := redacted.Root[0].Root
	if values[1].Text != "REDACTED-LONGER-VALUE" || !bytes.Equal(values[2].Bytes, []byte("REDACTED-LONGER-VALUE")) {
		t.Fatal("nested or duplicate scalar was not replaced")
	}
}

func TestStrictMissingAndUnknownExtension(t *testing.T) {
	original := sampleFrame()

	_, _, err := Redact(original, []Path{ParsePathOrFail(t, "account.missing")}, RedactOptions{Strict: true})
	if err != ErrPathNotFound {
		t.Fatalf("expected ErrPathNotFound, got %v", err)
	}

	_, _, err = Redact(original, []Path{ParsePathOrFail(t, "vendorX.hidden")}, RedactOptions{Strict: true})
	if err != ErrPathInUnknown {
		t.Fatalf("expected ErrPathInUnknown, got %v", err)
	}

	redacted, wire, err := Redact(original, []Path{
		ParsePathOrFail(t, "account.token"),
		ParsePathOrFail(t, "missing"),
	}, RedactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if redacted.Flags&FlagRedacted == 0 || bytes.Contains(wire, []byte("super-secret-token")) {
		t.Fatal("non-strict redaction leaked an untouched secret")
	}
}

func TestUnknownExtensionCannotHideSensitiveBytes(t *testing.T) {
	secret := []byte("shared-secret")
	frame := &Frame{
		Version: Version1,
		Root: Root{
			{Name: "known", Type: TypeString, Text: string(secret)},
			{Name: "x", Type: 77, Extra: append([]byte("prefix-"), secret...)},
		},
	}

	_, _, err := Redact(frame, []Path{ParsePathOrFail(t, "known")}, RedactOptions{Strict: true})
	if err != ErrSecretLeakage {
		t.Fatalf("expected ErrSecretLeakage, got %v", err)
	}
	if strings.Contains(err.Error(), string(secret)) {
		t.Fatal("error message contains sensitive value")
	}
}

func TestReplayLengthChangeIsDeterministic(t *testing.T) {
	original := sampleFrame()
	first, wire1, err := Redact(original, []Path{ParsePathOrFail(t, "account.token")}, RedactOptions{
		Replacement: []byte("short"),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, wire2, err := Redact(first, []Path{ParsePathOrFail(t, "account.token")}, RedactOptions{
		Replacement: []byte("expanded-replacement"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire1, redactedWireOrFail(t, first)) {
		t.Fatal("serialization is not deterministic")
	}
	if _, err := DecodeFrame(wire2); err != nil {
		t.Fatalf("replayed replacement is not parseable: %v", err)
	}
	if second.Sequence != original.Sequence || second.TimestampNano != original.TimestampNano {
		t.Fatal("replay changed sequence or timestamp")
	}
}

func TestDecoderArbitraryChunksAndIncompleteVsBad(t *testing.T) {
	first, err := sampleFrame().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	secondFrame := &Frame{Version: Version1, Sequence: 43, Root: Root{
		{Name: "ok", Type: TypeString, Text: "second"},
	}}
	second, err := secondFrame.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	stream := append(first, second...)

	random := rand.New(rand.NewSource(7))
	decoder := NewDecoder(MaxPayloadSize)
	var decoded []*Frame

	for offset := 0; offset < len(stream); {
		chunk := random.Intn(7) + 1
		if chunk > len(stream)-offset {
			chunk = len(stream) - offset
		}
		frames, err := decoder.Feed(stream[offset : offset+chunk])
		offset += chunk
		if offset < len(stream) {
			if err != ErrNeedMoreData && err != ErrTruncated {
				t.Fatalf("expected missing-data error, got %v", err)
			}
		} else if err != nil && err != ErrNeedMoreData && err != ErrTruncated {
			t.Fatal(err)
		}
		decoded = append(decoded, frames...)
	}

	frames, err := decoder.Feed(nil)
	if err != ErrNeedMoreData {
		t.Fatalf("expected trailing need-data, got %v", err)
	}
	decoded = append(decoded, frames...)
	if len(decoded) != 2 || decoded[0].Sequence != 42 || decoded[1].Sequence != 43 {
		t.Fatalf("decoded frames mismatch: %#v", decoded)
	}

	partial := make([]byte, HeaderLength-1)
	binary.BigEndian.PutUint32(partial[0:4], Magic)
	badDecoder := NewDecoder(MaxPayloadSize)
	if _, err := badDecoder.Feed(partial); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}

	badHeader := make([]byte, HeaderLength)
	binary.BigEndian.PutUint32(badHeader[0:4], 0x12345678)
	badDecoder = NewDecoder(MaxPayloadSize)
	if _, err := badDecoder.Feed(badHeader); err != ErrBadMagic {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}
	if _, err := badDecoder.Feed(nil); err != ErrBadMagic {
		t.Fatal("decoder continued after bad data")
	}
}

func TestPayloadLimitEnforcedBeforeAllocation(t *testing.T) {
	header := make([]byte, HeaderLength)
	binary.BigEndian.PutUint32(header[0:4], Magic)
	header[4] = Version1
	binary.BigEndian.PutUint32(header[8:12], uint32(1<<31-1))
	binary.BigEndian.PutUint64(header[12:20], 1)

	r := io.MultiReader(bytes.NewReader(header), bytes.NewReader([]byte("tiny")))
	if _, _, err := ReadFrame(r, MaxPayloadSize); err != ErrPayloadLimit {
		t.Fatalf("expected ErrPayloadLimit, got %v", err)
	}

	decoder := NewDecoder(64)
	binary.BigEndian.PutUint32(header[8:12], 65)
	if _, err := decoder.Feed(header); err != ErrPayloadLimit {
		t.Fatalf("expected stream ErrPayloadLimit, got %v", err)
	}
}

func TestBadLengthChecksumAndNoSensitiveErrorText(t *testing.T) {
	secret := []byte("do-not-print")
	frame := &Frame{Version: Version1, Root: Root{
		{Name: "secret", Type: TypeString, Text: string(secret)},
	}}
	wire, err := frame.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	tampered := append([]byte(nil), wire...)
	tampered[HeaderLength] ^= 0xff
	_, err = DecodeFrame(tampered)
	if err != ErrBadName && err != ErrBadLength && err != ErrChecksum {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), string(secret)) {
		t.Fatal("parser error leaked sensitive value")
	}

	badCRC := append([]byte(nil), wire...)
	badCRC[31] ^= 0xff
	_, err = DecodeFrame(badCRC)
	if err != ErrChecksum {
		t.Fatalf("expected ErrChecksum, got %v", err)
	}

	wrongLen := append([]byte(nil), wire...)
	binary.BigEndian.PutUint32(wrongLen[8:12], uint32(len(wire)-HeaderLength+1))
	if _, err := DecodeFrame(wrongLen); err != ErrBadLength {
		t.Fatalf("expected ErrBadLength, got %v", err)
	}

	trailing := append([]byte(nil), wire...)
	trailing = append(trailing, 0)
	binary.BigEndian.PutUint32(trailing[8:12], uint32(len(trailing)-HeaderLength))
	protected := append(append([]byte(nil), trailing[:28]...), trailing[32:]...)
	binary.BigEndian.PutUint32(trailing[28:32], crc32.ChecksumIEEE(protected))
	if _, err := DecodeFrame(trailing); err != ErrBadLength {
		t.Fatalf("expected top-level trailing bytes to be rejected, got %v", err)
	}
}

func TestReaderDistinguishesCleanEOFAndTruncation(t *testing.T) {
	wire, err := sampleFrame().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(wire[:HeaderLength-2])
	if _, _, err := ReadFrame(reader, MaxPayloadSize); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
	if _, _, err := ReadFrame(bytes.NewReader(nil), MaxPayloadSize); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestSentinelErrorSafety(t *testing.T) {
	sentinels := []error{
		ErrTruncated, ErrBadMagic, ErrBadVersion, ErrBadReserved, ErrPayloadLimit,
		ErrBadLength, ErrBadType, ErrChecksum, ErrBadName, ErrBadUTF8,
		ErrStructTooDeep, ErrNeedMoreData, ErrEmptyPath, ErrDuplicatePath,
		ErrPathNotFound, ErrPathNotScalar, ErrPathInUnknown, ErrSecretLeakage,
		ErrBadReplacement,
	}
	for _, err := range sentinels {
		if strings.ContainsAny(err.Error(), "\x00\r\n") {
			t.Fatalf("unsafe error text: %q", err.Error())
		}
	}
}

func TestCRCProtectsRecomputedHeader(t *testing.T) {
	wire, err := sampleFrame().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	protected := append(append([]byte(nil), wire[:28]...), wire[32:]...)
	expected := crc32.ChecksumIEEE(protected)
	if binary.BigEndian.Uint32(wire[28:32]) != expected {
		t.Fatal("header CRC mismatch")
	}
}

func ParsePathOrFail(t *testing.T, value string) Path {
	t.Helper()
	path, err := ParsePath(value)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func redactedWireOrFail(t *testing.T, frame *Frame) []byte {
	t.Helper()
	wire, err := frame.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return wire
}
