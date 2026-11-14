package netsec

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Same-account crossing orders are blocked as self trades and never rest.
func TestSelfTradePrevention(t *testing.T) {
	clock := newFakeClock()
	monitor := NewMonitor()
	book := NewOrderBook("BTC", WithClock(clock), WithMonitor(monitor))

	if _, err := book.Submit(newReq("sell-1", "alice", SideSell, 100, 5)); err != nil {
		t.Fatalf("resting sell: %v", err)
	}

	_, err := book.Submit(newReq("buy-1", "alice", SideBuy, 100, 5))
	if !errors.Is(err, ErrSelfTrade) {
		t.Fatalf("self trade err = %v, want ErrSelfTrade", err)
	}
	if book.Len(SideBuy) != 0 {
		t.Fatal("rejected self-trade order must not rest on the book")
	}
	if seq := book.NextSequence(); seq != 2 {
		t.Fatalf("next sequence = %d, want 2 (rejection consumes none)", seq)
	}

	violations := monitor.Violations()
	if len(violations) != 1 || violations[0].Kind != ViolationSelfTrade {
		t.Fatalf("violations = %+v, want one self-trade record", violations)
	}

	if _, err := book.Submit(newReq("buy-2", "bob", SideBuy, 100, 5)); err != nil {
		t.Fatalf("legitimate cross: %v", err)
	}
}

// Non-crossing same-owner orders on opposite sides do not trip self-trade.
func TestSelfTradeOnlyWhenCrossing(t *testing.T) {
	monitor := NewMonitor()
	book := NewOrderBook("BTC", WithMonitor(monitor))

	if _, err := book.Submit(newReq("ask", "alice", SideSell, 110, 1)); err != nil {
		t.Fatalf("resting ask: %v", err)
	}
	if _, err := book.Submit(newReq("bid", "alice", SideBuy, 100, 1)); err != nil {
		t.Fatalf("non-crossing same-owner bid rejected: %v", err)
	}
	if len(monitor.Violations()) != 0 {
		t.Fatalf("unexpected violations: %+v", monitor.Violations())
	}
}

// Accounts linked as one beneficial owner are treated as self trades too.
func TestSelfTradeLinkedAccounts(t *testing.T) {
	monitor := NewMonitor()
	monitor.LinkAccounts("alice", "alice-alt")
	book := NewOrderBook("BTC", WithMonitor(monitor))

	if _, err := book.Submit(newReq("s", "alice", SideSell, 100, 1)); err != nil {
		t.Fatalf("resting sell: %v", err)
	}
	if _, err := book.Submit(newReq("b", "alice-alt", SideBuy, 100, 1)); !errors.Is(err, ErrSelfTrade) {
		t.Fatalf("linked-account self trade not blocked: %v", err)
	}

	if _, err := book.Submit(newReq("b2", "carol", SideBuy, 100, 1)); err != nil {
		t.Fatalf("independent cross rejected: %v", err)
	}

	if !monitor.SameOwner("alice", "alice-alt") {
		t.Fatal("linked accounts not recognized as same owner")
	}
	if monitor.SameOwner("alice", "carol") {
		t.Fatal("independent accounts reported as same owner")
	}
}

// Chained LinkAccounts calls must produce one transitive equivalence class.
func TestLinkedAccountsTransitive(t *testing.T) {
	monitor := NewMonitor()
	monitor.LinkAccounts("a", "b")
	monitor.LinkAccounts("b", "c")

	for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}, {"a", "c"}} {
		if !monitor.SameOwner(pair[0], pair[1]) {
			t.Fatalf("%s and %s not recognized as same owner", pair[0], pair[1])
		}
	}
	if monitor.SameOwner("a", "d") {
		t.Fatal("unlinked account reported as same owner")
	}
}

// Executed same-owner trades are flagged as wash trades by the monitor.
func TestWashTradeDetection(t *testing.T) {
	monitor := NewMonitor()
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

	v := monitor.RecordTrade("alice", "t1", "alice", "m1", at)
	if v == nil || v.Kind != ViolationWashTrade {
		t.Fatalf("same-owner trade result = %+v, want wash trade", v)
	}

	monitor.LinkAccounts("alice", "alice-alt")
	if v := monitor.RecordTrade("alice", "t2", "alice-alt", "m2", at); v == nil {
		t.Fatal("linked-owner trade must produce a wash-trade violation")
	}

	if v := monitor.RecordTrade("alice", "t3", "bob", "m3", at); v != nil {
		t.Fatalf("independent trade flagged as wash: %+v", v)
	}
	if len(monitor.Violations()) != 2 {
		t.Fatalf("violations = %d, want 2", len(monitor.Violations()))
	}
}

// Submission and cancel rates are measured on engine-clock sliding windows.
func TestAbnormalFrequency(t *testing.T) {
	t.Run("submissions", func(t *testing.T) {
		clock := newFakeClock()
		rule := FrequencyRule{Window: time.Second, MaxSubmissions: 3}
		monitor := NewMonitor(WithFrequencyRule(rule))
		book := NewOrderBook("BTC", WithClock(clock), WithMonitor(monitor))

		for i := 0; i < 3; i++ {
			if _, err := book.Submit(newReq(fmt.Sprintf("ok-%d", i), "alice", SideBuy, 100, 1)); err != nil {
				t.Fatalf("submit %d: %v", i, err)
			}
		}
		if _, err := book.Submit(newReq("blocked", "alice", SideBuy, 100, 1)); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("4th submit err = %v, want ErrRateLimited", err)
		}

		if _, err := book.Submit(newReq("bob-ok", "bob", SideBuy, 100, 1)); err != nil {
			t.Fatalf("other account submit: %v", err)
		}

		clock.Advance(1100 * time.Millisecond)
		if _, err := book.Submit(newReq("after-window", "alice", SideBuy, 100, 1)); err != nil {
			t.Fatalf("post-window submit: %v", err)
		}

		violations := monitor.Violations()
		if len(violations) != 1 || violations[0].Kind != ViolationRateSubmission {
			t.Fatalf("violations = %+v, want one submission-rate record", violations)
		}
	})

	t.Run("cancels", func(t *testing.T) {
		clock := newFakeClock()
		rule := FrequencyRule{Window: time.Second, MaxCancels: 2}
		monitor := NewMonitor(WithFrequencyRule(rule))
		book := NewOrderBook("BTC", WithClock(clock), WithMonitor(monitor))

		for i := 0; i < 3; i++ {
			if _, err := book.Submit(newReq(fmt.Sprintf("c-%d", i), "alice", SideSell, 100, 1)); err != nil {
				t.Fatalf("submit %d: %v", i, err)
			}
		}
		for i := 0; i < 3; i++ {
			if _, err := book.Cancel(fmt.Sprintf("c-%d", i)); err != nil {
				t.Fatalf("cancel %d: %v", i, err)
			}
		}

		violations := monitor.Violations()
		if len(violations) != 1 || violations[0].Kind != ViolationRateCancel {
			t.Fatalf("violations = %+v, want one cancel-rate record", violations)
		}
		if violations[0].Count != 3 || violations[0].Limit != 2 {
			t.Fatalf("count/limit = %d/%d, want 3/2", violations[0].Count, violations[0].Limit)
		}

		clock.Advance(1100 * time.Millisecond)
		if _, err := book.Submit(newReq("d-0", "alice", SideSell, 100, 1)); err != nil {
			t.Fatalf("post-window submit: %v", err)
		}
		if _, err := book.Cancel("d-0"); err != nil {
			t.Fatalf("post-window cancel: %v", err)
		}
		if len(monitor.Violations()) != 1 {
			t.Fatalf("sliding window did not expire: %+v", monitor.Violations())
		}
	})
}

// With no rule configured, surveillance never limits benign traffic.
func TestMonitorDisabledByDefault(t *testing.T) {
	monitor := NewMonitor()
	book := NewOrderBook("BTC", WithMonitor(monitor))

	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("n-%d", i)
		if _, err := book.Submit(newReq(id, "alice", SideBuy, 100, 1)); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if _, err := book.Cancel(id); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}
	if len(monitor.Violations()) != 0 {
		t.Fatalf("unexpected violations: %+v", monitor.Violations())
	}
}
