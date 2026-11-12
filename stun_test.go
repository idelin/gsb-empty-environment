package netsec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

var testTransactionID = [12]byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
	0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c,
}

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	compact := strings.Join(strings.Fields(value), "")
	decoded, err := hex.DecodeString(compact)
	if err != nil {
		t.Fatalf("hex.DecodeString: %v", err)
	}
	return decoded
}

func buildSignedRequest(
	t *testing.T,
	secret []byte,
	keyRealm string,
	username string,
	realmAttribute string,
	messageType uint16,
	peers []STUNAddress,
	xorPeer bool,
) []byte {
	t.Helper()

	password := DerivePassword(secret, username)
	key := MessageIntegrityKey(username, keyRealm, password)
	attributes := []STUNAttribute{{
		Type:  STUNAttrUsername,
		Value: []byte(username),
	}}
	if realmAttribute != "" {
		attributes = append(attributes, STUNAttribute{
			Type:  STUNAttrRealm,
			Value: []byte(realmAttribute),
		})
	}
	for _, peer := range peers {
		attributeType := STUNAttrPeerAddress
		if xorPeer {
			attributeType = STUNAttrXORPeerAddress
		}
		value, err := EncodeAddress(attributeType, peer, testTransactionID)
		if err != nil {
			t.Fatalf("EncodeAddress: %v", err)
		}
		attributes = append(attributes, STUNAttribute{
			Type:  attributeType,
			Value: value,
		})
	}

	raw, err := EncodeSTUNMessage(messageType, testTransactionID, attributes, key)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}
	return raw
}

func buildValidAllocate(t *testing.T, peers ...STUNAddress) []byte {
	t.Helper()
	username := validUsername(testNow().Unix() + 3600)
	return buildSignedRequest(
		t,
		[]byte(testSecret),
		testRealm,
		username,
		testRealm,
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		peers,
		true,
	)
}

func appendRawAttribute(t *testing.T, raw []byte, attributeType uint16, value []byte) []byte {
	t.Helper()

	if len(raw) < STUNHeaderSize {
		t.Fatal("raw message is too short")
	}
	oldLength := binary.BigEndian.Uint16(raw[2:4])
	paddedLength := (len(value) + 3) &^ 3
	result := append([]byte(nil), raw...)
	result = append(result, make([]byte, 4+paddedLength)...)
	offset := len(result) - (4 + paddedLength)
	binary.BigEndian.PutUint16(result[offset:offset+2], attributeType)
	binary.BigEndian.PutUint16(result[offset+2:offset+4], uint16(len(value)))
	copy(result[offset+4:offset+4+len(value)], value)
	binary.BigEndian.PutUint16(result[2:4], oldLength+uint16(4+paddedLength))
	return result
}

func replaceOnce(t *testing.T, raw []byte, old, replacement string) []byte {
	t.Helper()

	if len(old) != len(replacement) {
		t.Fatalf("replacement length mismatch: %q vs %q", old, replacement)
	}
	index := bytes.Index(raw, []byte(old))
	if index < 0 {
		t.Fatalf("substring %q not found", old)
	}
	result := append([]byte(nil), raw...)
	copy(result[index:index+len(old)], replacement)
	return result
}

func TestRFC5769MessageIntegrityVector(t *testing.T) {
	raw := mustHex(t, `
		000100582112a442b7e7a701bc34d686fa87dfae
		802200105354554e207465737420636c69656e74
		002400046e0001ff
		80290008932ff9b151263b36
		000600096576746a3a68367659202020
		000800149aeaa70cbfd8cb56781ef2b5b2d3f249c1b571a2
		80280004e57a3bcf`)
	key := []byte("VOkJxbRl1RmTxUk/WvJxBt")

	message, err := ParseSTUNMessage(raw)
	if err != nil {
		t.Fatalf("ParseSTUNMessage: %v", err)
	}
	if err := message.VerifyIntegrity(key); err != nil {
		t.Fatalf("VerifyIntegrity: %v", err)
	}
	if err := VerifyMessageIntegrity(raw, key); err != nil {
		t.Fatalf("VerifyMessageIntegrity: %v", err)
	}

	integrity, ok := message.Value(STUNAttrMessageIntegrity)
	if !ok {
		t.Fatal("MESSAGE-INTEGRITY not found")
	}
	if got, want := fmt.Sprintf("%x", integrity), "9aeaa70cbfd8cb56781ef2b5b2d3f249c1b571a2"; got != want {
		t.Fatalf("MESSAGE-INTEGRITY = %s, want %s", got, want)
	}
}

func TestEncoderIntegrityRoundTrip(t *testing.T) {
	key := []byte("VOkJxbRl1RmTxUk/WvJxBt")
	attributes := []STUNAttribute{
		{Type: STUNAttrSoftware, Value: []byte("STUN test client")},
		{Type: 0x0024, Value: []byte{0x6e, 0x00, 0x01, 0xff}},
		{Type: 0x8029, Value: []byte{0x93, 0x2f, 0xf9, 0xb1, 0x51, 0x26, 0x3b, 0x36}},
		{Type: STUNAttrUsername, Value: []byte("evtj:h6vY")},
	}

	encoded, err := EncodeSTUNMessage(0x0001, [12]byte{
		0xb7, 0xe7, 0xa7, 0x01, 0xbc, 0x34,
		0xd6, 0x86, 0xfa, 0x87, 0xdf, 0xae,
	}, attributes, key)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}

	if got, want := fmt.Sprintf("%x", encoded[len(encoded)-20:]), "7907c2d2edbfea480e4c76d82962d5c3742af9e3"; got != want {
		t.Fatalf("encoded MESSAGE-INTEGRITY = %s, want %s", got, want)
	}
	if err := VerifyMessageIntegrity(encoded, key); err != nil {
		t.Fatalf("VerifyMessageIntegrity(encoded): %v", err)
	}
}

func TestSTUNMessageTypeHelpers(t *testing.T) {
	tests := []struct {
		method uint16
		class  uint8
		want   uint16
	}{
		{STUNMethodBinding, STUNClassRequest, 0x0001},
		{STUNMethodBinding, STUNClassIndication, 0x0011},
		{STUNMethodBinding, STUNClassSuccess, 0x0101},
		{STUNMethodBinding, STUNClassError, 0x0111},
		{STUNMethodAllocate, STUNClassRequest, 0x0003},
		{STUNMethodAllocate, STUNClassSuccess, 0x0103},
		{STUNMethodCreatePermission, STUNClassRequest, 0x0008},
		{STUNMethodChannelBind, STUNClassRequest, 0x0009},
	}
	for _, test := range tests {
		got := STUNMessageType(test.method, test.class)
		if got != test.want {
			t.Errorf("STUNMessageType(%d, %d) = 0x%04x, want 0x%04x", test.method, test.class, got, test.want)
		}
		if extracted := STUNMessageMethod(got); extracted != test.method {
			t.Errorf("STUNMessageMethod(0x%04x) = 0x%x, want 0x%x", got, extracted, test.method)
		}
		if extracted := STUNMessageClass(got); extracted != test.class {
			t.Errorf("STUNMessageClass(0x%04x) = %d, want %d", got, extracted, test.class)
		}
	}
}

func TestAuthenticateSTUNHappyPath(t *testing.T) {
	validator := testValidator(t)
	raw := buildValidAllocate(t)

	credentials, err := validator.AuthenticateSTUN(raw)
	if err != nil {
		t.Fatalf("AuthenticateSTUN: %v", err)
	}
	if credentials.Tenant != "acme" || credentials.User != "alice" {
		t.Errorf("unexpected credentials: %+v", credentials)
	}
	if credentials.Password != DerivePassword([]byte(testSecret), credentials.Username) {
		t.Errorf("unexpected password")
	}
}

func TestAuthenticateSTUNWithoutRealmAttribute(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)
	raw := buildSignedRequest(
		t,
		[]byte(testSecret),
		testRealm,
		username,
		"",
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		nil,
		true,
	)
	if _, err := validator.AuthenticateSTUN(raw); err != nil {
		t.Fatalf("AuthenticateSTUN without REALM attribute: %v", err)
	}
}

func TestAuthenticateSTUNRejectsTamperedUsername(t *testing.T) {
	validator := testValidator(t)
	raw := buildValidAllocate(t)

	tests := []struct {
		name        string
		old         string
		replacement string
	}{
		{"expiry", "1700003600", "1700007200"},
		{"tenant", "acme", "evil"},
		{"user", "alice", "malic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := replaceOnce(t, raw, test.old, test.replacement)
			if _, err := validator.AuthenticateSTUN(tampered); !errors.Is(err, ErrIntegrityMismatch) {
				t.Fatalf("tampered %s error = %v, want ErrIntegrityMismatch", test.name, err)
			}
		})
	}
}

func TestAuthenticateSTUNRejectsWrongSecret(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)
	raw := buildSignedRequest(
		t,
		[]byte("wrong-secret"),
		testRealm,
		username,
		testRealm,
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		nil,
		true,
	)
	if _, err := validator.AuthenticateSTUN(raw); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("wrong-secret error = %v, want ErrIntegrityMismatch", err)
	}
}

func TestAuthenticateSTUNRejectsExpiredButSignedCredential(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() - 10)
	raw := buildSignedRequest(
		t,
		[]byte(testSecret),
		testRealm,
		username,
		testRealm,
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		nil,
		true,
	)
	if _, err := validator.AuthenticateSTUN(raw); !errors.Is(err, ErrUsernameExpired) {
		t.Fatalf("expired signed credential error = %v, want ErrUsernameExpired", err)
	}
}

func TestAuthenticateSTUNRejectsRealmMismatch(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)
	raw := buildSignedRequest(
		t,
		[]byte(testSecret),
		testRealm,
		username,
		"other.example.org",
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		nil,
		true,
	)
	if _, err := validator.AuthenticateSTUN(raw); !errors.Is(err, ErrRealmMismatch) {
		t.Fatalf("realm mismatch error = %v, want ErrRealmMismatch", err)
	}
}

func TestAuthenticateSTUNRejectsMissingAttributes(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)

	noUsername, err := EncodeSTUNMessage(
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		testTransactionID,
		[]STUNAttribute{{Type: STUNAttrRealm, Value: []byte(testRealm)}},
		[]byte("0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}
	if _, err := validator.AuthenticateSTUN(noUsername); !errors.Is(err, ErrMissingUsername) {
		t.Fatalf("missing USERNAME error = %v", err)
	}

	noIntegrity, err := EncodeSTUNMessage(
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		testTransactionID,
		[]STUNAttribute{
			{Type: STUNAttrUsername, Value: []byte(username)},
			{Type: STUNAttrRealm, Value: []byte(testRealm)},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}
	if _, err := validator.AuthenticateSTUN(noIntegrity); !errors.Is(err, ErrMissingIntegrity) {
		t.Fatalf("missing MESSAGE-INTEGRITY error = %v", err)
	}
}

func TestAuthenticateSTUNRejectsDuplicateCredentials(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)
	key := MessageIntegrityKey(username, testRealm, DerivePassword([]byte(testSecret), username))

	duplicateUsername, err := EncodeSTUNMessage(
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		testTransactionID,
		[]STUNAttribute{
			{Type: STUNAttrUsername, Value: []byte(username)},
			{Type: STUNAttrUsername, Value: []byte(username)},
			{Type: STUNAttrRealm, Value: []byte(testRealm)},
		},
		key,
	)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}
	if _, err := validator.AuthenticateSTUN(duplicateUsername); !errors.Is(err, ErrSTUNAttribute) {
		t.Fatalf("duplicate USERNAME error = %v", err)
	}

	duplicateRealm, err := EncodeSTUNMessage(
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		testTransactionID,
		[]STUNAttribute{
			{Type: STUNAttrUsername, Value: []byte(username)},
			{Type: STUNAttrRealm, Value: []byte(testRealm)},
			{Type: STUNAttrRealm, Value: []byte(testRealm)},
		},
		key,
	)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}
	if _, err := validator.AuthenticateSTUN(duplicateRealm); !errors.Is(err, ErrSTUNAttribute) {
		t.Fatalf("duplicate REALM error = %v", err)
	}

	duplicateIntegrity := appendRawAttribute(t, buildValidAllocate(t), STUNAttrMessageIntegrity, make([]byte, 20))
	if _, err := validator.AuthenticateSTUN(duplicateIntegrity); !errors.Is(err, ErrSTUNAttribute) {
		t.Fatalf("duplicate MESSAGE-INTEGRITY error = %v", err)
	}
}

func TestAuthenticateSTUNRejectsAttributesAfterIntegrity(t *testing.T) {
	validator := testValidator(t)
	raw := buildValidAllocate(t)

	peerValue, err := EncodeAddress(STUNAttrXORPeerAddress, STUNAddress{
		IP:   net.ParseIP("10.0.0.1"),
		Port: 3478,
	}, testTransactionID)
	if err != nil {
		t.Fatalf("EncodeAddress: %v", err)
	}
	smuggled := appendRawAttribute(t, raw, STUNAttrXORPeerAddress, peerValue)
	if _, err := validator.AuthenticateSTUN(smuggled); !errors.Is(err, ErrIntegrityOrder) {
		t.Fatalf("unprotected peer attribute error = %v, want ErrIntegrityOrder", err)
	}

	software := appendRawAttribute(t, raw, STUNAttrSoftware, []byte("evil"))
	if _, err := validator.AuthenticateSTUN(software); !errors.Is(err, ErrIntegrityOrder) {
		t.Fatalf("unprotected SOFTWARE error = %v, want ErrIntegrityOrder", err)
	}

	// FINGERPRINT is the only attribute RFC 5389 permits after
	// MESSAGE-INTEGRITY. It is not part of the authenticated payload.
	withFingerprint := appendRawAttribute(t, raw, STUNAttrFingerprint, []byte{0, 0, 0, 0})
	if _, err := validator.AuthenticateSTUN(withFingerprint); err != nil {
		t.Fatalf("FINGERPRINT after MESSAGE-INTEGRITY should be allowed: %v", err)
	}
}

func TestAuthenticateSTUNPeerPolicy(t *testing.T) {
	validator := testValidator(t)
	tests := []struct {
		name    string
		ip      string
		blocked bool
	}{
		{"ipv4 loopback", "127.0.0.1", true},
		{"ipv4 rfc1918 10", "10.1.2.3", true},
		{"ipv4 rfc1918 172", "172.16.5.5", true},
		{"ipv4 rfc1918 192", "192.168.1.1", true},
		{"ipv4 cgnat", "100.64.0.1", true},
		{"ipv4 link-local", "169.254.1.1", true},
		{"ipv4 multicast", "224.0.0.1", true},
		{"ipv4 unspecified", "0.0.0.0", true},
		{"ipv4 reserved", "240.0.0.1", true},
		{"ipv6 loopback", "::1", true},
		{"ipv6 ula", "fc00::1", true},
		{"ipv6 link-local", "fe80::1", true},
		{"ipv6 multicast", "ff00::1", true},
		{"ipv4-mapped loopback", "::ffff:127.0.0.1", true},
		{"ipv4-mapped private", "::ffff:10.0.0.1", true},
		{"public ipv4", "8.8.8.8", false},
		{"public ipv4 cloudflare", "1.1.1.1", false},
		{"public ipv6", "2001:4860:4860::8888", false},
		{"documentation ipv4", "203.0.113.5", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := buildValidAllocate(t, STUNAddress{IP: net.ParseIP(test.ip), Port: 3478})
			_, err := validator.AuthenticateSTUN(raw)
			if test.blocked {
				if !errors.Is(err, ErrForbiddenPeer) {
					t.Fatalf("AuthenticateSTUN(%s) error = %v, want ErrForbiddenPeer", test.ip, err)
				}
			} else if err != nil {
				t.Fatalf("AuthenticateSTUN(%s): %v", test.ip, err)
			}
		})
	}
}

func TestAuthenticateSTUNChecksNonXORPeerAddress(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)
	raw := buildSignedRequest(
		t,
		[]byte(testSecret),
		testRealm,
		username,
		testRealm,
		STUNMessageType(STUNMethodCreatePermission, STUNClassRequest),
		[]STUNAddress{{IP: net.ParseIP("192.168.1.1"), Port: 3478}},
		false,
	)
	if _, err := validator.AuthenticateSTUN(raw); !errors.Is(err, ErrForbiddenPeer) {
		t.Fatalf("non-XOR private peer error = %v, want ErrForbiddenPeer", err)
	}
}

func TestAuthenticateSTUNRejectsMappedIPv6Peer(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)
	key := MessageIntegrityKey(username, testRealm, DerivePassword([]byte(testSecret), username))

	// An attacker can claim IPv6 family 0x02 while embedding a private IPv4
	// address in an IPv4-mapped IPv6 value.
	value := make([]byte, 20)
	value[0] = STUNAFIPv6
	binary.BigEndian.PutUint16(value[2:4], 3478)
	copy(value[4:], net.ParseIP("::ffff:10.0.0.1").To16())

	raw, err := EncodeSTUNMessage(
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		testTransactionID,
		[]STUNAttribute{
			{Type: STUNAttrUsername, Value: []byte(username)},
			{Type: STUNAttrRealm, Value: []byte(testRealm)},
			{Type: STUNAttrPeerAddress, Value: value},
		},
		key,
	)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}
	if _, err := validator.AuthenticateSTUN(raw); !errors.Is(err, ErrForbiddenPeer) {
		t.Fatalf("IPv4-mapped IPv6 peer error = %v, want ErrForbiddenPeer", err)
	}
}

func TestAuthenticateSTUNRejectsAnyPrivatePeer(t *testing.T) {
	validator := testValidator(t)
	raw := buildValidAllocate(t,
		STUNAddress{IP: net.ParseIP("8.8.8.8"), Port: 3478},
		STUNAddress{IP: net.ParseIP("10.0.0.1"), Port: 3478},
	)
	if _, err := validator.AuthenticateSTUN(raw); !errors.Is(err, ErrForbiddenPeer) {
		t.Fatalf("mixed public/private peers error = %v, want ErrForbiddenPeer", err)
	}
}

func TestAuthenticateSTUNRejectsMalformedPeerAddress(t *testing.T) {
	validator := testValidator(t)
	username := validUsername(testNow().Unix() + 3600)
	key := MessageIntegrityKey(username, testRealm, DerivePassword([]byte(testSecret), username))

	malformed, err := EncodeSTUNMessage(
		STUNMessageType(STUNMethodAllocate, STUNClassRequest),
		testTransactionID,
		[]STUNAttribute{
			{Type: STUNAttrUsername, Value: []byte(username)},
			{Type: STUNAttrRealm, Value: []byte(testRealm)},
			{Type: STUNAttrXORPeerAddress, Value: []byte{STUNAFIPv4, 0, 0x0d, 0x16}},
		},
		key,
	)
	if err != nil {
		t.Fatalf("EncodeSTUNMessage: %v", err)
	}
	if _, err := validator.AuthenticateSTUN(malformed); !errors.Is(err, ErrSTUNAttribute) {
		t.Fatalf("malformed peer error = %v, want ErrSTUNAttribute", err)
	}
}

func TestPeerPolicyOverrides(t *testing.T) {
	username := validUsername(testNow().Unix() + 3600)
	build := func(t *testing.T, secret []byte, config Config, ip string) error {
		t.Helper()
		validator, err := NewValidator(Config{
			SharedSecret: []byte(testSecret),
			Realm:        testRealm,
			Now:          testNow,
			Peers:        config.Peers,
		})
		if err != nil {
			t.Fatalf("NewValidator: %v", err)
		}
		raw := buildSignedRequest(
			t,
			[]byte(testSecret),
			testRealm,
			username,
			testRealm,
			STUNMessageType(STUNMethodAllocate, STUNClassRequest),
			[]STUNAddress{{IP: net.ParseIP(ip), Port: 3478}},
			true,
		)
		_, err = validator.AuthenticateSTUN(raw)
		return err
	}

	if err := build(t, []byte(testSecret), Config{Peers: PeerPolicy{AllowPrivate: true}}, "10.0.0.1"); err != nil {
		t.Fatalf("AllowPrivate should permit RFC1918: %v", err)
	}
	if err := build(t, []byte(testSecret), Config{Peers: PeerPolicy{AllowPrivate: true}}, "127.0.0.1"); !errors.Is(err, ErrForbiddenPeer) {
		t.Fatalf("AllowPrivate should still reject loopback: %v", err)
	}
	if err := build(t, []byte(testSecret), Config{Peers: PeerPolicy{AllowLoopback: true}}, "127.0.0.1"); err != nil {
		t.Fatalf("AllowLoopback should permit loopback: %v", err)
	}
	if err := build(t, []byte(testSecret), Config{Peers: PeerPolicy{AllowLoopback: true}}, "10.0.0.1"); !errors.Is(err, ErrForbiddenPeer) {
		t.Fatalf("AllowLoopback should still reject private: %v", err)
	}

	_, network, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	if err := build(t, []byte(testSecret), Config{Peers: PeerPolicy{Allowed: []*net.IPNet{network}}}, "10.0.0.1"); err != nil {
		t.Fatalf("explicit CIDR exception should permit peer: %v", err)
	}
}

func TestAddressEncodeDecodeRoundTrip(t *testing.T) {
	addresses := []STUNAddress{
		{IP: net.ParseIP("192.0.2.10"), Port: 3478},
		{IP: net.ParseIP("2001:db8::10"), Port: 5349},
	}
	attributeTypes := []uint16{
		STUNAttrMappedAddress,
		STUNAttrPeerAddress,
		STUNAttrXORMappedAddress,
		STUNAttrXORPeerAddress,
	}
	for _, attributeType := range attributeTypes {
		for _, address := range addresses {
			value, err := EncodeAddress(attributeType, address, testTransactionID)
			if err != nil {
				t.Fatalf("EncodeAddress(0x%04x): %v", attributeType, err)
			}
			decoded, err := DecodeAddress(attributeType, value, testTransactionID)
			if err != nil {
				t.Fatalf("DecodeAddress(0x%04x): %v", attributeType, err)
			}
			if !decoded.IP.Equal(address.IP) || decoded.Port != address.Port {
				t.Errorf("round trip 0x%04x: got %s:%d, want %s:%d", attributeType, decoded.IP, decoded.Port, address.IP, address.Port)
			}
		}
	}

	if _, err := DecodeAddress(STUNAttrUsername, nil, testTransactionID); !errors.Is(err, ErrSTUNAttribute) {
		t.Fatalf("non-address attribute error = %v", err)
	}
}

func TestParseSTUNMessageRejectsMalformedPackets(t *testing.T) {
	valid := buildValidAllocate(t)

	badCookie := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(badCookie[4:8], 0)

	lengthMismatch := append([]byte(nil), valid...)
	binary.BigEndian.PutUint16(lengthMismatch[2:4], binary.BigEndian.Uint16(lengthMismatch[2:4])+4)

	notAligned := make([]byte, STUNHeaderSize)
	binary.BigEndian.PutUint16(notAligned[0:2], 0x0001)
	binary.BigEndian.PutUint16(notAligned[2:4], 1)
	binary.BigEndian.PutUint32(notAligned[4:8], STUNMagicCookie)

	attributeOverrun := make([]byte, STUNHeaderSize+4)
	binary.BigEndian.PutUint16(attributeOverrun[0:2], 0x0001)
	binary.BigEndian.PutUint16(attributeOverrun[2:4], 4)
	binary.BigEndian.PutUint32(attributeOverrun[4:8], STUNMagicCookie)
	binary.BigEndian.PutUint16(attributeOverrun[20:22], STUNAttrUsername)
	binary.BigEndian.PutUint16(attributeOverrun[22:24], 8)

	reservedBits := append([]byte(nil), valid...)
	reservedBits[0] |= 0x80

	tests := []struct {
		name string
		raw  []byte
	}{
		{"nil", nil},
		{"short", []byte{0x00, 0x01}},
		{"bad cookie", badCookie},
		{"length mismatch", lengthMismatch},
		{"unaligned length", notAligned},
		{"attribute overrun", attributeOverrun},
		{"reserved message bits", reservedBits},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseSTUNMessage(test.raw); !errors.Is(err, ErrSTUNMessage) {
				t.Fatalf("ParseSTUNMessage error = %v, want ErrSTUNMessage", err)
			}
		})
	}
}

func TestParseEmptySTUNMessage(t *testing.T) {
	raw := make([]byte, STUNHeaderSize)
	binary.BigEndian.PutUint16(raw[0:2], 0x0001)
	binary.BigEndian.PutUint32(raw[4:8], STUNMagicCookie)
	message, err := ParseSTUNMessage(raw)
	if err != nil {
		t.Fatalf("ParseSTUNMessage: %v", err)
	}
	if len(message.Attributes) != 0 {
		t.Fatalf("attributes = %d, want 0", len(message.Attributes))
	}
}

func TestEncodeSTUNMessageRejectsManagedAttributes(t *testing.T) {
	if _, err := EncodeSTUNMessage(0x0001, testTransactionID, []STUNAttribute{{
		Type:  STUNAttrMessageIntegrity,
		Value: make([]byte, 20),
	}}, nil); !errors.Is(err, ErrSTUNAttribute) {
		t.Fatalf("managed MESSAGE-INTEGRITY error = %v", err)
	}
	if _, err := EncodeSTUNMessage(0x0001, testTransactionID, []STUNAttribute{{
		Type:  STUNAttrFingerprint,
		Value: make([]byte, 4),
	}}, nil); !errors.Is(err, ErrSTUNAttribute) {
		t.Fatalf("managed FINGERPRINT error = %v", err)
	}
}

func TestMessageValueCopies(t *testing.T) {
	message := NewSTUNMessage(0x0001, testTransactionID)
	message.AddString(STUNAttrUsername, "alice")
	if value, ok := message.StringValue(STUNAttrUsername); !ok || value != "alice" {
		t.Fatalf("StringValue = %q, %v", value, ok)
	}

	if raw, err := message.Encode(nil); err != nil {
		t.Fatalf("Encode: %v", err)
	} else {
		parsed, err := ParseSTUNMessage(raw)
		if err != nil {
			t.Fatalf("ParseSTUNMessage: %v", err)
		}
		value, _ := parsed.Value(STUNAttrUsername)
		value[0] = 'X'
		if got, _ := parsed.StringValue(STUNAttrUsername); got != "alice" {
			t.Fatalf("Value did not return a copy: %q", got)
		}
		rawCopy := parsed.Raw()
		rawCopy[0] = 0xff
		if bytes.Equal(parsed.Raw(), rawCopy) {
			t.Fatal("Raw did not return a copy")
		}
	}
}
