package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func acceptFinished(_ context.Context, req Request) error {
	if req.Finished.Type != 17 ||
		!bytes.HasPrefix(req.Finished.Value, []byte("fin")) {
		return errors.New("finished checksum rejected")
	}
	return nil
}

func testKey(seed byte) []byte {
	key := make([]byte, keySize)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func newTestManager(
	t *testing.T,
	instance string,
	partition string,
	protocolVersion uint16,
	keyring *Keyring,
	clock *testClock,
	ttl time.Duration,
) *Manager {
	t.Helper()
	manager, err := NewManager(Config{
		FrontendPartition: partition,
		KDCInstance:       instance,
		ProtocolVersion:   protocolVersion,
		Keyring:           keyring,
		FinishedVerifier:  FinishedVerifierFunc(acceptFinished),
		CookieTTL:         ttl,
		Clock:             clock.Now,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

func mustKeyring(t *testing.T, instance string, generation uint64, key []byte) *Keyring {
	t.Helper()
	keyring, err := NewKeyring(instance, generation, key)
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	return keyring
}

func testRequest() Request {
	return Request{
		Kind:           RequestKindTGS,
		Client:         Principal{NameType: 1, Components: []string{"alice", "example"}},
		OuterRealm:     "REALM.A",
		TargetRealm:    "REALM.A",
		Nonce:          0x11223344,
		Enctype:        18,
		BodyDigest:     DigestRequestBody([]byte("strict DER KDC-REQ-BODY")),
		Armor:          testArmor("REALM.A", bytes.Repeat([]byte{0xa5}, 32)),
		Finished:       FinishedChecksum{Type: 17, Value: []byte("fin-0")},
		EncryptedField: []byte("opaque encrypted pre-authentication field"),
		UnknownDER:     []byte("unknown parsed field"),
	}
}

func testArmor(realm string, ids ...[]byte) ArmorTicket {
	id := bytes.Repeat([]byte{0xa5}, 32)
	if len(ids) > 0 {
		id = ids[0]
	}
	return ArmorTicket{
		SessionID: id,
		Realm:     realm,
		Client: Principal{
			NameType:   2,
			Components: []string{"krbtgt", realm},
		},
	}
}

func requireRejected(t *testing.T, err error) {
	t.Helper()
	if err != ErrSessionRejected {
		t.Fatalf("error = %v, want ErrSessionRejected", err)
	}
}

func TestBindingsRejectCookieReuse(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(1))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	req := testRequest()
	cookie, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if phase, err := manager.Validate(context.Background(), cookie, req); err != nil || phase != PhasePreAuthPending {
		t.Fatalf("Validate(original) = (%v, %v), want pending", phase, err)
	}

	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"outer realm", func(r *Request) { r.OuterRealm = "REALM.B" }},
		{"target realm", func(r *Request) { r.TargetRealm = "REALM.B" }},
		{"client name type", func(r *Request) { r.Client.NameType = 2 }},
		{"client component", func(r *Request) { r.Client.Components[1] = "other" }},
		{"armor session", func(r *Request) { r.Armor.SessionID = bytes.Repeat([]byte{0x5a}, 32) }},
		{"armor realm", func(r *Request) { r.Armor.Realm = "REALM.B" }},
		{"armor client", func(r *Request) { r.Armor.Client.Components[1] = "REALM.B" }},
		{"nonce", func(r *Request) { r.Nonce++ }},
		{"enctype", func(r *Request) { r.Enctype = 17 }},
		{"body digest", func(r *Request) { r.BodyDigest = DigestRequestBody([]byte("other body")) }},
		{"request kind", func(r *Request) { r.Kind = RequestKindAS }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := testRequest()
			tc.mutate(&changed)
			_, err := manager.Validate(context.Background(), cookie, changed)
			requireRejected(t, err)
		})
	}
}

func TestFinishedAndBodyDigestAreRequired(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(1))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	badFinished := testRequest()
	badFinished.Finished.Value = []byte("rejected")
	if _, err := manager.Begin(context.Background(), badFinished); err != ErrSessionRejected {
		t.Fatalf("Begin(bad finished) error = %v, want ErrSessionRejected", err)
	}

	req := testRequest()
	cookie, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	changedBody := req
	changedBody.BodyDigest = DigestRequestBody([]byte("different body"))
	_, err = manager.Validate(context.Background(), cookie, changedBody)
	requireRejected(t, err)
}

func TestCookieTransplantAcrossFrontends(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	key := testKey(7)
	ownerKeyring := mustKeyring(t, "kdc-a", 1, key)
	owner := newTestManager(t, "kdc-a", "frontend-a", 1, ownerKeyring, clock, time.Minute)
	req := testRequest()
	cookie, err := owner.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	sameInstanceOtherPartition := newTestManager(
		t,
		"kdc-a",
		"frontend-b",
		1,
		mustKeyring(t, "kdc-a", 1, key),
		clock,
		time.Minute,
	)
	otherInstanceSamePartition := newTestManager(
		t,
		"kdc-b",
		"frontend-a",
		1,
		mustKeyring(t, "kdc-b", 1, key),
		clock,
		time.Minute,
	)
	otherProtocol := newTestManager(
		t,
		"kdc-a",
		"frontend-a",
		2,
		mustKeyring(t, "kdc-a", 1, key),
		clock,
		time.Minute,
	)

	for name, manager := range map[string]*Manager{
		"partition": sameInstanceOtherPartition,
		"instance":  otherInstanceSamePartition,
		"protocol":  otherProtocol,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := manager.Validate(context.Background(), cookie, req)
			requireRejected(t, err)
		})
	}

	expired := testRequest()
	expired.OuterRealm = "REALM.B"
	_, err = owner.Validate(context.Background(), cookie, expired)
	requireRejected(t, err)
}

func TestExpiryIsAbsoluteAndIndistinguishable(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(3))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	req := testRequest()
	cookie, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	wrongRealm := req
	wrongRealm.TargetRealm = "REALM.B"
	_, wrongRealmErr := manager.Validate(context.Background(), cookie, wrongRealm)
	requireRejected(t, wrongRealmErr)

	clock.Add(2 * time.Minute)
	_, expiredErr := manager.Validate(context.Background(), cookie, req)
	requireRejected(t, expiredErr)
	if wrongRealmErr != expiredErr || wrongRealmErr.Error() != expiredErr.Error() {
		t.Fatalf("realm and expiry errors differ: %v vs %v", wrongRealmErr, expiredErr)
	}

	// Advancing before expiry must not extend the original absolute expiry.
	clock = &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	manager = newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)
	req = testRequest()
	cookie, err = manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	clock.Add(30 * time.Second)
	cookie, err = manager.Advance(context.Background(), cookie, req, PhasePreAuthPending)
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	clock.Add(40 * time.Second)
	_, err = manager.Validate(context.Background(), cookie, req)
	requireRejected(t, err)
}

func TestConcurrentConsumeConsumesExactlyOnce(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(5))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	req := testRequest()
	cookie, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	const goroutines = 64
	start := make(chan struct{})
	results := make(chan error, goroutines)
	var ready sync.WaitGroup
	ready.Add(goroutines)
	var successes atomic.Int32
	for i := 0; i < goroutines; i++ {
		go func() {
			ready.Done()
			<-start
			err := manager.Consume(context.Background(), cookie, req)
			if err == nil {
				successes.Add(1)
			}
			results <- err
		}()
	}
	ready.Wait()
	close(start)
	for i := 0; i < goroutines; i++ {
		if err := <-results; err != nil && err != ErrSessionRejected {
			t.Errorf("Consume() error = %v, want nil or ErrSessionRejected", err)
		}
	}
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful consumes = %d, want 1", got)
	}
	_, err = manager.Validate(context.Background(), cookie, req)
	requireRejected(t, err)
}

func TestLegitimateMultiRoundPreAuthentication(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(9))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	req := testRequest()
	cookie0, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	req1 := req
	req1.Finished = FinishedChecksum{Type: 17, Value: []byte("fin-1")}
	req1.EncryptedField = []byte("round one encrypted field")
	req1.UnknownDER = []byte("round one unknown field")
	cookie1, err := manager.Advance(context.Background(), cookie0, req1, PhasePreAuthPending)
	if err != nil {
		t.Fatalf("Advance(round 1) error = %v", err)
	}
	if bytes.Equal(cookie0.Bytes(), cookie1.Bytes()) {
		t.Fatal("Advance reused cookie bytes")
	}
	_, err = manager.Validate(context.Background(), cookie0, req1)
	requireRejected(t, err)
	if _, err = manager.Validate(context.Background(), cookie1, req1); err != nil {
		t.Fatalf("Validate(cookie1) error = %v", err)
	}

	req2 := req
	req2.Finished = FinishedChecksum{Type: 17, Value: []byte("fin-2")}
	req2.EncryptedField = []byte("round two encrypted field")
	cookie2, err := manager.Advance(context.Background(), cookie1, req2, PhasePreAuthVerified)
	if err != nil {
		t.Fatalf("Advance(round 2) error = %v", err)
	}
	phase, err := manager.Validate(context.Background(), cookie2, req2)
	if err != nil || phase != PhasePreAuthVerified {
		t.Fatalf("Validate(cookie2) = (%v, %v), want verified", phase, err)
	}

	req3 := req
	req3.Finished = FinishedChecksum{Type: 17, Value: []byte("fin-3")}
	req3.EncryptedField = []byte("round three encrypted field")
	cookie3, err := manager.Advance(context.Background(), cookie2, req3, PhaseReady)
	if err != nil {
		t.Fatalf("Advance(round 3) error = %v", err)
	}
	if _, err = manager.Validate(context.Background(), cookie1, req3); err == nil {
		t.Fatal("old intermediate cookie remained valid")
	}
	if _, err = manager.Advance(context.Background(), cookie3, req3, PhasePreAuthVerified); err != ErrSessionRejected {
		t.Fatalf("Advance(backwards) error = %v, want ErrSessionRejected", err)
	}

	req4 := req
	req4.Finished = FinishedChecksum{Type: 17, Value: []byte("fin-4")}
	if err := manager.Consume(context.Background(), cookie3, req4); err != nil {
		t.Fatalf("Consume(final) error = %v", err)
	}
	if _, err := manager.Validate(context.Background(), cookie3, req4); err != ErrSessionRejected {
		t.Fatalf("Validate(consumed) error = %v, want ErrSessionRejected", err)
	}
}

func TestKeyRotationAndRollbackProtection(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(11))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	req := testRequest()
	cookie1, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin(generation 1) error = %v", err)
	}

	if err := keyring.Rotate(2, testKey(21)); err != nil {
		t.Fatalf("Rotate(2) error = %v", err)
	}
	_, err = manager.Validate(context.Background(), cookie1, req)
	requireRejected(t, err)

	cookie2, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin(generation 2) error = %v", err)
	}
	if _, err := manager.Validate(context.Background(), cookie2, req); err != nil {
		t.Fatalf("Validate(generation 2) error = %v", err)
	}

	if err := keyring.Activate(1); err != ErrKeyGenerationRollback {
		t.Fatalf("Activate(1) error = %v, want ErrKeyGenerationRollback", err)
	}
	if keyring.ActiveGeneration() != 2 {
		t.Fatalf("active generation = %d, want 2", keyring.ActiveGeneration())
	}
	if err := keyring.Rotate(1, testKey(11)); err != ErrKeyGenerationRollback {
		t.Fatalf("Rotate(1) error = %v, want ErrKeyGenerationRollback", err)
	}
	if err := keyring.InstallKey(1, testKey(11)); err != ErrKeyGenerationRollback {
		t.Fatalf("InstallKey(1) error = %v, want ErrKeyGenerationRollback", err)
	}

	if err := keyring.InstallKey(3, testKey(31)); err != nil {
		t.Fatalf("InstallKey(3) error = %v", err)
	}
	if keyring.ActiveGeneration() != 2 {
		t.Fatalf("InstallKey changed active generation to %d", keyring.ActiveGeneration())
	}
	if err := keyring.Activate(3); err != nil {
		t.Fatalf("Activate(3) error = %v", err)
	}
	_, err = manager.Validate(context.Background(), cookie2, req)
	requireRejected(t, err)

	cookie3, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin(generation 3) error = %v", err)
	}
	if _, err := manager.Validate(context.Background(), cookie3, req); err != nil {
		t.Fatalf("Validate(generation 3) error = %v", err)
	}
	if manager.KeyGeneration() != 3 {
		t.Fatalf("manager generation = %d, want 3", manager.KeyGeneration())
	}
}

func TestReferralCreatesFreshState(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(13))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	current := testRequest()
	sourceCookie, err := manager.Begin(context.Background(), current)
	if err != nil {
		t.Fatalf("Begin(source) error = %v", err)
	}

	verified := current
	verified.Finished = FinishedChecksum{Type: 17, Value: []byte("fin-1")}
	sourceCookie, err = manager.Advance(context.Background(), sourceCookie, verified, PhaseReady)
	if err != nil {
		t.Fatalf("Advance(source) error = %v", err)
	}

	next := current
	next.OuterRealm = "REALM.B"
	next.TargetRealm = "REALM.B"
	next.Nonce += 17
	next.BodyDigest = DigestRequestBody([]byte("cross-realm KDC-REQ-BODY"))
	next.Armor = testArmor("REALM.B", bytes.Repeat([]byte{0x3c}, 32))
	next.Finished = FinishedChecksum{}
	next.UnknownDER = []byte("referral unknown marker")

	referralCookie, err := manager.Refer(context.Background(), sourceCookie, verified, next)
	if err != nil {
		t.Fatalf("Refer() error = %v", err)
	}
	if bytes.Equal(sourceCookie.Bytes(), referralCookie.Bytes()) {
		t.Fatal("referral inherited source cookie bytes")
	}
	if bytes.Contains(referralCookie.Bytes(), next.UnknownDER) {
		t.Fatal("unknown referral field was echoed into cookie")
	}
	next.Finished = FinishedChecksum{Type: 17, Value: []byte("fin-referral")}

	// The source state is consumed and cannot be used on the destination path.
	_, err = manager.Validate(context.Background(), sourceCookie, verified)
	requireRejected(t, err)
	_, err = manager.Validate(context.Background(), sourceCookie, next)
	requireRejected(t, err)

	phase, err := manager.Validate(context.Background(), referralCookie, next)
	if err != nil || phase != PhasePreAuthPending {
		t.Fatalf("Validate(referral) = (%v, %v), want pending", phase, err)
	}

	wrongDestination := next
	wrongDestination.TargetRealm = "REALM.C"
	_, err = manager.Validate(context.Background(), referralCookie, wrongDestination)
	requireRejected(t, err)

	// A referral that does not change the target realm is not a new trust path.
	otherSource, err := manager.Begin(context.Background(), current)
	if err != nil {
		t.Fatalf("Begin(other source) error = %v", err)
	}
	_, err = manager.Refer(context.Background(), otherSource, current, current)
	requireRejected(t, err)
	if _, err := manager.Validate(context.Background(), otherSource, current); err != nil {
		t.Fatalf("failed referral consumed source state: %v", err)
	}
}

func TestCookieTamperingAndUnknownFields(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(15))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	marker := []byte("UNKNOWN-FIELD-MARKER-0123456789")
	req := testRequest()
	req.EncryptedField = append([]byte("encrypted-"), marker...)
	req.UnknownDER = append([]byte("unknown-"), marker...)
	cookie, err := manager.Begin(context.Background(), req)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if bytes.Contains(cookie.Bytes(), marker) {
		t.Fatal("unknown or encrypted input was echoed into cookie")
	}

	for name, mutate := range map[string]func([]byte) []byte{
		"flip tag": func(in []byte) []byte {
			out := cloneBytes(in)
			out[len(out)-1] ^= 0x80
			return out
		},
		"truncate": func(in []byte) []byte { return cloneBytes(in[:len(in)-1]) },
		"generation": func(in []byte) []byte {
			out := cloneBytes(in)
			out[13] ^= 0x01
			return out
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := manager.Validate(context.Background(), Cookie(mutate(cookie)), req)
			requireRejected(t, err)
		})
	}

	changed := req
	changed.OuterRealm = "REALM.B"
	_, err = manager.Validate(context.Background(), cookie, changed)
	requireRejected(t, err)
	if err.Error() != ErrSessionRejected.Error() {
		t.Fatalf("rejection leaked detail: %q", err.Error())
	}
}

func TestParsedASAndTGSAdapters(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
	keyring := mustKeyring(t, "kdc-a", 1, testKey(17))
	manager := newTestManager(t, "kdc-a", "frontend-a", 1, keyring, clock, time.Minute)

	common := testRequest()
	asReq := ASReq{
		Client:         common.Client,
		OuterRealm:     common.OuterRealm,
		TargetRealm:    common.TargetRealm,
		Nonce:          common.Nonce,
		Enctype:        common.Enctype,
		BodyDigest:     common.BodyDigest,
		Armor:          common.Armor,
		Finished:       common.Finished,
		EncryptedField: common.EncryptedField,
		UnknownDER:     common.UnknownDER,
	}
	cookie, err := manager.BeginAS(context.Background(), asReq)
	if err != nil {
		t.Fatalf("BeginAS() error = %v", err)
	}
	if _, err := manager.Validate(context.Background(), cookie, asReq.ToRequest()); err != nil {
		t.Fatalf("Validate(ASReq) error = %v", err)
	}
	if _, err := manager.Validate(context.Background(), cookie, common); err != ErrSessionRejected {
		t.Fatalf("AS cookie accepted for TGS request: %v", err)
	}

	tgsReq := TGSReq{
		Client:         common.Client,
		OuterRealm:     common.OuterRealm,
		TargetRealm:    common.TargetRealm,
		Nonce:          common.Nonce,
		Enctype:        common.Enctype,
		BodyDigest:     common.BodyDigest,
		Armor:          common.Armor,
		Finished:       common.Finished,
		EncryptedField: common.EncryptedField,
		UnknownDER:     common.UnknownDER,
	}
	if _, err := manager.BeginParsed(context.Background(), tgsReq); err != nil {
		t.Fatalf("BeginParsed(TGSReq) error = %v", err)
	}
	var nilAS *ASReq
	if _, err := manager.BeginParsed(context.Background(), nilAS); err != ErrSessionRejected {
		t.Fatalf("BeginParsed(nil ASReq) error = %v, want ErrSessionRejected", err)
	}
}

func TestConfigValidation(t *testing.T) {
	key := testKey(19)
	keyring := mustKeyring(t, "kdc-a", 1, key)
	verifier := FinishedVerifierFunc(acceptFinished)

	cases := []Config{
		{KDCInstance: "kdc-a", Keyring: keyring, FinishedVerifier: verifier},
		{FrontendPartition: "frontend", Keyring: keyring, FinishedVerifier: verifier},
		{FrontendPartition: "frontend", KDCInstance: "kdc-a", FinishedVerifier: verifier},
		{FrontendPartition: "frontend", KDCInstance: "kdc-a", Keyring: keyring},
		{FrontendPartition: "frontend", KDCInstance: "kdc-b", Keyring: keyring, FinishedVerifier: verifier},
		{FrontendPartition: "frontend", KDCInstance: "kdc-a", Keyring: keyring, FinishedVerifier: verifier, CookieTTL: -time.Second},
	}
	for i, config := range cases {
		if _, err := NewManager(config); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("NewManager(case %d) error = %v, want ErrInvalidConfiguration", i, err)
		}
	}
}

func TestRequestBodyDigestHelper(t *testing.T) {
	body := []byte("canonical DER")
	digest := DigestRequestBody(body)
	if digest != RequestBodyDigest(sha256.Sum256(body)) {
		t.Fatal("DigestRequestBody mismatch")
	}
	if digest == (RequestBodyDigest{}) {
		t.Fatal("DigestRequestBody returned zero digest")
	}
}
