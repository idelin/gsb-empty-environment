package netsec

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"testing"
)

func makeRandom(seed byte) []byte {
	r := make([]byte, randomLen)
	for i := range r {
		r[i] = seed + byte(i)
	}
	return r
}

// buildClientHello builds a minimal ClientHello handshake message carrying r
// as its random.
func buildClientHello(r []byte) []byte {
	body := make([]byte, 0, 2+randomLen)
	body = append(body, 0x03, 0x03) // legacy_version
	body = append(body, r...)
	msg := []byte{handshakeTypeClientHello, 0, 0, byte(len(body))}
	return append(msg, body...)
}

// buildServerHello builds a minimal ServerHello-shaped handshake message
// (also used for HRR) with the given random and extensions.
func buildServerHello(r []byte, exts []byte) []byte {
	body := make([]byte, 0, 2+randomLen+1+2+1+2+len(exts))
	body = append(body, 0x03, 0x03) // legacy_version
	body = append(body, r...)
	body = append(body, 0x00)       // session_id length
	body = append(body, 0x13, 0x01) // cipher_suite TLS_AES_128_GCM_SHA256
	body = append(body, 0x00)       // compression_method
	var extLen [2]byte
	binary.BigEndian.PutUint16(extLen[:], uint16(len(exts)))
	body = append(body, extLen[:]...)
	body = append(body, exts...)
	msg := []byte{handshakeTypeServerHello, 0, 0, byte(len(body))}
	return append(msg, body...)
}

func echExtension(data []byte) []byte {
	ext := make([]byte, 0, 4+len(data))
	var hdr [4]byte
	binary.BigEndian.PutUint16(hdr[0:2], ExtensionEncryptedClientHello)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(data)))
	ext = append(ext, hdr[:]...)
	return append(ext, data...)
}

// serverHelloWithConfirmation returns a ServerHello whose random's last 8
// bytes are the accept confirmation for (clientHelloInner, label, zeroLen).
// zeroLen controls how many trailing random bytes are zeroed before hashing,
// so tests can simulate a zeroing deviation.
func serverHelloWithConfirmation(clientHelloInner, shRandom []byte, label string, zeroLen int) []byte {
	zeroedSH := buildServerHello(shRandom, nil)
	clear(zeroedSH[helloRandomOffset+randomLen-zeroLen : helloRandomOffset+randomLen])
	transcript := append(bytes.Clone(clientHelloInner), zeroedSH...)
	conf := ComputeAcceptConfirmation(label, clientHelloInner[helloRandomOffset:helloRandomOffset+randomLen], transcript)
	sh := buildServerHello(shRandom, nil)
	copy(sh[helloRandomOffset+randomLen-AcceptConfirmationLength:], conf)
	return sh
}

// hrrWithConfirmation returns an HRR whose ECH extension ends with the accept
// confirmation computed with the given label.
func hrrWithConfirmation(chiRandom, hrrRandom []byte, label string) []byte {
	placeholder := buildServerHello(hrrRandom, echExtension(make([]byte, AcceptConfirmationLength)))
	conf := ComputeAcceptConfirmation(label, chiRandom, placeholder)
	return buildServerHello(hrrRandom, echExtension(conf))
}

func TestServerHelloAcceptMatch(t *testing.T) {
	chiRandom := makeRandom(0x10)
	shRandom := makeRandom(0xa0)
	chi := buildClientHello(chiRandom)
	sh := serverHelloWithConfirmation(chi, shRandom, LabelECHAcceptConfirmation, AcceptConfirmationLength)

	if !VerifyServerHelloAccept(chi, sh) {
		t.Fatal("valid accept confirmation was rejected")
	}

	innerKeys := []byte("inner-handshake-secret")
	keys, accepted := ProcessServerHello(chi, sh, innerKeys)
	if !accepted {
		t.Fatal("ProcessServerHello rejected a valid confirmation")
	}
	if !bytes.Equal(keys, []byte("inner-handshake-secret")) {
		t.Fatal("inner keys were modified on acceptance")
	}
}

func TestServerHelloAcceptLastByteFlipped(t *testing.T) {
	chiRandom := makeRandom(0x20)
	shRandom := makeRandom(0xb0)
	chi := buildClientHello(chiRandom)
	sh := serverHelloWithConfirmation(chi, shRandom, LabelECHAcceptConfirmation, AcceptConfirmationLength)

	sh[len(sh)-1] ^= 0x01 // flip the last byte of the confirmation

	if VerifyServerHelloAccept(chi, sh) {
		t.Fatal("forged confirmation with flipped last byte was accepted")
	}

	innerKeys := []byte("inner-handshake-secret")
	keys, accepted := ProcessServerHello(chi, sh, innerKeys)
	if accepted {
		t.Fatal("ProcessServerHello accepted a forged confirmation")
	}
	if keys != nil {
		t.Fatal("inner keys were released after rejection")
	}
	if !bytes.Equal(innerKeys, make([]byte, len(innerKeys))) {
		t.Fatal("inner keys were not destroyed after rejection")
	}
}

func TestHRRAcceptMatch(t *testing.T) {
	chiRandom := makeRandom(0x30)
	hrrRandom := makeRandom(0xc0)
	hrr := hrrWithConfirmation(chiRandom, hrrRandom, LabelHRRECHAcceptConfirmation)

	if !VerifyHelloRetryRequestAccept(chiRandom, hrr) {
		t.Fatal("valid HRR accept confirmation was rejected")
	}
}

func TestHRRAcceptWrongLabel(t *testing.T) {
	chiRandom := makeRandom(0x40)
	hrrRandom := makeRandom(0xd0)
	// Attacker/buggy peer used the plain ServerHello label instead of the
	// HRR-specific one.
	hrr := hrrWithConfirmation(chiRandom, hrrRandom, LabelECHAcceptConfirmation)

	if VerifyHelloRetryRequestAccept(chiRandom, hrr) {
		t.Fatal("HRR confirmation computed with the wrong label was accepted")
	}

	innerKeys := []byte("inner-handshake-secret")
	keys, accepted := ProcessHelloRetryRequest(chiRandom, hrr, innerKeys)
	if accepted || keys != nil {
		t.Fatal("wrong-label HRR confirmation was accepted")
	}
	if !bytes.Equal(innerKeys, make([]byte, len(innerKeys))) {
		t.Fatal("inner keys were not destroyed after rejection")
	}
}

func TestServerHelloAcceptZeroingDeviation(t *testing.T) {
	chiRandom := makeRandom(0x50)
	shRandom := makeRandom(0xe0)
	chi := buildClientHello(chiRandom)

	// Confirmation computed with only the last 7 bytes zeroed instead of 8.
	sh := serverHelloWithConfirmation(chi, shRandom, LabelECHAcceptConfirmation, AcceptConfirmationLength-1)

	if VerifyServerHelloAccept(chi, sh) {
		t.Fatal("confirmation computed with a zeroing deviation was accepted")
	}

	// Confirmation computed over a transcript whose random was not zeroed at all.
	sh2 := serverHelloWithConfirmation(chi, shRandom, LabelECHAcceptConfirmation, 0)
	if VerifyServerHelloAccept(chi, sh2) {
		t.Fatal("confirmation computed without zeroing was accepted")
	}
}

func TestVerifyRejectsMalformedInput(t *testing.T) {
	chi := buildClientHello(makeRandom(0x60))
	sh := serverHelloWithConfirmation(chi, makeRandom(0xf0), LabelECHAcceptConfirmation, AcceptConfirmationLength)

	if VerifyServerHelloAccept(chi[:10], sh) {
		t.Fatal("truncated ClientHelloInner was accepted")
	}
	if VerifyServerHelloAccept(chi, sh[:20]) {
		t.Fatal("truncated ServerHello was accepted")
	}
	if VerifyHelloRetryRequestAccept(make([]byte, 31), sh) {
		t.Fatal("wrong-length ClientHelloInner random was accepted")
	}
	noECH := buildServerHello(makeRandom(0x77), nil)
	if VerifyHelloRetryRequestAccept(makeRandom(0x60), noECH) {
		t.Fatal("HRR without ECH extension was accepted")
	}
}

func TestComputeAcceptConfirmationLength(t *testing.T) {
	rnd := make([]byte, randomLen)
	if _, err := rand.Read(rnd); err != nil {
		t.Fatal(err)
	}
	conf := ComputeAcceptConfirmation(LabelECHAcceptConfirmation, rnd, []byte("transcript"))
	if len(conf) != AcceptConfirmationLength {
		t.Fatalf("confirmation length = %d, want %d", len(conf), AcceptConfirmationLength)
	}
}
