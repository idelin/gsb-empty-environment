package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"
)

var testStart = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestRouter(t *testing.T, clk *clock, opts ...Option) *Router {
	t.Helper()
	opts = append([]Option{WithClock(clk.Now)}, opts...)
	r := NewRouter(opts...)
	for _, d := range []Device{
		{ID: "phone", AccountID: "acct-alice", Authorized: true},
		{ID: "laptop", AccountID: "acct-alice", Authorized: true},
		{ID: "tablet-revoked", AccountID: "acct-alice", Authorized: false},
		{ID: "attacker", AccountID: "acct-mallory", Authorized: true},
	} {
		if err := r.RegisterDevice(d); err != nil {
			t.Fatalf("register %s: %v", d.ID, err)
		}
	}
	return r
}

func newEntry(clk *clock, id, account, source string, typ EntryType, payload []byte) Entry {
	return Entry{
		ID:             id,
		AccountID:      account,
		SourceDeviceID: source,
		Type:           typ,
		Payload:        payload,
		CreatedAt:      clk.Now(),
		ExpiresAt:      clk.Now().Add(5 * time.Minute),
	}
}

func TestLegitimateSync(t *testing.T) {
	clk := &clock{now: testStart}
	r := newTestRouter(t, clk)

	e := newEntry(clk, "e1", "acct-alice", "phone", TypeText, []byte("hello clipboard"))
	if err := r.Route(e, "laptop"); err != nil {
		t.Fatalf("route: %v", err)
	}

	got := r.DeliveredTo("laptop")
	if len(got) != 1 || !bytes.Equal(got[0].Payload, e.Payload) {
		t.Fatalf("laptop deliveries = %+v, want the entry", got)
	}
	if n := len(r.DeliveredTo("attacker")); n != 0 {
		t.Fatalf("attacker received %d entries, want 0", n)
	}
	if h := r.History("acct-alice"); len(h) != 1 || h[0].ID != "e1" {
		t.Fatalf("history = %+v, want [e1]", h)
	}
}

func TestDeviceSubstitution(t *testing.T) {
	clk := &clock{now: testStart}
	r := newTestRouter(t, clk)
	e := newEntry(clk, "e1", "acct-alice", "phone", TypeText, []byte("secret note"))

	cases := []struct {
		name   string
		target string
		want   error
	}{
		{"attacker device on another account", "attacker", ErrTargetAccountMismatch},
		{"revoked device on same account", "tablet-revoked", ErrTargetDeviceRevoked},
		{"unregistered device", "ghost", ErrTargetDeviceUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := r.Route(e, tc.target); !errors.Is(err, tc.want) {
				t.Fatalf("route to %q: err = %v, want %v", tc.target, err, tc.want)
			}
			if n := len(r.DeliveredTo(tc.target)); n != 0 {
				t.Fatalf("%q received %d entries, want 0", tc.target, n)
			}
		})
	}

	// A device revoked after registration must stop receiving immediately.
	r.RevokeDevice("laptop")
	if err := r.Route(e, "laptop"); !errors.Is(err, ErrTargetDeviceRevoked) {
		t.Fatalf("route to revoked laptop: err = %v, want %v", err, ErrTargetDeviceRevoked)
	}
}

func TestAccountConfusion(t *testing.T) {
	clk := &clock{now: testStart}
	r := newTestRouter(t, clk)

	// Entry claims Alice's account but originates from Mallory's device.
	forged := newEntry(clk, "e1", "acct-alice", "attacker", TypeText, []byte("forged"))
	if err := r.Route(forged, "laptop"); !errors.Is(err, ErrSourceAccountMismatch) {
		t.Fatalf("forged source: err = %v, want %v", err, ErrSourceAccountMismatch)
	}

	// Entry claims Mallory's account but is routed to Alice's device.
	cross := newEntry(clk, "e2", "acct-mallory", "attacker", TypeText, []byte("cross"))
	if err := r.Route(cross, "laptop"); !errors.Is(err, ErrTargetAccountMismatch) {
		t.Fatalf("cross-account target: err = %v, want %v", err, ErrTargetAccountMismatch)
	}

	// Revoked or unknown sources cannot inject entries either.
	revokedSrc := newEntry(clk, "e3", "acct-alice", "tablet-revoked", TypeText, []byte("x"))
	if err := r.Route(revokedSrc, "laptop"); !errors.Is(err, ErrSourceDeviceRevoked) {
		t.Fatalf("revoked source: err = %v, want %v", err, ErrSourceDeviceRevoked)
	}
	unknownSrc := newEntry(clk, "e4", "acct-alice", "ghost", TypeText, []byte("x"))
	if err := r.Route(unknownSrc, "laptop"); !errors.Is(err, ErrSourceDeviceUnknown) {
		t.Fatalf("unknown source: err = %v, want %v", err, ErrSourceDeviceUnknown)
	}

	if n := len(r.DeliveredTo("laptop")); n != 0 {
		t.Fatalf("laptop received %d entries, want 0", n)
	}
	if n := len(r.History("acct-alice")); n != 0 {
		t.Fatalf("alice history has %d entries, want 0", n)
	}
}

func TestSensitiveTypes(t *testing.T) {
	clk := &clock{now: testStart}

	t.Run("blocked by default", func(t *testing.T) {
		r := newTestRouter(t, clk)
		for _, typ := range []EntryType{TypePassword, TypeToken} {
			e := newEntry(clk, "e-"+typ.String(), "acct-alice", "phone", typ, []byte("hunter2"))
			if err := r.Route(e, "laptop"); !errors.Is(err, ErrSensitiveBlocked) {
				t.Fatalf("%s: err = %v, want %v", typ, err, ErrSensitiveBlocked)
			}
		}
		if n := len(r.DeliveredTo("laptop")); n != 0 {
			t.Fatalf("laptop received %d sensitive entries, want 0", n)
		}
	})

	t.Run("requires confirmation when policy allows", func(t *testing.T) {
		r := newTestRouter(t, clk, WithSensitivePolicy(SensitiveConfirm))
		e := newEntry(clk, "tok1", "acct-alice", "phone", TypeToken, []byte("bearer abc"))

		if err := r.Route(e, "laptop"); !errors.Is(err, ErrConfirmationRequired) {
			t.Fatalf("unconfirmed: err = %v, want %v", err, ErrConfirmationRequired)
		}
		if n := len(r.DeliveredTo("laptop")); n != 0 {
			t.Fatalf("laptop received %d entries before confirmation, want 0", n)
		}

		r.Confirm("tok1")
		if err := r.Route(e, "laptop"); err != nil {
			t.Fatalf("confirmed: %v", err)
		}
		if n := len(r.DeliveredTo("laptop")); n != 1 {
			t.Fatalf("laptop received %d entries after confirmation, want 1", n)
		}
	})

	t.Run("confirmation never crosses accounts", func(t *testing.T) {
		r := newTestRouter(t, clk, WithSensitivePolicy(SensitiveConfirm))
		e := newEntry(clk, "tok2", "acct-alice", "phone", TypePassword, []byte("pw"))
		r.Confirm("tok2")
		if err := r.Route(e, "attacker"); !errors.Is(err, ErrTargetAccountMismatch) {
			t.Fatalf("err = %v, want %v", err, ErrTargetAccountMismatch)
		}
	})
}

func TestExpiry(t *testing.T) {
	clk := &clock{now: testStart}
	r := newTestRouter(t, clk)

	e := newEntry(clk, "e1", "acct-alice", "phone", TypeText, []byte("fresh"))
	if err := r.Route(e, "laptop"); err != nil {
		t.Fatalf("route fresh entry: %v", err)
	}

	clk.Advance(10 * time.Minute)
	stale := newEntry(clk, "e2", "acct-alice", "phone", TypeText, []byte("stale"))
	stale.CreatedAt = testStart
	stale.ExpiresAt = testStart.Add(5 * time.Minute)
	if err := r.Route(stale, "laptop"); !errors.Is(err, ErrEntryExpired) {
		t.Fatalf("expired entry: err = %v, want %v", err, ErrEntryExpired)
	}

	// The first entry expired in the meantime and must leave the history.
	if h := r.History("acct-alice"); len(h) != 0 {
		t.Fatalf("history after expiry = %+v, want empty", h)
	}
	if n := len(r.DeliveredTo("laptop")); n != 1 {
		t.Fatalf("laptop received %d entries, want 1", n)
	}
}

func TestPayloadSizeLimit(t *testing.T) {
	clk := &clock{now: testStart}
	r := newTestRouter(t, clk, WithMaxPayloadSize(16))

	ok := newEntry(clk, "e1", "acct-alice", "phone", TypeText, bytes.Repeat([]byte("a"), 16))
	if err := r.Route(ok, "laptop"); err != nil {
		t.Fatalf("at-limit payload: %v", err)
	}
	big := newEntry(clk, "e2", "acct-alice", "phone", TypeText, bytes.Repeat([]byte("a"), 17))
	if err := r.Route(big, "laptop"); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversize payload: err = %v, want %v", err, ErrPayloadTooLarge)
	}
}

func TestRateLimit(t *testing.T) {
	clk := &clock{now: testStart}
	r := newTestRouter(t, clk, WithRateLimit(3, time.Minute))

	for i := 0; i < 3; i++ {
		e := newEntry(clk, fmt.Sprintf("e%d", i), "acct-alice", "phone", TypeText, []byte("x"))
		if err := r.Route(e, "laptop"); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}
	e := newEntry(clk, "e-over", "acct-alice", "phone", TypeText, []byte("x"))
	if err := r.Route(e, "laptop"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4th entry: err = %v, want %v", err, ErrRateLimited)
	}

	// The limit is per account: Mallory's traffic is unaffected.
	other := newEntry(clk, "m1", "acct-mallory", "attacker", TypeText, []byte("x"))
	if err := r.Route(other, "attacker"); err != nil {
		t.Fatalf("other account entry: %v", err)
	}

	// After the window slides, Alice can sync again.
	clk.Advance(2 * time.Minute)
	e2 := newEntry(clk, "e-after", "acct-alice", "phone", TypeText, []byte("x"))
	if err := r.Route(e2, "laptop"); err != nil {
		t.Fatalf("after window: %v", err)
	}
}

func TestHistoryLimit(t *testing.T) {
	clk := &clock{now: testStart}
	r := newTestRouter(t, clk, WithHistoryLimit(3), WithRateLimit(0, time.Minute))

	for i := 0; i < 5; i++ {
		e := newEntry(clk, fmt.Sprintf("e%d", i), "acct-alice", "phone", TypeText, []byte("x"))
		if err := r.Route(e, "laptop"); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}

	h := r.History("acct-alice")
	if len(h) != 3 {
		t.Fatalf("history length = %d, want 3", len(h))
	}
	if h[0].ID != "e2" || h[2].ID != "e4" {
		t.Fatalf("history keeps newest entries, got %v", []string{h[0].ID, h[1].ID, h[2].ID})
	}
}
