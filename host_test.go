package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	tenantA TenantID = "tenant-a"
	tenantB TenantID = "tenant-b"
	target1 TargetID = "target-1"
	target2 TargetID = "target-2"
)

func rule(tenant TenantID, target TargetID, gen uint64, sig Signal) AuthRule {
	return AuthRule{Tenant: tenant, Target: target, Generation: gen, Signal: sig}
}

func testHost(t *testing.T, auth Authorizer, cfg Config) *Host {
	t.Helper()
	host, err := New(auth, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(host.Close)
	return host
}

func baseConfig() Config {
	return Config{
		Capacity:      10,
		ReservedSlots: 3,
		TenantQuota: map[TenantID]int{
			tenantA: 10,
			tenantB: 10,
		},
	}
}

func waitFor(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", msg)
}

func TestStandardSignalsCoalesce(t *testing.T) {
	auth := NewRuleAuthorizer(
		rule(tenantA, target1, 1, 9),
		rule(tenantA, target1, 1, 10),
		rule(tenantA, target1, 1, 12),
	)
	host := testHost(t, auth, baseConfig())
	ch, err := host.Register(target1)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// A barrier envelope (signal 9) is queued first. With the receiver unread
	// the pump blocks delivering it, guaranteeing signal 10 stays queued and
	// every later same-kind send merges before any delivery snapshot.
	barrier, err := host.Send(tenantA, target1, 9, nil)
	if err != nil {
		t.Fatalf("Send barrier: %v", err)
	}
	waitFor(t, func() bool { return host.Stats().Used == 1 }, "barrier billed")

	first, err := host.Send(tenantA, target1, 10, nil)
	if err != nil {
		t.Fatalf("Send first: %v", err)
	}
	second, err := host.Send(tenantA, target1, 10, nil)
	if err != nil {
		t.Fatalf("Send second: %v", err)
	}
	third, err := host.Send(tenantA, target1, 10, nil)
	if err != nil {
		t.Fatalf("Send third: %v", err)
	}
	if first != second || second != third {
		t.Fatal("same-kind standard sends must share one envelope")
	}
	if got := first.coalescedCount(); got != 2 {
		t.Fatalf("Coalesced = %d, want 2", got)
	}

	other, err := host.Send(tenantA, target1, 12, nil)
	if err != nil {
		t.Fatalf("Send other signal: %v", err)
	}
	if other == first {
		t.Fatal("different standard signal kinds must not coalesce")
	}

	stats := host.Stats()
	if stats.Used != 3 || stats.Tenants[tenantA] != 3 {
		t.Fatalf("stats = %+v, want 3 billed slots", stats)
	}

	// Drain in queue order: barrier, then coalesced signal 10, then signal 12.
	if got := <-ch; got.Signal != 9 || got.Coalesced != 0 {
		t.Fatalf("barrier delivery = %+v, want signal 9", got)
	}
	got := <-ch
	if got.Signal != 10 || got.Coalesced != 2 || got.Gen != 1 {
		t.Fatalf("first delivery = %+v, want coalesced signal 10", got)
	}
	got = <-ch
	if got.Signal != 12 || got.Coalesced != 0 {
		t.Fatalf("second delivery = %+v, want signal 12", got)
	}

	waitFor(t, func() bool { return host.Stats().Used == 0 }, "slots released after delivery")
	_ = barrier

	// After draining, a fresh same-kind send is a brand new envelope.
	again, err := host.Send(tenantA, target1, 10, nil)
	if err != nil {
		t.Fatalf("Send after drain: %v", err)
	}
	if again == first || again.coalescedCount() != 0 {
		t.Fatal("post-delivery standard send must not coalesce with the old envelope")
	}
}

func TestRealtimeSignalsRetainedWithPayloads(t *testing.T) {
	auth := NewRuleAuthorizer(
		rule(tenantA, target1, 1, 40),
		rule(tenantA, target1, 1, 41),
	)
	host := testHost(t, auth, baseConfig())
	ch, err := host.Register(target1)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	payloads := []string{"p0", "p1", "p2", "p3"}
	var envelopes []*QueuedEnvelope
	for index, payload := range payloads {
		envelope, err := host.Send(tenantA, target1, Signal(40+index%2), []byte(payload))
		if err != nil {
			t.Fatalf("Send realtime %d: %v", index, err)
		}
		envelopes = append(envelopes, envelope)
	}

	stats := host.Stats()
	if stats.Used != len(payloads) || stats.Tenants[tenantA] != len(payloads) {
		t.Fatalf("stats = %+v, want each realtime signal billed separately", stats)
	}

	// Sender-side mutation must not alter the queued payload.
	mutated := []byte("p0")
	mutated[0] = 'X'

	for index, want := range payloads {
		got := <-ch
		if got.Signal != Signal(40+index%2) {
			t.Fatalf("delivery %d signal = %d", index, got.Signal)
		}
		if string(got.Payload) != want {
			t.Fatalf("delivery %d payload = %q, want %q", index, got.Payload, want)
		}
	}
	waitFor(t, func() bool { return host.Stats().Used == 0 }, "realtime slots released")
}

func TestRestartInvalidatesOldGeneration(t *testing.T) {
	auth := NewRuleAuthorizer(
		rule(tenantA, target1, 1, 40),
		rule(tenantA, target1, 2, 40),
		rule(tenantA, target1, 1, 10),
		rule(tenantA, target1, 2, 10),
	)
	host := testHost(t, auth, baseConfig())
	oldCh, err := host.Register(target1)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// The receiver stays blocked so envelopes are still queued at restart.
	if _, err := host.Send(tenantA, target1, 40, []byte("old-rt-1")); err != nil {
		t.Fatalf("Send realtime: %v", err)
	}
	if _, err := host.Send(tenantA, target1, 40, []byte("old-rt-2")); err != nil {
		t.Fatalf("Send realtime: %v", err)
	}
	stdEnvelope, err := host.Send(tenantA, target1, 10, nil)
	if err != nil {
		t.Fatalf("Send standard: %v", err)
	}
	if host.Stats().Used != 3 {
		t.Fatalf("used before restart = %d, want 3", host.Stats().Used)
	}

	newCh, err := host.Restart(target1)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	waitFor(t, func() bool { return host.Stats().Used == 0 }, "restart released stale slots")

	// Old-generation envelopes must never be delivered, including through the
	// old channel, and their slots are released exactly once.
	select {
	case got := <-oldCh:
		t.Fatalf("stale envelope delivered after restart: %+v", got)
	default:
	}
	// Restart already invalidated the envelope; any later cancel must not
	// double-release its slot (usage stays zero, no negative-counter panic).
	_ = host.Cancel(stdEnvelope)
	if stats := host.Stats(); stats.Used != 0 {
		t.Fatalf("post-restart cancel changed accounting: %+v", stats)
	}

	// New traffic is pinned to the fresh generation and delivers on newCh.
	fresh, err := host.Send(tenantA, target1, 40, []byte("new-rt"))
	if err != nil {
		t.Fatalf("Send after restart: %v", err)
	}
	if fresh.Gen != 2 {
		t.Fatalf("fresh generation = %d, want 2", fresh.Gen)
	}
	select {
	case got := <-newCh:
		if got.Gen != 2 || string(got.Payload) != "new-rt" {
			t.Fatalf("post-restart delivery = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("post-restart envelope not delivered on the new channel")
	}

	// A standard signal queued again after restart must be a new envelope.
	again, err := host.Send(tenantA, target1, 10, nil)
	if err != nil {
		t.Fatalf("standard send after restart: %v", err)
	}
	if again == stdEnvelope {
		t.Fatal("standard signal must not coalesce across generations")
	}
	if got := <-newCh; got.Gen != 2 || got.Signal != 10 {
		t.Fatalf("post-restart standard delivery = %+v", got)
	}
}

func TestDeliveryRechecksAuthorization(t *testing.T) {
	sig := Signal(40)
	barrierSig := Signal(39)
	authRule := rule(tenantA, target1, 1, sig)
	auth := NewRuleAuthorizer(authRule, rule(tenantA, target1, 1, barrierSig))
	host := testHost(t, auth, baseConfig())
	ch, err := host.Register(target1)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// A barrier envelope is queued and blocks the pump on the unread
	// receiver. The signal-40 envelope therefore stays queued behind it and
	// never reaches a delivery-time check during the policy changes.
	if _, err := host.Send(tenantA, target1, barrierSig, nil); err != nil {
		t.Fatalf("Send barrier: %v", err)
	}
	waitFor(t, func() bool { return host.Stats().Used == 1 }, "barrier in flight")

	envelope, err := host.Send(tenantA, target1, sig, []byte("held"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return host.Stats().Used == 2 }, "envelope queued behind barrier")

	// Revoking after enqueue makes every later send fail the authorization
	// check, proving the policy is enforced beyond the sender side.
	auth.Revoke(authRule)
	if _, err := host.Send(tenantA, target1, sig, []byte("later")); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("post-revoke send error = %v, want ErrNotAuthorized", err)
	}
	if stats := host.Stats(); stats.Used != 2 {
		t.Fatalf("accounting after revoke = %+v, want 2 held slots", stats)
	}

	// Re-grant and drain in queue order: barrier first, then the held envelope
	// with its fixed generation and payload, then fresh traffic.
	auth.Grant(authRule)

	if got := <-ch; got.Signal != barrierSig {
		t.Fatalf("barrier delivery = %+v, want signal %d", got, barrierSig)
	}
	select {
	case got := <-ch:
		if got.Gen != envelope.Gen || string(got.Payload) != "held" {
			t.Fatalf("in-flight delivery = %+v, want held envelope gen 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("held envelope was not delivered")
	}
	waitFor(t, func() bool { return host.Stats().Used == 0 }, "slot released after delivery")

	followup, err := host.Send(tenantA, target1, sig, []byte("again"))
	if err != nil {
		t.Fatalf("Send after re-grant: %v", err)
	}
	select {
	case got := <-ch:
		if got.Gen != followup.Gen || string(got.Payload) != "again" {
			t.Fatalf("follow-up delivery = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("follow-up envelope was not delivered")
	}
}

func TestHighFrequencyTenantCannotConsumeReservedSlots(t *testing.T) {
	sig := Signal(40)
	auth := NewRuleAuthorizer(
		rule(tenantA, target1, 1, sig),
		rule(tenantB, target2, 1, sig),
	)
	cfg := Config{
		Capacity:      10,
		ReservedSlots: 3,
		TenantQuota: map[TenantID]int{
			tenantA: 10,
			tenantB: 10,
		},
	}
	host := testHost(t, auth, cfg)
	if _, err := host.Register(target1); err != nil {
		t.Fatalf("Register target1: %v", err)
	}
	if _, err := host.Register(target2); err != nil {
		t.Fatalf("Register target2: %v", err)
	}

	// Tenant A may fill the guaranteed share (capacity-reserved = 7) and no
	// more; the three protected slots must remain reachable by tenant B.
	for index := 0; index < 7; index++ {
		if _, err := host.Send(tenantA, target1, sig, []byte{byte(index)}); err != nil {
			t.Fatalf("tenant A send %d: %v", index, err)
		}
	}
	if _, err := host.Send(tenantA, target1, sig, nil); !errors.Is(err, ErrReservedSlots) {
		t.Fatalf("eighth tenant A send error = %v, want ErrReservedSlots", err)
	}

	// Tenant B uses its guaranteed headroom inside the protected reservation.
	for index := 0; index < 3; index++ {
		if _, err := host.Send(tenantB, target2, sig, []byte{byte(index)}); err != nil {
			t.Fatalf("tenant B send %d: %v", index, err)
		}
	}
	if stats := host.Stats(); stats.Used != 10 {
		t.Fatalf("full queue stats = %+v, want 10 used", stats)
	}
	if _, err := host.Send(tenantB, target2, sig, nil); !errors.Is(err, ErrCapacity) {
		t.Fatalf("over-capacity send error = %v, want ErrCapacity", err)
	}
}

func TestTenantQuotaAndCancelReleaseOnce(t *testing.T) {
	sig := Signal(40)
	auth := NewRuleAuthorizer(rule(tenantA, target1, 1, sig))
	cfg := Config{
		Capacity:      10,
		ReservedSlots: 0,
		TenantQuota:   map[TenantID]int{tenantA: 2},
	}
	host := testHost(t, auth, cfg)
	if _, err := host.Register(target1); err != nil {
		t.Fatalf("Register: %v", err)
	}

	first, err := host.Send(tenantA, target1, sig, []byte("1"))
	if err != nil {
		t.Fatalf("Send first: %v", err)
	}
	second, err := host.Send(tenantA, target1, sig, []byte("2"))
	if err != nil {
		t.Fatalf("Send second: %v", err)
	}
	if _, err := host.Send(tenantA, target1, sig, nil); !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("third send error = %v, want ErrTenantQuota", err)
	}

	if err := host.Cancel(first); err != nil {
		t.Fatalf("Cancel first: %v", err)
	}
	if err := host.Cancel(first); !errors.Is(err, ErrCanceled) {
		t.Fatalf("double Cancel error = %v, want ErrCanceled", err)
	}
	if stats := host.Stats(); stats.Used != 1 || stats.Tenants[tenantA] != 1 {
		t.Fatalf("stats after cancel = %+v", stats)
	}

	// Freed quota is immediately billable again.
	third, err := host.Send(tenantA, target1, sig, []byte("3"))
	if err != nil {
		t.Fatalf("Send after cancel: %v", err)
	}
	if host.Stats().Used != 2 {
		t.Fatalf("used = %d, want 2", host.Stats().Used)
	}

	// Repeated cancels and a cancel after restart-driven invalidation must not
	// double-release the slot.
	if err := host.Cancel(second); err != nil {
		t.Fatalf("Cancel second: %v", err)
	}
	if err := host.Cancel(second); !errors.Is(err, ErrCanceled) {
		t.Fatalf("repeat Cancel error = %v, want ErrCanceled", err)
	}
	if _, err := host.Restart(target1); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	// Restart invalidated third; a later cancel is a no-op for accounting.
	_ = host.Cancel(third)
	waitFor(t, func() bool { return host.Stats().Used == 0 }, "all slots released exactly once")
}

func TestBlockingReceiverBackpressureIsPerTarget(t *testing.T) {
	sig := Signal(40)
	auth := NewRuleAuthorizer(
		rule(tenantA, target1, 1, sig),
		rule(tenantB, target2, 1, sig),
	)
	host := testHost(t, auth, baseConfig())
	blockedCh, err := host.Register(target1)
	if err != nil {
		t.Fatalf("Register target1: %v", err)
	}
	freeCh, err := host.Register(target2)
	if err != nil {
		t.Fatalf("Register target2: %v", err)
	}

	// target1 has a blocked receiver: its envelope stays in-flight with its
	// slot billed, but target2's independent pump must still deliver.
	if _, err := host.Send(tenantA, target1, sig, []byte("blocked")); err != nil {
		t.Fatalf("Send to blocked target: %v", err)
	}
	if _, err := host.Send(tenantB, target2, sig, []byte("moving")); err != nil {
		t.Fatalf("Send to free target: %v", err)
	}

	select {
	case got := <-freeCh:
		if string(got.Payload) != "moving" {
			t.Fatalf("free target got payload %q", got.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("target2 was starved by target1's blocked receiver")
	}

	waitFor(t, func() bool {
		stats := host.Stats()
		return stats.Used == 1 && stats.Tenants[tenantA] == 1
	}, "free target slot released while blocked target stays billed")

	// Unblocking the receiver completes delivery and releases the last slot.
	got := <-blockedCh
	if string(got.Payload) != "blocked" {
		t.Fatalf("blocked target payload = %q", got.Payload)
	}
	waitFor(t, func() bool { return host.Stats().Used == 0 }, "in-flight slot released")
}

func TestEnqueueTimeValidation(t *testing.T) {
	auth := NewRuleAuthorizer(
		rule(tenantA, target1, 1, 10),
		rule(tenantA, target1, 1, 40),
	)
	cfg := Config{
		Capacity:        10,
		ReservedSlots:   0,
		TenantQuota:     map[TenantID]int{tenantA: 10},
		MaxPayloadBytes: 4,
	}
	host := testHost(t, auth, cfg)
	if _, err := host.Register(target1); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := host.Send(tenantA, target1, 0, nil); !errors.Is(err, ErrInvalidSignal) {
		t.Fatalf("signal 0 error = %v", err)
	}
	if _, err := host.Send(tenantA, target1, 65, nil); !errors.Is(err, ErrInvalidSignal) {
		t.Fatalf("signal 65 error = %v", err)
	}
	if _, err := host.Send(tenantA, target1, 10, []byte("x")); !errors.Is(err, ErrStandardPayload) {
		t.Fatalf("standard payload error = %v", err)
	}
	if _, err := host.Send(tenantA, target1, 40, []byte("toolong")); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized payload error = %v", err)
	}
	if _, err := host.Send(tenantA, target1, 40, []byte("ok")); err != nil {
		t.Fatalf("valid realtime send: %v", err)
	}
	if _, err := host.Send(TenantID("nobody"), target1, 40, nil); !errors.Is(err, ErrTenantUnknown) {
		t.Fatalf("unknown tenant error = %v", err)
	}
	if _, err := host.Send(tenantA, TargetID("ghost"), 40, nil); !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("unknown target error = %v", err)
	}
	if _, err := host.Send(tenantA, target1, 41, nil); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("unauthorized send error = %v", err)
	}
}

func TestConcurrentTenantsReserveAndReleaseOnce(t *testing.T) {
	const (
		capacity = 12
		reserved = 4
		signals  = 200
	)
	auth := NewRuleAuthorizer(
		rule(tenantA, target1, 1, 40),
		rule(tenantB, target2, 1, 40),
	)
	cfg := Config{
		Capacity:      capacity,
		ReservedSlots: reserved,
		TenantQuota: map[TenantID]int{
			tenantA: capacity,
			tenantB: capacity,
		},
	}
	host := testHost(t, auth, cfg)
	chA, err := host.Register(target1)
	if err != nil {
		t.Fatalf("Register target1: %v", err)
	}
	chB, err := host.Register(target2)
	if err != nil {
		t.Fatalf("Register target2: %v", err)
	}

	perTenantCap := capacity - reserved
	var wg sync.WaitGroup
	for _, item := range []struct {
		tenant TenantID
		target TargetID
		ch     chan Envelope
	}{
		{tenantA, target1, chA},
		{tenantB, target2, chB},
	} {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < signals; index++ {
				_, sendErr := host.Send(item.tenant, item.target, 40, []byte{byte(index)})
				if sendErr != nil {
					if !errors.Is(sendErr, ErrReservedSlots) && !errors.Is(sendErr, ErrCapacity) {
						t.Errorf("unexpected send error: %v", sendErr)
						return
					}
					// Back off by consuming one delivered envelope to free a slot.
					select {
					case <-item.ch:
					case <-time.After(time.Second):
						t.Errorf("tenant %s stuck without delivery", item.tenant)
						return
					}
					index--
					continue
				}
				if stats := host.Stats(); stats.Tenants[item.tenant] > perTenantCap {
					t.Errorf("tenant %s exceeded protected cap: %+v", item.tenant, stats)
					return
				}
				// Every queued envelope is accounted for exactly once: a
				// concurrent canceler may reclaim some slots, otherwise the
				// receiver consumes the delivery. Either path frees the slot.
				select {
				case got := <-item.ch:
					if got.Tenant != item.tenant {
						t.Errorf("cross-tenant delivery: %+v", got)
						return
					}
				case <-time.After(time.Second):
					t.Errorf("tenant %s delivery timeout at %d", item.tenant, index)
					return
				}
			}
		}()
	}

	wg.Wait()

	waitFor(t, func() bool { return host.Stats().Used == 0 }, "all slots drained after concurrent workload")
	for tenant, used := range host.Stats().Tenants {
		if used != 0 {
			t.Fatalf("tenant %s leaked %d slots", tenant, used)
		}
	}
}

func TestCloseReleasesQueuedAndInFlight(t *testing.T) {
	auth := NewRuleAuthorizer(rule(tenantA, target1, 1, 40))
	host := testHost(t, auth, baseConfig())
	if _, err := host.Register(target1); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for index := 0; index < 3; index++ {
		if _, err := host.Send(tenantA, target1, 40, []byte{byte(index)}); err != nil {
			t.Fatalf("Send %d: %v", index, err)
		}
	}
	host.Close()

	// Close is idempotent and all accounting must be settled without panicking.
	host.Close()
	if stats := host.Stats(); stats.Used != 0 || stats.Tenants[tenantA] != 0 {
		t.Fatalf("post-close stats = %+v", stats)
	}
	if _, err := host.Send(tenantA, target1, 40, nil); !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("post-close send error = %v, want ErrAlreadyClosed", err)
	}
}

func TestConcurrentCancelAndDeliveryReleaseOnce(t *testing.T) {
	const iterations = 40
	auth := NewRuleAuthorizer(rule(tenantA, target1, 1, 40))
	cfg := Config{
		Capacity:      8,
		ReservedSlots: 2,
		TenantQuota:   map[TenantID]int{tenantA: 8},
	}
	host := testHost(t, auth, cfg)
	ch, err := host.Register(target1)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	for round := 0; round < iterations; round++ {
		// Drain any delivery that landed after the previous non-blocking peek.
		select {
		case <-ch:
		default:
		}
		envelope, sendErr := host.Send(tenantA, target1, 40, []byte{byte(round)})
		if sendErr != nil {
			t.Fatalf("Send round %d: %v", round, sendErr)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = host.Cancel(envelope)
		}()
		go func() {
			defer wg.Done()
			// Non-blocking peek: cancel may win the race, in which case there
			// is nothing to receive. Delivery success also frees the slot.
			select {
			case <-ch:
			default:
			}
		}()
		wg.Wait()

		// Whichever path won, exactly one slot must be released and a second
		// cancel must observe the envelope as no longer queued.
		if err := host.Cancel(envelope); !errors.Is(err, ErrCanceled) {
			t.Fatalf("round %d second cancel error = %v, want ErrCanceled", round, err)
		}
		waitFor(t, func() bool { return host.Stats().Used == 0 }, "slot settled after cancel/delivery race")
	}

	if stats := host.Stats(); stats.Used != 0 || stats.Tenants[tenantA] != 0 {
		t.Fatalf("final accounting = %+v, want zero", stats)
	}
}
