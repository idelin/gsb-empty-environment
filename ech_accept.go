package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
)

const (
	// AcceptConfirmationLength is the length of the ECH accept confirmation
	// value carried in ServerHello.random or the HRR ECH extension.
	AcceptConfirmationLength = 8

	// LabelECHAcceptConfirmation is the HKDF label used for the plain
	// ServerHello accept confirmation.
	LabelECHAcceptConfirmation = "ech accept confirmation"
	// LabelHRRECHAcceptConfirmation is the HKDF label used once a
	// HelloRetryRequest has been sent.
	LabelHRRECHAcceptConfirmation = "hrr ech accept confirmation"

	// ExtensionEncryptedClientHello is the TLS extension type for ECH.
	ExtensionEncryptedClientHello = 0xfe0d

	handshakeTypeClientHello = 1
	handshakeTypeServerHello = 2

	handshakeHeaderLen = 4
	legacyVersionLen   = 2
	randomLen          = 32

	// helloRandomOffset is the offset of the random field inside a
	// ClientHello/ServerHello handshake message.
	helloRandomOffset = handshakeHeaderLen + legacyVersionLen
)

// ComputeAcceptConfirmation derives the ECH accept confirmation value:
//
//	HKDF-Expand-Label(HKDF-Extract(zeros, clientHelloInnerRandom),
//	                  label, SHA-256(transcript), 8)
func ComputeAcceptConfirmation(label string, clientHelloInnerRandom, transcript []byte) []byte {
	digest := sha256.Sum256(transcript)
	prk := hkdfExtract(make([]byte, sha256.Size), clientHelloInnerRandom)
	return hkdfExpandLabel(prk, label, digest[:], AcceptConfirmationLength)
}

// VerifyServerHelloAccept reports whether serverHello carries the ECH accept
// confirmation proving the server decrypted clientHelloInner. The
// confirmation is the last 8 bytes of ServerHello.random, computed over
// ClientHelloInner concatenated with the ServerHello whose random's last 8
// bytes are zeroed. The comparison is constant time; any mismatch or
// malformed input is treated as rejection.
func VerifyServerHelloAccept(clientHelloInner, serverHello []byte) bool {
	chiRandom := helloRandom(clientHelloInner, handshakeTypeClientHello)
	shRandom := helloRandom(serverHello, handshakeTypeServerHello)
	if chiRandom == nil || shRandom == nil {
		return false
	}
	received := shRandom[randomLen-AcceptConfirmationLength:]

	zeroedSH := bytes.Clone(serverHello)
	clear(zeroedSH[helloRandomOffset+randomLen-AcceptConfirmationLength : helloRandomOffset+randomLen])

	transcript := make([]byte, 0, len(clientHelloInner)+len(zeroedSH))
	transcript = append(transcript, clientHelloInner...)
	transcript = append(transcript, zeroedSH...)

	expected := ComputeAcceptConfirmation(LabelECHAcceptConfirmation, chiRandom, transcript)
	return subtle.ConstantTimeCompare(received, expected) == 1
}

// VerifyHelloRetryRequestAccept reports whether hrr carries a valid ECH
// accept confirmation after a HelloRetryRequest. The confirmation is the
// last 8 bytes of the HRR's ECH extension, computed with the HRR label over
// the HRR with those 8 bytes zeroed. The comparison is constant time; any
// mismatch or malformed input is treated as rejection.
func VerifyHelloRetryRequestAccept(clientHelloInnerRandom, hrr []byte) bool {
	if len(clientHelloInnerRandom) != randomLen {
		return false
	}
	if len(hrr) == 0 || hrr[0] != handshakeTypeServerHello {
		return false
	}
	offset, length, ok := echExtensionData(hrr)
	if !ok || length < AcceptConfirmationLength {
		return false
	}
	received := hrr[offset+length-AcceptConfirmationLength : offset+length]

	zeroedHRR := bytes.Clone(hrr)
	clear(zeroedHRR[offset+length-AcceptConfirmationLength : offset+length])

	expected := ComputeAcceptConfirmation(LabelHRRECHAcceptConfirmation, clientHelloInnerRandom, zeroedHRR)
	return subtle.ConstantTimeCompare(received, expected) == 1
}

// ProcessServerHello verifies the accept confirmation in serverHello and
// applies the ECH key-handling rules: on acceptance it returns innerKeys
// with accepted=true; on rejection it zeroes innerKeys in place and returns
// nil, false, so no inner key material or inner handshake parameter may be
// adopted.
func ProcessServerHello(clientHelloInner, serverHello, innerKeys []byte) (keys []byte, accepted bool) {
	return ReleaseInnerKeys(VerifyServerHelloAccept(clientHelloInner, serverHello), innerKeys)
}

// ProcessHelloRetryRequest is the HelloRetryRequest counterpart of
// ProcessServerHello.
func ProcessHelloRetryRequest(clientHelloInnerRandom, hrr, innerKeys []byte) (keys []byte, accepted bool) {
	return ReleaseInnerKeys(VerifyHelloRetryRequestAccept(clientHelloInnerRandom, hrr), innerKeys)
}

// ReleaseInnerKeys enforces the ECH key-handling rules after verification:
// when accepted is false every byte of innerKeys is destroyed in place and
// nil is returned; otherwise innerKeys is returned unchanged.
func ReleaseInnerKeys(accepted bool, innerKeys []byte) ([]byte, bool) {
	if !accepted {
		clear(innerKeys)
		return nil, false
	}
	return innerKeys, true
}

// helloRandom returns the 32-byte random field of a ClientHello/ServerHello
// handshake message, or nil if the message is malformed.
func helloRandom(msg []byte, msgType byte) []byte {
	if len(msg) < helloRandomOffset+randomLen || msg[0] != msgType {
		return nil
	}
	return msg[helloRandomOffset : helloRandomOffset+randomLen]
}

// echExtensionData locates the ECH extension in a ServerHello/HelloRetryRequest
// message and returns the offset and length of its extension_data.
func echExtensionData(msg []byte) (offset, length int, ok bool) {
	p := helloRandomOffset + randomLen
	if len(msg) < p+1 {
		return 0, 0, false
	}
	p += 1 + int(msg[p]) // session_id
	if len(msg) < p+2+1 {
		return 0, 0, false
	}
	p += 2 + 1 // cipher_suite + compression_method
	if len(msg) < p+2 {
		return 0, 0, false
	}
	extTotal := int(binary.BigEndian.Uint16(msg[p:]))
	p += 2
	if len(msg) < p+extTotal {
		return 0, 0, false
	}
	end := p + extTotal
	for p+4 <= end {
		extType := binary.BigEndian.Uint16(msg[p:])
		extLen := int(binary.BigEndian.Uint16(msg[p+2:]))
		p += 4
		if p+extLen > end {
			return 0, 0, false
		}
		if extType == ExtensionEncryptedClientHello {
			return p, extLen, true
		}
		p += extLen
	}
	return 0, 0, false
}

func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

func hkdfExpand(prk, info []byte, length int) []byte {
	var out, t []byte
	for counter := byte(1); len(out) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(t)
		mac.Write(info)
		mac.Write([]byte{counter})
		t = mac.Sum(nil)
		out = append(out, t...)
	}
	return out[:length]
}

// hkdfExpandLabel implements the TLS 1.3 HKDF-Expand-Label construction
// (RFC 8446, Section 7.1).
func hkdfExpandLabel(secret []byte, label string, context []byte, length int) []byte {
	fullLabel := "tls13 " + label
	info := make([]byte, 0, 2+1+len(fullLabel)+1+len(context))
	info = binary.BigEndian.AppendUint16(info, uint16(length))
	info = append(info, byte(len(fullLabel)))
	info = append(info, fullLabel...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	return hkdfExpand(secret, info, length)
}
