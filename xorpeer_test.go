package netsec_test

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"

	netsec "example.org/netsec/q0259"
)

const (
	testXORPortMask = 0x2112
)

var testMagicCookie = [4]byte{0x21, 0x12, 0xA4, 0x42}

// xorPeerAttribute builds an XOR-PEER-ADDRESS value independently of the
// implementation under test.
func xorPeerAttribute(t *testing.T, addrPort netip.AddrPort, transactionID [netsec.TransactionIDSize]byte) []byte {
	t.Helper()

	addr := addrPort.Addr()
	port := addrPort.Port() ^ testXORPortMask

	if addr.Is4() {
		out := make([]byte, 8)
		binary.BigEndian.PutUint16(out[0:2], 0x0001)
		binary.BigEndian.PutUint16(out[2:4], port)
		raw := addr.As4()
		for i := 0; i < 4; i++ {
			out[4+i] = raw[i] ^ testMagicCookie[i]
		}
		return out
	}

	out := make([]byte, 20)
	binary.BigEndian.PutUint16(out[0:2], 0x0002)
	binary.BigEndian.PutUint16(out[2:4], port)
	raw := addr.As16()
	for i := 0; i < 4; i++ {
		out[4+i] = raw[i] ^ testMagicCookie[i]
	}
	for i := 4; i < 16; i++ {
		out[4+i] = raw[i] ^ transactionID[i-4]
	}
	return out
}

func TestDecodeXORPeerAddressRFC5769IPv6Vector(t *testing.T) {
	// RFC 5769, section 2.3: mapped address
	// 2001:db8:1234:5678:11:2233:4455:6677 port 32853.
	value := []byte{
		0x00, 0x02, 0xa1, 0x47,
		0x01, 0x13, 0xa9, 0xfa,
		0xa5, 0xd3, 0xf1, 0x79,
		0xbc, 0x25, 0xf4, 0xb5,
		0xbe, 0xd2, 0xb9, 0xd9,
	}
	transactionID := [netsec.TransactionIDSize]byte{
		0xb7, 0xe7, 0xa7, 0x01,
		0xbc, 0x34, 0xd6, 0x86,
		0xfa, 0x87, 0xdf, 0xae,
	}

	got, err := netsec.DecodeXORPeerAddress(value, transactionID)
	if err != nil {
		t.Fatalf("DecodeXORPeerAddress: %v", err)
	}
	want := netip.MustParseAddrPort("[2001:db8:1234:5678:11:2233:4455:6677]:32853")
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDecodeXORPeerAddressIPv4(t *testing.T) {
	// 192.0.2.1 has RFC 5737 documentation meaning. The IPv4 address is
	// XORed with the magic cookie, not with the transaction ID.
	value := []byte{0x00, 0x01, 0xa1, 0x47, 0xe1, 0x12, 0xa6, 0x43}
	transactionID := [netsec.TransactionIDSize]byte{0xde, 0xad, 0xbe, 0xef}

	got, err := netsec.DecodeXORPeerAddress(value, transactionID)
	if err != nil {
		t.Fatalf("DecodeXORPeerAddress: %v", err)
	}
	want := netip.MustParseAddrPort("192.0.2.1:32853")
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}

	transactionID[0] = 0xff
	gotAgain, err := netsec.DecodeXORPeerAddress(value, transactionID)
	if err != nil {
		t.Fatalf("DecodeXORPeerAddress with changed transaction ID: %v", err)
	}
	if gotAgain != want {
		t.Fatalf("IPv4 decode changed with transaction ID: got %v, want %v", gotAgain, want)
	}
}

func TestDecodeXORPeerAddressIPv6UsesTransactionID(t *testing.T) {
	addrPort := netip.MustParseAddrPort("[2001:db8::1]:49152")
	transactionID := [netsec.TransactionIDSize]byte{
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c,
	}
	value := xorPeerAttribute(t, addrPort, transactionID)

	got, err := netsec.DecodeXORPeerAddress(value, transactionID)
	if err != nil {
		t.Fatalf("DecodeXORPeerAddress: %v", err)
	}
	if got != addrPort {
		t.Fatalf("got %v, want %v", got, addrPort)
	}

	var otherTransactionID [netsec.TransactionIDSize]byte
	for i := range otherTransactionID {
		otherTransactionID[i] = 0xff
	}
	gotOther, err := netsec.DecodeXORPeerAddress(value, otherTransactionID)
	if err != nil {
		t.Fatalf("DecodeXORPeerAddress with another transaction ID: %v", err)
	}
	if gotOther == addrPort {
		t.Fatal("IPv6 decode did not use the transaction ID")
	}
}

func TestDecodeXORPeerAddressBytesWrapper(t *testing.T) {
	addrPort := netip.MustParseAddrPort("[2001:db8::2]:1234")
	transactionID := [netsec.TransactionIDSize]byte{0xaa, 0xbb, 0xcc}
	value := xorPeerAttribute(t, addrPort, transactionID)

	got, err := netsec.DecodeXORPeerAddressBytes(value, transactionID[:])
	if err != nil {
		t.Fatalf("DecodeXORPeerAddressBytes: %v", err)
	}
	if got != addrPort {
		t.Fatalf("got %v, want %v", got, addrPort)
	}

	if _, err := netsec.DecodeXORPeerAddressBytes(value, transactionID[:11]); err == nil {
		t.Fatal("short transaction ID was accepted")
	} else if code := netsec.ErrorCode(err); code != netsec.CodeBadRequest {
		t.Fatalf("error code = %d, want %d", code, netsec.CodeBadRequest)
	}
}

func TestDecodeXORPeerAddressRejectsMalformedAttributes(t *testing.T) {
	var transactionID [netsec.TransactionIDSize]byte
	tests := []struct {
		name  string
		value []byte
	}{
		{name: "empty", value: nil},
		{name: "short IPv4", value: make([]byte, 7)},
		{name: "long IPv4", value: append([]byte{0x00, 0x01}, make([]byte, 7)...)},
		{name: "short IPv6", value: append([]byte{0x00, 0x02}, make([]byte, 17)...)},
		{name: "unknown family", value: append([]byte{0x00, 0x03}, make([]byte, 6)...)},
		{name: "IPv4 family with IPv6 length", value: append([]byte{0x00, 0x01}, make([]byte, 18)...)},
		{name: "IPv6 family with IPv4 length", value: append([]byte{0x00, 0x02}, make([]byte, 6)...)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := netsec.DecodeXORPeerAddress(test.value, transactionID)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := netsec.ErrorCode(err); code != netsec.CodeBadRequest {
				t.Fatalf("error code = %d, want %d (%v)", code, netsec.CodeBadRequest, err)
			}
		})
	}
}

func TestPeerForbiddenLoopbackMetadataAndMappedIPv6(t *testing.T) {
	forbidden := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.169.254/32"),
	}

	tests := []struct {
		peer string
		want bool
	}{
		{peer: "127.0.0.1", want: true},
		{peer: "127.10.20.30", want: true},
		{peer: "169.254.169.254", want: true},
		{peer: "::ffff:127.0.0.1", want: true},
		{peer: "::ffff:169.254.169.254", want: true},
		{peer: "8.8.8.8", want: false},
		{peer: "2001:4860:4860::8888", want: false},
	}

	for _, test := range tests {
		t.Run(test.peer, func(t *testing.T) {
			peer := netip.MustParseAddr(test.peer)
			if got := netsec.PeerForbidden(peer, forbidden); got != test.want {
				t.Fatalf("PeerForbidden(%v) = %v, want %v", peer, got, test.want)
			}
		})
	}

	if err := netsec.CheckPeerAllowed(
		netip.MustParseAddrPort("169.254.169.254:80"),
		forbidden,
	); netsec.ErrorCode(err) != netsec.CodeForbidden {
		t.Fatalf("CheckPeerAllowed error = %v, want code %d", err, netsec.CodeForbidden)
	}
	if err := netsec.CheckPeerAllowed(
		netip.MustParseAddrPort("8.8.8.8:53"),
		forbidden,
	); err != nil {
		t.Fatalf("CheckPeerAllowed rejected public address: %v", err)
	}
}

func TestProtocolErrorMatching(t *testing.T) {
	err := netsec.CheckPeerAllowed(
		netip.MustParseAddrPort("127.0.0.1:9"),
		[]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	)
	if !errors.Is(err, netsec.ErrForbiddenPeer) {
		t.Fatalf("errors.Is(%v, ErrForbiddenPeer) = false", err)
	}
	if errors.Is(err, netsec.ErrBadRequest) {
		t.Fatal("403 error incorrectly matched the 400 sentinel")
	}
}
