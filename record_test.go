package tls13

import (
	"bytes"
	"errors"
	"testing"
)

func TestNonceXORAndSequence(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	secret := mustHex(t, "b6 7b 7d 69 0c c1 6c 4e 75 e5 42 13 cb 2d 37 b4 e9 c9 12 bc de d9 10 5d 42 be fd 59 d3 91 ad 38")
	state, err := NewRecordState(cs, secret)
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "server hs key", state.Keys().Key, "3f ce 51 60 09 c2 17 27 d0 f2 e4 e8 6e e4 03 bc")
	assertHex(t, "server hs iv", state.Keys().IV, "5d 31 3e b2 67 12 76 ee 13 00 0b 30")

	nonce0, err := state.Nonce(0)
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "nonce seq 0", nonce0, "5d 31 3e b2 67 12 76 ee 13 00 0b 30")

	nonce1, err := state.Nonce(0x0102030405060708)
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "nonce seq pattern", nonce1, "5d 31 3e b2 66 10 75 ea 16 06 0c 38")
}

func TestSealMatchesIndependentAESGCMVector(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	secret := mustHex(t, "b6 7b 7d 69 0c c1 6c 4e 75 e5 42 13 cb 2d 37 b4 e9 c9 12 bc de d9 10 5d 42 be fd 59 d3 91 ad 38")
	state := mustNewRecordState(cs, secret)
	record, err := state.Seal(ContentTypeApplicationData, []byte("hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "AES-GCM record", record, `
17 03 03 00 16
b1 9a 5f 02 39 c0 7b 54 82 06 b8 3e fc 2e 70 26
bf cc 3e bb 7b ac`)
}

func TestRecordSealOpenArbitraryPadding(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	secret := mustHex(t, "9e 49 d4 49 d6 06 32 32 33 b9 65 82 a2 78 ef 7c 97 93 da 85 6d 47 37 f5 76 8a af bc c4 32 97 77")
	sender, err := NewRecordState(cs, secret)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := NewRecordState(cs, secret)
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("RFC 8446 protected application data")
	for _, padding := range []int{0, 1, 15, 1024} {
		record, err := sender.Seal(ContentTypeApplicationData, payload, &SealOptions{PaddingLength: padding})
		if err != nil {
			t.Fatalf("seal padding %d: %v", padding, err)
		}
		typ, got, err := receiver.Open(record)
		if err != nil {
			t.Fatalf("open padding %d: %v", padding, err)
		}
		if typ != ContentTypeApplicationData || !bytes.Equal(got, payload) {
			t.Fatalf("padding %d returned type=%d payload=%q", padding, typ, got)
		}
	}

	if sender.Sequence() != 4 || receiver.Sequence() != 4 {
		t.Fatalf("sequences = %d/%d", sender.Sequence(), receiver.Sequence())
	}
}

func TestTamperedCiphertextRejectedAndSequenceNotConsumed(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	secret := make([]byte, cs.HashSize())
	sender := mustNewRecordState(cs, secret)
	receiver := mustNewRecordState(cs, secret)

	record, err := sender.Seal(ContentTypeHandshake, []byte("finished"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), record...)
	tampered[len(tampered)-1] ^= 0x01
	if _, _, err := receiver.Open(tampered); !errors.Is(err, ErrDecryption) {
		t.Fatalf("tampered open error = %v, want ErrDecryption", err)
	}
	if receiver.Sequence() != 0 {
		t.Fatalf("failed decryption consumed sequence: %d", receiver.Sequence())
	}

	typ, plain, err := receiver.Open(record)
	if err != nil {
		t.Fatalf("original record after tamper: %v", err)
	}
	if typ != ContentTypeHandshake || string(plain) != "finished" {
		t.Fatalf("unexpected open result %d %q", typ, plain)
	}
}

func TestInvalidInnerPaddingRejected(t *testing.T) {
	if _, _, err := RemoveInnerPadding([]byte{0, 0, 0}); !errors.Is(err, ErrInvalidPadding) {
		t.Fatalf("all-zero padding error = %v", err)
	}
	if _, _, err := RemoveInnerPadding([]byte("data")); err == nil {
		t.Fatal("missing content type accepted")
	}

	padded := append([]byte("payload"), ContentTypeHandshake)
	padded = append(padded, 0, 0, 0, 0)
	plain, typ, err := RemoveInnerPadding(padded)
	if err != nil || typ != ContentTypeHandshake || string(plain) != "payload" {
		t.Fatalf("valid zero padding: plain=%q type=%d err=%v", plain, typ, err)
	}

	nonZeroPadding := append([]byte("payload"), ContentTypeHandshake, 0x01, 0x00)
	if _, _, err := RemoveInnerPadding(nonZeroPadding); !errors.Is(err, ErrInvalidPadding) {
		t.Fatalf("non-zero padding error = %v, want ErrInvalidPadding", err)
	}

	cs := TLS_AES_128_GCM_SHA256
	secret := make([]byte, cs.HashSize())
	receiver := mustNewRecordState(cs, secret)
	nonce, err := receiver.CurrentNonce()
	if err != nil {
		t.Fatal(err)
	}
	header := []byte{ContentTypeApplicationData, 3, 3, 0, 0}
	invalidInner := []byte("payload")
	header[3] = byte((len(invalidInner) + cs.TagLen()) >> 8)
	header[4] = byte(len(invalidInner) + cs.TagLen())
	record := receiver.aead.Seal(header, nonce, invalidInner, header)
	if _, _, err := receiver.Open(record); !errors.Is(err, ErrInvalidPadding) {
		t.Fatalf("authenticated malformed padding error = %v, want ErrInvalidPadding", err)
	}
}

func TestSequenceOverflowRejected(t *testing.T) {
	state := mustNewRecordState(TLS_AES_128_GCM_SHA256, make([]byte, 32))
	state.SetSequence(^uint64(0) - 1)
	if _, err := state.Seal(ContentTypeApplicationData, []byte("last allowed"), nil); err != nil {
		t.Fatalf("seq max-1 seal: %v", err)
	}
	if state.Sequence() != ^uint64(0) {
		t.Fatalf("sequence after max-1 = %x", state.Sequence())
	}
	if _, err := state.Seal(ContentTypeApplicationData, []byte("overflow"), nil); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("max sequence seal error = %v, want ErrSequenceExhausted", err)
	}
	if state.Sequence() != ^uint64(0) {
		t.Fatalf("failed seal changed sequence to %x", state.Sequence())
	}
}

func TestBidirectionalIndependentKeyUpdate(t *testing.T) {
	cs := TLS_AES_128_GCM_SHA256
	clientSecret := bytes.Repeat([]byte("c"), cs.HashSize())
	serverSecret := bytes.Repeat([]byte("s"), cs.HashSize())

	client, err := NewRecordProtector(cs, clientSecret, serverSecret, RoleClient)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewRecordProtector(cs, clientSecret, serverSecret, RoleServer)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := client.InitiateKeyUpdate(KeyUpdateRequested)
	if err != nil {
		t.Fatal(err)
	}
	assertHex(t, "key update message", msg, "18 00 00 01 01")

	sealed, err := client.Seal(ContentTypeHandshake, msg, nil)
	if err != nil {
		t.Fatal(err)
	}
	typ, plain, err := server.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if typ != ContentTypeHandshake || !bytes.Equal(plain, msg) {
		t.Fatalf("key update record = type %d payload %x", typ, plain)
	}
	response, respond, err := server.HandleInboundKeyUpdate(plain)
	if err != nil || !respond {
		t.Fatalf("handle key update response=%q respond=%v err=%v", response, respond, err)
	}

	if server.Inbound().KeyEpoch() != 1 || server.Inbound().Sequence() != 0 {
		t.Fatalf("server inbound epoch/seq = %d/%d", server.Inbound().KeyEpoch(), server.Inbound().Sequence())
	}
	if server.Outbound().KeyEpoch() != 0 {
		t.Fatal("inbound KeyUpdate changed outbound keys")
	}
	if client.Outbound().KeyEpoch() != 0 {
		t.Fatal("building KeyUpdate changed outbound keys before commit")
	}
	if err := client.CommitOutboundKeyUpdate(); err != nil {
		t.Fatal(err)
	}
	if client.Outbound().KeyEpoch() != 1 || client.Outbound().Sequence() != 0 {
		t.Fatalf("client outbound epoch/seq = %d/%d", client.Outbound().KeyEpoch(), client.Outbound().Sequence())
	}
	if client.Inbound().KeyEpoch() != 0 {
		t.Fatal("outbound commit changed inbound keys")
	}

	responseRecord, err := server.Seal(ContentTypeHandshake, response, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.CommitOutboundKeyUpdate(); err != nil {
		t.Fatal(err)
	}
	responseType, responsePlain, err := client.Open(responseRecord)
	if err != nil {
		t.Fatalf("client open KeyUpdate response: %v", err)
	}
	if responseType != ContentTypeHandshake || !bytes.Equal(responsePlain, response) {
		t.Fatalf("bad KeyUpdate response: %d %x", responseType, responsePlain)
	}
	responseBytes, wantsResponse, err := client.HandleInboundKeyUpdate(responsePlain)
	if err != nil || wantsResponse || responseBytes != nil {
		t.Fatalf("client handle response bytes=%x respond=%v err=%v", responseBytes, wantsResponse, err)
	}
	if client.Inbound().KeyEpoch() != 1 || client.Inbound().Sequence() != 0 {
		t.Fatalf("client inbound epoch/seq = %d/%d", client.Inbound().KeyEpoch(), client.Inbound().Sequence())
	}
	if server.Outbound().KeyEpoch() != 1 || server.Outbound().Sequence() != 0 {
		t.Fatalf("server outbound epoch/seq = %d/%d", server.Outbound().KeyEpoch(), server.Outbound().Sequence())
	}

	for i, message := range [][]byte{[]byte("after update client->server"), []byte("after update server->client")} {
		var record []byte
		var typ byte
		var plain []byte
		if i == 0 {
			record, err = client.Seal(ContentTypeApplicationData, message, nil)
			if err != nil {
				t.Fatal(err)
			}
			typ, plain, err = server.Open(record)
		} else {
			record, err = server.Seal(ContentTypeApplicationData, message, nil)
			if err != nil {
				t.Fatal(err)
			}
			typ, plain, err = client.Open(record)
		}
		if err != nil || typ != ContentTypeApplicationData || !bytes.Equal(plain, message) {
			t.Fatalf("post-update record %d failed: type=%d plain=%q err=%v", i, typ, plain, err)
		}
	}
}
