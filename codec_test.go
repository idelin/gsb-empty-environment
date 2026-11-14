package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func testKey(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, 32)
}

func TestCodecRoundTripOpaque(t *testing.T) {
	kr, err := NewKeyRing("k1", testKey(1), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := NewCodec(CodecConfig{ServerInstance: "nfs-1", KeyRing: kr})
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewExportID()
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{Tenant: "tenant-a", Version: 3, Allowed: OpAll}
	claims := Claims{
		ExportID:         id,
		ExportGeneration: 7,
		ObjectID:         "backend-object-1",
		ObjectGeneration: 11,
		ObjectType:       ObjectTypeFile,
		PolicyDigest:     policy.Digest(),
	}
	raw, err := codec.Seal(claims)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := codec.Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cap == nil {
		t.Fatal("nil capability")
	}
	if cap.claims != claims {
		t.Fatalf("claims mismatch: got %+v want %+v", cap.claims, claims)
	}

	typ := reflect.TypeOf(Capability{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).PkgPath == "" {
			t.Fatalf("Capability exposes field %q; parsed capabilities must be opaque", typ.Field(i).Name)
		}
	}
	rendered := fmt.Sprintf("%v %+v", cap, cap)
	if bytes.Contains([]byte(rendered), []byte("backend-object-1")) ||
		bytes.Contains([]byte(rendered), []byte(id.String())) {
		t.Fatalf("capability rendering leaked metadata: %s", rendered)
	}
	if !bytes.Equal(cap.Raw(), raw) {
		t.Fatal("Raw did not preserve handle")
	}
}

func TestCodecRejectsTamperingAndWrongContext(t *testing.T) {
	kr, err := NewKeyRing("k1", testKey(1), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := NewCodec(CodecConfig{ServerInstance: "nfs-1", KeyRing: kr})
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewExportID()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := codec.Seal(Claims{
		ExportID:         id,
		ExportGeneration: 1,
		ObjectID:         "object",
		ObjectGeneration: 1,
		ObjectType:       ObjectTypeFile,
		PolicyDigest:     Policy{Tenant: "a", Allowed: OpAll}.Digest(),
	})
	if err != nil {
		t.Fatal(err)
	}

	keyLen := int(raw[5])
	nonceOff := 6 + keyLen + serverTagSize
	cases := []struct {
		name string
		mut  func([]byte)
		want error
	}{
		{"ciphertext", func(b []byte) { b[len(b)-1] ^= 0x80 }, ErrHandleTampered},
		{"nonce", func(b []byte) { b[nonceOff] ^= 0x01 }, ErrHandleTampered},
		{"server tag", func(b []byte) { b[6+keyLen] ^= 0x01 }, ErrWrongServer},
		{"format version", func(b []byte) { b[4]++ }, ErrUnsupportedVersion},
		{"key id", func(b []byte) { b[6] ^= 0x01 }, ErrUnknownKey},
		{"magic", func(b []byte) { b[0] ^= 0xff }, ErrMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := append([]byte(nil), raw...)
			tc.mut(mutated)
			if _, err := codec.Open(mutated); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := codec.Open(raw[:len(raw)-1]); !errors.Is(err, ErrHandleTampered) {
		t.Fatalf("truncated ciphertext: got %v", err)
	}
	if _, err := codec.Open(raw[:10]); !errors.Is(err, ErrMalformed) {
		t.Fatalf("short header: got %v", err)
	}

	other, err := NewCodec(CodecConfig{ServerInstance: "nfs-2", KeyRing: kr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(raw); !errors.Is(err, ErrWrongServer) {
		t.Fatalf("wrong server: got %v", err)
	}
}

func TestCodecRejectsInvalidClaims(t *testing.T) {
	kr, err := NewKeyRing("k1", testKey(1), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := NewCodec(CodecConfig{ServerInstance: "nfs-1", KeyRing: kr})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewExportID()
	if _, err := codec.Seal(Claims{ExportID: id, ObjectType: ObjectTypeFile}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty object ID: got %v", err)
	}
	if _, err := codec.Seal(Claims{ExportID: id, ObjectID: "x", ObjectType: 99}); !errors.Is(err, ErrUnsupportedObjectType) {
		t.Fatalf("bad type: got %v", err)
	}
}
