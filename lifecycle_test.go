package netsec

import (
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testKey(n byte) PublicKey {
	var k PublicKey
	k[0] = n
	k[31] = 0xa5
	return k
}

func testManager(t *testing.T, clock *testClock, policy *ReclaimPolicy, nat CookieNATPolicy) *Manager {
	t.Helper()
	m, err := NewManager(Config{
		DeviceID:  "device-under-test",
		Now:       clock.Now,
		Reclaim:   policy,
		CookieNAT: nat,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

func responseFor(b *Binding) HandshakeResponse {
	auth := b.ResponseAuth()
	return HandshakeResponse{
		Index:         b.Index(),
		Generation:    b.Generation(),
		MAC:           auth.MAC,
		Timestamp:     auth.Timestamp,
		HandshakeHash: auth.HandshakeHash,
	}
}

func cookieFor(b *Binding, source netip.AddrPort) CookieReply {
	auth := b.CookieAuth()
	return CookieReply{
		Index:         b.Index(),
		Generation:    auth.Generation,
		MAC:           auth.MAC,
		MessageDigest: auth.MessageDigest,
		HandshakeHash: auth.HandshakeHash,
		Source:        source,
	}
}

func mustAllocate(t *testing.T, m *Manager, req AllocationRequest) *Binding {
	t.Helper()
	b, err := m.Allocate(req)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	return b
}

func mustRetireAndQuarantine(t *testing.T, b *Binding, reason string) {
	t.Helper()
	if err := b.RetireAndQuarantine(reason); err != nil {
		t.Fatalf("RetireAndQuarantine: %v", err)
	}
}

func TestForcedIndexCollisionIsIsolationNotIdentityMerge(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)

	old := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: testKey(1),
		Index:         100,
	})
	oldResp := responseFor(old)
	mustRetireAndQuarantine(t, old, "peer deleted")

	replacement := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: testKey(2),
		Index:         100,
		ForceIndex:    true,
	})
	if old.Capability() == replacement.Capability() {
		t.Fatal("forced index collision merged capability identities")
	}
	if replacement.State() != StateInitiationSent {
		t.Fatalf("replacement initial state = %s", replacement.State())
	}

	if _, err := m.HandleResponse(oldResp); !errors.Is(err, ErrStaleBinding) {
		t.Fatalf("late old response error = %v, want ErrStaleBinding", err)
	}
	if replacement.State() != StateInitiationSent {
		t.Fatalf("late old response changed replacement state to %s", replacement.State())
	}
	if replacement.AcceptedAt() != (time.Time{}) {
		t.Fatal("late old response populated replacement AcceptedAt")
	}

	if b, err := m.HandleResponse(responseFor(replacement)); err != nil || b != replacement {
		t.Fatalf("replacement response: b=%v err=%v", b, err)
	}
	if replacement.State() != StateResponseAccepted {
		t.Fatalf("replacement state = %s, want ResponseAccepted", replacement.State())
	}
	if old.State() != StateQuarantine {
		t.Fatalf("old state = %s, want Quarantine", old.State())
	}

	if got := m.Sweep(); got != 1 {
		t.Fatalf("Sweep reclaimed %d bindings, want 1 old binding", got)
	}
	if old.Reclaimed() != true {
		t.Fatal("old binding was not reclaimed")
	}
	if replacement.Reclaimed() {
		t.Fatal("replacement binding was reclaimed with the old index")
	}
	if got := len(m.BindingsByIndex(100)); got != 1 {
		t.Fatalf("index 100 has %d bindings after sweep, want replacement only", got)
	}
}

func TestForcedCollisionRoutesResponsesToDistinctOwners(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)

	old := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(3), Index: 150})
	replacement := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: testKey(4),
		Index:         150,
		ForceIndex:    true,
	})
	if !replacement.ForcedCollision() {
		t.Fatal("replacement did not record the forced collision")
	}
	if got := len(m.BindingsByIndex(150)); got != 2 {
		t.Fatalf("numeric index bucket has %d owners, want 2", got)
	}

	if got, err := m.HandleResponse(responseFor(old)); err != nil || got != old {
		t.Fatalf("old owner response: b=%v err=%v", got, err)
	}
	if replacement.State() != StateInitiationSent {
		t.Fatalf("old response changed replacement state to %s", replacement.State())
	}
	if got, err := m.HandleResponse(responseFor(replacement)); err != nil || got != replacement {
		t.Fatalf("replacement response: b=%v err=%v", got, err)
	}
	if replacement.State() != StateResponseAccepted {
		t.Fatalf("replacement state = %s", replacement.State())
	}
}

func TestPeerDeleteRecreateRejectsLateCookieAndResponse(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)

	peer := testKey(10)
	old := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: peer,
		Index:         200,
	})
	cookieSource := netip.MustParseAddrPort("192.0.2.10:40000")
	oldCookieAuth := CookieAuth{
		MAC:           MAC{0x10},
		MessageDigest: MessageDigest{0x20},
		Source:        cookieSource,
	}
	if err := m.SetCookieAuth(old, oldCookieAuth); err != nil {
		t.Fatalf("SetCookieAuth old: %v", err)
	}
	oldResp := responseFor(old)

	if got := m.DeletePeer(peer); got != 1 {
		t.Fatalf("DeletePeer affected %d bindings, want 1", got)
	}
	recreated := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: peer,
		Index:         200,
		ForceIndex:    true,
	})
	if recreated.Generation() != old.Generation()+1 {
		t.Fatalf("recreated generation = %d, want %d", recreated.Generation(), old.Generation()+1)
	}
	if recreated.Capability() == old.Capability() {
		t.Fatal("recreated peer reused old capability")
	}

	newCookieAuth := CookieAuth{
		MAC:           MAC{0x30},
		MessageDigest: MessageDigest{0x20},
		Source:        cookieSource,
	}
	if err := m.SetCookieAuth(recreated, newCookieAuth); err != nil {
		t.Fatalf("SetCookieAuth recreated: %v", err)
	}

	if _, err := m.HandleCookie(cookieFor(old, cookieSource)); !errors.Is(err, ErrStaleBinding) {
		t.Fatalf("late old cookie error = %v, want ErrStaleBinding", err)
	}
	if recreated.CookiesSeen() != 0 {
		t.Fatal("late old cookie was associated with the recreated peer")
	}
	if _, err := m.HandleResponse(oldResp); !errors.Is(err, ErrStaleBinding) {
		t.Fatalf("late old response error = %v, want ErrStaleBinding", err)
	}
	if recreated.State() != StateInitiationSent {
		t.Fatalf("late old response changed recreated state to %s", recreated.State())
	}

	if b, err := m.HandleCookie(cookieFor(recreated, cookieSource)); err != nil || b != recreated {
		t.Fatalf("recreated cookie: b=%v err=%v", b, err)
	}
	if recreated.CookiesSeen() != 1 {
		t.Fatalf("recreated CookiesSeen = %d, want 1", recreated.CookiesSeen())
	}
	if b, err := m.HandleResponse(responseFor(recreated)); err != nil || b != recreated {
		t.Fatalf("recreated response: b=%v err=%v", b, err)
	}
	if recreated.State() != StateResponseAccepted {
		t.Fatalf("recreated state = %s", recreated.State())
	}
}

func TestLateMessagesAfterOldIndexOwnerWasReclaimed(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)
	peer := testKey(15)
	source := netip.MustParseAddrPort("198.51.100.15:45000")

	old := mustAllocate(t, m, AllocationRequest{PeerPublicKey: peer, Index: 250})
	if err := m.SetCookieAuth(old, CookieAuth{
		MAC:           MAC{0x71},
		MessageDigest: MessageDigest{0x72},
		Source:        source,
	}); err != nil {
		t.Fatalf("SetCookieAuth old: %v", err)
	}
	oldResp := responseFor(old)
	oldCookie := cookieFor(old, source)

	m.DeletePeer(peer)
	replacement := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: peer,
		Index:         250,
		ForceIndex:    true,
	})
	if err := m.SetCookieAuth(replacement, CookieAuth{
		MAC:           MAC{0x73},
		MessageDigest: MessageDigest{0x74},
		Source:        source,
	}); err != nil {
		t.Fatalf("SetCookieAuth replacement: %v", err)
	}
	if got := m.Sweep(); got != 1 {
		t.Fatalf("Sweep reclaimed %d, want the old owner only", got)
	}
	if !old.Reclaimed() {
		t.Fatal("old owner was not reclaimed")
	}

	if _, err := m.HandleCookie(oldCookie); !errors.Is(err, ErrAuthMismatch) {
		t.Fatalf("late cookie after reclamation error = %v, want ErrAuthMismatch", err)
	}
	if _, err := m.HandleResponse(oldResp); !errors.Is(err, ErrAuthMismatch) {
		t.Fatalf("late response after reclamation error = %v, want ErrAuthMismatch", err)
	}
	if replacement.CookiesSeen() != 0 || replacement.State() != StateInitiationSent {
		t.Fatal("late old messages changed the replacement binding")
	}
	if b, err := m.HandleResponse(responseFor(replacement)); err != nil || b != replacement {
		t.Fatalf("replacement response: b=%v err=%v", b, err)
	}
}

func TestConcurrentResponsesAcceptExactlyOnce(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)
	b := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(20), Index: 300})
	msg := responseFor(b)

	const goroutines = 64
	var accepted atomic.Int32
	var already atomic.Int32
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wait.Done()
			got, err := m.HandleResponse(msg)
			switch {
			case err == nil && got == b:
				accepted.Add(1)
			case errors.Is(err, ErrResponseAlreadyAccepted):
				already.Add(1)
			default:
				t.Errorf("unexpected concurrent response result: b=%v err=%v", got, err)
			}
		}()
	}
	wait.Wait()

	if accepted.Load() != 1 {
		t.Fatalf("accepted %d responses, want exactly 1", accepted.Load())
	}
	if already.Load() != goroutines-1 {
		t.Fatalf("already-accepted errors = %d, want %d", already.Load(), goroutines-1)
	}
	if b.State() != StateResponseAccepted {
		t.Fatalf("state = %s, want ResponseAccepted", b.State())
	}
	if err := m.Activate(b); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := m.Activate(b); err != nil {
		t.Fatalf("idempotent Activate: %v", err)
	}
	if b.State() != StateKeypairActive {
		t.Fatalf("state = %s, want KeypairActive", b.State())
	}
}

func TestCookieNATPortRebindingPolicy(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)
	b := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(30), Index: 400})

	original := netip.MustParseAddrPort("192.0.2.30:50000")
	auth := CookieAuth{
		MAC:           MAC{0x44},
		MessageDigest: MessageDigest{0x55},
		Source:        original,
	}
	if err := m.SetCookieAuth(b, auth); err != nil {
		t.Fatalf("SetCookieAuth: %v", err)
	}

	if _, err := m.HandleCookie(cookieFor(b, original)); err != nil {
		t.Fatalf("exact cookie source rejected: %v", err)
	}

	missingIdentity := cookieFor(b, original)
	missingIdentity.Generation = 0
	missingIdentity.HandshakeHash = HandshakeHash{}
	if _, err := m.HandleCookie(missingIdentity); !errors.Is(err, ErrAuthMismatch) || !errors.Is(err, ErrGeneration) {
		t.Fatalf("cookie without binding generation error = %v", err)
	}

	reboundPort := netip.MustParseAddrPort("192.0.2.30:50001")
	if _, err := m.HandleCookie(cookieFor(b, reboundPort)); !errors.Is(err, ErrCookieSourcePortMismatch) {
		t.Fatalf("strict port rebind error = %v, want ErrCookieSourcePortMismatch", err)
	}

	ipv6Source := netip.MustParseAddrPort("[2001:db8::30]:50000")
	if _, err := m.HandleCookie(cookieFor(b, ipv6Source)); !errors.Is(err, ErrCookieSourceFamilyMismatch) {
		t.Fatalf("family change error = %v, want ErrCookieSourceFamilyMismatch", err)
	}

	m.SetCookieNATPolicy(CookieNATAllowPortRebind)
	if _, err := m.HandleCookie(cookieFor(b, reboundPort)); err != nil {
		t.Fatalf("explicit port-rebind policy rejected same IP: %v", err)
	}
	otherIP := netip.MustParseAddrPort("192.0.2.31:50001")
	if _, err := m.HandleCookie(cookieFor(b, otherIP)); !errors.Is(err, ErrCookieSourceAddressMismatch) {
		t.Fatalf("port-rebind policy accepted different IP: %v", err)
	}

	m.SetCookieNATPolicy(CookieNATAllowPortAndIPRebind)
	if _, err := m.HandleCookie(cookieFor(b, otherIP)); err != nil {
		t.Fatalf("explicit same-family IP rebind policy rejected cookie: %v", err)
	}
}

func TestLegitimateRekeyReusesIndexWithoutReusingIdentity(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)
	peer := testKey(40)

	first := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: peer,
		Index:         500,
		Nonce:         Nonce{0x01},
	})
	if b, err := m.HandleResponse(responseFor(first)); err != nil || b != first {
		t.Fatalf("first response: b=%v err=%v", b, err)
	}
	if err := m.Activate(first); err != nil {
		t.Fatalf("activate first: %v", err)
	}
	firstResp := responseFor(first)

	second := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: peer,
		Index:         501,
		Nonce:         Nonce{0x02},
	})
	if b, err := m.HandleResponse(responseFor(second)); err != nil || b != second {
		t.Fatalf("second response: b=%v err=%v", b, err)
	}
	if err := m.Activate(second); err != nil {
		t.Fatalf("activate second: %v", err)
	}
	if second.Generation() <= first.Generation() {
		t.Fatalf("rekey generation did not increase: %d -> %d", first.Generation(), second.Generation())
	}
	if got := m.RetireOtherGenerations(peer, second, "rekeyed"); got != 1 {
		t.Fatalf("RetireOtherGenerations = %d, want 1", got)
	}
	if err := first.SealLegacyMessages(); err != nil {
		t.Fatalf("SealLegacyMessages: %v", err)
	}
	if got := m.Sweep(); got != 1 {
		t.Fatalf("Sweep reclaimed %d, want old generation only", got)
	}
	if m.IndexInUse(500) {
		t.Fatal("old index still in use after reclamation")
	}

	third := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: peer,
		Index:         500,
		Nonce:         Nonce{0x03},
	})
	if third.Capability() == first.Capability() || third.Generation() == first.Generation() {
		t.Fatal("legitimate rekey reused old identity")
	}
	if third.Index() != first.Index() {
		t.Fatalf("third index = %d, want reused numeric index %d", third.Index(), first.Index())
	}
	if _, err := m.HandleResponse(firstResp); !errors.Is(err, ErrAuthMismatch) {
		t.Fatalf("old rekey response error = %v, want ErrAuthMismatch", err)
	}
	if third.State() != StateInitiationSent {
		t.Fatalf("old rekey response changed new state to %s", third.State())
	}
	if b, err := m.HandleResponse(responseFor(third)); err != nil || b != third {
		t.Fatalf("third response: b=%v err=%v", b, err)
	}
	if third.State() != StateResponseAccepted {
		t.Fatalf("third state = %s", third.State())
	}
}

func TestReclaimWaitsForTimeoutMessageBoundAndAsyncCrypto(t *testing.T) {
	clock := newTestClock()
	policy := ReclaimPolicy{
		OldKeyMessageLimit:  3,
		RejectAfterMessages: 8,
		RejectAfterTime:     time.Minute,
	}
	m := testManager(t, clock, &policy, CookieNATStrict)
	b := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(50), Index: 600})
	task, err := b.StartCryptoTask()
	if err != nil {
		t.Fatalf("StartCryptoTask: %v", err)
	}
	mustRetireAndQuarantine(t, b, "replaced")

	status := m.ReclaimStatus(b)
	if status.Ready || !status.AwaitingTimeout || !status.AwaitingMessageBound || !status.AwaitingCrypto {
		t.Fatalf("initial reclaim status = %+v", status)
	}
	if err := m.Reclaim(b); !errors.Is(err, ErrAsyncTaskActive) {
		t.Fatalf("initial Reclaim error = %v, want async blocker", err)
	}

	clock.Advance(2 * time.Minute)
	if got := m.Sweep(); got != 0 {
		t.Fatalf("Sweep before gates closed reclaimed %d", got)
	}

	if err := b.NoteLegacyMessage(4); err != nil {
		t.Fatalf("NoteLegacyMessage 4: %v", err)
	}
	if err := b.NoteLegacyMessage(5); err != nil {
		t.Fatalf("NoteLegacyMessage 5: %v", err)
	}
	status = m.ReclaimStatus(b)
	if !status.AwaitingMessageBound {
		t.Fatal("message bound closed before count/counter limit")
	}
	if err := m.Reclaim(b); !errors.Is(err, ErrAsyncTaskActive) {
		t.Fatalf("Reclaim with task active = %v", err)
	}
	if !task.Done() {
		t.Fatal("CryptoTask.Done returned false")
	}
	if err := m.Reclaim(b); !errors.Is(err, ErrMessageBound) {
		t.Fatalf("Reclaim before message bound = %v, want ErrMessageBound", err)
	}

	if err := b.NoteLegacyMessage(8); err != nil {
		t.Fatalf("NoteLegacyMessage 8: %v", err)
	}
	status = m.ReclaimStatus(b)
	if status.AwaitingMessageBound || status.AwaitingCrypto || status.AwaitingTimeout || !status.Ready {
		t.Fatalf("final reclaim status = %+v", status)
	}
	if got := m.Sweep(); got != 1 {
		t.Fatalf("Sweep reclaimed %d, want 1", got)
	}
	if !b.Reclaimed() {
		t.Fatal("binding not marked reclaimed")
	}
	if b.HasMaterials() {
		t.Fatal("reclaimed binding retained temporary material")
	}
	if m.IndexInUse(600) {
		t.Fatal("reclaimed index still reported in use")
	}
	if _, ok := m.BindingByIndex(600); ok {
		t.Fatal("reclaimed binding still reachable by numeric index")
	}
	if _, ok := m.BindingByID(b.ID()); ok {
		t.Fatal("reclaimed binding still reachable by capability handle")
	}

	newBinding := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(51), Index: 600})
	if newBinding.Index() != 600 {
		t.Fatalf("new binding index = %d, want 600", newBinding.Index())
	}
}

func TestReclaimWaitsForTimeout(t *testing.T) {
	clock := newTestClock()
	policy := ReclaimPolicy{RejectAfterTime: time.Minute}
	m := testManager(t, clock, &policy, CookieNATStrict)
	b := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(52), Index: 650})
	mustRetireAndQuarantine(t, b, "replaced")

	if err := m.Reclaim(b); !errors.Is(err, ErrReclaimTimeout) {
		t.Fatalf("Reclaim before timeout = %v, want ErrReclaimTimeout", err)
	}
	clock.Advance(59 * time.Second)
	if got := m.Sweep(); got != 0 {
		t.Fatalf("Sweep before timeout reclaimed %d", got)
	}
	clock.Advance(time.Second)
	if got := m.Sweep(); got != 1 {
		t.Fatalf("Sweep at timeout reclaimed %d, want 1", got)
	}
	if !b.Reclaimed() || !m.ReclaimStatus(b).Ready {
		t.Fatal("binding was not reclaimed at the timeout boundary")
	}
}

func TestLateAsyncTaskOnlyDestroysOldMaterial(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)
	peer := testKey(60)
	old := mustAllocate(t, m, AllocationRequest{PeerPublicKey: peer, Index: 700})
	oldKey := old.EphemeralPublicKey()
	oldNonce := old.CreationNonce()
	task, err := old.StartCryptoTask()
	if err != nil {
		t.Fatalf("StartCryptoTask: %v", err)
	}

	if got := m.DeletePeer(peer); got != 1 {
		t.Fatalf("DeletePeer = %d", got)
	}
	recreated := mustAllocate(t, m, AllocationRequest{
		PeerPublicKey: peer,
		Index:         700,
		ForceIndex:    true,
	})
	if b, err := m.HandleResponse(responseFor(recreated)); err != nil || b != recreated {
		t.Fatalf("recreated response: b=%v err=%v", b, err)
	}
	if err := m.Activate(recreated); err != nil {
		t.Fatalf("activate recreated: %v", err)
	}

	if task.Binding() != old || task.BindingID() != old.ID() {
		t.Fatal("task lost its old binding handle")
	}
	if !task.Done() {
		t.Fatal("first task Done returned false")
	}
	if task.Finish() {
		t.Fatal("second task completion was not idempotent")
	}
	if old.EphemeralPublicKey() != (PublicKey{}) || old.CreationNonce() != (Nonce{}) {
		t.Fatal("late task did not destroy old key material")
	}
	if oldKey == (PublicKey{}) || oldNonce == (Nonce{}) {
		t.Fatal("test captured zero old material")
	}
	if !recreated.HasMaterials() || recreated.EphemeralPublicKey() == (PublicKey{}) {
		t.Fatal("late task destroyed replacement material")
	}
	if recreated.State() != StateKeypairActive {
		t.Fatalf("replacement state = %s after late task", recreated.State())
	}
	if got := m.Sweep(); got != 1 {
		t.Fatalf("Sweep reclaimed %d, want old only", got)
	}
	if got := len(m.BindingsByIndex(700)); got != 1 {
		t.Fatalf("numeric index 700 resolved to %d bindings, want replacement only", got)
	}
	if got, ok := m.BindingByIndex(700); !ok || got != recreated {
		t.Fatal("numeric index did not resolve to the replacement binding")
	}
	if _, ok := m.BindingByID(old.ID()); ok {
		t.Fatal("reclaimed old handle still resolvable")
	}
	if got, ok := m.BindingByID(recreated.ID()); !ok || got != recreated {
		t.Fatal("replacement handle was lost")
	}
}

func TestResponseRequiresEntireAuthenticationTriplet(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)
	b := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(70), Index: 800})
	valid := responseFor(b)

	badMAC := valid
	badMAC.MAC = MAC{0xff}
	if _, err := m.HandleResponse(badMAC); !errors.Is(err, ErrAuthMismatch) || !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("bad MAC error = %v", err)
	}
	if b.State() != StateInitiationSent {
		t.Fatalf("bad MAC changed state to %s", b.State())
	}

	badTimestamp := valid
	badTimestamp.Timestamp = TimestampFromUint64(99)
	if _, err := m.HandleResponse(badTimestamp); !errors.Is(err, ErrAuthMismatch) || !errors.Is(err, ErrTimestampMismatch) {
		t.Fatalf("bad timestamp error = %v", err)
	}
	if b.State() != StateInitiationSent {
		t.Fatalf("bad timestamp changed state to %s", b.State())
	}

	badHash := valid
	badHash.HandshakeHash[0] ^= 0xff
	if _, err := m.HandleResponse(badHash); !errors.Is(err, ErrAuthMismatch) || !errors.Is(err, ErrHandshakeHashMismatch) {
		t.Fatalf("bad handshake hash error = %v", err)
	}
	if b.State() != StateInitiationSent {
		t.Fatalf("bad handshake hash changed state to %s", b.State())
	}

	if got, err := m.HandleResponse(valid); err != nil || got != b {
		t.Fatalf("valid response: b=%v err=%v", got, err)
	}
	if b.State() != StateResponseAccepted {
		t.Fatalf("valid response state = %s", b.State())
	}
	if _, err := m.HandleResponse(valid); !errors.Is(err, ErrResponseAlreadyAccepted) {
		t.Fatalf("second valid response error = %v", err)
	}
}

func TestCapabilityIsBoundToDeviceAndHandle(t *testing.T) {
	clock := newTestClock()
	mA := testManagerWithDevice(t, clock, "device-A")
	mB := testManagerWithDevice(t, clock, "device-B")

	nonce := Nonce{0x99}
	a := mustAllocate(t, mA, AllocationRequest{
		PeerPublicKey:      testKey(80),
		EphemeralPublicKey: testKey(81),
		Nonce:              nonce,
		Index:              900,
	})
	if !a.MatchesIdentity("device-A", testKey(80), a.Generation(), testKey(81), nonce) {
		t.Fatal("capability identity did not match its own fields")
	}
	if a.MatchesIdentity("device-B", testKey(80), a.Generation(), testKey(81), nonce) {
		t.Fatal("capability identity matched another device")
	}
	if _, ok := mA.BindingByCapability(a.Capability()); !ok {
		t.Fatal("capability lookup failed in owning manager")
	}
	if _, ok := mB.BindingByCapability(a.Capability()); ok {
		t.Fatal("binding capability crossed device instances")
	}
	if err := mB.Activate(a); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("foreign Activate error = %v", err)
	}
	if _, err := mB.StartCryptoTask(a); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("foreign StartCryptoTask error = %v", err)
	}
}

func TestDestroyMaterialsRequiresDrainedRetiredBinding(t *testing.T) {
	clock := newTestClock()
	m := testManager(t, clock, &ReclaimPolicy{}, CookieNATStrict)
	b := mustAllocate(t, m, AllocationRequest{PeerPublicKey: testKey(90), Index: 1000})

	if err := b.DestroyMaterials(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("active DestroyMaterials error = %v, want ErrInvalidState", err)
	}
	if !b.HasMaterials() {
		t.Fatal("active binding lost material")
	}

	task, err := b.StartCryptoTask()
	if err != nil {
		t.Fatalf("StartCryptoTask: %v", err)
	}
	mustRetireAndQuarantine(t, b, "done")
	if err := b.DestroyMaterials(); !errors.Is(err, ErrAsyncTaskActive) {
		t.Fatalf("DestroyMaterials with task active error = %v, want ErrAsyncTaskActive", err)
	}
	if !b.HasMaterials() {
		t.Fatal("material destroyed while async task still active")
	}
	if !task.Done() {
		t.Fatal("task Done returned false")
	}
	if err := b.DestroyMaterials(); err != nil {
		t.Fatalf("DestroyMaterials after drain: %v", err)
	}
	if b.HasMaterials() {
		t.Fatal("retired binding retained material after DestroyMaterials")
	}
}

func testManagerWithDevice(t *testing.T, clock *testClock, device DeviceID) *Manager {
	t.Helper()
	m, err := NewManager(Config{
		DeviceID: device,
		Now:      clock.Now,
		Reclaim:  &ReclaimPolicy{},
	})
	if err != nil {
		t.Fatalf("NewManager(%s): %v", device, err)
	}
	t.Cleanup(m.Close)
	return m
}
