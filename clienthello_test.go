package netsec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
)

func q0250ClientHello(groups []NamedGroup, shares []KeyShareEntry) *ClientHello {
	return &ClientHello{
		LegacyVersion:      0x0303,
		Random:             [32]byte{1, 2, 3, 4},
		SessionID:          []byte{1, 2, 3},
		CipherSuites:       []uint16{0x1301, 0x1302},
		CompressionMethods: []byte{0},
		SupportedGroups:    groups,
		KeyShares:          shares,
	}
}

func q0250Shares(groups ...NamedGroup) []KeyShareEntry {
	shares := make([]KeyShareEntry, len(groups))
	for i, group := range groups {
		shares[i] = KeyShareEntry{
			Group:       group,
			KeyExchange: bytes.Repeat([]byte{byte(i + 1)}, 32),
		}
	}
	return shares
}

func TestQ0250DecodeClientHelloAcceptsUniqueGroups(t *testing.T) {
	hello := q0250ClientHello(
		[]NamedGroup{0x001d, 0x0017, 0x0018},
		q0250Shares(0x001d, 0x0017),
	)
	body, err := hello.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeClientHello(body)
	if err != nil {
		t.Fatalf("unique groups rejected: %v", err)
	}
	if len(decoded.SupportedGroups) != 3 {
		t.Fatalf("got %d supported groups, want 3", len(decoded.SupportedGroups))
	}
	if len(decoded.KeyShares) != 2 {
		t.Fatalf("got %d key shares, want 2", len(decoded.KeyShares))
	}
}

func TestQ0250DecodeClientHelloRejectsDuplicateSupportedGroup(t *testing.T) {
	hello := q0250ClientHello(
		[]NamedGroup{0x001d, 0x0017, 0x001d},
		q0250Shares(0x001d, 0x0017),
	)
	body, err := hello.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecodeClientHello(body)
	if !errors.Is(err, ErrDuplicateGroup) {
		t.Fatalf("got %v, want ErrDuplicateGroup", err)
	}
	if !errors.Is(err, ErrDuplicateSupportedGroup) {
		t.Fatalf("got %v, want ErrDuplicateSupportedGroup", err)
	}
	var duplicate *DuplicateGroupError
	if !errors.As(err, &duplicate) || duplicate.Group != 0x001d {
		t.Fatalf("got %#v, want duplicate group 0x001d", duplicate)
	}
}

func TestQ0250DecodeClientHelloRejectsDuplicateKeyShareGroup(t *testing.T) {
	hello := q0250ClientHello(
		[]NamedGroup{0x001d, 0x0017},
		q0250Shares(0x001d, 0x0017, 0x001d),
	)
	body, err := hello.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecodeClientHello(body)
	if !errors.Is(err, ErrDuplicateGroup) {
		t.Fatalf("got %v, want ErrDuplicateGroup", err)
	}
	if !errors.Is(err, ErrDuplicateKeyShareGroup) {
		t.Fatalf("got %v, want ErrDuplicateKeyShareGroup", err)
	}
}

func TestQ0250DuplicateGroupInExtensionsIsRejected(t *testing.T) {
	groups := make([]byte, 2+4)
	binary.BigEndian.PutUint16(groups[:2], 4)
	binary.BigEndian.PutUint16(groups[2:4], 0x001d)
	binary.BigEndian.PutUint16(groups[4:6], 0x001d)

	hello := q0250ClientHello(nil, nil)
	hello.Extensions = []Extension{{Type: ExtensionSupportedGroups, Data: groups}}
	body, err := hello.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	_, err = ParseClientHello(body)
	if !errors.Is(err, ErrDuplicateSupportedGroup) {
		t.Fatalf("got %v, want ErrDuplicateSupportedGroup", err)
	}
}

func TestQ0250DecodeClientHelloFromTLSSRecords(t *testing.T) {
	hello := q0250ClientHello(
		[]NamedGroup{0x001d, 0x0017},
		q0250Shares(0x001d),
	)
	handshake, err := hello.MarshalHandshake()
	if err != nil {
		t.Fatal(err)
	}

	record := make([]byte, 5+len(handshake))
	record[0] = 22
	record[1] = 3
	record[2] = 3
	binary.BigEndian.PutUint16(record[3:5], uint16(len(handshake)))
	copy(record[5:], handshake)

	if _, err := DecodeClientHello(record); err != nil {
		t.Fatalf("complete record rejected: %v", err)
	}

	split := len(handshake) / 2
	var fragmented []byte
	for _, fragment := range [][]byte{handshake[:split], handshake[split:]} {
		header := make([]byte, 5)
		header[0] = 22
		header[1] = 3
		header[2] = 3
		binary.BigEndian.PutUint16(header[3:5], uint16(len(fragment)))
		fragmented = append(fragmented, header...)
		fragmented = append(fragmented, fragment...)
	}
	if _, err := DecodeClientHello(fragmented); err != nil {
		t.Fatalf("fragmented handshake rejected: %v", err)
	}
}

func TestQ0250DuplicateDetectionOnGroupLists(t *testing.T) {
	if err := ValidateSupportedGroups([]NamedGroup{0x001d, 0x0017}); err != nil {
		t.Fatalf("unique supported groups rejected: %v", err)
	}
	if err := ValidateSupportedGroups([]NamedGroup{0x001d, 0x001d}); !errors.Is(err, ErrDuplicateSupportedGroup) {
		t.Fatalf("got %v, want duplicate supported group", err)
	}
	if err := ValidateKeyShares(q0250Shares(0x001d, 0x0017)); err != nil {
		t.Fatalf("unique key shares rejected: %v", err)
	}
	if err := ValidateKeyShares(q0250Shares(0x001d, 0x001d)); !errors.Is(err, ErrDuplicateKeyShareGroup) {
		t.Fatalf("got %v, want duplicate key share group", err)
	}
	if err := ValidateGroups([]uint16{0x001d, 0x001d}); !errors.Is(err, ErrDuplicateSupportedGroup) {
		t.Fatalf("got %v, want duplicate supported group", err)
	}
	if err := ValidateGroups(q0250Shares(0x001d, 0x001d)); !errors.Is(err, ErrDuplicateKeyShareGroup) {
		t.Fatalf("got %v, want duplicate key share group", err)
	}

	keyShareBody := []byte{
		0x00, 0x0a,
		0x00, 0x1d, 0x00, 0x01, 0xaa,
		0x00, 0x1d, 0x00, 0x01, 0xbb,
	}
	if err := ValidateKeyShareGroups(keyShareBody); !errors.Is(err, ErrDuplicateKeyShareGroup) {
		t.Fatalf("got %v, want duplicate key share group from wire body", err)
	}
}

func TestQ0250DuplicateGroupAcrossListsIsAllowed(t *testing.T) {
	// It is normal for a key_share group to also appear in supported_groups.
	hello := q0250ClientHello(
		[]NamedGroup{0x001d, 0x0017},
		q0250Shares(0x001d),
	)
	body, err := hello.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeClientHello(body); err != nil {
		t.Fatalf("same group in supported_groups and key_share rejected: %v", err)
	}

	hexHello := hex.EncodeToString(body)
	if _, err := DecodeClientHello(hexHello); err != nil {
		t.Fatalf("hex ClientHello rejected: %v", err)
	}
}

func TestQ0250BareSupportedGroupsList(t *testing.T) {
	raw := []byte{0x00, 0x1d, 0x00, 0x17}
	groups, err := ParseSupportedGroups(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0] != 0x001d || groups[1] != 0x0017 {
		t.Fatalf("got %#v", groups)
	}
	if _, err := ParseSupportedGroups([]byte{0x00, 0x1d, 0x00, 0x1d}); !errors.Is(err, ErrDuplicateSupportedGroup) {
		t.Fatalf("got %v, want duplicate supported group", err)
	}
}
