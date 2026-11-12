package netsec_test

import (
	"errors"
	"sync"
	"testing"

	"example.org/netsec/q0106"
)

type grantKey struct {
	secret   string
	resource netsec.Resource
	access   netsec.Access
}

type authCall struct {
	credential netsec.Credential
	resource   netsec.Resource
	access     netsec.Access
}

type recordingAuthorizer struct {
	mu     sync.Mutex
	grants map[grantKey]bool
	calls  []authCall
}

func newRecordingAuthorizer() *recordingAuthorizer {
	return &recordingAuthorizer{grants: make(map[grantKey]bool)}
}

func (a *recordingAuthorizer) allow(secret string, resource netsec.Resource, access netsec.Access) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.grants[grantKey{secret: secret, resource: resource, access: access}] = true
}

func (a *recordingAuthorizer) Authorize(credential netsec.Credential, resource netsec.Resource, access netsec.Access) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, authCall{credential: credential, resource: resource, access: access})
	return a.grants[grantKey{secret: credential.Secret, resource: resource, access: access}]
}

func (a *recordingAuthorizer) callCount(secret string, resource netsec.Resource, access netsec.Access) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, call := range a.calls {
		if call.credential.Secret == secret && call.resource == resource && call.access == access {
			count++
		}
	}
	return count
}

func (a *recordingAuthorizer) lastGeneration(secret string, resource netsec.Resource, access netsec.Access) (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var generation uint64
	found := false
	for _, call := range a.calls {
		if call.credential.Secret == secret && call.resource == resource && call.access == access {
			generation = call.credential.Generation
			found = true
		}
	}
	return generation, found
}

func mustOpen(t *testing.T, conn *netsec.Connection, number uint16) *netsec.Channel {
	t.Helper()
	ch, err := conn.OpenChannel(number)
	if err != nil {
		t.Fatalf("OpenChannel(%d): %v", number, err)
	}
	return ch
}

func mustConsume(t *testing.T, ch *netsec.Channel, queue, tag string) {
	t.Helper()
	if _, err := ch.Consume(queue, tag); err != nil {
		t.Fatalf("Consume(%q, %q): %v", queue, tag, err)
	}
}

func wantErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want errors.Is(%v)", err, target)
	}
}

func TestRefreshForcesPermissionRecompute(t *testing.T) {
	auth := newRecordingAuthorizer()
	orders := netsec.Queue("orders")
	events := netsec.Exchange("events")
	auth.allow("v1", orders, netsec.Configure)
	auth.allow("v1", orders, netsec.Write)
	auth.allow("v1", orders, netsec.Read)
	auth.allow("v1", events, netsec.Write)

	conn := netsec.NewConnection("conn-1", "v1", auth)
	if got := conn.Generation(); got != 1 {
		t.Fatalf("initial generation = %d, want 1", got)
	}
	ch := mustOpen(t, conn, 7)

	if err := ch.DeclareQueue("orders"); err != nil {
		t.Fatalf("DeclareQueue under v1: %v", err)
	}
	if err := ch.DeclareQueue("orders"); err != nil {
		t.Fatalf("second DeclareQueue under v1: %v", err)
	}
	if err := ch.Publish("events"); err != nil {
		t.Fatalf("Publish under v1: %v", err)
	}
	if err := ch.Publish("events"); err != nil {
		t.Fatalf("second Publish under v1: %v", err)
	}
	if err := ch.Read(orders); err != nil {
		t.Fatalf("Read under v1: %v", err)
	}
	if err := ch.Read(orders); err != nil {
		t.Fatalf("second Read under v1: %v", err)
	}

	if got := auth.callCount("v1", orders, netsec.Configure); got != 1 {
		t.Fatalf("configure authorizer calls under v1 = %d, want 1 (cached)", got)
	}
	if got := auth.callCount("v1", events, netsec.Write); got != 1 {
		t.Fatalf("write authorizer calls under v1 = %d, want 1 (cached)", got)
	}
	if got := auth.callCount("v1", orders, netsec.Read); got != 1 {
		t.Fatalf("read authorizer calls under v1 = %d, want 1 (cached)", got)
	}
	if allowed, generation, ok := ch.CachedPermission(netsec.Read, orders); !ok || !allowed || generation != 1 {
		t.Fatalf("cached read = (%v, %d, %v), want (true, 1, true)", allowed, generation, ok)
	}

	if got := conn.UpdateSecret("v2"); got != 2 {
		t.Fatalf("UpdateSecret returned generation %d, want 2", got)
	}
	auth.allow("v2", events, netsec.Write)

	if err := ch.DeclareQueue("orders"); !errors.Is(err, netsec.ErrAccessRefused) {
		t.Fatalf("DeclareQueue under v2 = %v, want access refused", err)
	}
	if got := auth.callCount("v2", orders, netsec.Configure); got != 1 {
		t.Fatalf("configure authorizer calls under v2 = %d, want 1 (generation changed)", got)
	}
	if generation, ok := auth.lastGeneration("v2", orders, netsec.Configure); !ok || generation != 2 {
		t.Fatalf("configure check generation = (%d, %v), want (2, true)", generation, ok)
	}

	if err := ch.Read(orders); !errors.Is(err, netsec.ErrAccessRefused) {
		t.Fatalf("Read under v2 = %v, want access refused", err)
	}
	if got := auth.callCount("v2", orders, netsec.Read); got != 1 {
		t.Fatalf("read authorizer calls under v2 = %d, want 1", got)
	}

	if err := ch.Publish("events"); err != nil {
		t.Fatalf("Publish under v2: %v", err)
	}
	if got := auth.callCount("v2", events, netsec.Write); got != 1 {
		t.Fatalf("write authorizer calls under v2 = %d, want 1 (allow also recomputed)", got)
	}
	if err := ch.Publish("events"); err != nil {
		t.Fatalf("second Publish under v2: %v", err)
	}
	if got := auth.callCount("v2", events, netsec.Write); got != 1 {
		t.Fatalf("write authorizer calls after second Publish = %d, want 1 (v2 cached)", got)
	}

	if allowed, generation, ok := ch.CachedPermission(netsec.Read, orders); !ok || allowed || generation != 2 {
		t.Fatalf("cached read = (%v, %d, %v), want (false, 2, true)", allowed, generation, ok)
	}
}

func TestConsumerRevalidatedBeforeNextDelivery(t *testing.T) {
	auth := newRecordingAuthorizer()
	queue := netsec.Queue("revoked-queue")
	auth.allow("v1", queue, netsec.Read)

	conn := netsec.NewConnection("conn-1", "v1", auth)
	ch := mustOpen(t, conn, 3)
	mustConsume(t, ch, "revoked-queue", "consumer-1")

	first, err := ch.Deliver("consumer-1", []byte("first"))
	if err != nil {
		t.Fatalf("first Deliver: %v", err)
	}
	if first.Tag != 1 {
		t.Fatalf("first delivery tag = %d, want 1", first.Tag)
	}

	conn.UpdateSecret("v2")
	_, err = ch.Deliver("consumer-1", []byte("second"))
	wantErrorIs(t, err, netsec.ErrConsumerCancelled)
	wantErrorIs(t, err, netsec.ErrAccessRefused)

	if consumers := ch.Consumers(); len(consumers) != 0 {
		t.Fatalf("consumers after revoked delivery = %+v, want none", consumers)
	}
	if tags := ch.OutstandingDeliveryTags(); len(tags) != 1 || tags[0] != 1 {
		t.Fatalf("outstanding tags after cancelled delivery = %v, want [1] (no tag allocated)", tags)
	}
	if got := auth.callCount("v2", queue, netsec.Read); got != 1 {
		t.Fatalf("read rechecks under v2 = %d, want 1", got)
	}

	_, err = ch.Deliver("consumer-1", []byte("third"))
	wantErrorIs(t, err, netsec.ErrUnknownConsumer)
}

func TestAllowedRefreshInterleavedWithDeliveries(t *testing.T) {
	auth := newRecordingAuthorizer()
	queue := netsec.Queue("orders")
	auth.allow("v1", queue, netsec.Read)
	auth.allow("v2", queue, netsec.Read)

	conn := netsec.NewConnection("conn-1", "v1", auth)
	ch := mustOpen(t, conn, 5)
	mustConsume(t, ch, "orders", "worker")

	first, err := ch.Deliver("worker", []byte("one"))
	if err != nil {
		t.Fatalf("Deliver before refresh: %v", err)
	}
	if first.Tag != 1 {
		t.Fatalf("first tag = %d, want 1", first.Tag)
	}

	conn.UpdateSecret("v2")
	second, err := ch.Deliver("worker", []byte("two"))
	if err != nil {
		t.Fatalf("Deliver after allowed refresh: %v", err)
	}
	if second.Tag != 2 {
		t.Fatalf("second tag = %d, want 2", second.Tag)
	}
	if second.ChannelEpoch != first.ChannelEpoch {
		t.Fatalf("channel epoch changed across refresh: %d -> %d", first.ChannelEpoch, second.ChannelEpoch)
	}
	if err := ch.Ack(1); err != nil {
		t.Fatalf("Ack(1): %v", err)
	}

	conn.UpdateSecret("v3")
	_, err = ch.Deliver("worker", []byte("three"))
	wantErrorIs(t, err, netsec.ErrConsumerCancelled)
	wantErrorIs(t, err, netsec.ErrAccessRefused)

	if got := auth.callCount("v2", queue, netsec.Read); got != 1 {
		t.Fatalf("read checks under v2 = %d, want 1", got)
	}
	if got := auth.callCount("v3", queue, netsec.Read); got != 1 {
		t.Fatalf("read checks under v3 = %d, want 1", got)
	}
	if tags := ch.OutstandingDeliveryTags(); len(tags) != 1 || tags[0] != 2 {
		t.Fatalf("outstanding tags = %v, want [2]", tags)
	}
}

func TestChannelNumberReuseResetsDeliveryTagState(t *testing.T) {
	auth := newRecordingAuthorizer()
	auth.allow("v1", netsec.Queue("q"), netsec.Read)

	conn := netsec.NewConnection("conn-1", "v1", auth)
	old := mustOpen(t, conn, 9)
	if old.Epoch() != 1 {
		t.Fatalf("first epoch = %d, want 1", old.Epoch())
	}
	mustConsume(t, old, "q", "old-consumer")

	first, err := old.Deliver("old-consumer", []byte("one"))
	if err != nil {
		t.Fatalf("old Deliver one: %v", err)
	}
	second, err := old.Deliver("old-consumer", []byte("two"))
	if err != nil {
		t.Fatalf("old Deliver two: %v", err)
	}
	if first.Tag != 1 || second.Tag != 2 {
		t.Fatalf("old tags = %d, %d, want 1, 2", first.Tag, second.Tag)
	}

	if err := old.Close(); err != nil {
		t.Fatalf("Close old channel: %v", err)
	}
	if _, ok := conn.Channel(9); ok {
		t.Fatalf("closed channel 9 is still registered")
	}
	if err := old.Ack(1); !errors.Is(err, netsec.ErrChannelClosed) || !errors.Is(err, netsec.ErrUnknownDeliveryTag) {
		t.Fatalf("Ack on closed channel = %v, want closed and unknown delivery", err)
	}

	reopened := mustOpen(t, conn, 9)
	if reopened.Epoch() != 2 {
		t.Fatalf("reopened epoch = %d, want 2", reopened.Epoch())
	}
	if consumers := reopened.Consumers(); len(consumers) != 0 {
		t.Fatalf("reopened channel inherited consumers: %+v", consumers)
	}

	if err := conn.Ack(9, 2); !errors.Is(err, netsec.ErrUnknownDeliveryTag) {
		t.Fatalf("stale ack for old tag 2 = %v, want unknown delivery", err)
	}
	if err := conn.Nack(9, 1, false); !errors.Is(err, netsec.ErrUnknownDeliveryTag) {
		t.Fatalf("stale nack for old tag 1 = %v, want unknown delivery", err)
	}
	if _, err := conn.OpenChannel(9); !errors.Is(err, netsec.ErrChannelInUse) {
		t.Fatalf("second OpenChannel(9) = %v, want channel in use", err)
	}

	mustConsume(t, reopened, "q", "new-consumer")
	fresh, err := reopened.Deliver("new-consumer", []byte("fresh"))
	if err != nil {
		t.Fatalf("fresh Deliver: %v", err)
	}
	if fresh.Tag != 1 {
		t.Fatalf("fresh delivery tag = %d, want reset to 1", fresh.Tag)
	}
	if fresh.ChannelEpoch != reopened.Epoch() {
		t.Fatalf("fresh delivery epoch = %d, want %d", fresh.ChannelEpoch, reopened.Epoch())
	}
	if err := reopened.Ack(fresh.Tag); err != nil {
		t.Fatalf("Ack fresh tag: %v", err)
	}
	if err := conn.Ack(9, 2); !errors.Is(err, netsec.ErrUnknownDeliveryTag) {
		t.Fatalf("old tag 2 after one new delivery = %v, want unknown delivery", err)
	}
}

func TestMultipleChannelsKeepIndependentAuthAndTagState(t *testing.T) {
	auth := newRecordingAuthorizer()
	q1 := netsec.Queue("queue-1")
	q2 := netsec.Queue("queue-2")
	auth.allow("v1", q1, netsec.Read)
	auth.allow("v1", q2, netsec.Read)
	auth.allow("v2", q2, netsec.Read)

	conn := netsec.NewConnection("conn-1", "v1", auth)
	ch1 := mustOpen(t, conn, 1)
	ch2 := mustOpen(t, conn, 2)
	mustConsume(t, ch1, "queue-1", "consumer-1")
	mustConsume(t, ch2, "queue-2", "consumer-2")

	d1, err := ch1.Deliver("consumer-1", []byte("q1-one"))
	if err != nil {
		t.Fatalf("ch1 Deliver: %v", err)
	}
	d2, err := ch2.Deliver("consumer-2", []byte("q2-one"))
	if err != nil {
		t.Fatalf("ch2 Deliver: %v", err)
	}
	if d1.Tag != 1 || d2.Tag != 1 {
		t.Fatalf("independent channel tags = %d, %d, want 1, 1", d1.Tag, d2.Tag)
	}
	if err := ch1.Ack(d1.Tag); err != nil {
		t.Fatalf("ch1 Ack: %v", err)
	}
	if tags := ch2.OutstandingDeliveryTags(); len(tags) != 1 || tags[0] != 1 {
		t.Fatalf("ch1 ack changed ch2 tags: %v, want [1]", tags)
	}

	conn.UpdateSecret("v2")
	if _, err := ch1.Deliver("consumer-1", []byte("q1-two")); !errors.Is(err, netsec.ErrConsumerCancelled) {
		t.Fatalf("ch1 Deliver after revocation = %v, want consumer cancelled", err)
	}
	d2b, err := ch2.Deliver("consumer-2", []byte("q2-two"))
	if err != nil {
		t.Fatalf("ch2 Deliver after refresh: %v", err)
	}
	if d2b.Tag != 2 {
		t.Fatalf("ch2 second tag = %d, want 2", d2b.Tag)
	}

	if consumers := ch1.Consumers(); len(consumers) != 0 {
		t.Fatalf("ch1 consumers = %+v, want none", consumers)
	}
	if consumers := ch2.Consumers(); len(consumers) != 1 || consumers[0].Tag != "consumer-2" {
		t.Fatalf("ch2 consumers = %+v, want consumer-2", consumers)
	}
	if err := ch2.Ack(1); err != nil {
		t.Fatalf("ch2 Ack(1): %v", err)
	}
	if err := ch2.Ack(2); err != nil {
		t.Fatalf("ch2 Ack(2): %v", err)
	}
}

func TestPermissionCacheIsPerChannel(t *testing.T) {
	auth := newRecordingAuthorizer()
	queue := netsec.Queue("shared")
	auth.allow("v1", queue, netsec.Read)
	auth.allow("v2", queue, netsec.Read)

	conn := netsec.NewConnection("conn-1", "v1", auth)
	ch1 := mustOpen(t, conn, 1)
	ch2 := mustOpen(t, conn, 2)

	for i := 0; i < 2; i++ {
		if err := ch1.Read(queue); err != nil {
			t.Fatalf("ch1 Read: %v", err)
		}
		if err := ch2.Read(queue); err != nil {
			t.Fatalf("ch2 Read: %v", err)
		}
	}
	if got := auth.callCount("v1", queue, netsec.Read); got != 2 {
		t.Fatalf("v1 read authorizer calls = %d, want 2 (one cached entry per channel)", got)
	}

	conn.UpdateSecret("v2")
	if err := ch1.Read(queue); err != nil {
		t.Fatalf("ch1 Read after refresh: %v", err)
	}
	if got := auth.callCount("v2", queue, netsec.Read); got != 1 {
		t.Fatalf("v2 read authorizer calls after ch1 = %d, want 1", got)
	}
	if err := ch2.Read(queue); err != nil {
		t.Fatalf("ch2 Read after refresh: %v", err)
	}
	if got := auth.callCount("v2", queue, netsec.Read); got != 2 {
		t.Fatalf("v2 read authorizer calls after both channels = %d, want 2", got)
	}
}

func TestRefreshWithSameSecretStillInvalidatesCache(t *testing.T) {
	auth := newRecordingAuthorizer()
	queue := netsec.Queue("q")
	auth.allow("same", queue, netsec.Read)

	conn := netsec.NewConnection("conn-1", "same", auth)
	ch := mustOpen(t, conn, 1)
	if err := ch.Read(queue); err != nil {
		t.Fatalf("Read under generation 1: %v", err)
	}
	if err := ch.Read(queue); err != nil {
		t.Fatalf("cached Read under generation 1: %v", err)
	}
	if got := auth.callCount("same", queue, netsec.Read); got != 1 {
		t.Fatalf("authorizer calls under generation 1 = %d, want 1", got)
	}

	if got := conn.UpdateSecret("same"); got != 2 {
		t.Fatalf("UpdateSecret with unchanged secret returned %d, want 2", got)
	}
	if err := ch.Read(queue); err != nil {
		t.Fatalf("Read under generation 2: %v", err)
	}
	if got := auth.callCount("same", queue, netsec.Read); got != 2 {
		t.Fatalf("authorizer calls after same-secret refresh = %d, want 2", got)
	}
	if generation, ok := auth.lastGeneration("same", queue, netsec.Read); !ok || generation != 2 {
		t.Fatalf("last read check generation = (%d, %v), want (2, true)", generation, ok)
	}
	if _, generation, ok := ch.CachedPermission(netsec.Read, queue); !ok || generation != 2 {
		t.Fatalf("cached read generation = (%d, %v), want (2, true)", generation, ok)
	}
}

type refreshRaceAuthorizer struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once

	mu    sync.Mutex
	calls int
}

func (a *refreshRaceAuthorizer) Authorize(credential netsec.Credential, resource netsec.Resource, access netsec.Access) bool {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()

	if call == 2 {
		a.once.Do(func() { close(a.entered) })
		<-a.release
	}
	return credential.Generation < 3
}

func TestRefreshDuringAuthorizationDiscardsSupersededDecision(t *testing.T) {
	auth := &refreshRaceAuthorizer{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	conn := netsec.NewConnection("conn-1", "v1", auth)
	ch := mustOpen(t, conn, 4)
	mustConsume(t, ch, "q", "worker")
	if got := conn.UpdateSecret("v2"); got != 2 {
		t.Fatalf("first UpdateSecret returned generation %d, want 2", got)
	}

	delivered := make(chan error, 1)
	go func() {
		_, err := ch.Deliver("worker", []byte("racing"))
		delivered <- err
	}()

	<-auth.entered
	if got := conn.UpdateSecret("v3"); got != 3 {
		t.Fatalf("second UpdateSecret returned generation %d, want 3", got)
	}
	close(auth.release)

	err := <-delivered
	wantErrorIs(t, err, netsec.ErrConsumerCancelled)
	wantErrorIs(t, err, netsec.ErrAccessRefused)

	if tags := ch.OutstandingDeliveryTags(); len(tags) != 0 {
		t.Fatalf("outstanding tags = %v, want none", tags)
	}
	if consumers := ch.Consumers(); len(consumers) != 0 {
		t.Fatalf("consumers = %+v, want none", consumers)
	}

	auth.mu.Lock()
	calls := auth.calls
	auth.mu.Unlock()
	if calls != 3 {
		t.Fatalf("authorizer calls = %d, want 3 (consume, superseded delivery check, retry)", calls)
	}
}
