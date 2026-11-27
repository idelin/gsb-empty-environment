package tls13zerortt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// buildClientHello constructs a minimal but wire-valid TLS 1.3 ClientHello
// handshake message carrying one PSK identity (with a correctly computed
// binder) and an optional empty early_data extension. It mirrors the parser
// layout so round-trip tests exercise the real truncation code paths.
//
// If corrupt is non-nil it is applied to the final message before returning,
// allowing tamper tests to flip arbitrary bytes (lengths, binder bytes, etc.).
func buildClientHello(psk []byte, mode PSKMode, identity []byte, obfAge uint32,
	cipherSuite uint16, alpn []byte, withEarlyData bool,
	corrupt func(msg []byte) []byte) []byte {

	ks, err := NewKeySchedule(psk, mode)
	if err != nil {
		panic(err)
	}

	// Extension bodies.
	supportedVersions := []byte{0x00, 0x03, 0x04} // len 1, version 0x0304
	svExt := extension(43, supportedVersions)

	var alpnExt []byte
	if alpn != nil {
		protoList := append([]byte{byte(len(alpn))}, alpn...)
		body := append([]byte{byte(len(protoList))}, protoList...)
		alpnExt = extension(16, body)
	}

	var edExt []byte
	if withEarlyData {
		edExt = extension(ExtensionEarlyData, nil)
	}

	// Identities block of pre_shared_key.
	// One PskIdentity = opaque identity<1..2^16-1> + uint32 obfuscated age.
	identityEntry := vec16(identity)
	ageBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(ageBytes, obfAge)
	identities := vec16(append(identityEntry, ageBytes...))

	// First assemble a full message with a dummy zero binder, then let the
	// production parser+truncator produce the exact transcript the verifier
	// uses. This keeps test construction and verification on one code path.
	dummyBinder := make([]byte, HashLength)
	binders := vec16(append([]byte{byte(HashLength)}, dummyBinder...))
	dummy := buildWithLengths(cipherSuite, svExt, alpnExt, edExt, identities, binders)

	parsed, err := ParseClientHello(dummy)
	if err != nil {
		panic(err)
	}
	truncated, err := parsed.TruncatedClientHello()
	if err != nil {
		panic(err)
	}
	binder := ks.binderMAC(truncated)

	// Rebuild the full message with the real binder.
	binders = vec16(append([]byte{byte(len(binder))}, binder...))
	msg := buildWithLengths(cipherSuite, svExt, alpnExt, edExt, identities, binders)

	if corrupt != nil {
		msg = corrupt(msg)
	}
	return msg
}

func extension(typ uint16, body []byte) []byte {
	b := extensionHeader(typ, len(body))
	return append(b, body...)
}

func extensionHeader(typ uint16, bodyLen int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b[0:], typ)
	binary.BigEndian.PutUint16(b[2:], uint16(bodyLen))
	return b
}

func vec16(b []byte) []byte {
	out := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(out, uint16(len(b)))
	copy(out[2:], b)
	return out
}

func clientHelloBody(cipherSuite uint16, extensions []byte) []byte {
	var b []byte
	b = append(b, 0x03, 0x03) // legacy_version TLS 1.2
	b = append(b, make([]byte, 32)...)
	b = append(b, 0) // empty legacy_session_id

	cs := make([]byte, 2)
	binary.BigEndian.PutUint16(cs, cipherSuite)
	b = append(b, vec16(cs)...)
	b = append(b, 1, 0) // legacy_compression_methods = [null]
	b = append(b, vec16(extensions)...)
	return b
}

func handshakeMessage(body []byte) []byte {
	out := []byte{HandshakeTypeClientHello}
	lenBytes := make([]byte, 3)
	lenBytes[0] = byte(len(body) >> 16)
	lenBytes[1] = byte(len(body) >> 8)
	lenBytes[2] = byte(len(body))
	out = append(out, lenBytes...)
	return append(out, body...)
}

// buildWithLengths assembles the full ClientHello with correct nested lengths
// in one pass, so the parser sees a self-consistent wire message.
func buildWithLengths(cipherSuite uint16, svExt, alpnExt, edExt, identities, binders []byte) []byte {
	pskBody := append(append([]byte(nil), identities...), binders...)
	pskExt := extension(ExtensionPreSharedKey, pskBody)

	var exts []byte
	exts = append(exts, svExt...)
	exts = append(exts, alpnExt...)
	exts = append(exts, edExt...)
	exts = append(exts, pskExt...)

	body := clientHelloBody(cipherSuite, exts)
	return handshakeMessage(body)
}

// independentBinder recomputes the binder with a from-scratch HMAC using the
// documented formula, giving tests an oracle independent of KeySchedule
// internals.
func independentBinder(psk []byte, mode PSKMode, truncatedCH []byte) []byte {
	label := "res binder"
	if mode == ExternalPSK {
		label = "ext binder"
	}
	zero := make([]byte, sha256.Size)
	early := hmac.New(sha256.New, zero)
	early.Write(psk)
	earlySecret := early.Sum(nil)

	emptyHash := sha256.Sum256(nil)
	binderKey := mustExpandLabel(earlySecret, label, emptyHash[:])
	finishedKey := mustExpandLabel(binderKey, "finished", nil)

	trHash := sha256.Sum256(truncatedCH)
	mac := hmac.New(sha256.New, finishedKey)
	mac.Write(trHash[:])
	return mac.Sum(nil)
}

func mustExpandLabel(secret []byte, label string, context []byte) []byte {
	out, err := hkdfExpandLabel(secret, label, context, HashLength)
	if err != nil {
		panic(err)
	}
	return out
}
