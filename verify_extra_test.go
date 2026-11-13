package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestDeviceNameSpacesRawIsSignedVerbatim(t *testing.T) {
	// When the caller supplies the exact wire bytes of deviceNameSpaces they
	// must be embedded into DeviceAuthentication untouched.
	f := newFixture()
	resp := f.cloneResponse()

	var captured []byte
	options := f.options()
	options.VerifyDeviceSignature = func(_ any, _ []byte, deviceAuthentication []byte) error {
		captured = append([]byte(nil), deviceAuthentication...)
		return verifyCOSESign1(resp.MSO.DeviceKey,
			resp.DeviceSigned.DeviceAuth.DeviceAuthCOSE, deviceAuthentication)
	}

	wireBytes := []byte{0xa1, 0x63, 'k', 'e', 'y', 0x65, 'v', 'a', 'l', 'u', 'e'}
	resp.DeviceSigned.NameSpaces = nil
	resp.DeviceSigned.NameSpacesRaw = wireBytes
	deviceAuth, err := encodeDeviceAuthentication(
		f.sessionTranscript, resp.DocType, wireBytes)
	if err != nil {
		t.Fatal(err)
	}
	cose, err := signCOSESign1(f.key, deviceAuth)
	if err != nil {
		t.Fatal(err)
	}
	resp.DeviceSigned.DeviceAuth = DeviceAuth{DeviceAuthCOSE: cose}

	if err := VerifyResponse(resp, options); err != nil {
		t.Fatalf("raw device namespaces rejected: %v", err)
	}
	if !bytes.Contains(captured, wireBytes) {
		t.Fatal("DeviceAuthentication does not contain the raw device namespaces")
	}
}

func TestNilDeviceNameSpacesEncodesNull(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()
	resp.DeviceSigned.NameSpaces = nil
	resp.DeviceSigned.NameSpacesRaw = nil

	deviceAuth, err := encodeDeviceAuthentication(
		f.sessionTranscript, resp.DocType, nil)
	if err != nil {
		t.Fatal(err)
	}
	cose, err := signCOSESign1(f.key, deviceAuth)
	if err != nil {
		t.Fatal(err)
	}
	resp.DeviceSigned.DeviceAuth = DeviceAuth{DeviceAuthCOSE: cose}

	if err := VerifyResponse(resp, f.options()); err != nil {
		t.Fatalf("null device namespaces rejected: %v", err)
	}
}

func TestDecodedDeviceNameSpacesReEncodedAndSigned(t *testing.T) {
	// With only the decoded deviceNameSpaces map available, the package must
	// canonical-encode it itself before building DeviceAuthentication.
	f := newFixture()
	resp := f.cloneResponse()
	resp.DeviceSigned.NameSpacesRaw = nil

	dnsBytes, err := encodeDeviceNameSpaces(resp.DeviceSigned.NameSpaces)
	if err != nil {
		t.Fatal(err)
	}
	deviceAuth, err := encodeDeviceAuthentication(
		f.sessionTranscript, resp.DocType, dnsBytes)
	if err != nil {
		t.Fatal(err)
	}
	cose, err := signCOSESign1(f.key, deviceAuth)
	if err != nil {
		t.Fatal(err)
	}
	resp.DeviceSigned.DeviceAuth = DeviceAuth{DeviceAuthCOSE: cose}

	if err := VerifyResponse(resp, f.options()); err != nil {
		t.Fatalf("decoded device namespaces rejected: %v", err)
	}
}

func TestReencodingFailureSurfacesAsDeviceSignatureError(t *testing.T) {
	f := newFixture()
	resp := f.cloneResponse()
	resp.DeviceSigned.NameSpacesRaw = nil
	resp.DeviceSigned.NameSpaces = map[string]map[string]any{
		"ns": {"unsupported": struct{ X int }{X: 1}},
	}

	err := VerifyResponse(resp, f.options())
	if !errors.Is(err, ErrDeviceSignature) {
		t.Fatalf("expected ErrDeviceSignature for unencodable namespaces, got %v", err)
	}
}

func TestDeviceAuthenticationStructure(t *testing.T) {
	// Lock the exact 4-element array shape, the literal first element and the
	// verifier transcript binding.
	f := newFixture()
	encoded, err := encodeDeviceAuthentication(
		f.sessionTranscript, "org.iso.18013.5.1.mDL", []byte{0xa0})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cborDecode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := decoded.([]any)
	if !ok || len(arr) != 4 {
		t.Fatalf("expected 4-element array, got %#v", decoded)
	}
	if arr[0] != "DeviceAuthentication" {
		t.Fatalf("unexpected first element: %v", arr[0])
	}
	// Tag 24 is consumed by the test decoder, leaving the inner transcript
	// content byte string.
	if got, ok := arr[1].([]byte); !ok ||
		!bytes.Equal(got, []byte("verifier-session-transcript-payload")) {
		t.Fatalf("unexpected session transcript element: %#v", arr[1])
	}
	if arr[2] != "org.iso.18013.5.1.mDL" {
		t.Fatalf("unexpected docType: %v", arr[2])
	}
	if got := arr[3].([]byte); !bytes.Equal(got, []byte{0xa0}) {
		t.Fatalf("unexpected DeviceNameSpacesBytes: %x", got)
	}
}

func TestCanonicalCBOREncoding(t *testing.T) {
	value := map[string]any{
		"b":      int64(300),
		"aa":     true,
		"nested": map[string]any{"x": nil, "a": []any{1, -2, "hi", 1.5}},
	}
	encoded, err := mustEncode(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cborDecode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	root := decoded.(map[any]any)
	nested := root["nested"].(map[any]any)
	inner := nested["a"].([]any)
	if inner[0] != uint64(1) || inner[1] != int64(-2) || inner[2] != "hi" || inner[3] != 1.5 {
		t.Fatalf("unexpected nested array: %#v", inner)
	}
	// Canonical key ordering: the 1-byte key "b" precedes the 2-byte keys.
	wantHead := []byte{0xa3, 0x61, 'b'}
	if !bytes.HasPrefix(encoded, wantHead) {
		t.Fatalf("map keys not canonically ordered: %x", encoded)
	}

	again, err := mustEncode(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatal("encoding is not deterministic")
	}
}

func TestFloatEncodingWidths(t *testing.T) {
	tests := []struct {
		name string
		v    float64
		head byte
	}{
		{"zero uses f16", 0, 0xf9},
		{"f16 round trip", 1.5, 0xf9},
		{"f32 width", float64(float32(3.14159)), 0xfa},
		{"f64 width", 3.141592653589793, 0xfb},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			if err := cborEncode(buf, tc.v); err != nil {
				t.Fatal(err)
			}
			if buf.Bytes()[0] != tc.head {
				t.Fatalf("expected head %#x, got %x", tc.head, buf.Bytes())
			}
			decoded, err := cborDecode(buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if decoded != tc.v {
				t.Fatalf("round trip mismatch: got %v want %v", decoded, tc.v)
			}
		})
	}
}

func TestUnsupportedCBORTypeRejected(t *testing.T) {
	_, err := mustEncode(struct{ X int }{X: 1})
	if err == nil || err.Error() != "netsec: unsupported CBOR value type" {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestIssuerSignedItemDigestShape(t *testing.T) {
	item := IssuerSignedItem{
		DigestID:          7,
		Random:            bytes.Repeat([]byte{0xab}, 16),
		ElementIdentifier: "family_name",
		ElementValue:      "Smith",
	}
	encoded, err := encodeIssuerSignedItem(item)
	if err != nil {
		t.Fatal(err)
	}
	// Must start with tag 24 followed by a byte string head.
	if encoded[0] != 0xd8 || encoded[1] != 24 {
		t.Fatalf("expected tag 24 prefix, got %x", encoded[:2])
	}
	if encoded[2]&0xe0 != 0x40 {
		t.Fatalf("expected byte string after tag, got %x", encoded[2])
	}
	decoded, err := cborDecode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	innerBytes, ok := decoded.([]byte)
	if !ok {
		t.Fatalf("expected inner byte string after tag decode, got %#v", decoded)
	}
	innerDecoded, err := cborDecode(innerBytes)
	if err != nil {
		t.Fatal(err)
	}
	inner, ok := innerDecoded.(map[any]any)
	if !ok {
		t.Fatalf("expected map inside tagged bytes, got %#v", innerDecoded)
	}
	if inner[uint64(0)] != uint64(7) || inner[uint64(2)] != "family_name" {
		t.Fatalf("unexpected IssuerSignedItem map: %#v", inner)
	}
}

func TestHasherErrorPropagates(t *testing.T) {
	f := newFixture()
	options := f.options()
	options.NewHash = func(string) (HasherSize, error) {
		return nil, errors.New("hash backend unavailable")
	}
	err := VerifyResponse(f.response, options)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("hash backend unavailable")) {
		t.Fatalf("expected hasher error to propagate, got %v", err)
	}
}

func TestAlternateDigestAlgorithmPassedThrough(t *testing.T) {
	// The MSO algorithm string must be handed verbatim to the Hasher.
	f := newFixture()
	var seen string
	options := f.options()
	options.NewHash = func(algorithm string) (HasherSize, error) {
		seen = algorithm
		return testNewHash(algorithm)
	}
	f.response.MSO.DigestAlgorithm = "SHA-512"
	// Recompute all MSO digests with the new algorithm.
	for ns, items := range f.response.IssuerSigned.NameSpaces {
		for _, item := range items {
			encoded, err := encodeIssuerSignedItem(item)
			if err != nil {
				t.Fatal(err)
			}
			h, _ := testNewHash("SHA-512")
			h.Write(encoded)
			f.response.MSO.ValueDigests[ns][item.DigestID] = h.Sum(nil)
		}
	}
	if err := VerifyResponse(f.response, options); err != nil {
		t.Fatalf("SHA-512 response rejected: %v", err)
	}
	if seen != "SHA-512" {
		t.Fatalf("expected SHA-512 algorithm passthrough, got %q", seen)
	}
}
