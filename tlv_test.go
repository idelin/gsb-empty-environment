package sframe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestTLVRoundTrip(t *testing.T) {
	payload := testPayload()
	fields, err := ParsePayload(payload)
	if err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if got := MarshalObject(fields); !bytes.Equal(got, payload) {
		t.Fatal("marshal(parse(x)) != x")
	}
	// Nested lookup.
	creds := fields[0].Kids[1]
	if creds.Name != "credentials" || creds.Kids[0].Name != "password" {
		t.Fatalf("unexpected tree: %+v", creds)
	}
	if string(creds.Kids[0].Raw) != "s3cret-PA55word" {
		t.Fatal("nested value mismatch")
	}
}

func TestUnknownFieldTypeIsCorrupt(t *testing.T) {
	payload := []byte{1, 'x', 0x7f, 0, 0, 0, 0}
	_, err := ParsePayload(payload)
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("want CorruptError, got %T: %v", err, err)
	}
}

func TestTruncatedTLVIsCorrupt(t *testing.T) {
	payload := testPayload()
	// Payload comes from a CRC-valid frame, so truncation here means the
	// declared TLV lengths are inconsistent: corruption, not need-more.
	if _, err := ParsePayload(payload[:len(payload)-2]); err == nil || NeedMore(err) {
		t.Fatalf("want corruption, got %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	// Build a deeply nested object by hand.
	payload := []byte{1, 'a', byte(TypeInt64), 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := 0; i < maxDepth+5; i++ {
		inner := []byte{1, 'a', byte(TypeObject), 0, 0, 0, 0}
		binary.BigEndian.PutUint32(inner[3:7], uint32(len(payload)))
		inner = append(inner, payload...)
		payload = inner
	}
	if _, err := ParsePayload(payload); err == nil {
		t.Fatal("expected depth limit error")
	}
}
