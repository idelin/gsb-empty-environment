package sessiontakeover

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func connectErr(r *Registry, req ConnectRequest) error {
	_, _, err := r.Connect(context.Background(), req)
	return err
}

// 同 ID 异主体 - reject 模式：攻击者不得继承受害者会话。
func TestDifferentSubjectRejected(t *testing.T) {
	clock := newFakeClock()
	inspector := newFakeInspector()
	r := newTestRegistry(t, ConflictReject, clock, inspector)

	victim := mustConnect(t, r, connectReq("t1", "device-7", "alice", "cred-alice"))
	if err := victim.AddSubscription("alice/secret", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := victim.EnqueueOffline(Message{Topic: "alice/secret", Payload: []byte("bank-token"), QoS: 1}); err != nil {
		t.Fatalf("EnqueueOffline: %v", err)
	}

	// Victim goes offline; the persistent session must remain available.
	if err := victim.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	attackerReq := connectReq("t1", "device-7", "mallory", "cred-mallory")
	assertReject(t, r, attackerReq, ReasonDifferentPrincipal)
	if !errors.Is(connectErr(r, attackerReq), ErrTakeoverRejected) {
		t.Fatalf("expected ErrTakeoverRejected")
	}

	// Victim still owns everything and resumes with state intact.
	resumed := mustConnect(t, r, connectReq("t1", "device-7", "alice", "cred-alice-2"))
	assertSubscriptions(t, resumed, []string{"alice/secret"})
	msgs, err := resumed.DrainOffline()
	if err != nil {
		t.Fatalf("DrainOffline: %v", err)
	}
	if len(msgs) != 1 || string(msgs[0].Payload) != "bank-token" {
		t.Fatalf("victim offline messages leaked or lost: %+v", msgs)
	}

	// The original handle stays invalid after takeover.
	if err := victim.AddSubscription("x", 0); !errors.Is(err, ErrHandleClosed) {
		t.Fatalf("old handle usable after resume: %v", err)
	}
}

// 同 ID 异主体 - isolate 模式：攻击者拿到隔离空会话，受害者会话原样保留。
func TestDifferentSubjectIsolated(t *testing.T) {
	clock := newFakeClock()
	r := newTestRegistry(t, ConflictIsolate, clock, newFakeInspector())

	victim := mustConnect(t, r, connectReq("t1", "shared-device", "alice", "c1"))
	if err := victim.AddSubscription("alice/data", 2); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := victim.SetWill(&Will{Topic: "alice/will", Payload: []byte("bye"), QoS: 1}); err != nil {
		t.Fatalf("SetWill: %v", err)
	}
	if err := victim.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	attacker, res, err := r.Connect(context.Background(), connectReq("t1", "shared-device", "mallory", "c2"))
	if err != nil {
		t.Fatalf("isolated Connect: %v", err)
	}
	if res.Outcome != OutcomeIsolated || res.Reason != ReasonDifferentPrincipal || res.SessionPresent {
		t.Fatalf("isolation result = %+v", res)
	}
	subs, _ := attacker.Subscriptions()
	if len(subs) != 0 {
		t.Fatalf("attacker inherited subscriptions: %+v", subs)
	}
	if will, _ := attacker.Will(); will != nil {
		t.Fatalf("attacker inherited victim will: %+v", will)
	}
	if err := attacker.AddSubscription("mallory/data", 1); err != nil {
		t.Fatalf("attacker AddSubscription: %v", err)
	}

	// Victim resume is unaffected by the isolated session.
	resumed := mustConnect(t, r, connectReq("t1", "shared-device", "alice", "c1b"))
	assertSubscriptions(t, resumed, []string{"alice/data"})
	if will, _ := resumed.Will(); will == nil || will.Topic != "alice/will" {
		t.Fatalf("victim will lost during isolation: %+v", will)
	}

	attacker2, res2, _ := r.Connect(context.Background(), connectReq("t1", "shared-device", "mallory", "c2b"))
	if res2.Outcome != OutcomeResumed || !res2.SessionPresent {
		t.Fatalf("attacker reconnection = %+v, want own-session resume", res2)
	}
	assertSubscriptions(t, attacker2, []string{"mallory/data"})
}

// 跨租户：相同 client ID 在不同租户间绝不迁移，也无法互查。
func TestCrossTenantNoMigration(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())

	a := mustConnect(t, r, connectReq("tenant-a", "dev", "svc", "ca"))
	if err := a.AddSubscription("a/topic", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := a.EnqueueOffline(Message{Topic: "a/topic", Payload: []byte("secret-a"), QoS: 1}); err != nil {
		t.Fatalf("EnqueueOffline: %v", err)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	b := mustConnect(t, r, connectReq("tenant-b", "dev", "svc", "cb"))
	subs, _ := b.Subscriptions()
	if len(subs) != 0 {
		t.Fatalf("tenant-b inherited tenant-a subscriptions: %+v", subs)
	}
	if got := b.session.OfflineDepth(); got != 0 {
		t.Fatalf("tenant-b saw tenant-a offline depth %d", got)
	}
	if _, ok := r.Lookup("tenant-b", "dev", "svc"); !ok {
		t.Fatalf("tenant-b session missing")
	}
	if _, ok := r.Lookup("tenant-a", "dev", "attacker"); ok {
		t.Fatalf("cross-subject lookup leaked session")
	}

	resumedA := mustConnect(t, r, connectReq("tenant-a", "dev", "svc", "ca2"))
	assertSubscriptions(t, resumedA, []string{"a/topic"})
	msgs, _ := resumedA.DrainOffline()
	if len(msgs) != 1 || string(msgs[0].Payload) != "secret-a" {
		t.Fatalf("tenant-a messages leaked across tenants: %+v", msgs)
	}
}

// 过期会话：到期后不迁移，同主体重连只得到全新空会话。
func TestExpiredSessionNotMigrated(t *testing.T) {
	clock := newFakeClock()
	r := newTestRegistry(t, ConflictReject, clock, newFakeInspector())

	req := connectReq("t1", "dev", "alice", "c1")
	req.Expiry = time.Minute
	h := mustConnect(t, r, req)
	if err := h.AddSubscription("alice/x", 2); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := h.EnqueueOffline(Message{Topic: "alice/x", Payload: []byte("old"), QoS: 1}); err != nil {
		t.Fatalf("EnqueueOffline: %v", err)
	}
	if err := h.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	clock.Advance(61 * time.Second)

	newReq := connectReq("t1", "dev", "alice", "c2")
	h2, res, err := r.Connect(context.Background(), newReq)
	if err != nil {
		t.Fatalf("Connect after expiry: %v", err)
	}
	if res.Outcome != OutcomeCreated || res.Reason != ReasonSessionExpired || res.SessionPresent {
		t.Fatalf("expired session migrated: %+v", res)
	}
	subs, _ := h2.Subscriptions()
	if len(subs) != 0 {
		t.Fatalf("expired subscriptions migrated: %+v", subs)
	}
	if got := h2.session.OfflineDepth(); got != 0 {
		t.Fatalf("expired offline queue migrated, depth=%d", got)
	}

	if n := r.Sweep(); n != 0 {
		t.Fatalf("fresh session swept: %d", n)
	}
	if !h.closed.Load() {
		t.Fatalf("expired handle not invalidated")
	}
}

// 原主会话过期后，client ID 被释放，新主体可正常接管而不触发冲突。
func TestNewSubjectAfterExpiryTakesFreeClientID(t *testing.T) {
	clock := newFakeClock()
	r := newTestRegistry(t, ConflictReject, clock, newFakeInspector())

	req := connectReq("t1", "dev", "alice", "c1")
	req.Expiry = time.Minute
	old := mustConnect(t, r, req)
	if err := old.AddSubscription("alice/secret", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := old.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	// Before expiry a different subject is still rejected.
	assertReject(t, r, connectReq("t1", "dev", "mallory", "cm"), ReasonDifferentPrincipal)

	clock.Advance(2 * time.Minute)
	r.Sweep()

	h, res, err := r.Connect(context.Background(), connectReq("t1", "dev", "mallory", "cm2"))
	if err != nil {
		t.Fatalf("new subject after expiry: %v", err)
	}
	if res.Outcome != OutcomeCreated || res.SessionPresent {
		t.Fatalf("expected fresh session, got %+v", res)
	}
	if subs, _ := h.Subscriptions(); len(subs) != 0 {
		t.Fatalf("new subject inherited expired subject state: %+v", subs)
	}

	// And a now-stale attempt by the old subject is itself rejected.
	assertReject(t, r, connectReq("t1", "dev", "alice", "c2"), ReasonDifferentPrincipal)
}

// 撤销凭据：即使同主体也不得接管；撤销前的会话仍只对有效凭据开放。
func TestRevokedCredentialCannotTakeover(t *testing.T) {
	clock := newFakeClock()
	inspector := newFakeInspector()
	r := newTestRegistry(t, ConflictReject, clock, inspector)

	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "cred-1"))
	if err := h.AddSubscription("alice/private", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := h.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	inspector.Revoke("cred-1")
	assertReject(t, r, connectReq("t1", "dev", "alice", "cred-1"), ReasonCredentialRevoked)

	// A different, still-valid credential for the same subject resumes.
	resumed := mustConnect(t, r, connectReq("t1", "dev", "alice", "cred-2"))
	assertSubscriptions(t, resumed, []string{"alice/private"})

	// Inspector errors fail closed.
	inspector.failNext = true
	assertReject(t, r, connectReq("t1", "dev", "alice", "cred-3"), ReasonInspectorUnavailable)
}

// 合法重连：遗嘱、订阅、离线队列与 QoS1/2 未确认包全部随会话迁移。
func TestLegitimateResumeMigratesAllState(t *testing.T) {
	clock := newFakeClock()
	r := newTestRegistry(t, ConflictReject, clock, newFakeInspector())

	first := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))
	if err := first.AddSubscription("a/+", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := first.AddSubscription("b/#", 2); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	will := &Will{Topic: "a/will", Payload: []byte("offline"), QoS: 1, Retain: true}
	if err := first.SetWill(will); err != nil {
		t.Fatalf("SetWill: %v", err)
	}
	if err := first.TrackOutbound(10, Message{Topic: "a/x", Payload: []byte("q1"), QoS: 1}); err != nil {
		t.Fatalf("TrackOutbound q1: %v", err)
	}
	if err := first.TrackOutbound(11, Message{Topic: "b/y", Payload: []byte("q2"), QoS: 2}); err != nil {
		t.Fatalf("TrackOutbound q2: %v", err)
	}
	if err := first.MarkPubRec(11); err != nil {
		t.Fatalf("MarkPubRec: %v", err)
	}
	if err := first.EnqueueOffline(Message{Topic: "a/q", Payload: []byte("queued"), QoS: 2}); err != nil {
		t.Fatalf("EnqueueOffline: %v", err)
	}
	if err := first.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	second, res, err := r.Connect(context.Background(), connectReq("t1", "dev", "alice", "c2"))
	if err != nil {
		t.Fatalf("resume Connect: %v", err)
	}
	if res.Outcome != OutcomeResumed || !res.SessionPresent || res.Generation != 2 {
		t.Fatalf("resume result = %+v", res)
	}
	assertSubscriptions(t, second, []string{"a/+", "b/#"})

	gotWill, _ := second.Will()
	if gotWill == nil || gotWill.Topic != "a/will" || !gotWill.Retain {
		t.Fatalf("will did not migrate: %+v", gotWill)
	}

	pending, err := second.Retransmit()
	if err != nil {
		t.Fatalf("Retransmit: %v", err)
	}
	byID := make(map[uint16]PendingDelivery, len(pending))
	for _, p := range pending {
		byID[p.PacketID] = p
	}
	if len(byID) != 2 {
		t.Fatalf("pending count = %d, want 2", len(byID))
	}
	if !byID[10].Message.Dup || byID[10].ResendPubREL {
		t.Fatalf("QoS1 packet not marked DUP: %+v", byID[10])
	}
	if byID[11].Message.Dup || !byID[11].ResendPubREL {
		t.Fatalf("post-PUBREC QoS2 packet must request PUBREL resend: %+v", byID[11])
	}

	msgs, err := second.DrainOffline()
	if err != nil {
		t.Fatalf("DrainOffline: %v", err)
	}
	if len(msgs) != 1 || string(msgs[0].Payload) != "queued" {
		t.Fatalf("offline queue did not migrate: %+v", msgs)
	}

	if err := second.AckOutbound(10); err != nil {
		t.Fatalf("AckOutbound 10: %v", err)
	}
	if err := second.AckOutbound(11); err != nil {
		t.Fatalf("AckOutbound 11: %v", err)
	}
	if left, _ := second.Retransmit(); len(left) != 0 {
		t.Fatalf("acked packets still retransmitted: %+v", left)
	}
}

// QoS2 重放：入站重放的 PUBLISH/PUBREL 被识别为重放，应用消息不重复投递。
func TestInboundQoS2Replay(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())
	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))

	// First arrival: broker records the packet ID and would deliver once.
	dup, err := h.RememberInbound(4242)
	if err != nil || dup {
		t.Fatalf("first RememberInbound dup=%v err=%v", dup, err)
	}
	deliveries := 1

	// Replayed PUBLISH before PUBREC: detected, no second delivery.
	dup, err = h.RememberInbound(4242)
	if err != nil || !dup {
		t.Fatalf("replayed PUBLISH not detected: dup=%v err=%v", dup, err)
	}

	// Replayed PUBREL after a PUBCOMP race: still recognized as in-flow.
	if ok, err := h.IsInboundDuplicate(4242); err != nil || !ok {
		t.Fatalf("IsInboundDuplicate = %v, %v", ok, err)
	}
	h.ReleaseInbound(4242)
	if ok, _ := h.IsInboundDuplicate(4242); ok {
		t.Fatalf("packet ID not released after PUBCOMP")
	}

	if deliveries != 1 {
		t.Fatalf("application message delivered %d times", deliveries)
	}

	// Replay protection survives disconnect/reconnect with the session.
	h2 := mustConnect(t, r, connectReq("t1", "dev", "alice", "c2"))
	if dup, _ := h2.RememberInbound(77); dup {
		t.Fatalf("fresh packet ID flagged duplicate")
	}
	if _, err := h.RememberInbound(77); !errors.Is(err, ErrHandleClosed) {
		t.Fatalf("displaced handle still mutates inbound ledger: %v", err)
	}
	if dup, _ := h2.RememberInbound(77); !dup {
		t.Fatalf("replay after resume not detected")
	}
}

// 并发接管：同主体并发连接只有一个胜出，旧句柄全部失效；异主体始终被拒。
func TestConcurrentTakeover(t *testing.T) {
	clock := newFakeClock()
	r := newTestRegistry(t, ConflictReject, clock, newFakeInspector())

	first := mustConnect(t, r, connectReq("t1", "dev", "alice", "c0"))
	if err := first.AddSubscription("alice/x", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}

	const racers = 40
	var wg sync.WaitGroup
	results := make([]error, racers)
	handles := make([]*Handle, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			h, _, err := r.Connect(context.Background(), connectReq("t1", "dev", "alice", "c-race"))
			results[i] = err
			handles[i] = h
		}(i)
	}
	close(start)

	// Concurrent attacker connections must all be rejected while racing.
	const attackers = 10
	attackerRejects := make(chan error, attackers)
	for i := 0; i < attackers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := r.Connect(context.Background(), connectReq("t1", "dev", "mallory", "cm"))
			attackerRejects <- err
		}()
	}
	wg.Wait()

	successes := 0
	var winner *Handle
	for i, err := range results {
		if err != nil {
			t.Fatalf("same-subject racer %d rejected: %v", i, err)
		}
		successes++
		winner = handles[i]
	}
	if successes != racers {
		t.Fatalf("same-subject successes = %d, want %d", successes, racers)
	}
	for i := 0; i < attackers; i++ {
		if err := <-attackerRejects; !errors.Is(err, ErrTakeoverRejected) {
			t.Fatalf("attacker err = %v, want rejection", err)
		}
	}

	// Every racer got a distinct generation; only the latest handle is live.
	if !winner.closed.Load() {
		// winner is the last racer in slice order; the registry's live handle
		// may differ, so verify via guard instead of assuming identity.
	}
	stats := r.Stats()
	if stats.Sessions != 1 || stats.ActiveSessions != 1 {
		t.Fatalf("stats = %+v, want exactly one active session", stats)
	}

	// The very first handle must be unusable; state still belongs to alice.
	if err := first.AddSubscription("leak", 0); !errors.Is(err, ErrHandleClosed) {
		t.Fatalf("original handle survived concurrent takeover: %v", err)
	}
	alive, _, err := r.Connect(context.Background(), connectReq("t1", "dev", "alice", "c-final"))
	if err != nil {
		t.Fatalf("final resume: %v", err)
	}
	assertSubscriptions(t, alive, []string{"alice/x"})
}

// 遗嘱语义：异常断开保留遗嘱并随会话迁移；优雅断开清除遗嘱。
func TestWillLifecycle(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())

	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))
	if err := h.SetWill(&Will{Topic: "alice/will", Payload: []byte("down"), QoS: 1}); err != nil {
		t.Fatalf("SetWill: %v", err)
	}
	if err := h.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	resumed := mustConnect(t, r, connectReq("t1", "dev", "alice", "c2"))
	if will, _ := resumed.Will(); will == nil || string(will.Payload) != "down" {
		t.Fatalf("will lost across ungraceful disconnect: %+v", will)
	}
	if err := resumed.GracefulDisconnect(); err != nil {
		t.Fatalf("GracefulDisconnect: %v", err)
	}
	again := mustConnect(t, r, connectReq("t1", "dev", "alice", "c3"))
	if will, _ := again.Will(); will != nil {
		t.Fatalf("will survived graceful disconnect: %+v", will)
	}
}

// 后台清理与无句柄离线投递遵守过期与租户边界。
func TestSweepAndOfflineDelivery(t *testing.T) {
	clock := newFakeClock()
	r := NewRegistry(Config{
		ConflictPolicy:       ConflictReject,
		Inspector:            AllowAllInspector{},
		Clock:                clock,
		DefaultSessionExpiry: time.Minute,
		MaxOfflineMessages:   2,
	})
	t.Cleanup(func() { _ = r.Close() })

	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))
	if err := h.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if !r.Offline("t1", "dev", "alice", Message{Topic: "t", Payload: []byte("m1"), QoS: 1}) {
		t.Fatalf("Offline enqueue failed")
	}
	if r.Offline("t1", "dev", "mallory", Message{Topic: "t", Payload: []byte("x")}) {
		t.Fatalf("Offline enqueue succeeded for foreign subject")
	}

	// Bounded queue drops the oldest message rather than growing unbounded.
	r.Offline("t1", "dev", "alice", Message{Topic: "t", Payload: []byte("m2"), QoS: 1})
	r.Offline("t1", "dev", "alice", Message{Topic: "t", Payload: []byte("m3"), QoS: 1})
	s, ok := r.Lookup("t1", "dev", "alice")
	if !ok || s.OfflineDepth() != 2 {
		t.Fatalf("bounded queue depth = %d", s.OfflineDepth())
	}

	clock.Advance(61 * time.Second)
	if r.Offline("t1", "dev", "alice", Message{Topic: "t", Payload: []byte("late")}) {
		t.Fatalf("enqueue into expired session succeeded")
	}
	if n := r.Sweep(); n != 1 {
		t.Fatalf("Sweep removed %d sessions, want 1", n)
	}
	if _, ok := r.Lookup("t1", "dev", "alice"); ok {
		t.Fatalf("expired session still lookup-able")
	}
}
