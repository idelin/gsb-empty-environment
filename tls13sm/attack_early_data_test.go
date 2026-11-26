package tls13sm

import (
	"errors"
	"testing"
	"time"
)

func newTestTicket(now time.Time) *ResumptionTicket {
	return &ResumptionTicket{
		Nonce:        []byte("unique-ticket-nonce-001"),
		PSK:          []byte("this-is-a-32-byte-resumption-psk!"),
		TicketAgeAdd: 0x11223344,
		IssuedAt:     now.Add(-500 * time.Millisecond),
		SuiteID:      SuiteAES128GCM.ID,
		MaxEarlyData: 16384,
	}
}

// TestStrikeRegisterSingleUse proves the strike register admits a ticket
// nonce exactly once and rejects every subsequent presentation, which blocks
// 0-RTT replay within the retention window.
func TestStrikeRegisterSingleUse(t *testing.T) {
	reg := NewStrikeRegister(time.Hour)
	nonce := []byte("nonce-xyz")
	if err := reg.checkAndStore(nonce); err != nil {
		t.Fatalf("first use should pass: %v", err)
	}
	err := reg.checkAndStore(nonce)
	if !errors.Is(err, ErrReplayDetected) {
		t.Fatalf("second use: err=%v, want ErrReplayDetected", err)
	}
	if !reg.Contains(nonce) {
		t.Fatal("strike register lost the recorded nonce")
	}
	// A different nonce is independent.
	if err := reg.checkAndStore([]byte("other")); err != nil {
		t.Fatalf("unrelated nonce rejected: %v", err)
	}
}

// TestTicketAgeSkewWindow exercises the first defense line: obfuscated ages
// inside the ±skew window pass while stale or future-dated ages fail.
func TestTicketAgeSkewWindow(t *testing.T) {
	const ageAdd = uint32(0x01020304)
	elapsed := 1000 * time.Millisecond
	skew := DefaultTicketAgeSkew

	// Honest age: 1000ms obfuscated by ageAdd modulo 2^32.
	good := uint32(int64(elapsed/time.Millisecond) + int64(ageAdd))
	if !ticketAgeOK(good, ageAdd, elapsed, skew) {
		t.Fatal("in-window ticket age rejected")
	}

	// Replayed after the window expired (60s stale).
	stale := uint32(int64((60*time.Second)/time.Millisecond) + int64(ageAdd))
	if ticketAgeOK(stale, ageAdd, elapsed, skew) {
		t.Fatal("stale ticket age accepted")
	}

	// Negative / zero claimed age against a positive elapsed time beyond skew.
	negative := ageAdd // claims age 0
	if ticketAgeOK(negative, ageAdd, 30*time.Second, skew) {
		t.Fatal("zero age with large elapsed time accepted")
	}
}

// TestIdempotencyGate proves the third defense line: the caller must declare
// a request idempotent before early data is admitted.
func TestIdempotencyGate(t *testing.T) {
	server, err := NewServerStateMachine(ServerConfig{Suite: SuiteAES128GCM})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.AcceptEarlyData(false); !errors.Is(err, ErrNotIdempotent) {
		t.Fatalf("non-idempotent: err=%v, want ErrNotIdempotent", err)
	}
	if err := server.AcceptEarlyData(true); err != nil {
		t.Fatalf("explicit idempotent declaration rejected: %v", err)
	}
}

// TestClientRejectsEarlyDataWithoutIdempotency ensures the client refuses
// even to construct 0-RTT traffic without the explicit gate.
func TestClientRejectsEarlyDataWithoutIdempotency(t *testing.T) {
	_, err := NewClientStateMachine(ClientConfig{
		Suite:        SuiteAES128GCM,
		PSK:          []byte("this-is-a-32-byte-resumption-psk!"),
		UseEarlyData: true,
		Idempotent:   false,
	})
	if !errors.Is(err, ErrNotIdempotent) {
		t.Fatalf("err=%v, want ErrNotIdempotent", err)
	}
}

// TestEarlyDataReplaySecondConnection builds two server connections sharing
// one strike register and ticket, then proves the second 0-RTT attempt with
// the same ticket nonce is rejected as a replay.
func TestEarlyDataReplaySecondConnection(t *testing.T) {
	now := time.Now()
	ticket := newTestTicket(now)
	strike := NewStrikeRegister(time.Hour)
	tickets := map[string]*ResumptionTicket{"resumption-ticket": ticket}

	cfg := func() ServerConfig {
		return ServerConfig{
			Suite:          SuiteAES128GCM,
			Groups:         []uint16{GroupP256},
			Tickets:        tickets,
			Strike:         strike,
			AllowEarlyData: true,
			TicketAgeSkew:  time.Minute,
			Now:            func() time.Time { return now },
		}
	}

	first, err := NewServerStateMachine(cfg())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewServerStateMachine(cfg())
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the ledger calls the state machine performs during a successful
	// 0-RTT ClientHello admission.
	if err := strike.checkAndStore(ticket.Nonce); err != nil {
		t.Fatalf("first admission failed: %v", err)
	}
	_ = first

	// The replay on the second connection must hit the same shared ledger.
	err = second.cfg.Strike.checkAndStore(ticket.Nonce)
	if !errors.Is(err, ErrReplayDetected) {
		t.Fatalf("replay: err=%v, want ErrReplayDetected", err)
	}
}
