package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type testConn struct {
	mu            sync.Mutex
	invalidations int
	takeovers     int
	events        []string
	takenOver     chan struct{}
	takenOverOnce sync.Once
}

func newTestConn() *testConn {
	return &testConn{takenOver: make(chan struct{})}
}

func (c *testConn) InvalidateInflight() {
	c.mu.Lock()
	c.invalidations++
	c.events = append(c.events, "invalidate")
	c.mu.Unlock()
}

func (c *testConn) SessionTakenOver() {
	c.mu.Lock()
	c.takeovers++
	c.events = append(c.events, "taken-over")
	c.mu.Unlock()
	c.takenOverOnce.Do(func() { close(c.takenOver) })
}

func (c *testConn) counts() (invalidations, takeovers int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invalidations, c.takeovers
}

func (c *testConn) eventLog() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

func permissiveACL(principal string, filters ...string) *ACL {
	acl := NewACL()
	for _, filter := range filters {
		acl.Allow(principal, ActionSubscribe, filter)
		acl.Allow(principal, ActionReceive, filter)
	}
	return acl
}

func mustRecover(t *testing.T, m *Manager, req ConnectRequest, conn Connection) *Recovery {
	t.Helper()
	r, err := m.Recover(req, conn)
	if err != nil {
		t.Fatalf("Recover(%+v) error: %v", req, err)
	}
	return r
}

func mustAddSubscription(t *testing.T, r *Recovery, filter string) {
	t.Helper()
	if err := r.AddSubscription(Subscription{Filter: filter, QoS: 1}); err != nil {
		t.Fatalf("AddSubscription(%q) error: %v", filter, err)
	}
}

func mustEnqueue(t *testing.T, r *Recovery, topic string) {
	t.Helper()
	if err := r.Enqueue(QueuedMessage{
		Topic:   topic,
		Payload: []byte("payload-" + topic),
		QoS:     1,
	}); err != nil {
		t.Fatalf("Enqueue(%q) error: %v", topic, err)
	}
}

func TestPrincipalMismatchDiscardsSession(t *testing.T) {
	acl := NewACL()
	for _, principal := range []string{"alice", "mallory"} {
		acl.Allow(principal, ActionSubscribe, "#")
		acl.Allow(principal, ActionReceive, "#")
	}
	m := NewManager(acl)

	victimConn := newTestConn()
	victim := mustRecover(t, m, ConnectRequest{
		ClientID:   "shared-client",
		Principal:  "alice",
		CleanStart: true,
	}, victimConn)
	mustAddSubscription(t, victim, "orders/#")
	mustEnqueue(t, victim, "orders/42")

	attackerConn := newTestConn()
	attacker := mustRecover(t, m, ConnectRequest{
		ClientID:   "shared-client",
		Principal:  "mallory",
		CleanStart: false,
	}, attackerConn)

	if attacker.SessionPresent() {
		t.Fatal("Session Present = 1 for a different principal")
	}
	if got := attacker.Subscriptions(); len(got) != 0 {
		t.Fatalf("attacker inherited subscriptions: %+v", got)
	}
	if got := attacker.QueuedMessages(); len(got) != 0 {
		t.Fatalf("attacker inherited queued messages: %+v", got)
	}
	if _, ok := attacker.NextDelivery(); ok {
		t.Fatal("attacker received the victim's queued message")
	}

	if invalidations, takeovers := victimConn.counts(); invalidations != 0 || takeovers != 0 {
		t.Fatalf("different principal notified the victim: invalidations=%d takeovers=%d",
			invalidations, takeovers)
	}
	if victim.Valid() {
		t.Fatal("victim binding still valid after its session was discarded")
	}
	if _, ok := victim.NextDelivery(); ok {
		t.Fatal("discarded connection still drained the old queue")
	}
	if !attacker.Valid() {
		t.Fatal("attacker's new session is not valid")
	}
}

func TestSamePrincipalTakeoverInvalidatesOldConnection(t *testing.T) {
	acl := permissiveACL("alice", "sensors/#")
	m := NewManager(acl)

	oldConn := newTestConn()
	old := mustRecover(t, m, ConnectRequest{
		ClientID:   "sensor-client",
		Principal:  "alice",
		CleanStart: true,
	}, oldConn)
	mustAddSubscription(t, old, "sensors/#")
	mustEnqueue(t, old, "sensors/temperature")

	newConn := newTestConn()
	fresh := mustRecover(t, m, ConnectRequest{
		ClientID:   "sensor-client",
		Principal:  "alice",
		CleanStart: false,
	}, newConn)

	if !fresh.SessionPresent() {
		t.Fatal("Session Present = 0 for a same-principal resume")
	}
	if got := fresh.Subscriptions(); len(got) != 1 || got[0].Filter != "sensors/#" {
		t.Fatalf("unexpected restored subscriptions: %+v", got)
	}
	delivery, ok := fresh.NextDelivery()
	if !ok || delivery.Message.Topic != "sensors/temperature" {
		t.Fatalf("restored queue delivery = %+v, %v", delivery, ok)
	}

	invalidations, takeovers := oldConn.counts()
	if invalidations != 1 || takeovers != 1 {
		t.Fatalf("old connection callbacks: invalidations=%d takeovers=%d", invalidations, takeovers)
	}
	if events := oldConn.eventLog(); len(events) != 2 ||
		events[0] != "invalidate" || events[1] != "taken-over" {
		t.Fatalf("takeover callback order = %v", events)
	}
	if old.Valid() || old.PublishAllowed() {
		t.Fatal("old connection's publish lease is still valid after takeover")
	}
	if _, ok := old.NextDelivery(); ok {
		t.Fatal("old connection drained the queue after takeover")
	}
	if err := old.Enqueue(QueuedMessage{Topic: "sensors/temperature", QoS: 1}); !errors.Is(err, ErrSessionGone) {
		t.Fatalf("old connection enqueue error = %v, want ErrSessionGone", err)
	}
}

func TestRevokedPermissionsAreDroppedOnReconnect(t *testing.T) {
	acl := permissiveACL("alice", "keep/#", "drop/#")
	m := NewManager(acl)

	conn := newTestConn()
	first := mustRecover(t, m, ConnectRequest{
		ClientID:   "client",
		Principal:  "alice",
		CleanStart: true,
	}, conn)
	mustAddSubscription(t, first, "keep/#")
	mustAddSubscription(t, first, "drop/#")
	mustEnqueue(t, first, "keep/1")
	mustEnqueue(t, first, "drop/1")
	first.Close()

	acl.Revoke("alice", ActionSubscribe, "drop/#")
	acl.Revoke("alice", ActionReceive, "drop/#")

	second := mustRecover(t, m, ConnectRequest{
		ClientID:   "client",
		Principal:  "alice",
		CleanStart: false,
	}, newTestConn())
	if !second.SessionPresent() {
		t.Fatal("Session Present = 0 for a same-principal resume")
	}

	subs := second.Subscriptions()
	if len(subs) != 1 || subs[0].Filter != "keep/#" {
		t.Fatalf("restored subscriptions = %+v, want only keep/#", subs)
	}
	queue := second.QueuedMessages()
	if len(queue) != 1 || queue[0].Topic != "keep/1" {
		t.Fatalf("restored queue = %+v, want only keep/1", queue)
	}

	delivery, ok := second.NextDelivery()
	if !ok || delivery.Message.Topic != "keep/1" {
		t.Fatalf("delivery = %+v, %v; want keep/1", delivery, ok)
	}
	if _, ok := second.NextDelivery(); ok {
		t.Fatal("received a message from the revoked drop/# subscription")
	}
}

func TestDeliveryRechecksCurrentACL(t *testing.T) {
	acl := permissiveACL("alice", "a/#", "b/#")
	m := NewManager(acl)

	r := mustRecover(t, m, ConnectRequest{
		ClientID:   "client",
		Principal:  "alice",
		CleanStart: true,
	}, newTestConn())
	mustAddSubscription(t, r, "a/#")
	mustAddSubscription(t, r, "b/#")
	mustEnqueue(t, r, "a/1")
	mustEnqueue(t, r, "b/1")

	acl.Revoke("alice", ActionSubscribe, "a/#")
	acl.Revoke("alice", ActionReceive, "a/#")

	delivery, ok := r.NextDelivery()
	if !ok || delivery.Message.Topic != "b/1" {
		t.Fatalf("delivery = %+v, %v; want b/1 after a/# was revoked", delivery, ok)
	}

	acl.Revoke("alice", ActionSubscribe, "b/#")
	acl.Revoke("alice", ActionReceive, "b/#")
	if _, ok := r.NextDelivery(); ok {
		t.Fatal("delivered b/1 after its permission was revoked")
	}
}

func TestTakeoverRace(t *testing.T) {
	const contenders = 40

	acl := permissiveACL("alice", "#")
	m := NewManager(acl)

	conns := make([]*testConn, contenders)
	recoveries := make([]*Recovery, contenders)
	errs := make([]error, contenders)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conns[i] = newTestConn()
			<-start
			recoveries[i], errs[i] = m.Recover(ConnectRequest{
				ClientID:   "raced-client",
				Principal:  "alice",
				CleanStart: false,
			}, conns[i])
		}(i)
	}
	close(start)
	wg.Wait()

	valid := 0
	notifications := 0
	invalidations := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v", i, err)
		}
		if recoveries[i].Valid() {
			valid++
		}
		inv, taken := conns[i].counts()
		if inv > 1 || taken > 1 {
			t.Fatalf("contender %d notified more than once: invalidations=%d takeovers=%d", i, inv, taken)
		}
		invalidations += inv
		notifications += taken
	}
	if valid != 1 {
		t.Fatalf("%d sessions are valid at once, want exactly 1", valid)
	}
	if notifications != contenders-1 {
		t.Fatalf("takeover notifications = %d, want %d", notifications, contenders-1)
	}
	if invalidations != contenders-1 {
		t.Fatalf("in-flight invalidations = %d, want %d", invalidations, contenders-1)
	}

	for i, r := range recoveries {
		if r.Valid() {
			continue
		}
		if _, ok := r.NextDelivery(); ok {
			t.Fatalf("superseded contender %d still delivered a message", i)
		}
		if err := r.Enqueue(QueuedMessage{Topic: "x", QoS: 1}); !errors.Is(err, ErrSessionGone) {
			t.Fatalf("superseded contender %d enqueue error = %v", i, err)
		}
	}
}

func TestCleanStartDiscardsState(t *testing.T) {
	acl := permissiveACL("alice", "topic/#")
	m := NewManager(acl)

	oldConn := newTestConn()
	old := mustRecover(t, m, ConnectRequest{
		ClientID:   "client",
		Principal:  "alice",
		CleanStart: true,
	}, oldConn)
	mustAddSubscription(t, old, "topic/#")
	mustEnqueue(t, old, "topic/1")

	fresh := mustRecover(t, m, ConnectRequest{
		ClientID:   "client",
		Principal:  "alice",
		CleanStart: true,
	}, newTestConn())
	if fresh.SessionPresent() {
		t.Fatal("Session Present = 1 for Clean Start 1")
	}
	if got := fresh.Subscriptions(); len(got) != 0 {
		t.Fatalf("Clean Start kept subscriptions: %+v", got)
	}
	if got := fresh.QueuedMessages(); len(got) != 0 {
		t.Fatalf("Clean Start kept queued messages: %+v", got)
	}
	invalidations, takeovers := oldConn.counts()
	if invalidations != 1 || takeovers != 1 {
		t.Fatalf("old connection callbacks = %d/%d, want 1/1", invalidations, takeovers)
	}
}

func TestOfflineEnqueueUsesLiveACL(t *testing.T) {
	acl := permissiveACL("alice", "queue/#")
	m := NewManager(acl)

	conn := newTestConn()
	r := mustRecover(t, m, ConnectRequest{
		ClientID:   "offline-client",
		Principal:  "alice",
		CleanStart: true,
	}, conn)
	mustAddSubscription(t, r, "queue/#")
	r.Close()

	if err := m.Enqueue("offline-client", QueuedMessage{Topic: "queue/1", QoS: 1}); err != nil {
		t.Fatalf("offline Enqueue error: %v", err)
	}

	acl.Revoke("alice", ActionReceive, "queue/#")
	resumed := mustRecover(t, m, ConnectRequest{
		ClientID:   "offline-client",
		Principal:  "alice",
		CleanStart: false,
	}, newTestConn())
	if got := resumed.QueuedMessages(); len(got) != 0 {
		t.Fatalf("revoked offline message survived resume: %+v", got)
	}
}

func TestTopicMatches(t *testing.T) {
	tests := []struct {
		filter string
		topic  string
		want   bool
	}{
		{"sport/tennis/player1", "sport/tennis/player1", true},
		{"sport/tennis/player1", "sport/tennis/player2", false},
		{"sport/tennis/+", "sport/tennis/player1", true},
		{"sport/tennis/+", "sport/tennis/player1/ranking", false},
		{"sport/#", "sport", true},
		{"sport/#", "sport/tennis/player1", true},
		{"#", "sport/tennis", true},
		{"#", "$SYS/broker/uptime", false},
		{"+/broker/uptime", "$SYS/broker/uptime", false},
		{"$SYS/#", "$SYS/broker/uptime", true},
		{"a/+/c", "a//c", true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_%s", tc.filter, tc.topic), func(t *testing.T) {
			if got := TopicMatches(tc.filter, tc.topic); got != tc.want {
				t.Fatalf("TopicMatches(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.want)
			}
		})
	}
}
