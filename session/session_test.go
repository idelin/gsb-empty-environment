package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeACL is a mutable map-backed ACL with MQTT wildcard support so a policy
// can be revoked between two connections.
type fakeACL struct {
	mu         sync.Mutex
	canSub     map[string]bool
	canReceive map[string]bool
}

func newFakeACL() *fakeACL {
	return &fakeACL{
		canSub:     make(map[string]bool),
		canReceive: make(map[string]bool),
	}
}

func (a *fakeACL) allowSub(patterns ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range patterns {
		a.canSub[p] = true
	}
}

func (a *fakeACL) allowReceive(patterns ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range patterns {
		a.canReceive[p] = true
	}
}

func (a *fakeACL) denyReceive(pattern string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.canReceive, pattern)
}

func (a *fakeACL) denySub(pattern string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.canSub, pattern)
}

func (a *fakeACL) CanSubscribe(_ context.Context, _ Principal, topicFilter string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for pattern, allowed := range a.canSub {
		if allowed && TopicMatch(pattern, topicFilter) {
			return true
		}
	}
	return false
}

func (a *fakeACL) CanReceive(_ context.Context, _ Principal, topicName string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for pattern, allowed := range a.canReceive {
		if allowed && TopicMatch(pattern, topicName) {
			return true
		}
	}
	return false
}

type fakeConn struct {
	name   string
	taken  chan struct{}
	takeMu sync.Mutex
	takes  int
}

func newFakeConn(name string) *fakeConn {
	return &fakeConn{name: name, taken: make(chan struct{})}
}

func (c *fakeConn) SessionTakenOver() {
	c.takeMu.Lock()
	c.takes++
	if c.takes == 1 {
		close(c.taken)
	}
	c.takeMu.Unlock()
}

func (c *fakeConn) takeoverCount() int {
	c.takeMu.Lock()
	defer c.takeMu.Unlock()
	return c.takes
}

func (c *fakeConn) waitTaken(t *testing.T) {
	t.Helper()
	select {
	case <-c.taken:
	case <-time.After(2 * time.Second):
		t.Fatalf("connection %q was not notified of session take over", c.name)
	}
}

func (c *fakeConn) assertNotTaken(t *testing.T) {
	t.Helper()
	select {
	case <-c.taken:
		t.Fatalf("connection %q unexpectedly received session take over", c.name)
	case <-time.After(20 * time.Millisecond):
	}
}

func restore(t *testing.T, store *Store, req RestoreRequest) *RestoreResult {
	t.Helper()
	res, err := store.Restore(context.Background(), req)
	if err != nil {
		t.Fatalf("Restore(%s): %v", req.ClientID, err)
	}
	return res
}

func topicSet(msgs []Message) map[string]bool {
	out := make(map[string]bool)
	for _, msg := range msgs {
		out[msg.Topic] = true
	}
	return out
}

// TestRestore_PrincipalMismatch_DiscardsSession is the stolen-Client-ID
// attack: attacker credentials must never inherit the victim's session.
func TestRestore_PrincipalMismatch_DiscardsSession(t *testing.T) {
	store := NewStore()
	const clientID = "shared-device-42"
	victim := Principal{Name: "alice"}
	attacker := Principal{Name: "mallory"}

	aliceACL := newFakeACL()
	aliceACL.allowSub("alice/#", "shared/#")
	aliceACL.allowReceive("alice/#", "shared/#")

	// Alice establishes a persistent session with subscriptions and queued
	// QoS 1 traffic, then goes offline.
	first := restore(t, store, RestoreRequest{
		ClientID:  clientID,
		Principal: victim,
		ACL:       aliceACL,
		Conn:      newFakeConn("alice-1"),
	})
	if first.SessionPresent {
		t.Fatal("first CONNECT must report Session Present = 0")
	}
	sess := store.Lookup(clientID)
	if sess == nil || sess.Principal() != victim {
		t.Fatal("session must be bound to the first authenticated principal")
	}
	if !sess.AddSubscription(context.Background(), Subscription{Filter: "alice/upstairs", QoS: 1}) {
		t.Fatal("initial subscription denied")
	}
	if !sess.AddSubscription(context.Background(), Subscription{Filter: "shared/status", QoS: 1}) {
		t.Fatal("initial subscription denied")
	}
	sess.EnqueueMessage(Message{Topic: "alice/upstairs", Payload: []byte("secret"), QoS: 1})
	sess.EnqueueMessage(Message{Topic: "shared/status", Payload: []byte("open"), QoS: 1})
	first.Handle.Detach()

	// Mallory connects reusing Alice's Client ID with her own credentials.
	malloryACL := newFakeACL()
	malloryACL.allowSub("mallory/#")
	malloryACL.allowReceive("mallory/#")
	attack := restore(t, store, RestoreRequest{
		ClientID:  clientID,
		Principal: attacker,
		ACL:       malloryACL,
		Conn:      newFakeConn("mallory-1"),
	})
	if attack.SessionPresent {
		t.Fatal("principal mismatch must report Session Present = 0")
	}
	if len(attack.DroppedMessages) != 4 {
		t.Fatalf("expected 2 subscriptions + 2 messages dropped, got %d: %+v",
			len(attack.DroppedMessages), attack.DroppedMessages)
	}

	sess = store.Lookup(clientID)
	if sess.Principal() != attacker {
		t.Fatal("session must be rebound to the new principal after discard")
	}
	if len(sess.Subscriptions()) != 0 || len(sess.QueuedMessages()) != 0 {
		t.Fatalf("attacker inherited state: subs=%v queue=%v",
			sess.Subscriptions(), sess.QueuedMessages())
	}

	// Alice's original handle is now stale and cannot touch the new session.
	if err := first.Handle.AckPublish(7); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("stale handle AckPublish err = %v, want ErrStaleHandle", err)
	}

	// Mallory's own subscriptions work but cannot see Alice's topics.
	if !sess.AddSubscription(context.Background(), Subscription{Filter: "mallory/notes", QoS: 1}) {
		t.Fatal("attacker should be able to build her own fresh session")
	}

	// Alice reconnecting with correct credentials also starts clean: the old
	// session was destroyed when Mallory took the Client ID.
	attack.Handle.Detach()
	aliceAgain := restore(t, store, RestoreRequest{
		ClientID:  clientID,
		Principal: victim,
		ACL:       aliceACL,
		Conn:      newFakeConn("alice-2"),
	})
	if aliceAgain.SessionPresent {
		t.Fatal("session discarded earlier must not reappear as present")
	}
	if got := aliceAgain.Handle.Session().QueuedMessages(); len(got) != 0 {
		t.Fatalf("Alice's old queued messages leaked: %v", got)
	}
	if got := aliceAgain.Handle.Session().Subscriptions(); len(got) != 0 {
		t.Fatalf("Alice's old subscriptions leaked: %v", got)
	}
}

// TestRestore_RevokedPermissions_FilteredOnReconnect verifies that every
// retained subscription and queued QoS 1 message is re-authorized on restore.
func TestRestore_RevokedPermissions_FilteredOnReconnect(t *testing.T) {
	store := NewStore()
	const clientID = "sensor-7"
	principal := Principal{Name: "bob"}

	acl := newFakeACL()
	acl.allowSub("sensors/temp", "sensors/cmd")
	acl.allowReceive("sensors/temp", "sensors/cmd")

	first := restore(t, store, RestoreRequest{
		ClientID:  clientID,
		Principal: principal,
		ACL:       acl,
		Conn:      newFakeConn("bob-1"),
	})
	sess := first.Handle.Session()
	if !sess.AddSubscription(context.Background(), Subscription{Filter: "sensors/temp", QoS: 1}) ||
		!sess.AddSubscription(context.Background(), Subscription{Filter: "sensors/cmd", QoS: 1}) {
		t.Fatal("initial subscriptions denied")
	}

	// One queued message is currently in flight; it must be redelivered.
	if err := first.Handle.MarkSending(1, Message{Topic: "sensors/temp", Payload: []byte("21C"), QoS: 1}); err != nil {
		t.Fatal(err)
	}
	// Two messages sit in the offline queue.
	sess.EnqueueMessage(Message{Topic: "sensors/temp", Payload: []byte("22C"), QoS: 1})
	sess.EnqueueMessage(Message{Topic: "sensors/cmd", Payload: []byte("reboot"), QoS: 1})

	// Policy change before reconnect: the command channel is fully revoked,
	// including receive permission for already-queued messages.
	acl.denySub("sensors/cmd")
	acl.denyReceive("sensors/cmd")

	second := restore(t, store, RestoreRequest{
		ClientID:  clientID,
		Principal: principal,
		ACL:       acl,
		Conn:      newFakeConn("bob-2"),
	})
	if !second.SessionPresent {
		t.Fatal("same-principal reconnect must report Session Present = 1")
	}

	subs := second.Handle.Session().Subscriptions()
	if len(subs) != 1 || subs[0].Filter != "sensors/temp" {
		t.Fatalf("revoked subscription not removed: %+v", subs)
	}

	// Requeued in-flight redelivery (21C) plus the surviving queued message
	// (22C); the revoked reboot message must have been dropped.
	got := topicSet(second.DroppedMessages)
	if !got["sensors/cmd"] {
		t.Fatalf("dropped list missing revoked entries: %v", got)
	}

	var delivered []string
	for {
		msg, ok := second.Handle.NextPending()
		if !ok {
			break
		}
		delivered = append(delivered, msg.Topic)
	}
	if len(delivered) != 2 {
		t.Fatalf("expected 2 authorized messages, got %v", delivered)
	}
	for _, topic := range delivered {
		if topic == "sensors/cmd" {
			t.Fatal("message on revoked topic was delivered")
		}
	}

	// A brand-new subscription on the revoked filter must be refused too.
	if sess.AddSubscription(context.Background(), Subscription{Filter: "sensors/cmd", QoS: 1}) {
		t.Fatal("subscription on revoked filter was accepted")
	}
}

// TestRestore_TakeoverRace hammers one Client ID from many simultaneous
// CONNECT packets: only the final connection may own the session, every
// displaced connection is notified exactly once and stale in-flight state
// cannot be acknowledged.
func TestRestore_TakeoverRace(t *testing.T) {
	store := NewStore()
	const clientID = "race-client"
	principal := Principal{Name: "carol"}
	acl := newFakeACL()
	acl.allowSub("#")
	acl.allowReceive("#")

	const n = 24
	conns := make([]*fakeConn, n)
	handles := make([]*ConnHandle, n)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		conns[i] = newFakeConn(fmt.Sprintf("carol-%d", i))
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := store.Restore(context.Background(), RestoreRequest{
				ClientID:  clientID,
				Principal: principal,
				ACL:       acl,
				Conn:      conns[i],
			})
			if err != nil {
				t.Errorf("restore %d: %v", i, err)
				return
			}
			handles[i] = res.Handle
		}(i)
	}
	close(start)
	wg.Wait()

	sess := store.Lookup(clientID)
	if sess == nil {
		t.Fatal("session missing after race")
	}

	currentCount := 0
	var current *fakeConn
	for i, h := range handles {
		if h == nil {
			t.Fatalf("handle %d was not installed", i)
		}
		if h.IsCurrent() {
			currentCount++
			current = conns[i]
		} else {
			conns[i].waitTaken(t)
			if conns[i].takeoverCount() != 1 {
				t.Fatalf("conn %d notified %d times, want exactly 1", i, conns[i].takeoverCount())
			}
		}
	}
	if currentCount != 1 {
		t.Fatalf("exactly one current handle expected, found %d", currentCount)
	}
	current.assertNotTaken(t)

	// In-flight publish invalidation: a stale handle's PUBACK is rejected and
	// the packet id can be reused by the current connection.
	stale := handles[0]
	if stale.IsCurrent() {
		stale = handles[1]
	}
	if err := stale.AckPublish(1234); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("late PUBACK err = %v, want ErrStaleHandle", err)
	}
	if err := stale.MarkSending(1234, Message{Topic: "x", QoS: 1}); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("stale MarkSending err = %v, want ErrStaleHandle", err)
	}

	curHandle := sess.handle
	if err := curHandle.MarkSending(1234, Message{Topic: "x", Payload: []byte("y"), QoS: 1}); err != nil {
		t.Fatalf("current handle reuse packet id: %v", err)
	}
	if err := curHandle.AckPublish(1234); err != nil {
		t.Fatalf("current handle PUBACK: %v", err)
	}
}

// TestRestore_TakeoverRequeuesInFlight checks that an unacknowledged
// PUBLISH survives takeover and is redelivered to the new connection.
func TestRestore_TakeoverRequeuesInFlight(t *testing.T) {
	store := NewStore()
	acl := newFakeACL()
	acl.allowSub("#")
	acl.allowReceive("#")
	principal := Principal{Name: "dave"}

	oldConn := newFakeConn("dave-1")
	first := restore(t, store, RestoreRequest{
		ClientID: "dave", Principal: principal, ACL: acl, Conn: oldConn,
	})
	msg := Message{Topic: "dave/mail", Payload: []byte("hello"), QoS: 1}
	if err := first.Handle.MarkSending(42, msg); err != nil {
		t.Fatal(err)
	}

	newConn := newFakeConn("dave-2")
	second := restore(t, store, RestoreRequest{
		ClientID: "dave", Principal: principal, ACL: acl, Conn: newConn,
	})
	if !second.SessionPresent {
		t.Fatal("same principal takeover must set Session Present = 1")
	}
	oldConn.waitTaken(t)

	got, ok := second.Handle.NextPending()
	if !ok || got.Topic != "dave/mail" {
		t.Fatalf("in-flight message not redelivered: ok=%v got=%+v", ok, got)
	}
	if inf := second.Handle.Session().InFlightPublishes(); len(inf) != 0 {
		t.Fatalf("old in-flight entries must be cleared: %+v", inf)
	}

	// Detach of the dead connection is a no-op and must not disturb the
	// current owner.
	first.Handle.Detach()
	if !second.Handle.IsCurrent() {
		t.Fatal("stale Detach displaced the current handle")
	}
}

func TestRestore_CleanStartWipesState(t *testing.T) {
	store := NewStore()
	acl := newFakeACL()
	acl.allowSub("#")
	acl.allowReceive("#")
	principal := Principal{Name: "erin"}

	first := restore(t, store, RestoreRequest{
		ClientID: "erin", Principal: principal, ACL: acl, Conn: newFakeConn("erin-1"),
	})
	sess := first.Handle.Session()
	if !sess.AddSubscription(context.Background(), Subscription{Filter: "a/b", QoS: 1}) {
		t.Fatal("subscription denied")
	}
	sess.EnqueueMessage(Message{Topic: "a/b", QoS: 1})

	second := restore(t, store, RestoreRequest{
		ClientID: "erin", Principal: principal, CleanStart: true, ACL: acl,
		Conn: newFakeConn("erin-2"),
	})
	if second.SessionPresent {
		t.Fatal("Clean Start = 1 must report Session Present = 0")
	}
	if len(second.Handle.Session().Subscriptions()) != 0 ||
		len(second.Handle.Session().QueuedMessages()) != 0 {
		t.Fatal("Clean Start did not wipe retained state")
	}
}

func TestRestore_Validation(t *testing.T) {
	store := NewStore()
	acl := newFakeACL()
	if _, err := store.Restore(context.Background(), RestoreRequest{
		Principal: Principal{Name: "x"}, ACL: acl,
	}); !errors.Is(err, ErrEmptyClientID) {
		t.Fatalf("empty client id err = %v", err)
	}
	if _, err := store.Restore(context.Background(), RestoreRequest{
		ClientID: "x", Principal: Principal{Name: "y"},
	}); !errors.Is(err, ErrNilACL) {
		t.Fatalf("nil acl err = %v", err)
	}
}

func TestHandle_TerminateRemovesSession(t *testing.T) {
	store := NewStore()
	acl := newFakeACL()
	principal := Principal{Name: "frank"}
	res := restore(t, store, RestoreRequest{
		ClientID: "frank", Principal: principal, ACL: acl, Conn: newFakeConn("frank-1"),
	})
	res.Handle.Terminate()
	if store.Lookup("frank") != nil {
		t.Fatal("Terminate did not remove the session")
	}

	// A stale handle cannot delete a session now owned by someone else.
	first := restore(t, store, RestoreRequest{
		ClientID: "frank", Principal: principal, ACL: acl, Conn: newFakeConn("frank-2"),
	})
	second := restore(t, store, RestoreRequest{
		ClientID: "frank", Principal: principal, ACL: acl, Conn: newFakeConn("frank-3"),
	})
	first.Handle.Terminate()
	if store.Lookup("frank") == nil {
		t.Fatal("stale Terminate deleted a session it no longer owns")
	}
	_ = second
}

func TestTopicMatch(t *testing.T) {
	cases := []struct {
		filter string
		topic  string
		want   bool
	}{
		{"sport/tennis/player1", "sport/tennis/player1", true},
		{"sport/tennis/player1", "sport/tennis/player2", false},
		{"sport/+/player1", "sport/tennis/player1", true},
		{"sport/+/player1", "sport/tennis/player2", false},
		{"sport/+", "sport", false},
		{"#", "sport/tennis/player1", true},
		{"sport/#", "sport", true},
		{"sport/#", "sport/tennis", true},
		{"sport/tennis/#", "sport/tennis/player1/ranking", true},
		{"/#", "/finance", true},
		{"#", "$SYS/clients", false},
		{"+", "$SYS", false},
		{"", "a/b", false},
		{"a/#", "", false},
	}
	for _, tc := range cases {
		if got := TopicMatch(tc.filter, tc.topic); got != tc.want {
			t.Errorf("TopicMatch(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}
