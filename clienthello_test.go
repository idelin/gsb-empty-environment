package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func testClientHello(extensions ...Extension) *ClientHello {
	var random [32]byte
	for i := range random {
		random[i] = byte(i + 1)
	}
	return &ClientHello{
		LegacyVersion:      0x0303,
		Random:             random,
		SessionID:          []byte{0xaa, 0xbb},
		CipherSuites:       []uint16{0x1301, 0x1302},
		CompressionMethods: []byte{0},
		Extensions:         extensions,
	}
}

func extension(typ uint16, data []byte) Extension {
	return Extension{Type: typ, Data: data}
}

func serverNameExtension(name string) Extension {
	entry := []byte{0, 0, 0}
	binary.BigEndian.PutUint16(entry[1:3], uint16(len(name)))
	entry = append(entry, []byte(name)...)
	list := []byte{0, 0}
	binary.BigEndian.PutUint16(list, uint16(len(entry)))
	return extension(ExtensionServerName, append(list, entry...))
}

func outerExtensionsPayload(types ...uint16) []byte {
	data := make([]byte, len(types)*2)
	for i, typ := range types {
		binary.BigEndian.PutUint16(data[i*2:], typ)
	}
	return data
}

func marshalClientHello(t *testing.T, ch *ClientHello) []byte {
	t.Helper()
	wire, err := ch.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return wire
}

func encodedInner(t *testing.T, inner *ClientHello, padding int) []byte {
	t.Helper()
	wire := marshalClientHello(t, inner)
	return append(wire, make([]byte, padding)...)
}

func validatedOuterClientHello() *ClientHello {
	return testClientHello(
		extension(0x0010, []byte("outer-10")),
		serverNameExtension("public.example"),
		extension(0x000a, []byte("outer-a")),
		extension(0x002b, []byte("outer-2b")),
		extension(ExtensionEncryptedClientHello, []byte{ECHTypeOuter}),
	)
}

func validatedInnerClientHello(refs ...uint16) *ClientHello {
	return testClientHello(
		serverNameExtension("inner.example"),
		extension(ExtensionECHOuterExtensions, outerExtensionsPayload(refs...)),
		extension(ExtensionEncryptedClientHello, []byte{ECHTypeInner}),
	)
}

func TestClientHelloRoundTrip(t *testing.T) {
	want := validatedOuterClientHello()
	wire := marshalClientHello(t, want)
	got, err := ParseClientHello(wire)
	if err != nil {
		t.Fatalf("ParseClientHello() error = %v", err)
	}
	if got.LegacyVersion != want.LegacyVersion {
		t.Fatalf("LegacyVersion = 0x%04x, want 0x%04x", got.LegacyVersion, want.LegacyVersion)
	}
	if !bytes.Equal(got.SessionID, want.SessionID) {
		t.Fatalf("SessionID = %x, want %x", got.SessionID, want.SessionID)
	}
	if len(got.Extensions) != len(want.Extensions) {
		t.Fatalf("len(Extensions) = %d, want %d", len(got.Extensions), len(want.Extensions))
	}
	for i := range want.Extensions {
		if got.Extensions[i].Type != want.Extensions[i].Type ||
			!bytes.Equal(got.Extensions[i].Data, want.Extensions[i].Data) {
			t.Fatalf("Extensions[%d] = %+v, want %+v", i, got.Extensions[i], want.Extensions[i])
		}
	}
}

func TestParseClientHelloHandshakeMessage(t *testing.T) {
	body := marshalClientHello(t, validatedOuterClientHello())
	handshake := append([]byte{1, 0, 0, 0}, body...)
	binary.BigEndian.PutUint16(handshake[2:4], uint16(len(body)))
	got, err := ParseClientHello(handshake)
	if err != nil {
		t.Fatalf("ParseClientHello() error = %v", err)
	}
	name, err := got.ServerName()
	if err != nil {
		t.Fatalf("ServerName() error = %v", err)
	}
	if name != "public.example" {
		t.Fatalf("ServerName() = %q, want %q", name, "public.example")
	}
}

func TestParseClientHelloRejectsTrailingBytes(t *testing.T) {
	wire := append(marshalClientHello(t, validatedOuterClientHello()), 0)
	if _, err := ParseClientHello(wire); err == nil {
		t.Fatal("ParseClientHello() accepted trailing bytes")
	}
}

func TestParseClientHelloRejectsTruncatedExtension(t *testing.T) {
	wire := marshalClientHello(t, validatedOuterClientHello())
	wire = wire[:len(wire)-1]
	_, err := ParseClientHello(wire)
	if !errors.Is(err, ErrMalformedClientHello) {
		t.Fatalf("ParseClientHello() error = %v, want ErrMalformedClientHello", err)
	}
}
