package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

type testExtension struct {
	typ  uint16
	data []byte
}

func buildClientHello(t testing.TB, extensions []testExtension, identities []string, binderLen int, record bool) []byte {
	t.Helper()

	body := make([]byte, 0, 256)
	body = appendU16(body, 0x0303)
	body = append(body, bytes.Repeat([]byte{0x42}, 32)...)
	body = append(body, 0) // empty legacy_session_id
	body = appendU16(body, 2)
	body = appendU16(body, 0x1301) // TLS_AES_128_GCM_SHA256
	body = append(body, 1, 0)      // null compression

	var exts []byte
	for _, ext := range extensions {
		exts = appendU16(exts, int(ext.typ))
		exts = appendU16(exts, len(ext.data))
		exts = append(exts, ext.data...)
	}

	var ids []byte
	for i, identity := range identities {
		ids = appendU16(ids, len(identity))
		ids = append(ids, identity...)
		ids = appendU32(ids, uint32(i+1))
	}
	var binders []byte
	for range identities {
		binders = append(binders, byte(binderLen))
		binders = append(binders, make([]byte, binderLen)...)
	}
	psk := appendU16(nil, len(ids))
	psk = append(psk, ids...)
	psk = appendU16(psk, len(binders))
	psk = append(psk, binders...)
	exts = appendU16(exts, extensionPreSharedKey)
	exts = appendU16(exts, len(psk))
	exts = append(exts, psk...)

	body = appendU16(body, len(exts))
	body = append(body, exts...)

	hello := []byte{handshakeTypeClientHello}
	hello = appendU24(hello, len(body))
	hello = append(hello, body...)
	if !record {
		return hello
	}
	out := []byte{recordTypeHandshake, 0x03, 0x01}
	out = appendU16(out, len(hello))
	return append(out, hello...)
}

func standardExtensions() []testExtension {
	return []testExtension{
		{typ: 0x0000, data: []byte{0, 4, 't', 'e', 's', 't'}}, // server_name
		{typ: 0x002b, data: []byte{2, 0x03, 0x04}},            // supported_versions
		{typ: 0x000d, data: []byte{0, 2, 0x04, 0x03}},         // signature_algorithms
		{typ: 0x0033, data: []byte{0, 4, 0, 29, 0, 1}},        // key_share
	}
}

func appendU16(dst []byte, v int) []byte {
	return append(dst, byte(v>>8), byte(v))
}

func appendU24(dst []byte, v int) []byte {
	return append(dst, byte(v>>16), byte(v>>8), byte(v))
}

func appendU32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

func TestBinderCoversCompleteClientHelloPrefix(t *testing.T) {
	hello := buildClientHello(t, standardExtensions(), []string{"ticket-one"}, sha256.Size, false)
	filled, err := FillClientHelloBinders(hello)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filled); err != nil {
		t.Fatalf("complete-prefix binder was rejected: %v", err)
	}

	ch, err := ParseClientHello(filled)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(filled[ch.MessageOffset:ch.Binders[0].Offset])
	if !bytes.Equal(ch.Binders[0].Value, want[:]) {
		t.Fatalf("binder does not equal hash of complete prefix")
	}

	standard, err := FillClientHelloBinders(hello, StandardCoverage)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(standard, StandardCoverage); err != nil {
		t.Fatalf("RFC 8446 binder was rejected: %v", err)
	}
	standardCH, err := ParseClientHello(standard)
	if err != nil {
		t.Fatal(err)
	}
	standardWant := sha256.Sum256(standard[standardCH.MessageOffset:standardCH.IdentitiesEnd])
	if !bytes.Equal(standardCH.Binders[0].Value, standardWant[:]) {
		t.Fatalf("standard binder does not equal hash through identities")
	}
}

func TestBinderTruncatedBeforeExtensionFails(t *testing.T) {
	hello := buildClientHello(t, standardExtensions(), []string{"ticket-one"}, sha256.Size, false)
	ch, err := ParseClientHello(hello)
	if err != nil {
		t.Fatal(err)
	}
	// Hash everything through the identities vector except one authenticated
	// extension.
	var version Extension
	for _, ext := range ch.Extensions {
		if ext.Type == 0x002b {
			version = ext
			break
		}
	}
	shortPrefix := append([]byte(nil), hello[ch.MessageOffset:version.Offset]...)
	shortPrefix = append(shortPrefix, hello[version.DataOffset+len(version.Data):ch.IdentitiesEnd]...)
	short := sha256.Sum256(shortPrefix)
	binder := ch.Binders[0]
	copy(hello[binder.Offset:binder.Offset+len(binder.Value)], short[:])
	if err := VerifyClientHelloBinders(hello); !errors.Is(err, ErrBinderMismatch) {
		t.Fatalf("binder over a transcript missing an extension was accepted: %v", err)
	}
}

func TestRemovingExtensionInvalidatesBinder(t *testing.T) {
	hello := buildClientHello(t, standardExtensions(), []string{"ticket-one"}, sha256.Size, false)
	filled, err := FillClientHelloBinders(hello)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filled); err != nil {
		t.Fatal(err)
	}

	withoutExtension, err := RemoveExtension(filled, 0x002b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseClientHello(withoutExtension); err != nil {
		t.Fatalf("removed extension produced malformed ClientHello: %v", err)
	}
	if err := VerifyClientHelloBinders(withoutExtension); !errors.Is(err, ErrBinderMismatch) {
		t.Fatalf("binder still validated after removing an authenticated extension: %v", err)
	}

	// Covering the new complete prefix must work.
	refilled, err := FillClientHelloBinders(withoutExtension)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(refilled); err != nil {
		t.Fatalf("binder covering the new complete prefix was rejected: %v", err)
	}
}

func TestRemoveEachPrecedingExtension(t *testing.T) {
	exts := standardExtensions()
	hello, err := FillClientHelloBinders(buildClientHello(t, exts, []string{"ticket-one"}, sha256.Size, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range exts {
		without, err := RemoveExtension(hello, ext.typ)
		if err != nil {
			t.Fatalf("remove extension %d: %v", ext.typ, err)
		}
		if err := VerifyClientHelloBinders(without); !errors.Is(err, ErrBinderMismatch) {
			t.Fatalf("extension %d: binder still validated: %v", ext.typ, err)
		}
		refilled, err := FillClientHelloBinders(without)
		if err != nil {
			t.Fatalf("refill after removing extension %d: %v", ext.typ, err)
		}
		if err := VerifyClientHelloBinders(refilled); err != nil {
			t.Fatalf("refilled binder for extension %d rejected: %v", ext.typ, err)
		}
	}
}

func TestRemovingEarlierIdentityInvalidatesBinder(t *testing.T) {
	hello := buildClientHello(t, standardExtensions(), []string{"ticket-one", "ticket-two"}, sha256.Size, false)
	filled, err := FillClientHelloBinders(hello)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filled); err != nil {
		t.Fatal(err)
	}

	withoutFirst, err := RemoveIdentity(filled, 0)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := ParseClientHello(withoutFirst)
	if err != nil {
		t.Fatalf("removing an identity produced malformed ClientHello: %v", err)
	}
	if len(ch.Identities) != 1 || string(ch.Identities[0].Identity) != "ticket-two" {
		t.Fatalf("unexpected identities after removal: %#v", ch.Identities)
	}
	if err := VerifyClientHelloBinders(withoutFirst); !errors.Is(err, ErrBinderMismatch) {
		t.Fatalf("binder ignored a removed preceding identity: %v", err)
	}
}

func TestPositionalAndStandardCoverage(t *testing.T) {
	hello := buildClientHello(t, standardExtensions(), []string{"ticket-one", "ticket-two"}, sha256.Size, false)
	ch, err := ParseClientHello(hello)
	if err != nil {
		t.Fatal(err)
	}
	for _, binder := range ch.Binders {
		sum := sha256.Sum256(hello[ch.MessageOffset:binder.Offset])
		copy(hello[binder.Offset:binder.Offset+len(binder.Value)], sum[:])
	}
	if err := VerifyClientHelloBinders(hello, PositionalCoverage); err != nil {
		t.Fatalf("positional binders rejected: %v", err)
	}
	if err := VerifyClientHelloBinders(hello); err != nil {
		t.Fatalf("default verifier rejected positional binders: %v", err)
	}

	filled, err := FillPositionalBinders(buildClientHello(t, standardExtensions(), []string{"ticket-one", "ticket-two"}, sha256.Size, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filled, PositionalCoverage); err != nil {
		t.Fatalf("sequentially filled positional binders rejected: %v", err)
	}

	computed, err := ComputeClientHelloBinders(buildClientHello(t, standardExtensions(), []string{"ticket-one", "ticket-two"}, sha256.Size, false))
	if err != nil {
		t.Fatal(err)
	}
	computedCH, err := ParseClientHello(filled)
	if err != nil {
		t.Fatal(err)
	}
	for i := range computed {
		if !bytes.Equal(computed[i], computedCH.Binders[i].Value) {
			t.Fatalf("computed binder %d differs from sequential fill", i)
		}
	}
}

func TestHeaderlessAndRecordWrappedClientHello(t *testing.T) {
	hello := buildClientHello(t, standardExtensions(), []string{"ticket-one"}, sha256.Size, false)
	filled, err := FillClientHelloBinders(hello)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filled); err != nil {
		t.Fatal(err)
	}

	bodyOnly := filled[4:]
	filledBody, err := FillClientHelloBinders(bodyOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filledBody); err != nil {
		t.Fatalf("headerless ClientHello rejected: %v", err)
	}

	recordHello := buildClientHello(t, standardExtensions(), []string{"ticket-one"}, sha256.Size, true)
	filledRecord, err := FillClientHelloBinders(recordHello)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filledRecord); err != nil {
		t.Fatalf("record-wrapped ClientHello rejected: %v", err)
	}
}

func TestBinderKeyMode(t *testing.T) {
	prefix := []byte("complete ClientHello prefix")
	key := []byte("derived binder key")
	binder := ComputeBinder(prefix, BinderKey(key), sha256.New)
	if !VerifyBinder(prefix, binder, key, sha256.New) {
		t.Fatal("HMAC binder was rejected")
	}
	if VerifyBinder(append(prefix, 0), binder, key, sha256.New) {
		t.Fatal("HMAC binder validated against a different transcript")
	}

	hello := buildClientHello(t, standardExtensions(), []string{"ticket-one"}, sha256.Size, false)
	filled, err := FillClientHelloBinders(hello, BinderKey(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(filled, BinderKey(key)); err != nil {
		t.Fatalf("keyed ClientHello binder rejected: %v", err)
	}
	ch, err := ParseClientHello(filled)
	if err != nil {
		t.Fatal(err)
	}
	want := ComputeBinder(filled[ch.MessageOffset:ch.IdentitiesEnd], BinderKey(key))
	if !bytes.Equal(ch.Binders[0].Value, want) {
		t.Fatal("keyed binder did not default to the RFC 8446 prefix")
	}
}

func TestLowLevelPrefixCoverage(t *testing.T) {
	prefix := []byte{1, 0, 0, 20, 3, 3, 'e', 'x', 't', '-', 'a', 'n', 'd', '-', 'i', 'd'}
	binder := ComputeBinder(prefix)
	if !VerifyBinder(prefix, binder) {
		t.Fatal("complete transcript prefix was rejected")
	}
	if VerifyBinder(prefix[:len(prefix)-1], binder) {
		t.Fatal("binder validated after dropping the final byte of the transcript")
	}
}

func TestPSKExtensionMustBeLast(t *testing.T) {
	// Put a normal extension after pre_shared_key. A prefix ending at the PSK
	// identities would silently omit that extension.
	ch, err := ParseClientHello(buildClientHelloWithPSKFirstThenExtension(t, standardExtensions()))
	if err == nil || ch != nil {
		t.Fatalf("expected malformed ClientHello for non-final PSK, got %#v, %v", ch, err)
	}
	if !errors.Is(err, ErrMalformedClientHello) {
		t.Fatalf("unexpected error type: %v", err)
	}
}

func TestMissingPreSharedKeyIsRejected(t *testing.T) {
	hello, err := FillClientHelloBinders(buildClientHello(t, standardExtensions(), []string{"ticket-one"}, sha256.Size, false))
	if err != nil {
		t.Fatal(err)
	}
	withoutPSK, err := RemoveExtension(hello, ExtensionPreSharedKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClientHelloBinders(withoutPSK); !errors.Is(err, ErrNoPreSharedKey) {
		t.Fatalf("expected ErrNoPreSharedKey, got %v", err)
	}
}

func FuzzParseClientHello(f *testing.F) {
	f.Add(buildClientHello(f, standardExtensions(), []string{"ticket-one"}, sha256.Size, false))
	f.Add([]byte{1, 0, 0, 0})
	f.Add([]byte{0x16, 3, 1, 0, 4, 1, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		ch, err := ParseClientHello(data)
		if err != nil {
			return
		}
		_ = ch.BinderPrefix()
		for i := range ch.Binders {
			_ = ch.PositionalPrefix(i)
			_ = ch.Binders[i].Value
		}
		_ = ch.Verify()
	})
}

func buildClientHelloWithPSKFirstThenExtension(t testing.TB, exts []testExtension) []byte {
	t.Helper()
	// buildClientHello always appends PSK after the supplied extensions. To get
	// an extension after PSK, splice the extension into the PSK's position.
	hello := buildClientHello(t, exts, []string{"ticket-one"}, sha256.Size, false)
	ch, err := ParseClientHello(hello)
	if err != nil {
		t.Fatal(err)
	}
	psk := ch.PSKExtension
	extra := appendU16(nil, 0x000a)
	extra = appendU16(extra, 2)
	extra = append(extra, 0, 1)
	out := make([]byte, 0, len(hello)+len(extra))
	out = append(out, hello[:psk.Offset+4+len(psk.Data)]...)
	out = append(out, extra...)
	out = append(out, hello[psk.Offset+4+len(psk.Data):]...)
	// Fix the handshake and extension vector lengths.
	decrementUint24(out, ch.MessageOffset+1, -len(extra))
	decrementUint16(out, ch.Extensions[0].Offset-2, -len(extra))
	return out
}
