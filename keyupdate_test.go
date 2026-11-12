package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func testPair(t *testing.T, serverConfig Config) (*KeyUpdateState, *KeyUpdateState) {
	t.Helper()
	clientToServer := sha256.Sum256([]byte("test client -> server traffic secret"))
	serverToClient := sha256.Sum256([]byte("test server -> client traffic secret"))

	serverConfig.ReadSecret = clientToServer[:]
	serverConfig.WriteSecret = serverToClient[:]
	if serverConfig.MaxUpdates == 0 {
		serverConfig.MaxUpdates = 100
	}
	if serverConfig.UpdateWindow == 0 {
		serverConfig.UpdateWindow = time.Minute
	}
	server, err := NewKeyUpdateState(serverConfig)
	if err != nil {
		t.Fatalf("NewKeyUpdateState(server): %v", err)
	}
	peer, err := NewKeyUpdateState(Config{
		ReadSecret:  serverToClient[:],
		WriteSecret: clientToServer[:],
		MaxUpdates:  100,
	})
	if err != nil {
		t.Fatalf("NewKeyUpdateState(peer): %v", err)
	}
	return server, peer
}

func mustSendKeyUpdate(t *testing.T, s *KeyUpdateState, request KeyUpdateRequest) []byte {
	t.Helper()
	record, err := s.SendKeyUpdate(request)
	if err != nil {
		t.Fatalf("SendKeyUpdate(%d): %v", request, err)
	}
	return record
}

func TestHKDFExpandRFC5869(t *testing.T) {
	prk, err := hex.DecodeString("077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5")
	if err != nil {
		t.Fatal(err)
	}
	info, err := hex.DecodeString("f0f1f2f3f4f5f6f7f8f9")
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString("3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")
	if err != nil {
		t.Fatal(err)
	}

	got, err := HKDFExpand(sha256.New, prk, info, 42)
	if err != nil {
		t.Fatalf("HKDFExpand: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("HKDFExpand = %x, want %x", got, want)
	}
}

func TestMarshalParseKeyUpdate(t *testing.T) {
	for _, request := range []KeyUpdateRequest{KeyUpdateNotRequested, KeyUpdateRequested} {
		message, err := MarshalKeyUpdate(request)
		if err != nil {
			t.Fatalf("MarshalKeyUpdate(%d): %v", request, err)
		}
		got, err := ParseKeyUpdate(message)
		if err != nil {
			t.Fatalf("ParseKeyUpdate(%x): %v", message, err)
		}
		if got != request {
			t.Fatalf("round trip = %d, want %d", got, request)
		}
	}
	if _, err := MarshalKeyUpdate(KeyUpdateRequest(2)); err == nil {
		t.Fatal("MarshalKeyUpdate accepted an invalid request value")
	}
	if _, err := ParseKeyUpdate([]byte{KeyUpdateMessageType, 0, 0, 1}); err == nil {
		t.Fatal("ParseKeyUpdate accepted a truncated message")
	}
}

func TestNextTrafficSecretUsesTrafficUpdLabelAndHashLength(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, 32)
	next, err := NextTrafficSecret(sha256.New, secret)
	if err != nil {
		t.Fatalf("NextTrafficSecret: %v", err)
	}
	if len(next) != 32 {
		t.Fatalf("next secret length = %d, want 32", len(next))
	}
	if bytes.Equal(next, secret) {
		t.Fatal("next secret equals old secret")
	}

	// Independently build HKDF-Expand-Label's info encoding:
	// length(2) || len(label)(1) || "tls13 traffic upd" || len(context)(1).
	info := append([]byte{0, 32, 17}, []byte("tls13 traffic upd")...)
	info = append(info, 0)
	want, err := HKDFExpand(sha256.New, secret, info, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(next, want) {
		t.Fatalf("NextTrafficSecret = %x, want %x", next, want)
	}

	secret384 := bytes.Repeat([]byte{0x24}, 48)
	next384, err := NextTrafficSecret(sha512.New384, secret384)
	if err != nil {
		t.Fatalf("NextTrafficSecret(SHA-384): %v", err)
	}
	if len(next384) != 48 {
		t.Fatalf("SHA-384 next secret length = %d, want 48", len(next384))
	}
}

func TestUpdateRequestedProducesExactlyOneResponseAndRotatesBothKeys(t *testing.T) {
	server, peer := testPair(t, Config{})

	oldReadSecret := server.ReadSecret()
	oldWriteSecret := server.WriteSecret()
	record := mustSendKeyUpdate(t, peer, KeyUpdateRequested)

	result, err := server.ProcessRecord(record)
	if err != nil {
		t.Fatalf("ProcessRecord(update_requested): %v", err)
	}
	if result.ContentType != ContentTypeHandshake {
		t.Fatalf("content type = %#x, want handshake", result.ContentType)
	}
	if !bytes.Equal(result.Plaintext, marshalKeyUpdate(KeyUpdateRequested)) {
		t.Fatalf("plaintext = %x, want KeyUpdate(update_requested)", result.Plaintext)
	}
	if result.Response == nil {
		t.Fatal("update_requested did not produce a response")
	}

	wantRead, err := NextTrafficSecret(sha256.New, oldReadSecret)
	if err != nil {
		t.Fatal(err)
	}
	if got := server.ReadSecret(); !bytes.Equal(got, wantRead) {
		t.Fatalf("read secret = %x, want HKDF-Expand-Label(old, traffic upd) = %x", got, wantRead)
	}
	if !bytes.Equal(server.ReadSecret(), peer.WriteSecret()) {
		t.Fatal("server read secret does not match peer write secret after KeyUpdate")
	}
	if server.ReadGeneration() != 1 || server.ReadSequence() != 0 {
		t.Fatalf("read gen/seq = %d/%d, want 1/0", server.ReadGeneration(), server.ReadSequence())
	}
	if server.WriteGeneration() != 1 || server.WriteSequence() != 0 {
		t.Fatalf("write gen/seq = %d/%d, want 1/0", server.WriteGeneration(), server.WriteSequence())
	}
	if got := server.WriteSecret(); bytes.Equal(got, oldWriteSecret) {
		t.Fatal("write secret was not rotated")
	}

	// The peer must see exactly one KeyUpdate(update_not_requested) and no
	// second response.
	peerResult, err := peer.ProcessRecord(result.Response)
	if err != nil {
		t.Fatalf("peer ProcessRecord(response): %v", err)
	}
	if peerResult.Response != nil {
		t.Fatal("update_not_requested produced an unexpected response")
	}
	if !bytes.Equal(peerResult.Plaintext, marshalKeyUpdate(KeyUpdateNotRequested)) {
		t.Fatalf("response = %x, want KeyUpdate(update_not_requested)", peerResult.Plaintext)
	}
	if peer.ReadGeneration() != 1 || peer.ReadSequence() != 0 {
		t.Fatalf("peer read gen/seq = %d/%d, want 1/0", peer.ReadGeneration(), peer.ReadSequence())
	}
	if !bytes.Equal(peer.ReadSecret(), server.WriteSecret()) {
		t.Fatal("peer read secret does not match server write secret after response")
	}
}

func TestUpdateNotRequestedRotatesReadKeyWithoutResponse(t *testing.T) {
	server, peer := testPair(t, Config{})
	record := mustSendKeyUpdate(t, peer, KeyUpdateNotRequested)

	result, err := server.ProcessRecord(record)
	if err != nil {
		t.Fatalf("ProcessRecord(update_not_requested): %v", err)
	}
	if result.Response != nil {
		t.Fatal("update_not_requested must not produce a response")
	}
	if server.ReadGeneration() != 1 || server.ReadSequence() != 0 {
		t.Fatalf("read gen/seq = %d/%d, want 1/0", server.ReadGeneration(), server.ReadSequence())
	}
	if server.WriteGeneration() != 0 || server.WriteSequence() != 0 {
		t.Fatalf("write gen/seq = %d/%d, want 0/0", server.WriteGeneration(), server.WriteSequence())
	}
}

func TestAES256GCMKeyUpdate(t *testing.T) {
	clientToServer := sha512.Sum384([]byte("client -> server SHA-384 secret"))
	serverToClient := sha512.Sum384([]byte("server -> client SHA-384 secret"))
	server, err := NewKeyUpdateState(Config{
		Suite:       TLS13AES256GCM,
		ReadSecret:  clientToServer[:],
		WriteSecret: serverToClient[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := NewKeyUpdateState(Config{
		Suite:       TLS13AES256GCM,
		ReadSecret:  serverToClient[:],
		WriteSecret: clientToServer[:],
	})
	if err != nil {
		t.Fatal(err)
	}

	record := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
	result, err := server.ProcessRecord(record)
	if err != nil {
		t.Fatalf("ProcessRecord: %v", err)
	}
	if len(result.Response) == 0 {
		t.Fatal("missing response")
	}
	if got := len(server.ReadSecret()); got != 48 {
		t.Fatalf("SHA-384 read secret length = %d, want 48", got)
	}
	if server.ReadGeneration() != 1 || server.WriteGeneration() != 1 {
		t.Fatalf("gen = %d/%d, want 1/1", server.ReadGeneration(), server.WriteGeneration())
	}
}

func TestFloodOverBudgetClosesConnection(t *testing.T) {
	var closeCalls atomic.Int32
	server, peer := testPair(t, Config{
		MaxUpdates:   3,
		UpdateWindow: time.Minute,
		OnClose: func(error) {
			closeCalls.Add(1)
		},
	})

	for i := 0; i < 3; i++ {
		record := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
		result, err := server.ProcessRecord(record)
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		if len(result.Response) == 0 {
			t.Fatalf("update %d: missing response", i)
		}
	}
	if server.ReadGeneration() != 3 || server.WriteGeneration() != 3 {
		t.Fatalf("gen = %d/%d, want 3/3", server.ReadGeneration(), server.WriteGeneration())
	}

	overflow := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
	result, err := server.ProcessRecord(overflow)
	if !errors.Is(err, ErrUpdateBudgetExceeded) {
		t.Fatalf("overflow error = %v, want ErrUpdateBudgetExceeded", err)
	}
	if result.Response != nil {
		t.Fatal("budget overflow must not produce a response")
	}
	if !server.Closed() {
		t.Fatal("budget overflow did not close the state machine")
	}
	if !errors.Is(server.Err(), ErrUpdateBudgetExceeded) {
		t.Fatalf("server.Err = %v, want ErrUpdateBudgetExceeded", server.Err())
	}
	if server.ReadGeneration() != 3 || server.WriteGeneration() != 3 {
		t.Fatalf("overflow advanced generations to %d/%d", server.ReadGeneration(), server.WriteGeneration())
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("OnClose calls = %d, want 1", closeCalls.Load())
	}

	// Every later operation must fail with the original close reason.
	next := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
	if _, err := server.ProcessRecord(next); !errors.Is(err, ErrUpdateBudgetExceeded) {
		t.Fatalf("ProcessRecord after close = %v, want ErrUpdateBudgetExceeded", err)
	}
}

func TestSlidingWindowBudgetResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	server, peer := testPair(t, Config{
		MaxUpdates:   2,
		UpdateWindow: 10 * time.Second,
		Now:          func() time.Time { return now },
	})

	for i := 0; i < 2; i++ {
		if _, err := server.ProcessRecord(mustSendKeyUpdate(t, peer, KeyUpdateRequested)); err != nil {
			t.Fatalf("first window update %d: %v", i, err)
		}
	}

	now = now.Add(11 * time.Second)
	for i := 0; i < 2; i++ {
		if _, err := server.ProcessRecord(mustSendKeyUpdate(t, peer, KeyUpdateRequested)); err != nil {
			t.Fatalf("second window update %d: %v", i, err)
		}
	}

	now = now.Add(time.Second)
	record := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
	if _, err := server.ProcessRecord(record); !errors.Is(err, ErrUpdateBudgetExceeded) {
		t.Fatalf("third update in second window = %v, want ErrUpdateBudgetExceeded", err)
	}
}

func TestAuthenticationFailureDoesNotAdvanceReadGeneration(t *testing.T) {
	server, peer := testPair(t, Config{})

	genBefore := server.ReadGeneration()
	seqBefore := server.ReadSequence()
	secretBefore := server.ReadSecret()
	record := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
	tampered := append([]byte(nil), record...)
	tampered[len(tampered)-1] ^= 0x01

	result, err := server.ProcessRecord(tampered)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered record error = %v, want ErrAuthentication", err)
	}
	if result.Response != nil {
		t.Fatal("unauthenticated record produced a response")
	}
	if server.ReadGeneration() != genBefore {
		t.Fatalf("read generation advanced to %d after auth failure", server.ReadGeneration())
	}
	if server.ReadSequence() != seqBefore {
		t.Fatalf("read sequence advanced to %d after auth failure", server.ReadSequence())
	}
	if !bytes.Equal(server.ReadSecret(), secretBefore) {
		t.Fatal("read secret changed after auth failure")
	}
	if server.Closed() {
		t.Fatal("authentication failure unexpectedly closed the state machine")
	}

	// Because the failed record consumed no sequence number or key epoch, the
	// genuine record still decrypts and performs exactly one update.
	result, err = server.ProcessRecord(record)
	if err != nil {
		t.Fatalf("genuine record after auth failure: %v", err)
	}
	if len(result.Response) == 0 {
		t.Fatal("genuine update_requested produced no response")
	}
	if server.ReadGeneration() != 1 || server.ReadSequence() != 0 {
		t.Fatalf("read gen/seq = %d/%d, want 1/0", server.ReadGeneration(), server.ReadSequence())
	}
}

func TestUnauthenticatedFloodDoesNotConsumeUpdateBudget(t *testing.T) {
	server, peer := testPair(t, Config{MaxUpdates: 2})
	record := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
	tampered := append([]byte(nil), record...)
	tampered[len(tampered)-1] ^= 0x80

	for i := 0; i < 20; i++ {
		if _, err := server.ProcessRecord(tampered); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("tampered record %d: %v", i, err)
		}
	}
	if server.Closed() {
		t.Fatal("unauthenticated flood closed the state machine")
	}

	result, err := server.ProcessRecord(record)
	if err != nil {
		t.Fatalf("genuine KeyUpdate: %v", err)
	}
	if len(result.Response) == 0 {
		t.Fatal("genuine KeyUpdate produced no response")
	}
	if server.ReadGeneration() != 1 {
		t.Fatalf("read generation = %d, want 1", server.ReadGeneration())
	}
}

func TestOversizedRecordRejected(t *testing.T) {
	server, _ := testPair(t, Config{})
	plaintext := bytes.Repeat([]byte{0x41}, maxRecordPlaintext)
	if _, err := server.SealRecord(0x17, plaintext); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("SealRecord oversize error = %v, want ErrRecordTooLarge", err)
	}
}

func TestFragmentedOrCoalescedKeyUpdateRejected(t *testing.T) {
	tests := []struct {
		name      string
		plaintext []byte
		wantErr   error
	}{
		{
			name:      "truncated header",
			plaintext: []byte{KeyUpdateMessageType, 0, 0},
			wantErr:   ErrMalformedKeyUpdate,
		},
		{
			name:      "truncated body",
			plaintext: []byte{KeyUpdateMessageType, 0, 0, 1},
			wantErr:   ErrMalformedKeyUpdate,
		},
		{
			name: "coalesced KeyUpdates",
			plaintext: append(
				marshalKeyUpdate(KeyUpdateRequested),
				marshalKeyUpdate(KeyUpdateNotRequested)...,
			),
			wantErr: ErrKeyUpdateNotIsolated,
		},
		{
			name: "coalesced with another handshake message",
			plaintext: append(
				marshalKeyUpdate(KeyUpdateRequested),
				[]byte{20, 0, 0, 0}..., // Finished with an empty body
			),
			wantErr: ErrKeyUpdateNotIsolated,
		},
		{
			name:      "invalid request value",
			plaintext: []byte{KeyUpdateMessageType, 0, 0, 1, 2},
			wantErr:   ErrMalformedKeyUpdate,
		},
		{
			name:      "invalid body length",
			plaintext: []byte{KeyUpdateMessageType, 0, 0, 2, 0, 1},
			wantErr:   ErrMalformedKeyUpdate,
		},
		{
			name:      "KeyUpdate not first",
			plaintext: []byte{20, 0, 0, 0, KeyUpdateMessageType, 0, 0, 1, 1},
			wantErr:   ErrKeyUpdateNotIsolated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, peer := testPair(t, Config{})
			record, err := peer.SealRecord(ContentTypeHandshake, tt.plaintext)
			if err != nil {
				t.Fatalf("SealRecord: %v", err)
			}
			result, err := server.ProcessRecord(record)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ProcessRecord error = %v, want %v", err, tt.wantErr)
			}
			if result.Response != nil {
				t.Fatal("invalid KeyUpdate produced a response")
			}
			if server.ReadGeneration() != 0 {
				t.Fatalf("read generation = %d after invalid KeyUpdate", server.ReadGeneration())
			}
			if !server.Closed() {
				t.Fatal("invalid KeyUpdate did not close the state machine")
			}
		})
	}
}

func TestOutOfOrderRecordsDoNotAdvanceReadState(t *testing.T) {
	server, peer := testPair(t, Config{})
	first, err := peer.SealRecord(0x17, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := peer.SealRecord(0x17, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := server.ProcessRecord(second); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("out-of-order second record = %v, want ErrAuthentication", err)
	}
	if server.ReadSequence() != 0 {
		t.Fatalf("read sequence = %d after out-of-order record, want 0", server.ReadSequence())
	}

	result, err := server.ProcessRecord(first)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if !bytes.Equal(result.Plaintext, []byte("first")) {
		t.Fatalf("first plaintext = %q", result.Plaintext)
	}
	if server.ReadSequence() != 1 {
		t.Fatalf("read sequence = %d, want 1", server.ReadSequence())
	}

	result, err = server.ProcessRecord(second)
	if err != nil {
		t.Fatalf("second record after reordering: %v", err)
	}
	if !bytes.Equal(result.Plaintext, []byte("second")) {
		t.Fatalf("second plaintext = %q", result.Plaintext)
	}
}

func TestOutOfOrderKeyUpdatesDoNotAdvanceGeneration(t *testing.T) {
	server, peer := testPair(t, Config{})
	first := mustSendKeyUpdate(t, peer, KeyUpdateRequested)
	second := mustSendKeyUpdate(t, peer, KeyUpdateRequested)

	if _, err := server.ProcessRecord(second); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("new-key-epoch record delivered early = %v, want ErrAuthentication", err)
	}
	if server.ReadGeneration() != 0 {
		t.Fatalf("read generation = %d after early record, want 0", server.ReadGeneration())
	}

	result, err := server.ProcessRecord(first)
	if err != nil {
		t.Fatalf("first KeyUpdate: %v", err)
	}
	if len(result.Response) == 0 {
		t.Fatal("first KeyUpdate produced no response")
	}
	if server.ReadGeneration() != 1 {
		t.Fatalf("read generation = %d, want 1", server.ReadGeneration())
	}

	result, err = server.ProcessRecord(second)
	if err != nil {
		t.Fatalf("second KeyUpdate after first: %v", err)
	}
	if len(result.Response) == 0 {
		t.Fatal("second KeyUpdate produced no response")
	}
	if server.ReadGeneration() != 2 {
		t.Fatalf("read generation = %d, want 2", server.ReadGeneration())
	}
}

func TestStalePreUpdateRecordRejectedAfterKeyChange(t *testing.T) {
	server, peer := testPair(t, Config{})
	oldRecord, err := peer.SealRecord(0x17, []byte("before update"))
	if err != nil {
		t.Fatal(err)
	}
	update := mustSendKeyUpdate(t, peer, KeyUpdateNotRequested)

	if _, err := server.ProcessRecord(oldRecord); err != nil {
		t.Fatalf("old record before update: %v", err)
	}
	if _, err := server.ProcessRecord(update); err != nil {
		t.Fatalf("KeyUpdate: %v", err)
	}
	if server.ReadGeneration() != 1 || server.ReadSequence() != 0 {
		t.Fatalf("read gen/seq = %d/%d, want 1/0", server.ReadGeneration(), server.ReadSequence())
	}

	if _, err := server.ProcessRecord(oldRecord); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("replayed pre-update record = %v, want ErrAuthentication", err)
	}
	if server.ReadGeneration() != 1 {
		t.Fatalf("read generation changed to %d after stale record", server.ReadGeneration())
	}
}
