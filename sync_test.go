package netsec

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time          { return c.now }
func (c *testClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func testPrivateKey(seed byte) ed25519.PrivateKey {
	raw := make([]byte, ed25519.SeedSize)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	return ed25519.NewKeyFromSeed(raw)
}

func testSession(n byte) SessionID {
	var s SessionID
	s[0] = n
	s[len(s)-1] = n ^ 0xa5
	return s
}

func mustVRPs(t *testing.T, vrps ...VRP) []VRP {
	t.Helper()
	out, err := CanonicalVRPs(vrps)
	if err != nil {
		t.Fatalf("canonicalise VRPs: %v", err)
	}
	return out
}

func testCache(t *testing.T, cacheID string, private ed25519.PrivateKey, serial uint32, vrps []VRP) *Cache {
	t.Helper()
	cache, err := NewCacheWithConfig(CacheConfig{
		CacheID:    cacheID,
		PrivateKey: private,
		Session:    testSession(1),
		Serial:     serial,
		VRPs:       vrps,
	})
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	return cache
}

func syncReset(t *testing.T, router *Router, cache *Cache, reverse bool) View {
	t.Helper()
	query, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatalf("reset query: %v", err)
	}
	messages, err := cache.ResetResponse(query)
	if err != nil {
		t.Fatalf("reset response: %v", err)
	}
	if reverse {
		messages = reversed(messages)
	}
	if err := router.IngestResponse(messages); err != nil {
		t.Fatalf("ingest reset: %v", err)
	}
	view, ok := router.View(cache.CacheID())
	if !ok {
		t.Fatal("no committed view after reset")
	}
	return view
}

func syncSerial(t *testing.T, router *Router, cache *Cache, reverse bool) View {
	t.Helper()
	query, err := router.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatalf("serial query: %v", err)
	}
	messages, err := cache.SerialResponse(query)
	if err != nil {
		t.Fatalf("serial response: %v", err)
	}
	if reverse {
		messages = reversed(messages)
	}
	if err := router.IngestResponse(messages); err != nil {
		t.Fatalf("ingest serial: %v", err)
	}
	view, ok := router.View(cache.CacheID())
	if !ok {
		t.Fatal("no committed view after serial update")
	}
	return view
}

func reversed(messages []SignedMessage) []SignedMessage {
	out := make([]SignedMessage, len(messages))
	for i := range messages {
		out[len(messages)-1-i] = messages[i].copy()
	}
	return out
}

func resignForQuery(t *testing.T, private ed25519.PrivateKey, query Query, messages []SignedMessage) []SignedMessage {
	t.Helper()
	out := make([]SignedMessage, len(messages))
	copy(out, messages)
	for i := range out {
		out[i] = out[i].copy()
		out[i].ConnectionGeneration = query.ConnectionGeneration
		out[i].RequestID = query.RequestID
		signed, err := SignMessage(private, out[i])
		if err != nil {
			t.Fatalf("re-sign message: %v", err)
		}
		out[i] = signed
	}
	return out
}

func assertVRPList(t *testing.T, got, want []VRP) {
	t.Helper()
	canonicalGot, err := CanonicalVRPs(got)
	if err != nil {
		t.Fatalf("canonicalise got: %v", err)
	}
	canonicalWant, err := CanonicalVRPs(want)
	if err != nil {
		t.Fatalf("canonicalise want: %v", err)
	}
	if !reflect.DeepEqual(canonicalGot, canonicalWant) {
		t.Fatalf("VRP set mismatch\ngot:  %#v\nwant: %#v", canonicalGot, canonicalWant)
	}
}

func assertRoot(t *testing.T, view View, want []VRP) {
	t.Helper()
	root, err := ComputeRoot(want)
	if err != nil {
		t.Fatalf("compute root: %v", err)
	}
	if view.RootDigest != root {
		t.Fatalf("root mismatch: got %s want %s", view.RootDigest, root)
	}
}

func TestResetAndOutOfOrderSerialUpdate(t *testing.T) {
	private := testPrivateKey(10)
	initial := mustVRPs(t,
		VRP{Prefix: "10.0.0.0/8", MaxLength: 24, OriginAS: 64500},
		VRP{Prefix: "192.0.2.0/24", MaxLength: 24, OriginAS: 64501},
	)
	cache := testCache(t, "cache-a", private, 100, initial)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	view := syncReset(t, router, cache, true)
	if view.Serial != 100 || view.Status != ViewFresh {
		t.Fatalf("unexpected reset view: serial=%d status=%s", view.Serial, view.Status)
	}
	assertVRPList(t, view.VRPs, initial)
	assertRoot(t, view, initial)

	next := mustVRPs(t,
		VRP{Prefix: "192.0.2.0/24", MaxLength: 24, OriginAS: 64501},
		VRP{Prefix: "198.51.100.0/24", MaxLength: 24, OriginAS: 64502},
	)
	newSerial, err := cache.ReplaceVRPs(next)
	if err != nil {
		t.Fatalf("replace VRPs: %v", err)
	}
	if newSerial != 101 {
		t.Fatalf("serial did not advance: %d", newSerial)
	}

	view = syncSerial(t, router, cache, true)
	if view.Serial != 101 {
		t.Fatalf("serial did not commit: %d", view.Serial)
	}
	assertVRPList(t, view.VRPs, next)
	assertRoot(t, view, next)

	validation := router.Validate(cache.CacheID(), netip.MustParsePrefix("198.51.100.0/24"), 64502)
	if validation.Status != StatusValid || len(validation.Matches) != 1 {
		t.Fatalf("expected valid route, got %s matches=%d", validation.Status, len(validation.Matches))
	}
	validation = router.Validate(cache.CacheID(), netip.MustParsePrefix("10.1.0.0/16"), 64500)
	if validation.Status != StatusInvalid {
		t.Fatalf("removed authorization remained valid: %s", validation.Status)
	}
}

func TestReplayedOldSessionIsRejected(t *testing.T) {
	private := testPrivateKey(20)
	revoked := mustVRPs(t, VRP{Prefix: "203.0.113.0/24", MaxLength: 24, OriginAS: 64510})
	cache := testCache(t, "cache-old-session", private, 7, revoked)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	oldQuery, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatalf("old reset query: %v", err)
	}
	oldMessages, err := cache.ResetResponse(oldQuery)
	if err != nil {
		t.Fatalf("old reset response: %v", err)
	}
	if err := router.IngestResponse(oldMessages); err != nil {
		t.Fatalf("initial reset: %v", err)
	}

	if _, err := cache.RotateSession(); err != nil {
		t.Fatalf("rotate session: %v", err)
	}
	clean := mustVRPs(t, VRP{Prefix: "203.0.113.0/24", MaxLength: 24, OriginAS: 64511})
	if _, err := cache.ReplaceVRPs(clean); err != nil {
		t.Fatalf("publish clean set: %v", err)
	}
	_ = syncReset(t, router, cache, true)

	current, ok := router.View(cache.CacheID())
	if !ok {
		t.Fatal("missing view after session change")
	}
	if current.Serial != 1 || current.Session == testSession(1) {
		t.Fatalf("new session did not commit: session=%s serial=%d", current.Session, current.Serial)
	}

	// Raw replay from the previous query must fail even before cryptographic
	// re-signing can be considered.
	replayQuery, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatalf("replay reset query: %v", err)
	}
	if err := router.IngestResponse(oldMessages); !errors.Is(err, ErrUnexpectedResponse) {
		t.Fatalf("raw replay error = %v, want %v", err, ErrUnexpectedResponse)
	}

	// A validly signed but old-session snapshot is still a session rollback.
	forged := resignForQuery(t, private, replayQuery, oldMessages)
	if err := router.IngestResponse(forged); !errors.Is(err, ErrSessionRetired) {
		t.Fatalf("old-session replay error = %v, want %v", err, ErrSessionRetired)
	}
	view, ok := router.View(cache.CacheID())
	if !ok {
		t.Fatal("view disappeared after rejected replay")
	}
	assertVRPList(t, view.VRPs, clean)
	if view.Status != ViewFresh {
		t.Fatalf("rejected replay poisoned freshness: %s", view.Status)
	}
}

func TestSerialWrapAroundAndContinuousDelta(t *testing.T) {
	private := testPrivateKey(30)
	initial := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64520})
	cache := testCache(t, "cache-wrap", private, math.MaxUint32, initial)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	view := syncReset(t, router, cache, false)
	if view.Serial != math.MaxUint32 {
		t.Fatalf("initial serial = %d", view.Serial)
	}

	wrapped := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64521})
	if _, err := cache.ReplaceVRPs(wrapped); err != nil {
		t.Fatalf("wrap update: %v", err)
	}
	if cache.Serial() != 0 {
		t.Fatalf("cache did not wrap: %d", cache.Serial())
	}
	view = syncSerial(t, router, cache, true)
	if view.Serial != 0 {
		t.Fatalf("router did not wrap: %d", view.Serial)
	}
	assertVRPList(t, view.VRPs, wrapped)

	next := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64522})
	if _, err := cache.ReplaceVRPs(next); err != nil {
		t.Fatalf("post-wrap update: %v", err)
	}
	view = syncSerial(t, router, cache, false)
	if view.Serial != 1 {
		t.Fatalf("post-wrap serial = %d", view.Serial)
	}
	assertVRPList(t, view.VRPs, next)
}

func TestMissingSerialEventIsRejected(t *testing.T) {
	private := testPrivateKey(40)
	a := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64530}
	b := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64531}
	c := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64532}
	cache := testCache(t, "cache-gap", private, 10, mustVRPs(t, a))
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	view := syncReset(t, router, cache, false)
	if view.Serial != 10 {
		t.Fatalf("initial serial = %d", view.Serial)
	}
	if _, err := cache.ApplyChanges([]Change{{Operation: ChangeAdd, VRP: b}}); err != nil {
		t.Fatalf("step 11: %v", err)
	}
	if _, err := cache.ApplyChanges([]Change{{Operation: ChangeAdd, VRP: c}}); err != nil {
		t.Fatalf("step 12: %v", err)
	}

	query, err := router.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatalf("serial query: %v", err)
	}
	messages, err := cache.SerialResponse(query)
	if err != nil {
		t.Fatalf("serial response: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 fragments, got %d", len(messages))
	}

	// Remove the serial-11 event, renumber the remaining event and End, and
	// sign the forged response. The result claims to go directly to serial 12.
	forged := []SignedMessage{messages[1].copy(), messages[2].copy()}
	forged[0].Index = 0
	forged[0].Total = 2
	forged[1].Index = 1
	forged[1].Total = 2
	for i := range forged {
		signed, signErr := SignMessage(private, forged[i])
		if signErr != nil {
			t.Fatalf("sign forged gap: %v", signErr)
		}
		forged[i] = signed
	}
	if err := router.IngestResponse(forged); !errors.Is(err, ErrSerialGap) {
		t.Fatalf("missing-event error = %v, want %v", err, ErrSerialGap)
	}
	view, ok := router.View(cache.CacheID())
	if !ok || view.Serial != 10 || len(view.VRPs) != 1 {
		t.Fatalf("failed delta changed the committed view: %#v", view)
	}
}

func TestRootMismatchLeavesOldViewIntact(t *testing.T) {
	private := testPrivateKey(50)
	oldSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64540})
	cache := testCache(t, "cache-root", private, 3, oldSet)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	_ = syncReset(t, router, cache, false)

	query, err := router.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatalf("serial query: %v", err)
	}
	messages, err := cache.SerialResponse(query)
	if err != nil {
		t.Fatalf("serial response: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected no-op end, got %d fragments", len(messages))
	}
	badRoot, err := ComputeRoot(mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64541}))
	if err != nil {
		t.Fatal(err)
	}
	forged := messages[0].copy()
	forged.RootDigest = badRoot
	signed, err := SignMessage(private, forged)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.IngestResponse([]SignedMessage{signed}); !errors.Is(err, ErrDuplicateSerial) {
		t.Fatalf("duplicate-different-content error = %v, want %v", err, ErrDuplicateSerial)
	}

	// A complete reset with a recomputed-but-wrong End root must also leave the
	// old view as the only query-visible state.
	newSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64542})
	if _, err := cache.ReplaceVRPs(newSet); err != nil {
		t.Fatal(err)
	}
	resetQuery, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	resetMessages, err := cache.ResetResponse(resetQuery)
	if err != nil {
		t.Fatal(err)
	}
	last := len(resetMessages) - 1
	bad := resetMessages[last].copy()
	bad.RootDigest = oldSetRoot(t, oldSet)
	signed, err = SignMessage(private, bad)
	if err != nil {
		t.Fatal(err)
	}
	resetMessages[last] = signed
	if err := router.IngestResponse(reversed(resetMessages)); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("root mismatch error = %v, want %v", err, ErrRootMismatch)
	}
	view, ok := router.View(cache.CacheID())
	if !ok {
		t.Fatal("view disappeared after root mismatch")
	}
	assertVRPList(t, view.VRPs, oldSet)
	if view.Serial != 3 {
		t.Fatalf("old serial changed: %d", view.Serial)
	}

	serialQuery, err := router.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	serialMessages, err := cache.SerialResponse(serialQuery)
	if err != nil {
		t.Fatal(err)
	}
	last = len(serialMessages) - 1
	bad = serialMessages[last].copy()
	bad.RootDigest = oldSetRoot(t, oldSet)
	signed, err = SignMessage(private, bad)
	if err != nil {
		t.Fatal(err)
	}
	serialMessages[last] = signed
	if err := router.IngestResponse(reversed(serialMessages)); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("delta root mismatch error = %v, want %v", err, ErrRootMismatch)
	}
	view, ok = router.View(cache.CacheID())
	if !ok || view.Serial != 3 {
		t.Fatalf("delta root mismatch changed view: %#v", view)
	}
	assertVRPList(t, view.VRPs, oldSet)
}

func oldSetRoot(t *testing.T, vrps []VRP) Digest {
	t.Helper()
	root, err := ComputeRoot(vrps)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDeltaConflictsAndRemovesAreAtomic(t *testing.T) {
	private := testPrivateKey(60)
	existing := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64550}
	missing := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64551}
	cache := testCache(t, "cache-conflict", private, 4, mustVRPs(t, existing))
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)
	initialRoot := cache.RootDigest()

	check := func(kind MessageKind, vrp VRP, want error) {
		t.Helper()
		query, queryErr := router.SerialQuery(cache.CacheID())
		if queryErr != nil {
			t.Fatalf("query: %v", queryErr)
		}
		messages := make([]SignedMessage, 2)
		messages[0] = SignedMessage{
			CacheID: cache.CacheID(), ConnectionGeneration: query.ConnectionGeneration, RequestID: query.RequestID,
			Kind: kind, Index: 0, Total: 2, Session: cache.Session(), Serial: 5, FromSerial: 4, ToSerial: 5, VRP: vrp,
		}
		messages[1] = SignedMessage{
			CacheID: cache.CacheID(), ConnectionGeneration: query.ConnectionGeneration, RequestID: query.RequestID,
			Kind: MessageSerialEnd, Index: 1, Total: 2, Session: cache.Session(), Serial: 5, FromSerial: 4, ToSerial: 5, RootDigest: initialRoot,
		}
		for i := range messages {
			signed, signErr := SignMessage(private, messages[i])
			if signErr != nil {
				t.Fatalf("sign: %v", signErr)
			}
			messages[i] = signed
		}
		if err := router.IngestResponse(messages); !errors.Is(err, want) {
			t.Fatalf("%s error = %v, want %v", kind, err, want)
		}
		view, ok := router.View(cache.CacheID())
		if !ok || view.Serial != 4 || view.RootDigest != initialRoot {
			t.Fatalf("%s mutated state: %#v", kind, view)
		}
	}
	check(MessageAdd, existing, ErrAddConflict)
	check(MessageRemove, missing, ErrRemoveMissing)
}

func TestSessionChangeMarksOldStaleAndCommitsAtomically(t *testing.T) {
	private := testPrivateKey(70)
	oldSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64560})
	cache := testCache(t, "cache-switch", private, 9, oldSet)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)

	if _, err := cache.RotateSession(); err != nil {
		t.Fatal(err)
	}
	newSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64561})
	if _, err := cache.ReplaceVRPs(newSet); err != nil {
		t.Fatal(err)
	}
	query, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	messages, err := cache.ResetResponse(query)
	if err != nil {
		t.Fatal(err)
	}

	// Only Begin is visible. The old committed set remains complete and is
	// explicitly marked stale; the new partial set is never query-visible.
	if err := router.IngestMessage(messages[0]); err != nil {
		t.Fatal(err)
	}
	view, ok := router.View(cache.CacheID())
	if !ok || view.Serial != 9 || view.Status != ViewStale {
		t.Fatalf("old view not served stale during reset: %#v", view)
	}
	assertVRPList(t, view.VRPs, oldSet)
	if _, err := router.SerialQuery(cache.CacheID()); !errors.Is(err, ErrNeedReset) {
		t.Fatalf("serial query during session change error = %v, want %v", err, ErrNeedReset)
	}

	for _, message := range reversed(messages[1:]) {
		if err := router.IngestMessage(message); err != nil {
			t.Fatalf("finish reset: %v", err)
		}
	}
	view, ok = router.View(cache.CacheID())
	if !ok || view.Session == testSession(1) || view.Status != ViewFresh {
		t.Fatalf("new view not atomically committed: %#v", view)
	}
	assertVRPList(t, view.VRPs, newSet)
	assertRoot(t, view, newSet)
}

func TestStalePolicyNeverReportsValid(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	private := testPrivateKey(80)
	vrps := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64570})
	cache := testCache(t, "cache-stale", private, 1, vrps)
	config := RouterConfig{
		TrustedKeys: map[CacheID]ed25519.PublicKey{cache.CacheID(): cache.PublicKey()},
		StaleAfter:  10 * time.Second,
		MaxStale:    20 * time.Second,
		StalePolicy: PolicyFailClosed,
		Now:         clock.Now,
	}
	router, err := NewRouter(config)
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)

	clock.Advance(11 * time.Second)
	view, _ := router.View(cache.CacheID())
	if view.Status != ViewStale {
		t.Fatalf("view status = %s, want stale", view.Status)
	}
	validation := router.Validate(cache.CacheID(), netip.MustParsePrefix("10.0.0.0/8"), 64570)
	if validation.Status != StatusStale || validation.Valid() {
		t.Fatalf("stale route status = %s valid=%v", validation.Status, validation.Valid())
	}

	clock.Advance(10 * time.Second)
	view, _ = router.View(cache.CacheID())
	if view.Status != ViewExpired {
		t.Fatalf("view status = %s, want expired", view.Status)
	}
	validation = router.Validate(cache.CacheID(), netip.MustParsePrefix("10.0.0.0/8"), 64570)
	if validation.Status != StatusInvalid || len(validation.Matches) != 0 {
		t.Fatalf("fail-closed result = %#v", validation)
	}

	unknownConfig := config
	unknownConfig.StalePolicy = PolicyUnknown
	unknownRouter, err := NewRouter(unknownConfig)
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, unknownRouter, cache, false)
	clock.Advance(21 * time.Second)
	validation = unknownRouter.Validate(cache.CacheID(), netip.MustParsePrefix("10.0.0.0/8"), 64570)
	if validation.Status != StatusUnknown || validation.Valid() || len(validation.Matches) != 0 {
		t.Fatalf("unknown-policy result = %#v", validation)
	}
}

func TestConnectionGenerationAndRequestBinding(t *testing.T) {
	private := testPrivateKey(90)
	cache := testCache(t, "cache-bind", private, 1, mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64580}))
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	oldQuery, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	oldMessages, err := cache.ResetResponse(oldQuery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.BeginConnection(cache.CacheID()); err != nil {
		t.Fatal(err)
	}
	if err := router.IngestResponse(oldMessages); !errors.Is(err, ErrConnectionGeneration) {
		t.Fatalf("old-generation error = %v", err)
	}

	tampered := oldMessages[0].copy()
	tampered.RequestID++
	if err := router.IngestMessage(tampered); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered signature error = %v", err)
	}

	newQuery, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	staleRequest := oldMessages[0].copy()
	staleRequest.ConnectionGeneration = newQuery.ConnectionGeneration
	signed, err := SignMessage(private, staleRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.IngestMessage(signed); !errors.Is(err, ErrUnexpectedResponse) {
		t.Fatalf("old-query error = %v", err)
	}
}

func TestInvalidResetRootKeepsStaleOldView(t *testing.T) {
	private := testPrivateKey(100)
	oldSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64590})
	cache := testCache(t, "cache-bad-reset", private, 5, oldSet)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)
	if _, err := cache.RotateSession(); err != nil {
		t.Fatal(err)
	}
	newSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64591})
	if _, err := cache.ReplaceVRPs(newSet); err != nil {
		t.Fatal(err)
	}
	query, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	messages, err := cache.ResetResponse(query)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1].copy()
	last.RootDigest = cache.RootDigest()
	last.RootDigest[0] ^= 0xff
	signed, err := SignMessage(private, last)
	if err != nil {
		t.Fatal(err)
	}
	messages[len(messages)-1] = signed
	if err := router.IngestResponse(reversed(messages)); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("root mismatch = %v", err)
	}
	view, ok := router.View(cache.CacheID())
	if !ok || view.Status != ViewStale || view.Serial != 5 {
		t.Fatalf("old view not retained stale: %#v", view)
	}
	assertVRPList(t, view.VRPs, oldSet)
}

func TestDualCachesHaveIndependentStateAndPreferredView(t *testing.T) {
	privateA := testPrivateKey(110)
	privateB := testPrivateKey(120)
	cacheA := testCache(t, "cache-primary", privateA, 1, mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64600}))
	cacheB := testCache(t, "cache-secondary", privateB, 20, mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64601}))
	router, err := NewRouter(RouterConfig{
		TrustedKeys: map[CacheID]ed25519.PublicKey{
			cacheA.CacheID(): cacheA.PublicKey(),
			cacheB.CacheID(): cacheB.PublicKey(),
		},
		Preference: []CacheID{cacheA.CacheID(), cacheB.CacheID()},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cacheB, false)
	preferred, ok := router.PreferredView()
	if !ok || preferred.CacheID != cacheB.CacheID() {
		t.Fatalf("preferred view skipped committed cache: %#v", preferred)
	}

	queryA, err := router.ResetQuery(cacheA.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	responseA, err := cacheA.ResetResponse(queryA)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.IngestMessage(responseA[0]); err != nil {
		t.Fatal(err)
	}
	if _, ok := router.View(cacheA.CacheID()); ok {
		t.Fatal("partial primary reset became visible")
	}
	preferred, ok = router.PreferredView()
	if !ok || preferred.CacheID != cacheB.CacheID() {
		t.Fatalf("partial cache displaced committed preferred view: %#v", preferred)
	}
	secondaryView, ok := router.View(cacheB.CacheID())
	if !ok || secondaryView.Serial != 20 {
		t.Fatalf("secondary state changed: %#v", secondaryView)
	}

	for _, message := range reversed(responseA[1:]) {
		if err := router.IngestMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	preferred, ok = router.PreferredView()
	if !ok || preferred.CacheID != cacheA.CacheID() {
		t.Fatalf("committed primary was not selected: %#v", preferred)
	}
	if err := router.PromoteCache(cacheB.CacheID()); err != nil {
		t.Fatalf("promote committed secondary: %v", err)
	}
	preferred, _ = router.PreferredView()
	if preferred.CacheID != cacheB.CacheID() {
		t.Fatalf("promotion failed: %s", preferred.CacheID)
	}
}

func TestExportedStateNeverExposesPartialSwitch(t *testing.T) {
	private := testPrivateKey(130)
	oldSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64610})
	cache := testCache(t, "cache-crash", private, 2, oldSet)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)
	if _, err := cache.RotateSession(); err != nil {
		t.Fatal(err)
	}
	newSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64611})
	if _, err := cache.ReplaceVRPs(newSet); err != nil {
		t.Fatal(err)
	}
	query, err := router.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	messages, err := cache.ResetResponse(query)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.IngestMessage(messages[0]); err != nil {
		t.Fatal(err)
	}

	state := router.ExportState()
	if len(state.Caches) != 1 {
		t.Fatalf("unexpected exported cache count: %d", len(state.Caches))
	}
	entry := state.Caches[0]
	assertVRPList(t, entry.VRPs, oldSet)
	if entry.Serial != 2 || !entry.NeedsResync {
		t.Fatalf("partial reset leaked or failed to mark stale: %#v", entry)
	}

	restored, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreState(state); err != nil {
		t.Fatalf("restore: %v", err)
	}
	view, ok := restored.View(cache.CacheID())
	if !ok || view.Serial != 2 || view.Status != ViewStale {
		t.Fatalf("restored partial switch view = %#v", view)
	}
	assertVRPList(t, view.VRPs, oldSet)
	_ = query
	_ = messages
}

func TestConcurrentReadersSeeOnlyCompleteViews(t *testing.T) {
	private := testPrivateKey(140)
	setA := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64620})
	setB := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64621})
	cache := testCache(t, "cache-concurrent", private, 100, setA)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)

	roots := map[Digest]struct{}{}
	for _, set := range [][]VRP{setA, setB} {
		root, rootErr := ComputeRoot(set)
		if rootErr != nil {
			t.Fatal(rootErr)
		}
		roots[root] = struct{}{}
	}

	stop := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		defer close(readErr)
		for {
			select {
			case <-stop:
				readErr <- nil
				return
			default:
			}
			view, ok := router.View(cache.CacheID())
			if !ok {
				readErr <- fmt.Errorf("view disappeared")
				return
			}
			if _, known := roots[view.RootDigest]; !known {
				readErr <- fmt.Errorf("unknown root %s", view.RootDigest)
				return
			}
			root, rootErr := ComputeRoot(view.VRPs)
			if rootErr != nil {
				readErr <- rootErr
				return
			}
			if root != view.RootDigest || len(view.VRPs) != 1 {
				readErr <- fmt.Errorf("mixed VRP set: root=%s computed=%s count=%d", view.RootDigest, root, len(view.VRPs))
				return
			}
		}
	}()

	for i := 0; i < 200; i++ {
		target := setA
		if i%2 == 0 {
			target = setB
		}
		if _, err := cache.ReplaceVRPs(target); err != nil {
			t.Fatal(err)
		}
		query, err := router.ResetQuery(cache.CacheID())
		if err != nil {
			t.Fatal(err)
		}
		messages, err := cache.ResetResponse(query)
		if err != nil {
			t.Fatal(err)
		}
		if err := router.IngestResponse(reversed(messages)); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}
}
