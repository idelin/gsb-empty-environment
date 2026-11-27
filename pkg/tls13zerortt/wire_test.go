package tls13zerortt_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"

	"formal.local/w28-742f7bec6e/pkg/tls13zerortt"
)

// wireBuilder is a small external-package ClientHello constructor used by the
// runnable example. It derives the binder through the package's exported
// primitives so the example stays independent of internal test helpers.
type wireBuilder struct{}

func ext(typ uint16, body []byte) []byte {
	b := make([]byte, 4+len(body))
	binary.BigEndian.PutUint16(b[0:], typ)
	binary.BigEndian.PutUint16(b[2:], uint16(len(body)))
	copy(b[4:], body)
	return b
}

func vec16(b []byte) []byte {
	out := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(out, uint16(len(b)))
	copy(out[2:], b)
	return out
}

func helloBody(cipherSuite uint16, extensions []byte) []byte {
	var b []byte
	b = append(b, 0x03, 0x03)
	b = append(b, make([]byte, 32)...)
	b = append(b, 0)
	cs := make([]byte, 2)
	binary.BigEndian.PutUint16(cs, cipherSuite)
	b = append(b, vec16(cs)...)
	b = append(b, 1, 0)
	b = append(b, vec16(extensions)...)
	return b
}

func handshake(body []byte) []byte {
	out := []byte{tls13zerortt.HandshakeTypeClientHello,
		byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(out, body...)
}

// buildHello builds a one-identity ClientHello with a valid binder. It first
// assembles a dummy-binder message, truncates it via the exported parser, then
// computes the real binder over the truncated transcript.
func (wireBuilder) buildHello(psk []byte, identity []byte, obfAge uint32,
	cipherSuite uint16, withEarlyData bool) []byte {

	sv := ext(43, []byte{0x00, 0x03, 0x04})
	var ed []byte
	if withEarlyData {
		ed = ext(tls13zerortt.ExtensionEarlyData, nil)
	}

	age := make([]byte, 4)
	binary.BigEndian.PutUint32(age, obfAge)
	identities := vec16(append(vec16(identity), age...))
	dummyBinders := vec16(append([]byte{byte(tls13zerortt.HashLength)},
		make([]byte, tls13zerortt.HashLength)...))
	pskExt := ext(tls13zerortt.ExtensionPreSharedKey,
		append(identities, dummyBinders...))

	exts := append(sv, ed...)
	exts = append(exts, pskExt...)
	dummy := handshake(helloBody(cipherSuite, exts))

	parsed, err := tls13zerortt.ParseClientHello(dummy)
	if err != nil {
		panic(err)
	}

	// Derive the key chain with the exported schedule and compute the binder.
	ks, err := tls13zerortt.NewKeySchedule(psk, tls13zerortt.ResumptionPSK)
	if err != nil {
		panic(err)
	}
	truncated, err := parsed.TruncatedClientHello()
	if err != nil {
		panic(err)
	}
	trHash := sha256.Sum256(truncated)
	mac := hmac.New(sha256.New, ks.FinishedKey)
	mac.Write(trHash[:])
	binder := mac.Sum(nil)

	realBinders := vec16(append([]byte{byte(len(binder))}, binder...))
	pskExt = ext(tls13zerortt.ExtensionPreSharedKey,
		append(identities, realBinders...))
	exts = append(sv, ed...)
	exts = append(exts, pskExt...)
	return handshake(helloBody(cipherSuite, exts))
}
