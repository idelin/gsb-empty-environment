package session

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// mapACL is a test ACL with per-principal subscribe filters and
// deliverable topics that can be revoked at runtime.
type mapACL struct {
	mu       sync.Mutex
	subs     map[Principal]map[string]bool
	delivers map[Principal]map[string]bool
}

func newMapACL() *mapACL {
	return &mapACL{
		subs:     make(map[Principal]map[string]bool),
		delivers: make(map[Principal]map[string]bool),
	}
}

func (a *mapACL) grant(p Principal, filter, topic string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.subs[p] == nil {
		a.subs[p] = make(map[string]bool)
	}
	if a.delivers[p] == nil {
		a.delivers[p] = make(map[string]bool)
	}
	a.subs[p][filter] = true
	a.delivers[p][topic] = true
}

func (a *mapACL) revoke(p Principal, filter, topic string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.subs[p], filter)
	delete(a.delivers[p], topic)
}

func (a *mapACL) AllowSubscribe(p Principal, filter string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.subs[p][filter]
}

func (a *mapACL) AllowDeliver(p Principal, topic string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.delivers[p][topic]
}

// TestPrincipalMismatchDiscardsSession: attacker connects with the
// victim's ClientID but their own credentials. The old session must be
// destroyed, Session Present must be 0, and nothing may be inherited.
func TestPrincipalMismatchDiscardsSession(t *testing.T) {
	acl := newMapACL()
	acl.grant("alice", "alerts/#", "alerts/1")
	m := NewManager(acl)

	victim, res := m.Connect(ConnectRequest{ClientID: "dev-1", Principal: "alice"})
	if res.SessionPresent {
		t.Fatal("first connect must not report Session Present")
	}
	if err := victim.Subscribe(Subscription{Filter: "alerts/#", QoS: 1}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	m.Enqueue("dev-1", Message{Topic: "alerts/1", Payload: []byte("secret"), PacketID: 7})

	// Attacker replays the ClientID with their own authenticated
	// principal and Clean Start 0, hoping to resume alice's session.
	attacker, res := m.Connect(ConnectRequest{ClientID: "dev-1", Principal: "mallory"})
	if res.SessionPresent {
		t.Fatal("cross-principal resume must return Session Present 0")
	}
	sess := m.Session("dev-1")
	if sess == nil {
		t.Fatal("a fresh session must exist for the attacker")
	}
	if sess.Owner() != "mallory" {
		t.Fatalf("session owner = %q, want mallory", sess.Owner())
	}
	if got := len(sess.Subscriptions()); got != 0 {
		t.Fatalf("attacker inherited %d subscriptions", got)
	}
	if got := sess.Queued(); got != 0 {
		t.Fatalf("attacker inherited %d queued messages", got)
	}
	if _, ok, err := attacker.NextDelivery(); err != nil || ok {
		t.Fatalf("attacker drained a queued message: ok=%v err=%v", ok, err)
	}
	// The displaced victim connection is invalidated too.
	if !victim.TakenOver() {
		t.Fatal("victim connection should have been invalidated")
	}
}

// TestReauthorizationOnResume: grants revoked while the client is
// offline must strip subscriptions and queued QoS 1 messages at resume,
// and revocations after resume must block delivery.
func TestReauthorizationOnResume(t *testing.T) {
	acl := newMapACL()
	acl.grant("alice", "open/#", "open/1")
	acl.grant("alice", "secret/#", "secret/1")
	m := NewManager(acl)

	conn, _ := m.Connect(ConnectRequest{ClientID: "dev-2", Principal: "alice"})
	if err := conn.Subscribe(Subscription{Filter: "open/#", QoS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Subscribe(Subscription{Filter: "secret/#", QoS: 1}); err != nil {
		t.Fatal(err)
	}
	m.Enqueue("dev-2", Message{Topic: "open/1", Payload: []byte("a"), PacketID: 1})
	m.Enqueue("dev-2", Message{Topic: "secret/1", Payload: []byte("b"), PacketID: 2})

	// Revoke secret/# while alice is offline, then she reconnects.
	acl.revoke("alice", "secret/#", "secret/1")
	conn2, res := m.Connect(ConnectRequest{ClientID: "dev-2", Principal: "alice"})
	if !res.SessionPresent {
		t.Fatal("same-principal resume must return Session Present 1")
	}
	sess := m.Session("dev-2")
	subs := sess.Subscriptions()
	if len(subs) != 1 || subs[0].Filter != "open/#" {
		t.Fatalf("subscriptions after resume = %v, want only open/#", subs)
	}
	if got := sess.Queued(); got != 1 {
		t.Fatalf("queued after resume = %d, want 1 (revoked message dropped)", got)
	}
	msg, ok, err := conn2.NextDelivery()
	if err != nil || !ok || msg.Topic != "open/1" {
		t.Fatalf("NextDelivery = %v,%v,%v; want open/1", msg, ok, err)
	}

	// Revoke open/# after resume; a newly queued message must be
	// dropped at delivery time, never reaching the client.
	acl.revoke("alice", "open/#", "open/1")
	m.Enqueue("dev-2", Message{Topic: "open/1", Payload: []byte("late"), PacketID: 3})
	if _, ok, err := conn2.NextDelivery(); err != nil || ok {
		t.Fatalf("revoked message delivered: ok=%v err=%v", ok, err)
	}
	if got := sess.Queued(); got != 0 {
		t.Fatalf("revoked message left in queue: %d", got)
	}
}

// TestTakeoverRace: concurrent reconnects with the same ClientID and
// principal must leave exactly one live connection; every displaced
// connection is notified once and its in-flight publishes fail.
func TestTakeoverRace(t *testing.T) {
	m := NewManager(nil)

	first, res := m.Connect(ConnectRequest{ClientID: "dev-3", Principal: "alice"})
	if res.SessionPresent {
		t.Fatal("first connect must not report Session Present")
	}
	if err := first.BeginPublish(42); err != nil {
		t.Fatalf("BeginPublish: %v", err)
	}

	var notified atomic.Int64
	first.OnTakenOver(func() { notified.Add(1) })

	const racers = 32
	var wg sync.WaitGroup
	conns := make([]*Connection, racers)
	presents := make([]bool, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, r := m.Connect(ConnectRequest{ClientID: "dev-3", Principal: "alice"})
			conns[i] = c
			presents[i] = r.SessionPresent
		}(i)
	}
	wg.Wait()

	if got := notified.Load(); got != 1 {
		t.Fatalf("takeover notifications = %d, want exactly 1", got)
	}
	if !first.TakenOver() {
		t.Fatal("original connection must be taken over")
	}
	// In-flight publish on the displaced connection is dead.
	if err := first.CompletePublish(42); !errors.Is(err, ErrTakenOver) {
		t.Fatalf("CompletePublish on displaced conn = %v, want ErrTakenOver", err)
	}
	if err := first.BeginPublish(43); !errors.Is(err, ErrTakenOver) {
		t.Fatalf("BeginPublish on displaced conn = %v, want ErrTakenOver", err)
	}

	// Every racer saw the persisted session.
	for i, p := range presents {
		if !p {
			t.Fatalf("racer %d did not see Session Present", i)
		}
	}
	// Exactly one connection is current; all others are taken over.
	live := 0
	for i, c := range conns {
		if c.TakenOver() {
			continue
		}
		live++
		if err := c.BeginPublish(uint16(100 + i)); err != nil {
			t.Fatalf("live connection cannot publish: %v", err)
		}
	}
	if live != 1 {
		t.Fatalf("live connections = %d, want exactly 1", live)
	}

	// A racing attacker with a different principal must not inherit the
	// session even while legitimate reconnects are in flight.
	m2 := NewManager(nil)
	if _, r := m2.Connect(ConnectRequest{ClientID: "dev-4", Principal: "alice"}); r.SessionPresent {
		t.Fatal("setup: unexpected Session Present")
	}
	m2.Enqueue("dev-4", Message{Topic: "t", PacketID: 1})
	var wg2 sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			p := Principal("alice")
			if i%2 == 0 {
				p = "mallory"
			}
			m2.Connect(ConnectRequest{ClientID: "dev-4", Principal: p})
		}(i)
	}
	wg2.Wait()
	owner := m2.Session("dev-4").Owner()
	if owner != "alice" && owner != "mallory" {
		t.Fatalf("session owner corrupted: %q", owner)
	}
	if owner == "mallory" {
		// If the attacker won the last connect, the session must be
		// empty: alice's queued message was discarded with her session.
		if got := m2.Session("dev-4").Queued(); got != 0 {
			t.Fatalf("attacker-owned session holds %d inherited messages", got)
		}
	}
}
