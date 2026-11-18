package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type credential struct {
	private *ecdsa.PrivateKey
	key     DNSKEY
}

type fixture struct {
	t          *testing.T
	zone       string
	now        time.Time
	inception  time.Time
	expiration time.Time
	ttl        uint32

	current   credential
	currentDS DS

	next   credential
	nextDS DS
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	f := &fixture{
		t:          t,
		zone:       "child.example.",
		now:        now,
		inception:  now.Add(-time.Hour),
		expiration: now.Add(48 * time.Hour),
		ttl:        3600,
	}
	f.current = newCredential(t)
	f.next = newCredential(t)

	var err error
	f.currentDS, err = f.current.key.ToDS(f.zone, DigestSHA256)
	if err != nil {
		t.Fatalf("current DS: %v", err)
	}
	f.nextDS, err = f.next.key.ToDS(f.zone, DigestSHA256)
	if err != nil {
		t.Fatalf("next DS: %v", err)
	}
	return f
}

func newCredential(t *testing.T) credential {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	public := make([]byte, 64)
	private.X.FillBytes(public[:32])
	private.Y.FillBytes(public[32:])
	return credential{
		private: private,
		key: DNSKEY{
			Flags:     257,
			Protocol:  3,
			Algorithm: AlgorithmECDSAP256SHA256,
			PublicKey: public,
		},
	}
}

func (f *fixture) sign(cred credential, typ uint16, records [][]byte) RRSIG {
	f.t.Helper()
	sig, err := SignRRSet(f.zone, typ, records, cred.private, cred.key, f.inception, f.expiration, f.ttl)
	if err != nil {
		f.t.Fatalf("sign RRset type %d: %v", typ, err)
	}
	return sig
}

func (f *fixture) published() []DNSKEY {
	return []DNSKEY{f.current.key, f.next.key}
}

func (f *fixture) response(server string, cds []CDS, cdnskeys []CDNSKEY, published []DNSKEY) ServerResponse {
	f.t.Helper()
	resp := ServerResponse{
		Server:  server,
		CDS:     cds,
		CDNSKEY: cdnskeys,
		DNSKEY:  published,
	}
	resp.DNSKEYSignatures = []RRSIG{
		f.sign(f.current, TypeDNSKEY, dnskeyRData(published)),
	}
	if len(cds) != 0 {
		resp.CDSSignatures = []RRSIG{
			f.sign(f.current, TypeCDS, dsRData(cds)),
		}
	}
	if len(cdnskeys) != 0 {
		resp.CDNSKEYSignatures = []RRSIG{
			f.sign(f.current, TypeCDNSKEY, dnskeyRData(cdnskeys)),
		}
	}
	return resp
}

func (f *fixture) updateServers() []ServerResponse {
	return []ServerResponse{
		f.response("ns1", []CDS{f.nextDS}, []CDNSKEY{f.next.key}, f.published()),
		f.response("ns2", []CDS{f.nextDS}, []CDNSKEY{f.next.key}, f.published()),
		f.response("ns3", []CDS{f.nextDS}, []CDNSKEY{f.next.key}, f.published()),
	}
}

func (f *fixture) request(servers []ServerResponse) Request {
	return Request{
		Zone:      f.zone,
		CurrentDS: []DS{f.currentDS},
		Servers:   servers,
	}
}

func TestAcceptsConsistentUpdateAfterObservationPeriod(t *testing.T) {
	f := newFixture(t)
	req := f.request(f.updateServers())
	acceptor, err := NewAcceptor(Config{ObservationPeriod: 2 * time.Hour})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}

	result := acceptor.ObserveAt(f.now, req)
	if result.Status != StatusPending {
		t.Fatalf("first observation status = %s (%s), want pending", result.Status, result.Reason)
	}
	if result.ObservedFor != 0 {
		t.Fatalf("first observation duration = %v, want 0", result.ObservedFor)
	}

	result = acceptor.ObserveAt(f.now.Add(time.Hour), req)
	if result.Status != StatusPending {
		t.Fatalf("second observation status = %s (%s), want pending", result.Status, result.Reason)
	}

	result = acceptor.ObserveAt(f.now.Add(2*time.Hour), req)
	if result.Status != StatusAccepted || !result.Accepted {
		t.Fatalf("third observation status = %s (%s), want accepted", result.Status, result.Reason)
	}
	if result.Signal != SignalUpdate {
		t.Fatalf("signal = %s, want update", result.Signal)
	}
	if !reflect.DeepEqual(result.NewDS, []DS{f.nextDS}) {
		t.Fatalf("new DS = %v, want %v", result.NewDS, []DS{f.nextDS})
	}
	if !result.AcceptedAt.Equal(f.now.Add(2 * time.Hour)) {
		t.Fatalf("accepted at %v, want %v", result.AcceptedAt, f.now.Add(2*time.Hour))
	}
}

func TestSingleServerDivergenceIsRejected(t *testing.T) {
	f := newFixture(t)
	third := newCredential(t)
	thirdDS, err := third.key.ToDS(f.zone, DigestSHA256)
	if err != nil {
		t.Fatalf("third DS: %v", err)
	}

	servers := []ServerResponse{
		f.response("ns1", []CDS{f.nextDS}, []CDNSKEY{f.next.key}, f.published()),
		f.response("ns2", []CDS{f.nextDS}, []CDNSKEY{f.next.key}, f.published()),
		f.response("ns3", []CDS{thirdDS}, []CDNSKEY{third.key}, []DNSKEY{f.current.key, f.next.key, third.key}),
	}
	req := f.request(servers)
	acceptor, err := NewAcceptor(Config{ObservationPeriod: 2 * time.Hour})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}

	result := acceptor.ObserveAt(f.now, req)
	if result.Status != StatusRejected {
		t.Fatalf("status = %s, want rejected for one divergent server", result.Status)
	}
	if !strings.Contains(result.Reason, "different CDS set") {
		t.Fatalf("reason = %q, want a CDS divergence reason", result.Reason)
	}
	if result.Accepted {
		t.Fatal("divergent observation must not be accepted")
	}

	result = acceptor.ObserveAt(f.now.Add(3*time.Hour), req)
	if result.Status != StatusRejected || result.Accepted {
		t.Fatalf("status after deadline = %s accepted=%v, want rejected", result.Status, result.Accepted)
	}
}

func TestCDSSignedByWrongKeyIsRejected(t *testing.T) {
	f := newFixture(t)
	base := f.response("ns1", []CDS{f.nextDS}, nil, f.published())

	t.Run("signature metadata names another KSK", func(t *testing.T) {
		resp := base
		resp.CDSSignatures = []RRSIG{
			f.sign(f.next, TypeCDS, dsRData(resp.CDS)),
		}
		_, err := Evaluate(f.now, f.request([]ServerResponse{resp}))
		if err == nil {
			t.Fatal("Evaluate accepted a CDS RRset signed by a key other than the current DS KSK")
		}
		if !strings.Contains(err.Error(), "CDS RRset") {
			t.Fatalf("error = %v, want a CDS signature failure", err)
		}
	})

	t.Run("signature bytes do not match advertised KSK", func(t *testing.T) {
		resp := base
		resp.CDSSignatures = []RRSIG{
			f.sign(credential{private: f.next.private, key: f.current.key}, TypeCDS, dsRData(resp.CDS)),
		}
		_, err := Evaluate(f.now, f.request([]ServerResponse{resp}))
		if err == nil {
			t.Fatal("Evaluate accepted an invalid CDS signature")
		}
		if !strings.Contains(err.Error(), "no valid RRSIG") {
			t.Fatalf("error = %v, want an invalid signature failure", err)
		}
	})
}

func TestMixedDeleteRecordIsRejected(t *testing.T) {
	f := newFixture(t)

	t.Run("delete plus update CDS", func(t *testing.T) {
		resp := f.response("ns1", []CDS{DeleteCDS(), f.nextDS}, nil, f.published())
		_, err := Evaluate(f.now, f.request([]ServerResponse{resp}))
		if err == nil {
			t.Fatal("Evaluate accepted a mixed delete/update CDS set")
		}
		if !strings.Contains(err.Error(), "must not be mixed") {
			t.Fatalf("error = %v, want a mixed-delete failure", err)
		}
	})

	t.Run("delete plus CDNSKEY", func(t *testing.T) {
		resp := f.response("ns1", []CDS{DeleteCDS()}, []CDNSKEY{f.next.key}, f.published())
		_, err := Evaluate(f.now, f.request([]ServerResponse{resp}))
		if err == nil {
			t.Fatal("Evaluate accepted a delete CDS alongside CDNSKEY records")
		}
		if !strings.Contains(err.Error(), "must not be mixed") {
			t.Fatalf("error = %v, want a mixed-delete failure", err)
		}
	})
}

func TestObservationInterruptionRestartsWindow(t *testing.T) {
	f := newFixture(t)
	req := f.request(f.updateServers())
	acceptor, err := NewAcceptor(Config{ObservationPeriod: 2 * time.Hour})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}

	if result := acceptor.ObserveAt(f.now, req); result.Status != StatusPending {
		t.Fatalf("initial status = %s (%s), want pending", result.Status, result.Reason)
	}
	if result := acceptor.ObserveAt(f.now.Add(30*time.Minute), req); result.Status != StatusPending {
		t.Fatalf("status before interruption = %s (%s), want pending", result.Status, result.Reason)
	}

	broken := req
	broken.Servers = append([]ServerResponse(nil), req.Servers...)
	broken.Servers[1].Err = errors.New("timeout")
	if result := acceptor.ObserveAt(f.now.Add(time.Hour), broken); result.Status != StatusRejected {
		t.Fatalf("interrupted status = %s (%s), want rejected", result.Status, result.Reason)
	}

	resumed := f.now.Add(90 * time.Minute)
	result := acceptor.ObserveAt(resumed, req)
	if result.Status != StatusPending {
		t.Fatalf("resumed status = %s (%s), want pending", result.Status, result.Reason)
	}
	if !result.Since.Equal(resumed) {
		t.Fatalf("window since = %v, want reset at %v", result.Since, resumed)
	}

	result = acceptor.ObserveAt(f.now.Add(2*time.Hour), req)
	if result.Status != StatusPending {
		t.Fatalf("status at original deadline = %s (%s), want pending after reset", result.Status, result.Reason)
	}

	result = acceptor.ObserveAt(resumed.Add(2*time.Hour), req)
	if result.Status != StatusAccepted {
		t.Fatalf("status after full new window = %s (%s), want accepted", result.Status, result.Reason)
	}
}

func TestMaxObservationGapRestartsWindow(t *testing.T) {
	f := newFixture(t)
	req := f.request(f.updateServers())
	acceptor, err := NewAcceptor(Config{
		ObservationPeriod: 2 * time.Hour,
		MaxObservationGap: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}

	if result := acceptor.ObserveAt(f.now, req); result.Status != StatusPending {
		t.Fatalf("initial status = %s (%s), want pending", result.Status, result.Reason)
	}
	result := acceptor.ObserveAt(f.now.Add(2*time.Hour), req)
	if result.Status != StatusPending {
		t.Fatalf("status after sparse poll = %s (%s), want pending", result.Status, result.Reason)
	}
	if !result.Since.Equal(f.now.Add(2 * time.Hour)) {
		t.Fatalf("window since = %v, want reset at %v", result.Since, f.now.Add(2*time.Hour))
	}
	if !strings.Contains(result.Reason, "gap") {
		t.Fatalf("reason = %q, want an observation-gap reason", result.Reason)
	}

	if result := acceptor.ObserveAt(f.now.Add(150*time.Minute), req); result.Status != StatusPending {
		t.Fatalf("status at half window = %s (%s), want pending", result.Status, result.Reason)
	}
	if result := acceptor.ObserveAt(f.now.Add(210*time.Minute), req); result.Status != StatusPending {
		t.Fatalf("status at 1.5 windows = %s (%s), want pending", result.Status, result.Reason)
	}
	if result := acceptor.ObserveAt(f.now.Add(4*time.Hour), req); result.Status != StatusAccepted {
		t.Fatalf("status after full window = %s (%s), want accepted", result.Status, result.Reason)
	}
}

func TestNewDSMustMatchPublishedDNSKEY(t *testing.T) {
	f := newFixture(t)
	third := newCredential(t)
	thirdDS, err := third.key.ToDS(f.zone, DigestSHA256)
	if err != nil {
		t.Fatalf("third DS: %v", err)
	}
	resp := f.response("ns1", []CDS{thirdDS}, nil, f.published())
	_, err = Evaluate(f.now, f.request([]ServerResponse{resp}))
	if err == nil {
		t.Fatal("Evaluate accepted a CDS that does not match a published DNSKEY")
	}
	if !strings.Contains(err.Error(), "does not match a published DNSKEY") {
		t.Fatalf("error = %v, want a published-DNSKEY mismatch", err)
	}
}

func TestCDSAndCDNSKEYMustCorrespond(t *testing.T) {
	f := newFixture(t)
	resp := f.response("ns1", []CDS{f.nextDS}, []CDNSKEY{f.current.key}, f.published())
	_, err := Evaluate(f.now, f.request([]ServerResponse{resp}))
	if err == nil {
		t.Fatal("Evaluate accepted a CDS that has no matching CDNSKEY")
	}
	if !strings.Contains(err.Error(), "no matching CDNSKEY") {
		t.Fatalf("error = %v, want a CDS/CDNSKEY correspondence failure", err)
	}
}

func TestCDNSKEYOnlyDerivesDS(t *testing.T) {
	f := newFixture(t)
	resp := f.response("ns1", nil, []CDNSKEY{f.next.key}, f.published())
	candidate, err := Evaluate(f.now, f.request([]ServerResponse{resp}))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if candidate.Signal != SignalUpdate {
		t.Fatalf("signal = %s, want update", candidate.Signal)
	}
	if !reflect.DeepEqual(candidate.NewDS, []DS{f.nextDS}) {
		t.Fatalf("derived DS = %v, want %v", candidate.NewDS, []DS{f.nextDS})
	}

	acceptor, err := NewAcceptor(Config{
		CDNSKEYDigestTypes: []uint8{DigestSHA1, DigestSHA384},
	})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}
	result := acceptor.ObserveAt(f.now, f.request([]ServerResponse{resp}))
	if result.Status != StatusAccepted {
		t.Fatalf("status = %s (%s), want accepted", result.Status, result.Reason)
	}
	if len(result.NewDS) != 2 {
		t.Fatalf("derived %d DS records, want 2", len(result.NewDS))
	}
}

func TestDeleteSignalAcceptedAfterObservationPeriod(t *testing.T) {
	f := newFixture(t)
	resp := f.response("ns1", []CDS{DeleteCDS()}, nil, []DNSKEY{f.current.key})
	req := f.request([]ServerResponse{resp})
	acceptor, err := NewAcceptor(Config{ObservationPeriod: time.Hour})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}

	if result := acceptor.ObserveAt(f.now, req); result.Status != StatusPending || !result.Delete {
		t.Fatalf("first status = %s delete=%v, want pending delete", result.Status, result.Delete)
	}
	result := acceptor.ObserveAt(f.now.Add(time.Hour), req)
	if result.Status != StatusAccepted || !result.Delete {
		t.Fatalf("final status = %s delete=%v (%s), want accepted delete", result.Status, result.Delete, result.Reason)
	}
	if len(result.NewDS) != 0 {
		t.Fatalf("delete NewDS = %v, want empty", result.NewDS)
	}
}

func TestNoSignalResetsWindow(t *testing.T) {
	f := newFixture(t)
	acceptor, err := NewAcceptor(Config{ObservationPeriod: time.Hour})
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}
	if result := acceptor.ObserveAt(f.now, f.request(f.updateServers())); result.Status != StatusPending {
		t.Fatalf("update status = %s (%s), want pending", result.Status, result.Reason)
	}

	empty := f.request([]ServerResponse{
		{Server: "ns1"},
		{Server: "ns2"},
		{Server: "ns3"},
	})
	if result := acceptor.ObserveAt(f.now.Add(30*time.Minute), empty); result.Status != StatusNoSignal {
		t.Fatalf("empty status = %s, want no-signal", result.Status)
	}
	result := acceptor.ObserveAt(f.now.Add(2*time.Hour), f.request(f.updateServers()))
	if result.Status != StatusPending {
		t.Fatalf("status after no-signal = %s (%s), want pending", result.Status, result.Reason)
	}
}

func TestDeleteRecordParsing(t *testing.T) {
	record, err := ParseDS("0 0 0 00")
	if err != nil {
		t.Fatalf("ParseDS: %v", err)
	}
	if !record.IsDelete() {
		t.Fatalf("parsed record %#v is not a delete signal", record)
	}
	if got := record.String(); got != "0 0 0 00" {
		t.Fatalf("delete string = %q, want %q", got, "0 0 0 00")
	}
}
