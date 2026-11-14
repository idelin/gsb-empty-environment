package netsec

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

var (
	testMaster   = []byte("0123456789abcdef0123456789abcdef")
	testRecovery = []byte("recovery-key-000000000000000000")
)

func testConfig(clock Clock, gen CIDGenerator, journal Journal, route RoutePartition, label string) Config {
	return Config{
		Clock:             clock,
		Generator:         gen,
		Route:             route,
		MaxPacketLifetime: 10 * time.Second,
		ConfirmationGrace: 2 * time.Second,
		MasterSecret:      testMaster,
		RecoveryKey:       testRecovery,
		Journal:           journal,
		Label:             label,
	}
}

func mustManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestTokenDerivationBindsGenerationRouteAndFullCID(t *testing.T) {
	key := bytes.Repeat([]byte{0x5a}, 32)
	cid1 := []byte{0x01, 0x02, 0x03}
	cid2 := []byte{0x01, 0x02, 0x04}

	base, err := DeriveResetToken(key, 0, "route-a", cid1)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DeriveResetToken(key, 0, "route-a", cid1)
	if err != nil {
		t.Fatal(err)
	}
	if !base.Equal(again) {
		t.Fatal("token derivation is not deterministic")
	}
	cases := []struct {
		name string
		got  ResetToken
	}{
		{"generation", func() ResetToken {
			x, _ := DeriveResetToken(key, 1, "route-a", cid1)
			return x
		}()},
		{"route", func() ResetToken {
			x, _ := DeriveResetToken(key, 0, "route-b", cid1)
			return x
		}()},
		{"cid", func() ResetToken {
			x, _ := DeriveResetToken(key, 0, "route-a", cid2)
			return x
		}()},
	}
	for _, tc := range cases {
		if base.Equal(tc.got) {
			t.Fatalf("%s is not bound into the token", tc.name)
		}
	}
	if _, err := DeriveResetToken(key[:8], 0, "route-a", cid1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("short key error = %v", err)
	}
}

func TestTokenLedgerNeverReusesAValue(t *testing.T) {
	ledger := NewTokenLedger()
	var token ResetToken
	for i := range token {
		token[i] = byte(i + 1)
	}
	at := time.Unix(50, 0).UTC()
	if !ledger.Reserve(token, at) {
		t.Fatal("first reservation failed")
	}
	if ledger.Reserve(token, at.Add(time.Hour)) {
		t.Fatal("token value was reused")
	}
	if !ledger.Used(token) || ledger.Len() != 1 {
		t.Fatalf("ledger state used=%v len=%d", ledger.Used(token), ledger.Len())
	}
}

func TestLifecycleRetentionEndsOnlyAfterBothWindows(t *testing.T) {
	start := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	gen := SequentialCIDGenerator([]byte{0xaa}, 8)
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))

	grant, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m.CIDInfo(grant.CID); got.State != CIDIssued {
		t.Fatalf("state after issue = %v", got.State)
	}
	if tr, err := m.ConfirmPeerUse(grant.CID); err != nil || !tr.Changed {
		t.Fatalf("confirm: %v changed=%v", err, tr.Changed)
	}
	if tr, err := m.RequestRetire(grant.CID, "retire_prior_to"); err != nil || tr.To != CIDRetiring {
		t.Fatalf("retire request: %v %v", err, tr)
	}

	clock.Advance(5 * time.Second)
	ret, err := m.RetireFromPeer(grant.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	if ret.Outcome != RetireApplied || !ret.Changed {
		t.Fatalf("peer retire outcome = %v", ret.Outcome)
	}
	deadline := clock.Now().Add(12 * time.Second)
	info, _ := m.CIDInfo(grant.CID)
	if !info.RetentionDeadline.Equal(deadline) {
		t.Fatalf("deadline = %v, want %v", info.RetentionDeadline, deadline)
	}

	clock.Advance(11 * time.Second)
	if _, err := m.PurgeExpired(); err != nil {
		t.Fatal(err)
	}
	info, _ = m.CIDInfo(grant.CID)
	if info.State != CIDRetired {
		t.Fatalf("expired before both windows ended: %v", info.State)
	}
	if token, ok := m.TokenForCID(grant.CID); !ok || !token.Equal(grant.Token) {
		t.Fatal("token was removed before the retention deadline")
	}

	clock.Advance(1 * time.Second)
	witnesses, err := m.PurgeExpired()
	if err != nil {
		t.Fatal(err)
	}
	if len(witnesses) != 1 || witnesses[0].Kind != AuditCIDExpired {
		t.Fatalf("expiry witnesses = %+v", witnesses)
	}
	info, _ = m.CIDInfo(grant.CID)
	if info.State != CIDExpired {
		t.Fatalf("state at deadline = %v", info.State)
	}
	if _, ok := m.TokenForCID(grant.CID); ok {
		t.Fatal("expired token is still matchable")
	}
	if refs := m.KeyStatus(0).KeyReferences; refs != 0 {
		t.Fatalf("key references after expiry = %d", refs)
	}
	if err := VerifyAuditChain(m.AuditLog()); err != nil {
		t.Fatalf("audit chain: %v", err)
	}
}

func TestCIDCannotBeReissuedDuringQuarantine(t *testing.T) {
	clock := NewManualClock(time.Unix(100, 0).UTC())
	fixed := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}
	gen := CIDGeneratorFunc(func() ([]byte, error) { return append([]byte(nil), fixed...), nil })
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))

	grant, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.IssueCID(); !errors.Is(err, ErrCIDReused) {
		t.Fatalf("second issue error = %v, want ErrCIDReused", err)
	}
	audit := m.AuditLog()
	if audit[len(audit)-1].Kind != AuditCIDReuseRejected {
		t.Fatalf("last audit = %v", audit[len(audit)-1].Kind)
	}
	if _, err := m.RetireFromPeer(grant.Sequence); err != nil {
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second)
	if _, err := m.PurgeExpired(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.IssueCID(); !errors.Is(err, ErrCIDReused) {
		t.Fatalf("CID reissued after quarantine: %v", err)
	}
}

func TestOutOfOrderRetireNeverClosesConnection(t *testing.T) {
	clock := NewManualClock(time.Unix(200, 0).UTC())
	gen := SequentialCIDGenerator([]byte{0xbb}, 8)
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))
	var grants []CIDGrant
	for i := 0; i < 3; i++ {
		g, err := m.IssueCID()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.ConfirmPeerUse(g.CID); err != nil {
			t.Fatal(err)
		}
		grants = append(grants, g)
	}
	for _, seq := range []uint64{2, 0, 1} {
		r, err := m.RetireFromPeer(seq)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != RetireApplied {
			t.Fatalf("retire %d outcome = %v", seq, r.Outcome)
		}
	}
	if r, _ := m.RetireFromPeer(2); r.Outcome != RetireDuplicate {
		t.Fatalf("duplicate retire outcome = %v", r.Outcome)
	}
	if r, _ := m.RetireFromPeer(99); r.Outcome != RetireUnknown {
		t.Fatalf("unknown retire outcome = %v", r.Outcome)
	}
	if m.IsClosed() {
		t.Fatal("RETIRE_CONNECTION_ID closed the connection")
	}
	for _, g := range grants {
		if _, ok := m.TokenForCID(g.CID); !ok {
			t.Fatalf("token for retiring CID %d removed too early", g.Sequence)
		}
	}
}

func TestLostControlFramesLeaveIdempotentState(t *testing.T) {
	clock := NewManualClock(time.Unix(250, 0).UTC())
	gen := SequentialCIDGenerator([]byte{0xbc}, 8)
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))
	g, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	// The NEW_CONNECTION_ID frame was lost, so the CID never becomes active.
	clock.Advance(time.Second)
	if info, _ := m.CIDInfo(g.CID); info.State != CIDIssued {
		t.Fatalf("lost NEW_CONNECTION_ID changed state: %v", info.State)
	}
	if token, ok := m.TokenForCID(g.CID); !ok || !token.Equal(g.Token) {
		t.Fatal("token lost before CID was confirmed")
	}
	// A local retire request is retransmitted after loss.
	t1, err := m.RequestRetire(g.CID, "retire_prior_to")
	if err != nil || !t1.Changed {
		t.Fatalf("first retire request: %v %+v", err, t1)
	}
	count := len(m.AuditLog())
	t2, err := m.RequestRetire(g.CID, "retire_prior_to")
	if err != nil || t2.Changed || len(m.AuditLog()) != count {
		t.Fatalf("retransmitted retire request duplicated transition: %+v", t2)
	}
	// The peer's confirmation was lost too; the token stays live.
	clock.Advance(time.Second)
	if token, ok := m.TokenForCID(g.CID); !ok || !token.Equal(g.Token) {
		t.Fatal("token lost while peer confirmation was missing")
	}
	r1, err := m.RetireFromPeer(g.Sequence)
	if err != nil || r1.Outcome != RetireApplied {
		t.Fatalf("late peer retire: %v %+v", err, r1)
	}
	r2, err := m.RetireFromPeer(g.Sequence)
	if err != nil || r2.Outcome != RetireDuplicate || r2.Changed {
		t.Fatalf("retransmitted peer retire: %+v", r2)
	}
	if m.IsClosed() {
		t.Fatal("loss/retransmission closed the connection")
	}
}

func TestUnconfirmedRetireExpiresAfterBoundedGrace(t *testing.T) {
	clock := NewManualClock(time.Unix(275, 0).UTC())
	m := mustManager(t, testConfig(clock, SequentialCIDGenerator([]byte{0xbd}, 8), nil, "edge-a", "c1"))
	g, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RequestRetire(g.CID, "lost_retire"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second)
	if _, err := m.PurgeExpired(); err != nil {
		t.Fatal(err)
	}
	if info, _ := m.CIDInfo(g.CID); info.State != CIDRetiring {
		t.Fatalf("expired before both windows: %v", info.State)
	}
	clock.Advance(time.Second)
	if _, err := m.PurgeExpired(); err != nil {
		t.Fatal(err)
	}
	if info, _ := m.CIDInfo(g.CID); info.State != CIDExpired {
		t.Fatalf("unconfirmed retire did not expire: %v", info.State)
	}
	if _, ok := m.TokenForCID(g.CID); ok {
		t.Fatal("unconfirmed retire retained its token forever")
	}
	if refs := m.KeyStatus(0).KeyReferences; refs != 0 {
		t.Fatalf("unconfirmed retire leaked %d key references", refs)
	}
	if m.IsClosed() {
		t.Fatal("local retirement closed the connection")
	}
}

func TestNATRebindAndMalformedPacketsDoNotClose(t *testing.T) {
	clock := NewManualClock(time.Unix(300, 0).UTC())
	gen := SequentialCIDGenerator([]byte{0xcc}, 8)
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))
	g, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.HandleNATRebind("198.51.100.7:443")
	if err != nil {
		t.Fatal(err)
	}
	if w.Kind != AuditPathRebound || m.IsClosed() {
		t.Fatalf("rebind witness=%v closed=%v", w.Kind, m.IsClosed())
	}
	valid := BuildResetPacket([]byte{0x40, 0x11, 0x22, 0x33, 0x44}, g.Token)
	probeFalse := func([]byte) bool { return false }
	probeTrue := func([]byte) bool { return true }

	tests := []struct {
		name   string
		packet []byte
		opts   ResetOptions
		reason ResetReason
	}{
		{"short", valid[:10], ResetOptions{Route: "edge-a", Probe: probeFalse}, ResetTooShort},
		{"fixed-bit", func() []byte {
			p := append([]byte(nil), valid...)
			p[0] &^= 0x40
			return p
		}(), ResetOptions{Route: "edge-a", Probe: probeFalse}, ResetBadFixedBit},
		{"long-header", func() []byte {
			p := append([]byte(nil), valid...)
			p[0] |= 0x80
			return p
		}(), ResetOptions{Route: "edge-a", Probe: probeFalse}, ResetLongHeader},
		{"decryptable", valid, ResetOptions{Route: "edge-a", Probe: probeTrue}, ResetDecryptable},
		{"no-probe", valid, ResetOptions{Route: "edge-a"}, ResetNoProbe},
		{"wrong-token", func() []byte {
			p := append([]byte(nil), valid...)
			p[len(p)-1] ^= 0xff
			return p
		}(), ResetOptions{Route: "edge-a", Probe: probeFalse}, ResetNoToken},
		{"wrong-route", valid, ResetOptions{Route: "edge-b", Probe: probeFalse}, ResetWrongRoute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := m.DetectReset(tc.packet, tc.opts)
			if d.Matched || d.Closed {
				t.Fatalf("malformed packet matched/closed: %+v", d)
			}
			if d.Reason != tc.reason {
				t.Fatalf("reason = %v, want %v", d.Reason, tc.reason)
			}
		})
	}
	if m.IsClosed() {
		t.Fatal("ordinary invalid packets closed the connection")
	}
}

func TestLegitimateResetClosesWithAuditWitness(t *testing.T) {
	clock := NewManualClock(time.Unix(400, 0).UTC())
	gen := SequentialCIDGenerator([]byte{0xdd}, 8)
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))
	g, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	packet := BuildResetPacket([]byte{0x40, 0x01, 0x02, 0x03, 0x04}, g.Token)
	d := m.DetectReset(packet, ResetOptions{
		Route: "edge-a",
		Probe: func(p []byte) bool { return false },
	})
	if !d.Matched || !d.Closed || d.Reason != ResetAccepted {
		t.Fatalf("decision = %+v", d)
	}
	if d.Witness == nil || d.Witness.Kind != AuditResetAccepted {
		t.Fatalf("reset witness = %+v", d.Witness)
	}
	if d.Close == nil || d.Close.CIDsClosed != 1 {
		t.Fatalf("close result = %+v", d.Close)
	}
	if !m.IsClosed() {
		t.Fatal("valid reset did not close connection")
	}
	if info, _ := m.CIDInfo(g.CID); info.State != CIDClosed {
		t.Fatalf("CID state = %v", info.State)
	}

	// Replaying the old reset token after close is harmless.
	d2 := m.DetectReset(packet, ResetOptions{Route: "edge-a", CallerVerifiedUndecryptable: true})
	if !d2.Matched || d2.Closed || d2.Reason != ResetAlreadyClosed {
		t.Fatalf("replayed reset = %+v", d2)
	}
	if err := VerifyAuditChain(m.AuditLog()); err != nil {
		t.Fatal(err)
	}
}

func TestTokenCollisionIsAmbiguousAndClosesNothing(t *testing.T) {
	clock := NewManualClock(time.Unix(500, 0).UTC())
	gen := SequentialCIDGenerator([]byte{0xee}, 8)
	m := mustManager(t, testConfig(clock, gen, nil, "edge-a", "c1"))
	g, err := m.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	// Inject a second live record with the same token, as an adversarial
	// collision would appear after a broken derivation/generator.
	rec := &cidRecord{
		cid:        []byte{0xee, 0, 0, 0, 0, 0, 0, 2},
		key:        string([]byte{0xee, 0, 0, 0, 0, 0, 0, 2}),
		sequence:   2,
		generation: g.Generation,
		route:      "edge-a",
		token:      g.Token,
		state:      CIDActive,
		issuedAt:   clock.Now(),
	}
	m.records[rec.key] = rec
	m.bySeq[rec.sequence] = rec
	m.tokens[rec.token] = append(m.tokens[rec.token], rec)

	packet := BuildResetPacket([]byte{0x40, 0x01, 0x02, 0x03, 0x04}, g.Token)
	d := m.DetectReset(packet, ResetOptions{Route: "edge-a", Probe: func([]byte) bool { return false }})
	if d.Reason != ResetAmbiguous || d.Matched || d.Closed {
		t.Fatalf("collision decision = %+v", d)
	}
	if d.Witness == nil || d.Witness.Kind != AuditResetAmbiguous {
		t.Fatalf("ambiguity witness = %+v", d.Witness)
	}
	if m.IsClosed() {
		t.Fatal("token collision closed a connection")
	}
}
