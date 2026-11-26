package tls13

import (
	"bytes"
	"testing"
)

func TestHRRMessageHashEncoding(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	got, err := SynthesizeMessageHash(cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "message_hash(empty)", got, `
fe 00 00 20
e3 b0 c4 42 98 fc 1c 14 9a fb f4 c8 99 6f b9 24
27 ae 41 e4 64 9b 93 4c a4 95 99 1b 78 52 b8 55`)

	cs384 := TLS_AES_256_GCM_SHA384
	got384, err := SynthesizeMessageHash(cs384, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got384[0] != 0xfe || got384[3] != 48 || len(got384) != 52 {
		t.Fatalf("SHA-384 message_hash framing wrong: %x", got384[:4])
	}
}

func TestHRRTranscriptReplacesClientHello1(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	ch1 := []byte("client-hello-one")
	hrr := []byte("hello-retry-request")
	ch2 := []byte("client-hello-two")

	rewritten, err := NewHRRTranscript(cs, ch1, hrr)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewritten.WriteMessages(ch2); err != nil {
		t.Fatal(err)
	}

	messageHash, err := SynthesizeMessageHash(cs, ch1)
	if err != nil {
		t.Fatal(err)
	}
	expected := NewTranscript(cs)
	_ = expected.WriteMessages(messageHash, hrr, ch2)
	if !bytes.Equal(rewritten.Sum(), expected.Sum()) {
		t.Fatal("HRR transcript does not match message_hash || HRR || ClientHello2")
	}

	direct := NewTranscript(cs)
	_ = direct.WriteMessages(ch1, hrr, ch2)
	if bytes.Equal(rewritten.Sum(), direct.Sum()) {
		t.Fatal("rewritten HRR transcript unexpectedly equals direct concatenation")
	}

	secret := make([]byte, cs.HashSize())
	right := DeriveSecret(cs, secret, "c hs traffic", rewritten.Sum())
	wrong := DeriveSecret(cs, secret, "c hs traffic", direct.Sum())
	if bytes.Equal(right, wrong) {
		t.Fatal("HRR rewrite did not change derived traffic secret")
	}
}

func TestPSKBinderKeyAndVerifyData(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	psk := bytes.Repeat([]byte{0x42}, cs.HashSize())
	early := NewEarlySecret(cs, psk)
	truncatedCHHash := cs.NewHash().Sum(nil)

	resumptionKey, err := early.BinderKey(truncatedCHHash, ResumptionBinder)
	if err != nil {
		t.Fatal(err)
	}
	externalKey, err := early.BinderKey(truncatedCHHash, ExternalBinder)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(resumptionKey, externalKey) {
		t.Fatal("resumption and external binder keys must differ")
	}

	binder := FinishedVerifyData(cs, resumptionKey, truncatedCHHash)
	finishedKey := FinishedKey(cs, resumptionKey)
	expected := FinishedMAC(cs, finishedKey, truncatedCHHash)
	if !bytes.Equal(binder, expected) {
		t.Fatal("binder HMAC mismatch")
	}
	if !VerifyFinished(cs, resumptionKey, truncatedCHHash, binder) {
		t.Fatal("VerifyFinished rejected valid binder")
	}
	if VerifyFinished(cs, resumptionKey, truncatedCHHash, bytes.Repeat([]byte{0xff}, cs.HashSize())) {
		t.Fatal("VerifyFinished accepted invalid binder")
	}
}

func TestAES256SHA384RecordRoundTrip(t *testing.T) {
	cs := TLS_AES_256_GCM_SHA384
	secret := make([]byte, cs.HashSize())
	sender, err := NewRecordState(cs, secret)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := NewRecordState(cs, secret)
	if err != nil {
		t.Fatal(err)
	}
	record, err := sender.Seal(ContentTypeApplicationData, []byte("sha384 and aes256"), &SealOptions{PaddingLength: 31})
	if err != nil {
		t.Fatal(err)
	}
	typ, plain, err := receiver.Open(record)
	if err != nil {
		t.Fatal(err)
	}
	if typ != ContentTypeApplicationData || string(plain) != "sha384 and aes256" {
		t.Fatalf("unexpected record: type=%d plain=%q", typ, plain)
	}
}
