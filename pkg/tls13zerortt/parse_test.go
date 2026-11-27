package tls13zerortt

import (
	"encoding/binary"
	"errors"
	"testing"
)

// TestMalformedClientHello feeds truncated and length-inconsistent inputs.
func TestMalformedClientHello(t *testing.T) {
	psk := testPSK()
	good := buildClientHello(psk, ResumptionPSK, []byte("id"), 1, 0x1301,
		nil, true, nil)

	cases := map[string][]byte{
		"empty":             nil,
		"short":             good[:10],
		"bad_handshake_len": badHandshakeLength(good),
		"bad_extension_len": badExtensionLength(good),
		"record_bad_frag":   badRecordFragment(good),
		"not_client_hello":  {0x02, 0x00, 0x00, 0x01, 0x00},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseClientHello(in); !errors.Is(err, ErrMalformedClientHello) {
				t.Fatalf("want ErrMalformedClientHello, got %v", err)
			}
		})
	}
}

func badHandshakeLength(msg []byte) []byte {
	out := append([]byte(nil), msg...)
	out[1]++ // inflate uint24 handshake length
	return out
}

func badExtensionLength(msg []byte) []byte {
	out := append([]byte(nil), msg...)
	parsed, err := ParseClientHello(msg)
	if err != nil {
		panic(err)
	}
	off := parsed.extensionsLengthOffset
	v := binary.BigEndian.Uint16(out[off:])
	binary.BigEndian.PutUint16(out[off:], v+1)
	return out
}

func badRecordFragment(msg []byte) []byte {
	out := []byte{contentTypeHandshake, 0x03, 0x03, 0x00, 0x00}
	binary.BigEndian.PutUint16(out[3:], uint16(len(msg))+1)
	return append(out, msg...)
}

// TestPreSharedKeyMustBeLast constructs an extension block where an extension
// follows pre_shared_key by reordering a hand-built message.
func TestPreSharedKeyMustBeLast(t *testing.T) {
	// Build a valid message, then move the pre_shared_key extension ahead of
	// the early_data extension by swapping their raw bytes is layout-sensitive;
	// instead construct the reordered extension block directly.
	supportedVersions := extension(43, []byte{0x00, 0x03, 0x04})
	earlyData := extension(ExtensionEarlyData, nil)

	identities := vec16(append(vec16([]byte("id")), 0, 0, 0, 1))
	dummyBinders := vec16(append([]byte{byte(HashLength)}, make([]byte, HashLength)...))
	pskExt := extension(ExtensionPreSharedKey, append(identities, dummyBinders...))

	// Order: supported_versions, pre_shared_key, early_data (invalid).
	var exts []byte
	exts = append(exts, supportedVersions...)
	exts = append(exts, pskExt...)
	exts = append(exts, earlyData...)
	msg := handshakeMessage(clientHelloBody(0x1301, exts))

	_, err := ParseClientHello(msg)
	if !errors.Is(err, ErrPreSharedKeyNotLast) {
		t.Fatalf("want ErrPreSharedKeyNotLast, got %v", err)
	}
}

func TestNoPreSharedKeyRejected(t *testing.T) {
	supportedVersions := extension(43, []byte{0x00, 0x03, 0x04})
	earlyData := extension(ExtensionEarlyData, nil)
	var exts []byte
	exts = append(exts, supportedVersions...)
	exts = append(exts, earlyData...)
	msg := handshakeMessage(clientHelloBody(0x1301, exts))

	if _, err := ParseClientHello(msg); !errors.Is(err, ErrNoPreSharedKey) {
		t.Fatalf("want ErrNoPreSharedKey, got %v", err)
	}
}

func TestBinderIndexOutOfRange(t *testing.T) {
	parsed, err := ParseClientHello(buildClientHello(testPSK(), ResumptionPSK,
		[]byte("id"), 1, 0x1301, nil, true, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.VerifyBinder(mustSchedule(t, testPSK(), ResumptionPSK), 7); !errors.Is(err, ErrBinderIndex) {
		t.Fatalf("want ErrBinderIndex, got %v", err)
	}
}

// TestUnsupportedBinderLength crafts a binder entry whose length is not 32.
func TestUnsupportedBinderLength(t *testing.T) {
	identities := vec16(append(vec16([]byte("id")), 0, 0, 0, 1))
	// A 16-byte binder entry is invalid for SHA-256.
	badEntry := append([]byte{16}, make([]byte, 16)...)
	binders := vec16(badEntry)
	exts := extension(43, []byte{0x00, 0x03, 0x04})
	exts = append(exts, extension(ExtensionPreSharedKey, append(identities, binders...))...)
	msg := handshakeMessage(clientHelloBody(0x1301, exts))

	if _, err := ParseClientHello(msg); !errors.Is(err, ErrBinderLength) {
		t.Fatalf("want ErrBinderLength, got %v", err)
	}
}

func TestAccessorBoundsAndCounts(t *testing.T) {
	parsed, err := ParseClientHello(buildClientHello(testPSK(), ResumptionPSK,
		[]byte("id"), 42, 0x1301, nil, true, nil))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.IdentityCount() != 1 {
		t.Fatalf("identity count = %d", parsed.IdentityCount())
	}
	if age, ok := parsed.ObfuscatedTicketAge(0); !ok || age != 42 {
		t.Fatalf("obf age = %d,%v", age, ok)
	}
	if id, ok := parsed.Identity(0); !ok || string(id) != "id" {
		t.Fatalf("identity = %q,%v", id, ok)
	}
	if _, ok := parsed.ObfuscatedTicketAge(-1); ok {
		t.Fatal("negative index must fail")
	}
}
