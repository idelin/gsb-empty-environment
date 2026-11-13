package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"testing"
)

func TestQ0243IndependentVector(t *testing.T) {
	psk := []byte("a shared secret provisioned out of band")
	transcript := sha256.Sum256([]byte("the truncated ClientHello"))
	tests := []struct {
		typ  PSKType
		want string
	}{
		{PSKExternal, "3584617d8035a60e33ff6b2c058a9745dfdc3529833bdb50ce1529544a7c37aa"},
		{PSKResumption, "d96c17255827e70821695b0a501687c8336d285f9c2baa261b1beb3f8c5519d4"},
		{PSKImported, "61c3af7dd59fc4133923f48a49ab062f43ace3a26a52b4be17de498b4845e625"},
	}
	for _, tt := range tests {
		want, err := hex.DecodeString(tt.want)
		if err != nil {
			t.Fatal(err)
		}
		got := ComputeBinder(psk, transcript[:], tt.typ, sha256.New)
		if !bytes.Equal(got, want) {
			t.Errorf("%v binder = %x, want %x", tt.typ, got, want)
		}
	}
}

func TestQ0243ExternalAndResumptionBinderKeysAreSeparated(t *testing.T) {
	psk := []byte("a shared secret provisioned out of band")

	ext := q0243MustBinderKey(t, psk, PSKExternal, sha256.New)
	res := q0243MustBinderKey(t, psk, PSKResumption, sha256.New)
	if ext == nil || res == nil {
		t.Fatal("BinderKeyFor returned nil")
	}
	if bytes.Equal(ext, res) {
		t.Fatal("external and resumption PSKs derived the same binder key")
	}
	if !bytes.Equal(ext, q0243ExpectedBinderKey(psk, ExternalBinderLabel, sha256.New)) {
		t.Fatal("external binder key does not match the RFC 8446 derivation")
	}
	if !bytes.Equal(res, q0243ExpectedBinderKey(psk, ResumptionBinderLabel, sha256.New)) {
		t.Fatal("resumption binder key does not match the RFC 8446 derivation")
	}
}

func TestQ0243BinderTypeCannotBeConfused(t *testing.T) {
	psk := []byte("a shared secret provisioned out of band")
	transcript := sha256.Sum256([]byte("the truncated ClientHello"))
	extBinder := ComputeBinder(psk, transcript[:], PSKExternal, sha256.New)
	resBinder := ComputeBinder(psk, transcript[:], PSKResumption, sha256.New)

	if len(extBinder) != sha256.Size || len(resBinder) != sha256.Size {
		t.Fatalf("unexpected binder lengths: %d, %d", len(extBinder), len(resBinder))
	}
	if bytes.Equal(extBinder, resBinder) {
		t.Fatal("the same PSK produced the same binder for external and resumption")
	}
	if !VerifyBinder(psk, transcript[:], extBinder, PSKExternal, sha256.New) {
		t.Fatal("external binder did not verify as external")
	}
	if got := ComputeBinder(psk, transcript, PSKExternal, sha256.New); !bytes.Equal(got, extBinder) {
		t.Fatal("array-valued transcript changed the binder")
	}
	if VerifyBinder(psk, transcript[:], extBinder, PSKResumption, sha256.New) {
		t.Fatal("external binder verified with the resumption derivation")
	}
	if VerifyBinder(psk, transcript[:], resBinder, PSKExternal, sha256.New) {
		t.Fatal("resumption binder verified with the external derivation")
	}

	extKey := q0243MustBinderKey(t, psk, PSKExternal, sha256.New)
	resKey := q0243MustBinderKey(t, psk, PSKResumption, sha256.New)
	if !VerifyBinderFromKey(extKey, transcript[:], extBinder, PSKExternal, sha256.New) {
		t.Fatal("external binder did not verify with its binder key")
	}
	if VerifyBinderFromKey(resKey, transcript[:], extBinder, PSKExternal, sha256.New) {
		t.Fatal("resumption binder key verified an external binder")
	}
}

func TestQ0243AlreadyDerivedBinderKeysUseKeyMode(t *testing.T) {
	psk := []byte("a shared secret provisioned out of band")
	transcript := sha256.Sum256([]byte("the truncated ClientHello"))
	extKey := q0243MustBinderKey(t, psk, PSKExternal, sha256.New)
	resKey := q0243MustBinderKey(t, psk, PSKResumption, sha256.New)

	extBinder := ComputeBinder(extKey, transcript[:], sha256.New)
	want := q0243ExpectedBinderFromKey(extKey, transcript[:], sha256.New)
	if !bytes.Equal(extBinder, want) {
		t.Fatalf("key-mode binder = %x, want %x", extBinder, want)
	}
	if !VerifyBinder(extKey, transcript[:], extBinder, sha256.New) {
		t.Fatal("external binder key did not verify its own binder")
	}
	if VerifyBinder(resKey, transcript[:], extBinder, sha256.New) {
		t.Fatal("resumption binder key verified an external binder")
	}
}

func TestQ0243FullClientHelloBinder(t *testing.T) {
	psk := []byte("external psk")
	identity := []byte("client-identity")
	placeholder := make([]byte, sha256.Size)

	full := q0243BuildClientHello(identity, placeholder)
	truncated, binders, err := TruncateClientHelloBinders(full)
	if err != nil {
		t.Fatal(err)
	}
	if len(binders) != 1 || !bytes.Equal(binders[0], placeholder) {
		t.Fatalf("unexpected binders: %x", binders)
	}
	if bytes.Equal(truncated, full) {
		t.Fatal("truncation did not remove the binders list")
	}

	binder := ComputeBinder(psk, full, PSKExternal, sha256.New)
	want := q0243ExpectedBinder(psk, PSKExternal, sha256.Sum256(truncated), sha256.New)
	if !bytes.Equal(binder, want) {
		t.Fatalf("binder = %x, want %x", binder, want)
	}
	full = q0243BuildClientHello(identity, binder)
	if !VerifyBinder(psk, full, binder, PSKExternal, sha256.New) {
		t.Fatal("external binder for a complete ClientHello did not verify")
	}
	if VerifyBinder(psk, full, binder, PSKResumption, sha256.New) {
		t.Fatal("external ClientHello binder verified as resumption")
	}

	record := []byte{0x16, 0x03, 0x03, byte(len(full) >> 8), byte(len(full))}
	record = append(record, full...)
	if got := ComputeBinder(psk, record, PSKExternal, sha256.New); !bytes.Equal(got, binder) {
		t.Fatal("binder changed when the ClientHello was passed in a TLS record")
	}
}

func TestQ0243SHA384Binder(t *testing.T) {
	psk := []byte("sha384 psk")
	transcript := sha512.Sum384([]byte("transcript"))
	ext := ComputeBinder(psk, transcript[:], PSKExternal, sha512.New384)
	res := ComputeBinder(psk, transcript[:], PSKResumption, sha512.New384)
	if len(ext) != sha512.Size384 || len(res) != sha512.Size384 {
		t.Fatalf("unexpected binder length: %d, %d", len(ext), len(res))
	}
	if !VerifyBinder(psk, transcript[:], ext, PSKExternal, sha512.New384) {
		t.Fatal("SHA-384 external binder did not verify")
	}
	if VerifyBinder(psk, transcript[:], ext, PSKResumption, sha512.New384) {
		t.Fatal("SHA-384 external binder verified as resumption")
	}
}

func TestQ0243EarlySecretMethods(t *testing.T) {
	psk := []byte("early secret psk")
	es := NewEarlySecret(sha256.New, psk)
	if es == nil {
		t.Fatal("NewEarlySecret returned nil")
	}
	if !bytes.Equal(es.ExternalBinderKey(), q0243MustBinderKey(t, psk, PSKExternal, sha256.New)) {
		t.Fatal("ExternalBinderKey mismatch")
	}
	if !bytes.Equal(es.ResumptionBinderKey(), q0243MustBinderKey(t, psk, PSKResumption, sha256.New)) {
		t.Fatal("ResumptionBinderKey mismatch")
	}
	if bytes.Equal(es.ExternalBinderKey(), es.ResumptionBinderKey()) {
		t.Fatal("EarlySecret reused one binder key for both PSK types")
	}
	if !bytes.Equal(es.BinderKey(PSKResumption), es.ResumptionBinderKey()) {
		t.Fatal("BinderKey(PSKResumption) mismatch")
	}
	if !bytes.Equal(es.BinderKey(true), es.ExternalBinderKey()) {
		t.Fatal("bool compatibility path mismatch")
	}
	if es.FinishedKey(PSKExternal) == nil {
		t.Fatal("FinishedKey returned nil")
	}
}

func TestQ0243InvalidInputsAndErrors(t *testing.T) {
	psk := []byte("psk")
	bad := PSKType(99)
	if bad.Valid() {
		t.Fatal("invalid PSK type reported valid")
	}
	if got := DeriveBinderKey(psk, bad, sha256.New); got != nil {
		t.Fatalf("invalid PSK type derived %x", got)
	}
	if _, err := DeriveBinderKeyChecked(psk, bad, sha256.New); !errors.Is(err, ErrInvalidPSKType) {
		t.Fatalf("DeriveBinderKeyChecked error = %v", err)
	}
	transcript := sha256.Sum256([]byte("transcript"))
	binder := ComputeBinder(psk, transcript[:], PSKExternal, sha256.New)
	if err := VerifyBinderE(psk, transcript[:], binder, PSKResumption, sha256.New); !errors.Is(err, ErrBinderMismatch) {
		t.Fatalf("VerifyBinderE error = %v, want ErrBinderMismatch", err)
	}
	if VerifyBinder(psk, transcript[:], binder[:len(binder)-1], PSKExternal, sha256.New) {
		t.Fatal("truncated binder verified")
	}
}

func q0243BuildClientHello(identity, binder []byte) []byte {
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0) // session_id
	body = append(body, 0, 2, 0x13, 0x01)
	body = append(body, 1, 0) // compression methods

	var exts []byte
	exts = append(exts, 0x00, 0x2b, 0, 3, 2, 0x03, 0x04) // supported_versions

	var identities []byte
	identities = append(identities, byte(len(identity)>>8), byte(len(identity)))
	identities = append(identities, identity...)
	identities = append(identities, 0, 0, 0, 0) // obfuscated_ticket_age

	pskData := []byte{byte(len(identities) >> 8), byte(len(identities))}
	pskData = append(pskData, identities...)
	var binders []byte
	binders = append(binders, byte(len(binder)))
	binders = append(binders, binder...)
	pskData = append(pskData, byte(len(binders)>>8), byte(len(binders)))
	pskData = append(pskData, binders...)

	exts = append(exts, 0, extensionPreSharedKey, byte(len(pskData)>>8), byte(len(pskData)))
	exts = append(exts, pskData...)
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)

	msg := []byte{handshakeTypeClientHello, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(msg, body...)
}

func q0243ExpectedBinderKey(psk []byte, label string, h func() hash.Hash) []byte {
	early := testHKDFExtract(h, nil, psk)
	return testDeriveSecret(h, early, label, h().Sum(nil))
}

func q0243MustBinderKey(t *testing.T, psk []byte, typ PSKType, h func() hash.Hash) []byte {
	t.Helper()
	key, err := BinderKeyFor(psk, typ, h)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func q0243ExpectedBinder(psk []byte, typ PSKType, transcriptHash [32]byte, h func() hash.Hash) []byte {
	return q0243ExpectedBinderWithHash(psk, typ, transcriptHash[:], h)
}

func q0243ExpectedBinderWithHash(psk []byte, typ PSKType, transcriptHash []byte, h func() hash.Hash) []byte {
	key := q0243ExpectedBinderKey(psk, typ.Label(), h)
	return q0243ExpectedBinderFromKey(key, transcriptHash, h)
}

func q0243ExpectedBinderFromKey(key, transcriptHash []byte, h func() hash.Hash) []byte {
	finished := testExpandLabel(h, key, "finished", nil, h().Size())
	mac := hmac.New(h, finished)
	_, _ = mac.Write(transcriptHash)
	return mac.Sum(nil)
}

func testHKDFExtract(h func() hash.Hash, salt, ikm []byte) []byte {
	if salt == nil {
		salt = make([]byte, h().Size())
	}
	mac := hmac.New(h, salt)
	_, _ = mac.Write(ikm)
	return mac.Sum(nil)
}

func testExpandLabel(h func() hash.Hash, secret []byte, label string, context []byte, length int) []byte {
	fullLabel := "tls13 " + label
	info := make([]byte, 0, 4+len(fullLabel)+len(context))
	info = append(info, byte(length>>8), byte(length), byte(len(fullLabel)))
	info = append(info, fullLabel...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	return testHKDFExpand(h, secret, info, length)
}

func testDeriveSecret(h func() hash.Hash, secret []byte, label string, transcriptHash []byte) []byte {
	return testExpandLabel(h, secret, label, transcriptHash, h().Size())
}

func testHKDFExpand(h func() hash.Hash, prk, info []byte, length int) []byte {
	var out, t []byte
	for i := byte(1); len(out) < length; i++ {
		mac := hmac.New(h, prk)
		_, _ = mac.Write(t)
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{i})
		t = mac.Sum(nil)
		out = append(out, t...)
	}
	return out[:length]
}

func TestQ0243ClientHelloTruncationMatchesManualPrefix(t *testing.T) {
	full := q0243BuildClientHello([]byte("id"), make([]byte, sha256.Size))
	truncated, _, err := TruncateClientHelloBinders(full)
	if err != nil {
		t.Fatal(err)
	}
	// The removed portion is the two-byte binders length, the one-byte binder
	// length, and the binder itself.
	wantPrefix := full[:len(full)-(2+1+sha256.Size)]
	if !bytes.Equal(truncated, wantPrefix) {
		t.Fatal("truncated ClientHello does not end before the binders length")
	}
	if len(truncated) != len(full)-2-1-sha256.Size {
		t.Fatalf("unexpected truncated length %d", len(truncated))
	}
}

func TestQ0243Uint16Helpers(t *testing.T) {
	// Keep the test independent of unexported helpers while exercising the
	// extension length format used above.
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], 0x1234)
	if b != [2]byte{0x12, 0x34} {
		t.Fatal("unexpected network byte order")
	}
}
