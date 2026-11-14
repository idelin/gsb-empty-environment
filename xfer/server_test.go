package xfer

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

var (
	opsNet   = netip.MustParsePrefix("10.20.0.0/24")
	opsAddr  = netip.MustParseAddr("10.20.0.7")
	evilAddr = netip.MustParseAddr("203.0.113.66")

	opsKey = Key{
		ID:         "ops-xfer",
		Secret:     []byte("s3cret-shared-key-for-ops"),
		Zone:       "corp.example.",
		Generation: 2,
	}
	otherZoneKey = Key{
		ID:         "lab-xfer",
		Secret:     []byte("lab-only-secret"),
		Zone:       "lab.example.",
		Generation: 1,
	}
)

func zoneRecords(serial uint32) []Record {
	return []Record{
		{Name: "corp.example.", Type: "SOA", TTL: 3600, Value: "ns1.corp.example. hostmaster.corp.example."},
		{Name: "corp.example.", Type: "NS", TTL: 3600, Value: "ns1.corp.example."},
		{Name: "db-01.corp.example.", Type: "A", TTL: 300, Value: "10.20.3.11"},
		{Name: "vault.corp.example.", Type: "A", TTL: 300, Value: "10.20.3.12"},
		{Name: "git.corp.example.", Type: "A", TTL: 300, Value: "10.20.3.13"},
	}
}

func newServer(t *testing.T) *Server {
	t.Helper()
	store := NewStore(8)
	if err := store.Load("corp.example.", 100, zoneRecords(100)); err != nil {
		t.Fatalf("load zone: %v", err)
	}
	if err := store.Load("lab.example.", 50, []Record{
		{Name: "lab.example.", Type: "SOA", TTL: 60, Value: "ns1.lab.example. hostmaster.lab.example."},
	}); err != nil {
		t.Fatalf("load lab zone: %v", err)
	}
	policy := Policy{
		"corp.example.": {{
			Networks: []netip.Prefix{opsNet},
			KeyIDs:   []string{"ops-xfer"},
			Types:    []RequestType{TypeAXFR, TypeIXFR},
			TCPOnly:  true,
		}},
	}
	return NewServer(policy, store, NewKeyring(opsKey, otherZoneKey))
}

func signedReq(req *Request, key Key) *Request {
	req.TSIG = Sign(req, key, time.Now(), 300)
	return req
}

func axfrReq(source netip.Addr) *Request {
	return &Request{Zone: "corp.example.", Type: TypeAXFR, Proto: ProtoTCP, Source: source}
}

func drain(t *testing.T, tr *Transfer) []Record {
	t.Helper()
	var all []Record
	for {
		chunk, done, err := tr.Next(2)
		if err != nil {
			t.Fatalf("stream chunk: %v", err)
		}
		all = append(all, chunk...)
		if done {
			return all
		}
	}
}

func TestArbitrarySourceAXFRDenied(t *testing.T) {
	srv := newServer(t)
	tr, err := srv.Begin(axfrReq(evilAddr))
	if !IsDeny(err) {
		t.Fatalf("expected denial, got err=%v", err)
	}
	if tr != nil {
		t.Fatal("unauthorized request received a transfer")
	}
}

func TestWrongTSIGDenied(t *testing.T) {
	srv := newServer(t)
	req := axfrReq(opsAddr)
	req.TSIG = Sign(req, Key{
		ID: opsKey.ID, Secret: []byte("attacker-guess"),
		Zone: opsKey.Zone, Generation: opsKey.Generation,
	}, time.Now(), 300)
	tr, err := srv.Begin(req)
	if !IsDeny(err) {
		t.Fatalf("expected denial, got err=%v", err)
	}
	if tr != nil {
		t.Fatal("bad signature received a transfer")
	}
}

func TestCrossZoneKeyDenied(t *testing.T) {
	srv := newServer(t)
	// The lab key is validly signed but bound to lab.example., not corp.example.
	req := signedReq(axfrReq(opsAddr), otherZoneKey)
	tr, err := srv.Begin(req)
	if !IsDeny(err) {
		t.Fatalf("expected denial, got err=%v", err)
	}
	if tr != nil {
		t.Fatal("cross-zone key received a transfer")
	}
}

func TestReplayDenied(t *testing.T) {
	srv := newServer(t)
	req := signedReq(axfrReq(opsAddr), opsKey)
	if _, err := srv.Begin(req); err != nil {
		t.Fatalf("first use of signature should pass: %v", err)
	}
	if _, err := srv.Begin(req); !IsDeny(err) {
		t.Fatalf("replayed signature should be denied, got err=%v", err)
	}
}

func TestRetiredGenerationDenied(t *testing.T) {
	srv := newServer(t)
	stale := opsKey
	stale.Generation = 1
	req := signedReq(axfrReq(opsAddr), stale)
	if _, err := srv.Begin(req); !IsDeny(err) {
		t.Fatalf("retired key generation should be denied, got err=%v", err)
	}
}

func TestAXFRoverUDPDenied(t *testing.T) {
	srv := newServer(t)
	req := axfrReq(opsAddr)
	req.Proto = ProtoUDP
	signedReq(req, opsKey)
	if _, err := srv.Begin(req); !IsDeny(err) {
		t.Fatalf("AXFR over UDP should be denied, got err=%v", err)
	}
}

func TestZoneChangeMidTransferAborts(t *testing.T) {
	srv := newServer(t)
	tr, err := srv.Begin(signedReq(axfrReq(opsAddr), opsKey))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	first, done, err := tr.Next(2)
	if err != nil || done {
		t.Fatalf("first chunk: done=%v err=%v", done, err)
	}
	if first[0].Type != "SOA" {
		t.Fatalf("first chunk must open with SOA, got %s", first[0].Type)
	}
	// Operator commits a new zone version while the transfer streams.
	newRecords := append([]Record{}, zoneRecords(101)...)
	newRecords = append(newRecords, Record{Name: "new.corp.example.", Type: "A", TTL: 300, Value: "10.20.3.99"})
	err = srv.store.Update("corp.example.", 101, newRecords, Delta{
		Added: []Record{{Name: "new.corp.example.", Type: "A", TTL: 300, Value: "10.20.3.99"}},
	})
	if err != nil {
		t.Fatalf("update zone: %v", err)
	}
	if _, _, err := tr.Next(2); !errors.Is(err, ErrZoneChanged) {
		t.Fatalf("expected ErrZoneChanged, got %v", err)
	}
}

func TestLegitOpsAXFR(t *testing.T) {
	srv := newServer(t)
	tr, err := srv.Begin(signedReq(axfrReq(opsAddr), opsKey))
	if err != nil {
		t.Fatalf("legit ops source denied: %v", err)
	}
	all := drain(t, tr)
	if all[0].Type != "SOA" || all[len(all)-1].Type != "SOA" {
		t.Fatal("AXFR stream must be bracketed by SOA records")
	}
	if len(all) != len(zoneRecords(100))+1 {
		t.Fatalf("expected %d records, got %d", len(zoneRecords(100))+1, len(all))
	}
}

func TestLegitIXFR(t *testing.T) {
	srv := newServer(t)
	newRecords := append([]Record{}, zoneRecords(101)...)
	newRecords = append(newRecords, Record{Name: "api.corp.example.", Type: "A", TTL: 300, Value: "10.20.4.10"})
	err := srv.store.Update("corp.example.", 101, newRecords, Delta{
		Removed: []Record{},
		Added:   []Record{{Name: "api.corp.example.", Type: "A", TTL: 300, Value: "10.20.4.10"}},
	})
	if err != nil {
		t.Fatalf("update zone: %v", err)
	}
	req := &Request{
		Zone: "corp.example.", Type: TypeIXFR, Proto: ProtoTCP,
		Source: opsAddr, ClientSerial: 100,
	}
	tr, err := srv.Begin(signedReq(req, opsKey))
	if err != nil {
		t.Fatalf("legit IXFR denied: %v", err)
	}
	all := drain(t, tr)
	if all[0].Type != "SOA" || all[len(all)-1].Type != "SOA" {
		t.Fatal("IXFR stream must be bracketed by SOA records")
	}
	var sawAdded bool
	for _, r := range all {
		if r.Name == "api.corp.example." {
			sawAdded = true
		}
		if r.Name == "db-01.corp.example." {
			t.Fatal("IXFR delta leaked unchanged full-zone records")
		}
	}
	if !sawAdded {
		t.Fatal("IXFR delta missing added record")
	}
}

func TestIXFRUpToDate(t *testing.T) {
	srv := newServer(t)
	req := &Request{
		Zone: "corp.example.", Type: TypeIXFR, Proto: ProtoTCP,
		Source: opsAddr, ClientSerial: 100,
	}
	tr, err := srv.Begin(signedReq(req, opsKey))
	if err != nil {
		t.Fatalf("legit IXFR denied: %v", err)
	}
	all := drain(t, tr)
	if len(all) != 1 || all[0].Type != "SOA" {
		t.Fatalf("up-to-date IXFR should return only SOA, got %v", all)
	}
}
