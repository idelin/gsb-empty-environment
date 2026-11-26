package tls13sm

// clientHelloBuilder assembles a TLS 1.3 ClientHello with correct PSK binder
// placement. The message is built in two passes:
//
//  1. Every extension except pre_shared_key is serialized and hashed after
//     being truncated to the binder vector length prefix (RFC 8446 4.2.11.2).
//  2. The binders are computed, then the final extension is appended with its
//     exact length, guaranteeing that the bytes covered by the binder hash are
//     byte-identical to the transmitted message.
type clientHelloBuilder struct {
	random        []byte
	sessionID     []byte
	cipherSuites  []uint16
	groups        []uint16
	keyShares     []*keyShare
	pskMode       byte
	withEarlyData bool
	cookie        []byte
	pskIdentity   []byte
	obfuscatedAge uint32
	binderLen     int
}

func newClientHelloBuilder() *clientHelloBuilder {
	return &clientHelloBuilder{
		sessionID: make([]byte, 0),
		pskMode:   1, // psk_dhe_ke
		binderLen: 0,
	}
}

func (b *clientHelloBuilder) bodyPrefix() []byte {
	var body []byte
	body = appendU16(body, LegacyVersionTLS12)
	body = append(body, b.random...)
	body = appendU8(body, byte(len(b.sessionID)))
	body = append(body, b.sessionID...)

	var suites []byte
	for _, suite := range b.cipherSuites {
		suites = appendU16(suites, suite)
	}
	body = appendLength16(body, suites)
	body = appendU8(body, 1)
	body = appendU8(body, 0) // null compression

	var exts []extension

	if len(b.groups) > 0 {
		var groups []byte
		for _, group := range b.groups {
			groups = appendU16(groups, group)
		}
		exts = append(exts, extension{Type: ExtSupportedGroups, Data: appendLength16(nil, groups)})
	}
	if b.pskMode != 0xff {
		exts = append(exts, extension{Type: ExtPSKKeyExchangeModes, Data: []byte{1, b.pskMode}})
	}

	var versions []byte
	versions = appendU16(versions, SupportedTLS13)
	exts = append(exts, extension{Type: ExtSupportedVersions, Data: appendU8(nil, byte(len(versions)))})
	exts[len(exts)-1].Data = append(exts[len(exts)-1].Data, versions...)

	if len(b.keyShares) > 0 {
		exts = append(exts, extension{Type: ExtKeyShare, Data: marshalKeyShareList(b.keyShares)})
	}
	if b.withEarlyData {
		exts = append(exts, extension{Type: ExtEarlyData, Data: nil})
	}
	if len(b.cookie) > 0 {
		exts = append(exts, extension{Type: ExtCookie, Data: appendLength16(nil, b.cookie)})
	}

	// pre_shared_key must be the last extension (RFC 8446 4.2.11).
	if b.pskIdentity != nil {
		var identityList []byte
		identityList = appendLength16(identityList, b.pskIdentity)
		identityList = appendU32(identityList, b.obfuscatedAge)
		var pskData []byte
		pskData = appendLength16(pskData, identityList)
		// Two-byte binder list length placeholder; filled by buildWithBinder.
		pskData = appendU16(pskData, 0)
		exts = append(exts, extension{Type: ExtPSK, Data: pskData})
	}

	body = append(body, marshalExtensions(exts)...)
	return body
}

// truncatedMessage returns the full handshake message (with header) through
// the two-byte binder list length, with the binder entries themselves
// omitted. The length of the returned slice is exactly the bytes covered by
// Transcript-Hash for binder computation.
func (b *clientHelloBuilder) truncatedMessage() []byte {
	return marshalHandshake(HandshakeClientHello, b.bodyPrefix())
}

// buildWithBinder replaces the zero binder-list placeholder with the supplied
// binders and returns the final handshake message.
func (b *clientHelloBuilder) buildWithBinder(binders [][]byte) []byte {
	// Locate the final pre_shared_key extension and rebuild its data.
	var identityList []byte
	identityList = appendLength16(identityList, b.pskIdentity)
	identityList = appendU32(identityList, b.obfuscatedAge)

	var binderList []byte
	for _, binder := range binders {
		binderList = appendU8(binderList, byte(len(binder)))
		binderList = append(binderList, binder...)
	}
	var pskData []byte
	pskData = appendLength16(pskData, identityList)
	pskData = appendLength16(pskData, binderList)

	// Rebuild extension list without the placeholder pre_shared_key entry.
	exts := b.extensionsWithoutPSK()
	exts = append(exts, extension{Type: ExtPSK, Data: pskData})

	// Replace the trailing extension block in body.
	prefix := b.bodyBeforeExtensions()
	final := append([]byte(nil), prefix...)
	final = append(final, marshalExtensions(exts)...)
	return marshalHandshake(HandshakeClientHello, final)
}

func (b *clientHelloBuilder) bodyBeforeExtensions() []byte {
	var body []byte
	body = appendU16(body, LegacyVersionTLS12)
	body = append(body, b.random...)
	body = appendU8(body, byte(len(b.sessionID)))
	body = append(body, b.sessionID...)

	var suites []byte
	for _, suite := range b.cipherSuites {
		suites = appendU16(suites, suite)
	}
	body = appendLength16(body, suites)
	body = appendU8(body, 1)
	body = appendU8(body, 0)
	return body
}

func (b *clientHelloBuilder) extensionsWithoutPSK() []extension {
	var exts []extension
	if len(b.groups) > 0 {
		var groups []byte
		for _, group := range b.groups {
			groups = appendU16(groups, group)
		}
		exts = append(exts, extension{Type: ExtSupportedGroups, Data: appendLength16(nil, groups)})
	}
	if b.pskMode != 0xff {
		exts = append(exts, extension{Type: ExtPSKKeyExchangeModes, Data: []byte{1, b.pskMode}})
	}
	var versions []byte
	versions = appendU16(versions, SupportedTLS13)
	versionExt := appendU8(nil, byte(len(versions)))
	versionExt = append(versionExt, versions...)
	exts = append(exts, extension{Type: ExtSupportedVersions, Data: versionExt})
	if len(b.keyShares) > 0 {
		exts = append(exts, extension{Type: ExtKeyShare, Data: marshalKeyShareList(b.keyShares)})
	}
	if b.withEarlyData {
		exts = append(exts, extension{Type: ExtEarlyData, Data: nil})
	}
	if len(b.cookie) > 0 {
		exts = append(exts, extension{Type: ExtCookie, Data: appendLength16(nil, b.cookie)})
	}
	return exts
}

// buildPlain constructs a ClientHello without PSK/early-data material.
func (b *clientHelloBuilder) buildPlain() []byte {
	prefix := b.bodyBeforeExtensions()
	final := append([]byte(nil), prefix...)
	final = append(final, marshalExtensions(b.extensionsWithoutPSK())...)
	return marshalHandshake(HandshakeClientHello, final)
}
