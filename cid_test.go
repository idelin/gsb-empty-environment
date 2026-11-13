package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"net"
	"testing"
)

func udpAddr(ip string, port int) net.Addr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
}

func testRecord(cid []byte, epoch uint16, seq uint64, plaintextLen int) *Record {
	return &Record{
		Type:       ContentTypeTLS12CID,
		Version:    0xfefd,
		Epoch:      epoch,
		Sequence:   seq,
		CIDLength:  uint8(len(cid)),
		CID:        append([]byte(nil), cid...),
		Ciphertext: bytes.Repeat([]byte{0x5a}, plaintextLen),
	}
}

func TestNoAddressChangeBeforeAuthentication(t *testing.T) {
	cid := []byte{0x11, 0x22, 0x33, 0x44}
	oldPeer := udpAddr("10.0.0.1", 4444)
	newPeer := udpAddr("10.0.0.2", 5555)

	s := NewSession(cid, oldPeer)
	r := NewRouter()
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}

	authCalls := 0
	s.Verify = func(got *Session, rec *Record) ([]byte, error) {
		authCalls++
		if got.PeerAddress() != oldPeer {
			t.Fatalf("peer changed before authentication: %v", got.PeerAddress())
		}
		return nil, ErrAuthentication
	}

	probeCalls := 0
	s.SendProbe = func(net.Addr, []byte) error {
		probeCalls++
		return nil
	}

	rec := testRecord(cid, 1, 7, 32)
	if _, err := r.HandleRecord(rec, newPeer); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected authentication failure, got %v", err)
	}
	if authCalls != 1 {
		t.Fatalf("authenticator called %d times, want 1", authCalls)
	}
	if s.PeerAddress() != oldPeer {
		t.Fatalf("peer changed after failed authentication: %v", s.PeerAddress())
	}
	if probeCalls != 0 {
		t.Fatalf("path probe sent before authentication: %d", probeCalls)
	}
	if _, ok := s.Path(); ok {
		t.Fatal("path state created before successful authentication")
	}
}

func TestCIDRoutesOnlyTLS12CID(t *testing.T) {
	cid := []byte{1, 2, 3, 4}
	s := NewSession(cid, udpAddr("10.0.0.1", 1))
	r := NewRouter()
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}

	calls := 0
	s.Authenticator = func(*Record) ([]byte, error) {
		calls++
		return nil, nil
	}

	rec := testRecord(cid, 1, 1, 16)
	rec.Type = 23
	if _, err := r.HandleRecord(rec, udpAddr("10.0.0.2", 2)); !errors.Is(err, ErrNotCID) {
		t.Fatalf("expected ErrNotCID, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("authenticator called for non-CID record")
	}
}

func TestCIDLengthMismatchIsDropped(t *testing.T) {
	cid := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	oldPeer := udpAddr("10.0.0.1", 4444)
	s := NewSession(cid, oldPeer)
	r := NewRouter()
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}

	calls := 0
	probeCalls := 0
	s.Authenticator = func(*Record) ([]byte, error) {
		calls++
		return []byte("ok"), nil
	}
	s.SendProbe = func(net.Addr, []byte) error {
		probeCalls++
		return nil
	}

	rec := testRecord(cid, 1, 1, 16)
	rec.CIDLength = 8
	if _, err := r.HandleRecord(rec, udpAddr("10.0.0.2", 5555)); !errors.Is(err, ErrCIDLength) {
		t.Fatalf("expected ErrCIDLength, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("authenticator called for CID-length mismatch")
	}
	if s.PeerAddress() != oldPeer || probeCalls != 0 {
		// The record must be dropped before it can affect the session.
		t.Fatalf("CID-length mismatch changed peer or sent probe")
	}
}

func TestReplayInWindowCannotMigrateAddress(t *testing.T) {
	cid := []byte{0x10, 0x20, 0x30, 0x40}
	oldPeer := udpAddr("10.1.0.1", 4000)
	newPeer := udpAddr("10.1.0.9", 9000)
	attacker := udpAddr("10.6.6.6", 6666)

	s := NewSession(cid, oldPeer)
	r := NewRouter(Config{AmplificationFactor: 3})
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}

	authCalls := 0
	probeCalls := 0
	s.Authenticator = func(*Record) ([]byte, error) {
		authCalls++
		return []byte("plaintext"), nil
	}
	s.SendProbe = func(net.Addr, []byte) error {
		probeCalls++
		return nil
	}

	first := testRecord(cid, 2, 100, 32)
	res, err := r.HandleRecord(first, newPeer)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if !res.Migrated || res.Replayed {
		t.Fatalf("first authenticated record did not migrate: %+v", res)
	}
	if s.PeerAddress() != newPeer {
		t.Fatalf("peer = %v, want %v", s.PeerAddress(), newPeer)
	}
	if probeCalls != 1 {
		t.Fatalf("probe calls = %d, want 1", probeCalls)
	}

	replay := testRecord(cid, 2, 100, 32)
	res, err = r.HandleRecord(replay, attacker)
	if err != nil {
		t.Fatalf("replay routing returned error: %v", err)
	}
	if !res.Replayed || res.Accepted || res.Migrated {
		t.Fatalf("replay result not marked correctly: %+v", res)
	}
	if s.PeerAddress() != newPeer {
		t.Fatalf("replayed record moved peer to %v", s.PeerAddress())
	}
	if probeCalls != 1 {
		t.Fatalf("replay caused another path probe: %d", probeCalls)
	}
	if authCalls != 2 {
		t.Fatalf("authenticator calls = %d, want 2 (replay is authenticated first)", authCalls)
	}

	second := testRecord(cid, 2, 101, 32)
	if _, err := r.HandleRecord(second, newPeer); err != nil {
		t.Fatalf("second valid record: %v", err)
	}
	olderReplay := testRecord(cid, 2, 101, 32)
	res, err = r.HandleRecord(olderReplay, attacker)
	if err != nil {
		t.Fatalf("window replay returned error: %v", err)
	}
	if !res.Replayed || res.Migrated || s.PeerAddress() != newPeer {
		t.Fatal("record from inside replay window migrated the session")
	}
}

func TestReplayDoesNotMigrateFromExistingPeer(t *testing.T) {
	cid := []byte{1, 2, 3, 4, 5}
	oldPeer := udpAddr("10.2.0.1", 1)
	attacker := udpAddr("10.2.0.2", 2)
	s := NewSession(cid, oldPeer)
	r := NewRouter()
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	probeCalls := 0
	s.Authenticator = func(*Record) ([]byte, error) { return []byte("ok"), nil }
	s.SendProbe = func(net.Addr, []byte) error {
		probeCalls++
		return nil
	}

	first := testRecord(cid, 3, 1, 8)
	if _, err := r.HandleRecord(first, oldPeer); err != nil {
		t.Fatalf("first record: %v", err)
	}
	replay := testRecord(cid, 3, 1, 8)
	res, err := r.HandleRecord(replay, attacker)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !res.Replayed || s.PeerAddress() != oldPeer || probeCalls != 0 {
		t.Fatalf("replay migrated session: peer=%v probeCalls=%d", s.PeerAddress(), probeCalls)
	}
}

func TestReorderedOlderRecordCannotRevertAddress(t *testing.T) {
	cid := []byte{1, 2, 3, 4}
	peerA := udpAddr("10.7.0.1", 1)
	peerB := udpAddr("10.7.0.2", 2)
	s := NewSession(cid, peerA)
	r := NewRouter()
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	s.Authenticator = func(*Record) ([]byte, error) { return nil, nil }
	s.SendProbe = func(net.Addr, []byte) error { return nil }

	if _, err := r.HandleRecord(testRecord(cid, 8, 90, 16), peerA); err != nil {
		t.Fatalf("newer record: %v", err)
	}
	olderButUnseen := testRecord(cid, 8, 80, 16)
	res, err := r.HandleRecord(olderButUnseen, peerB)
	if err != nil {
		t.Fatalf("older record: %v", err)
	}
	if res.Replayed || res.Migrated || s.PeerAddress() != peerA {
		t.Fatalf("older reordered record changed address: %+v peer=%v", res, s.PeerAddress())
	}
}

func TestPathProbeAndAmplificationBudget(t *testing.T) {
	cid := []byte{0xde, 0xad, 0xbe, 0xef}
	oldPeer := udpAddr("10.3.0.1", 3000)
	newPeer := udpAddr("10.3.0.9", 9000)

	probe := []byte("0123456789abcdef")
	s := NewSession(cid, oldPeer)
	r := NewRouter(Config{AmplificationFactor: 3})
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	s.ProbePayload = func(*Session) []byte { return append([]byte(nil), probe...) }

	probeCalls := 0
	probeAddrs := make([]net.Addr, 0, 2)
	s.SendProbe = func(addr net.Addr, payload []byte) error {
		probeCalls++
		probeAddrs = append(probeAddrs, addr)
		if !bytes.Equal(payload, probe) {
			t.Fatalf("unexpected probe payload: %x", payload)
		}
		return nil
	}

	var sentToNew int
	s.Write = func(addr net.Addr, payload []byte) error {
		if sameAddr(addr, newPeer) {
			sentToNew += len(payload)
		}
		return nil
	}
	s.Authenticator = func(*Record) ([]byte, error) { return []byte("plaintext"), nil }

	rec := testRecord(cid, 4, 1, 32)
	res, err := r.HandleRecord(rec, newPeer)
	if err != nil {
		t.Fatalf("migrating record: %v", err)
	}
	if !res.Migrated || !res.ProbeSent {
		t.Fatalf("migration/probe result: %+v", res)
	}
	if probeCalls != 1 || len(probeAddrs) != 1 || !sameAddr(probeAddrs[0], newPeer) {
		t.Fatalf("probe sent %d times to %v", probeCalls, probeAddrs)
	}
	if s.PeerAddress() != newPeer {
		t.Fatalf("peer = %v, want %v", s.PeerAddress(), newPeer)
	}

	path, ok := s.Path()
	if !ok || !path.Awaiting || path.Received != uint64(rec.WireSize()) || path.Sent != uint64(len(probe)) {
		t.Fatalf("unexpected path state: %+v", path)
	}
	budget := 3 * path.Received
	remaining := int(budget - path.Sent)

	if err := r.Send(s, bytes.Repeat([]byte{1}, remaining)); err != nil {
		t.Fatalf("send at amplification limit: %v", err)
	}
	if err := r.Send(s, []byte{2}); !errors.Is(err, ErrAmplification) {
		t.Fatalf("send over limit: %v", err)
	}
	if sentToNew != int(budget)-len(probe) {
		t.Fatalf("bytes sent to new address = %d, want %d", sentToNew, int(budget)-len(probe))
	}

	if err := r.ConfirmPath(s, newPeer, probe); err != nil {
		t.Fatalf("confirm path: %v", err)
	}
	if s.AwaitingPathValidation() {
		t.Fatal("path still awaiting validation after confirmation")
	}
	if err := r.Send(s, bytes.Repeat([]byte{3}, 4096)); err != nil {
		t.Fatalf("send after path confirmation: %v", err)
	}
	if sentToNew != int(budget)-len(probe)+4096 {
		t.Fatalf("post-confirmation send accounting = %d", sentToNew)
	}

	if err := r.ConfirmPath(s, udpAddr("10.9.9.9", 9999), probe); !errors.Is(err, ErrWrongPath) && s.AwaitingPathValidation() {
		t.Fatalf("path response from wrong address accepted: %v", err)
	}
}

func TestProbeIsSentOnceAndBudgetGrowsWithAuthenticatedRecords(t *testing.T) {
	cid := []byte{1, 2, 3, 4}
	oldPeer := udpAddr("10.4.0.1", 1)
	newPeer := udpAddr("10.4.0.2", 2)
	s := NewSession(cid, oldPeer)
	r := NewRouter()
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	s.ProbePayload = func(*Session) []byte { return []byte("probe") }
	probeCalls := 0
	s.SendProbe = func(net.Addr, []byte) error {
		probeCalls++
		return nil
	}
	s.Authenticator = func(*Record) ([]byte, error) { return nil, nil }

	if _, err := r.HandleRecord(testRecord(cid, 5, 1, 32), newPeer); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := r.HandleRecord(testRecord(cid, 5, 2, 32), newPeer); err != nil {
		t.Fatalf("second: %v", err)
	}
	if probeCalls != 1 {
		t.Fatalf("probe calls = %d, want exactly 1 per migration", probeCalls)
	}
	path, _ := s.Path()
	if path.Received != 2*uint64(testRecord(cid, 5, 1, 32).WireSize()) {
		t.Fatalf("received accounting = %d", path.Received)
	}
}

func TestRFC9146RecordRoundTrip(t *testing.T) {
	cid := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	key := bytes.Repeat([]byte{0x42}, 16)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}

	peer := udpAddr("10.5.0.1", 1)
	s := NewSession(cid, peer)
	s.AEAD = aead
	s.NoncePrefix = []byte{0xa0, 0xa1, 0xa2, 0xa3}

	r := NewRouter()
	r.DefaultCIDLength = len(cid)
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	probes := 0
	s.SendProbe = func(net.Addr, []byte) error {
		probes++
		return nil
	}

	rec := &Record{
		Type:     ContentTypeTLS12CID,
		Version:  0xfefd,
		Epoch:    6,
		Sequence: 1,
		CID:      append([]byte(nil), cid...),
	}
	raw, err := s.SealRecord(rec, []byte("hello dtls"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	res, err := r.HandlePacket(raw, udpAddr("10.5.0.9", 9))
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if !bytes.Equal(res.Plaintext, []byte("hello dtls")) {
		t.Fatalf("plaintext = %q", res.Plaintext)
	}
	if !res.Migrated || !res.ProbeSent || probes != 1 {
		t.Fatalf("migration result: %+v probes=%d", res, probes)
	}

	res, err = r.HandlePacket(raw, udpAddr("10.5.0.10", 10))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !res.Replayed || res.Migrated {
		t.Fatalf("wire replay migrated: %+v", res)
	}
}

func TestUnknownCIDAndNonCIDWireRecord(t *testing.T) {
	r := NewRouter()
	if _, err := r.HandlePacket([]byte{23, 0, 1}, udpAddr("10.0.0.1", 1)); !errors.Is(err, ErrNotCID) {
		t.Fatalf("expected ErrNotCID, got %v", err)
	}
	cid := []byte{9, 9, 9}
	s := NewSession(cid, udpAddr("10.0.0.1", 1))
	if err := r.Register(s); err != nil {
		t.Fatal(err)
	}
	rec := testRecord([]byte{1, 1, 1}, 1, 1, 8)
	if _, err := r.Route(rec, udpAddr("10.0.0.2", 2)); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestReplayWindowBoundaries(t *testing.T) {
	w := NewReplayWindow(64)
	for i := uint64(1); i <= 64; i++ {
		if w.Seen(i) {
			t.Fatalf("sequence %d unexpectedly seen", i)
		}
		w.Mark(i)
	}
	if !w.Seen(1) || !w.Seen(0) || w.Seen(65) {
		t.Fatal("window boundary accounting is wrong")
	}
	w.Mark(65)
	if !w.Seen(2) || !w.Seen(1) || w.Seen(66) {
		t.Fatal("window sliding accounting is wrong")
	}
}

func ExampleRouter() {
	cid := []byte("example-cid")
	r := NewRouter()
	s := NewSession(cid, udpAddr("192.0.2.1", 5000))
	s.Authenticator = func(*Record) ([]byte, error) { return []byte("ok"), nil }
	s.SendProbe = func(addr net.Addr, probe []byte) error { return nil }
	_ = r.Register(s)
	fmt.Println("session registered")
	// Output: session registered
}
