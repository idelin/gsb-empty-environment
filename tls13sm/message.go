package tls13sm

import (
	"bytes"
)

// ServerHello is the parsed form of the TLS 1.3 ServerHello /
// HelloRetryRequest handshake body (RFC 8446 4.1.3).
type ServerHello struct {
	Version           uint16 // legacy_version, must be 0x0303
	Random            []byte // 32 bytes
	SessionID         []byte
	CipherSuite       uint16
	CompressionMethod uint8
	Extensions        []extension

	SelectedGroup    uint16
	SelectedKeyShare []byte
	SupportedVersion uint16
	PSKIdentityIndex uint16
	PSKOffered       bool
}

// isHelloRetryRequest implements the detection rule of RFC 8446 4.1.4: an HRR
// is a ServerHello whose supported_versions selects TLS 1.3 and whose
// key_share extension contains a NamedGroup rather than a KeyShareEntry.
func (sh *ServerHello) isHelloRetryRequest() bool {
	raw, ok := extensionData(sh.Extensions, ExtKeyShare)
	if !ok || len(raw) != 2 {
		return false
	}
	sh.SelectedGroup = uint16(raw[0])<<8 | uint16(raw[1])
	return true
}

// downgradeSentinel returns the detected downgrade sentinel, if any, in the
// trailing eight bytes of ServerHello.random.
func (sh *ServerHello) downgradeSentinel() []byte {
	if len(sh.Random) != 32 {
		return nil
	}
	tail := sh.Random[24:32]
	if bytes.Equal(tail, DowngradeSentinelTLS12) {
		return DowngradeSentinelTLS12
	}
	if bytes.Equal(tail, DowngradeSentinelTLS11) {
		return DowngradeSentinelTLS11
	}
	return nil
}

// parseServerHello parses a ServerHello handshake body and performs the
// mandatory structural and downgrade checks.
func parseServerHello(body []byte) (*ServerHello, error) {
	r := newReader(body)
	version, err := r.u16()
	if err != nil {
		return nil, err
	}
	random, err := r.bytes(32)
	if err != nil {
		return nil, err
	}
	sessionID, err := r.vector8()
	if err != nil {
		return nil, err
	}
	cipherSuite, err := r.u16()
	if err != nil {
		return nil, err
	}
	compression, err := r.u8()
	if err != nil {
		return nil, err
	}
	extRaw, err := r.vector16Raw()
	if err != nil {
		return nil, err
	}
	if r.remaining() != 0 {
		return nil, errProto("trailing bytes after ServerHello")
	}
	exts, err := parseExtensions(extRaw)
	if err != nil {
		return nil, err
	}

	sh := &ServerHello{
		Version:           version,
		Random:            random,
		SessionID:         sessionID,
		CipherSuite:       cipherSuite,
		CompressionMethod: compression,
		Extensions:        exts,
	}
	if version != LegacyVersionTLS12 {
		return nil, errProto("ServerHello legacy_version 0x%04x is not 0x0303", version)
	}
	if compression != 0 {
		return nil, errProto("ServerHello compression method %d is not null", compression)
	}
	if sv, ok := extensionData(exts, ExtSupportedVersions); ok {
		if len(sv) != 2 {
			return nil, errProto("malformed supported_versions in ServerHello")
		}
		sh.SupportedVersion = uint16(sv[0])<<8 | uint16(sv[1])
		if sh.SupportedVersion != SupportedTLS13 {
			return nil, errProto("negotiated supported_versions 0x%04x is not TLS 1.3", sh.SupportedVersion)
		}
	} else {
		return nil, errProto("ServerHello missing supported_versions extension")
	}
	if psk, ok := extensionData(exts, ExtPSK); ok {
		if len(psk) != 2 {
			return nil, errProto("malformed pre_shared_key response")
		}
		sh.PSKIdentityIndex = uint16(psk[0])<<8 | uint16(psk[1])
		sh.PSKOffered = true
	}

	if sentinel := sh.downgradeSentinel(); sentinel != nil {
		return nil, ErrDowngradeDetected
	}

	if !sh.isHelloRetryRequest() {
		raw, ok := extensionData(exts, ExtKeyShare)
		if !ok {
			return nil, errProto("ServerHello missing key_share extension")
		}
		sr := newReader(raw)
		group, err := sr.u16()
		if err != nil {
			return nil, err
		}
		key, err := sr.vector16()
		if err != nil {
			return nil, err
		}
		if sr.remaining() != 0 {
			return nil, errProto("trailing bytes after ServerHello key_share")
		}
		sh.SelectedGroup = group
		sh.SelectedKeyShare = key
	}
	return sh, nil
}

// marshalServerHello serializes a ServerHello body (without handshake
// header). keyShareGroup + keyShareKey encode a normal KeyShareEntry; set
// keyShareKey to nil to emit the two-byte HRR group form.
func marshalServerHello(random []byte, sessionID []byte, cipherSuite uint16, exts []extension) []byte {
	var body []byte
	body = appendU16(body, LegacyVersionTLS12)
	body = append(body, random...)
	body = appendU8(body, byte(len(sessionID)))
	body = append(body, sessionID...)
	body = appendU16(body, cipherSuite)
	body = appendU8(body, 0)
	body = append(body, marshalExtensions(exts)...)
	return body
}

// ClientHello is the parsed form of a TLS 1.3 ClientHello.
type ClientHello struct {
	LegacyVersion      uint16
	Random             []byte
	SessionID          []byte
	CipherSuites       []uint16
	CompressionMethods []byte
	Extensions         []extension

	SupportedGroups     []uint16
	KeyShares           []keyShare
	SupportedVersions   []uint16
	PSKModes            []byte
	EarlyDataOffered    bool
	Cookie              []byte
	ObfuscatedTicketAge uint32
	PSKIdentities       []pskIdentity
	PSKBinders          [][]byte
	Raw                 []byte
}

type pskIdentity struct {
	Identity      []byte
	ObfuscatedAge uint32
}

func parseClientHello(body, fullMessage []byte) (*ClientHello, error) {
	r := newReader(body)
	version, err := r.u16()
	if err != nil {
		return nil, err
	}
	random, err := r.bytes(32)
	if err != nil {
		return nil, err
	}
	sessionID, err := r.vector8()
	if err != nil {
		return nil, err
	}
	cipherRaw, err := r.vector16()
	if err != nil {
		return nil, err
	}
	if len(cipherRaw)%2 != 0 {
		return nil, errProto("odd-length cipher_suites vector")
	}
	var suites []uint16
	for i := 0; i < len(cipherRaw); i += 2 {
		suites = append(suites, uint16(cipherRaw[i])<<8|uint16(cipherRaw[i+1]))
	}
	compressions, err := r.vector8()
	if err != nil {
		return nil, err
	}
	extRaw, err := r.vector16Raw()
	if err != nil {
		return nil, err
	}
	if r.remaining() != 0 {
		return nil, errProto("trailing bytes after ClientHello")
	}
	exts, err := parseExtensions(extRaw)
	if err != nil {
		return nil, err
	}

	ch := &ClientHello{
		LegacyVersion:      version,
		Random:             random,
		SessionID:          sessionID,
		CipherSuites:       suites,
		CompressionMethods: compressions,
		Extensions:         exts,
		Raw:                append([]byte(nil), fullMessage...),
	}
	if version != LegacyVersionTLS12 {
		return nil, errProto("ClientHello legacy_version 0x%04x is not 0x0303", version)
	}

	if sg, ok := extensionData(exts, ExtSupportedGroups); ok {
		groups, err := newReader(sg).vector16()
		if err != nil {
			return nil, err
		}
		for i := 0; i+1 < len(groups); i += 2 {
			ch.SupportedGroups = append(ch.SupportedGroups, uint16(groups[i])<<8|uint16(groups[i+1]))
		}
	}
	if ks, ok := extensionData(exts, ExtKeyShare); ok {
		shares, err := parseKeyShareList(ks)
		if err != nil {
			return nil, err
		}
		ch.KeyShares = shares
	}
	if sv, ok := extensionData(exts, ExtSupportedVersions); ok {
		versions, err := newReader(sv).vector8()
		if err != nil {
			return nil, err
		}
		for i := 0; i+1 < len(versions); i += 2 {
			ch.SupportedVersions = append(ch.SupportedVersions, uint16(versions[i])<<8|uint16(versions[i+1]))
		}
	}
	if modes, ok := extensionData(exts, ExtPSKKeyExchangeModes); ok {
		ch.PSKModes, err = newReader(modes).vector8()
		if err != nil {
			return nil, err
		}
	}
	if _, ok := extensionData(exts, ExtEarlyData); ok {
		ch.EarlyDataOffered = true
	}
	if cookie, ok := extensionData(exts, ExtCookie); ok {
		ch.Cookie, err = newReader(cookie).vector16()
		if err != nil {
			return nil, err
		}
	}
	if psk, ok := extensionData(exts, ExtPSK); ok {
		if err := parsePSKExtension(psk, ch); err != nil {
			return nil, err
		}
	}
	return ch, nil
}

func parsePSKExtension(raw []byte, ch *ClientHello) error {
	r := newReader(raw)
	identitiesRaw, err := r.vector16()
	if err != nil {
		return err
	}
	ir := newReader(identitiesRaw)
	for ir.remaining() > 0 {
		identity, err := ir.vector16()
		if err != nil {
			return err
		}
		age, err := ir.u32()
		if err != nil {
			return err
		}
		ch.PSKIdentities = append(ch.PSKIdentities, pskIdentity{Identity: identity, ObfuscatedAge: age})
	}
	bindersRaw, err := r.vector16()
	if err != nil {
		return err
	}
	br := newReader(bindersRaw)
	for br.remaining() > 0 {
		binder, err := br.vector8()
		if err != nil {
			return err
		}
		ch.PSKBinders = append(ch.PSKBinders, binder)
	}
	if r.remaining() != 0 {
		return errProto("trailing bytes after pre_shared_key extension")
	}
	return nil
}

// EncryptedExtensions is parsed only far enough to locate the early_data
// extension that marks server acceptance of 0-RTT.
type EncryptedExtensions struct {
	Extensions  []extension
	EarlyDataOK bool
}

func parseEncryptedExtensions(body []byte) (*EncryptedExtensions, error) {
	exts, err := parseExtensions(body)
	if err != nil {
		return nil, err
	}
	_, ok := extensionData(exts, ExtEarlyData)
	return &EncryptedExtensions{Extensions: exts, EarlyDataOK: ok}, nil
}

// KeyUpdate is the four-byte KeyUpdate handshake message.
type KeyUpdate struct {
	RequestUpdate uint8
}

func parseKeyUpdate(body []byte) (*KeyUpdate, error) {
	if len(body) != 1 {
		return nil, errProto("KeyUpdate body must be one byte, got %d", len(body))
	}
	if body[0] > KeyUpdateRequested {
		return nil, errProto("KeyUpdate request_update value %d is illegal", body[0])
	}
	return &KeyUpdate{RequestUpdate: body[0]}, nil
}
