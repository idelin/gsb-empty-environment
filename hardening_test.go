package netsec

import (
	"crypto/ed25519"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestSerialDistanceBoundaries(t *testing.T) {
	tests := []struct {
		name string
		from uint32
		to   uint32
		want bool
	}{
		{name: "one", from: 0, to: 1, want: true},
		{name: "half-minus-one", from: 0, to: math.MaxInt32, want: true},
		{name: "half", from: 0, to: 1 << 31, want: false},
		{name: "duplicate", from: 7, to: 7, want: false},
		{name: "regression", from: 8, to: 7, want: false},
		{name: "wrap", from: math.MaxUint32, to: 0, want: true},
		{name: "wrap-two", from: math.MaxUint32, to: 1, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsForwardSerial(test.from, test.to); got != test.want {
				t.Fatalf("IsForwardSerial(%d, %d) = %v, want %v", test.from, test.to, got, test.want)
			}
		})
	}
}

func TestSessionChangeCannotUseDelta(t *testing.T) {
	private := testPrivateKey(150)
	vrps := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64630})
	cache := testCache(t, "cache-delta-session", private, 3, vrps)
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
	query, err := router.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	end := SignedMessage{
		CacheID: cache.CacheID(), ConnectionGeneration: query.ConnectionGeneration, RequestID: query.RequestID,
		Kind: MessageSerialEnd, Index: 0, Total: 1, Session: cache.Session(), Serial: 3,
		FromSerial: 3, ToSerial: 3, RootDigest: cache.RootDigest(),
	}
	signed, err := SignMessage(private, end)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.IngestMessage(signed); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("delta session change error = %v, want %v", err, ErrSessionMismatch)
	}
	view, _ := router.View(cache.CacheID())
	assertVRPList(t, view.VRPs, vrps)
	if view.Serial != 3 {
		t.Fatalf("view changed: %d", view.Serial)
	}
}

func TestNoopSerialResponseRefreshesLiveness(t *testing.T) {
	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	private := testPrivateKey(160)
	vrps := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64640})
	cache := testCache(t, "cache-noop", private, 1, vrps)
	router, err := NewRouter(RouterConfig{
		TrustedKeys: map[CacheID]ed25519.PublicKey{cache.CacheID(): cache.PublicKey()},
		StaleAfter:  10 * time.Second,
		MaxStale:    30 * time.Second,
		Now:         clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := syncReset(t, router, cache, false)
	clock.Advance(9 * time.Second)
	second := syncSerial(t, router, cache, false)
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("no-op response did not refresh liveness: first=%s second=%s", first.UpdatedAt, second.UpdatedAt)
	}
	if second.Status != ViewFresh {
		t.Fatalf("no-op response left view stale: %s", second.Status)
	}
}

func TestIncompleteBatchIsNotCommitted(t *testing.T) {
	private := testPrivateKey(165)
	first := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64645}
	second := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64646}
	cache := testCache(t, "cache-incomplete", private, 1, mustVRPs(t, first))
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)
	if _, err := cache.ApplyChanges([]Change{{Operation: ChangeAdd, VRP: second}}); err != nil {
		t.Fatal(err)
	}
	query, err := router.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	messages, err := cache.SerialResponse(query)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.IngestResponse(messages[:len(messages)-1]); !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("truncated response error = %v, want %v", err, ErrIncompleteResponse)
	}
	view, _ := router.View(cache.CacheID())
	assertVRPList(t, view.VRPs, mustVRPs(t, first))
	if view.Serial != 1 {
		t.Fatalf("truncated response committed serial %d", view.Serial)
	}

	// A new query discards the abandoned partial batch and can resynchronise.
	view = syncSerial(t, router, cache, false)
	assertVRPList(t, view.VRPs, mustVRPs(t, first, second))
	if view.Serial != 2 {
		t.Fatalf("complete retry serial = %d", view.Serial)
	}
}

func TestSerialRegressionAndAmbiguousForwardDistanceAreRejected(t *testing.T) {
	private := testPrivateKey(167)
	vrps := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64647})
	cache := testCache(t, "cache-regression", private, 20, vrps)
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)
	root := cache.RootDigest()

	for _, target := range []uint32{19, 20 + 1<<31} {
		query, err := router.SerialQuery(cache.CacheID())
		if err != nil {
			t.Fatal(err)
		}
		end := SignedMessage{
			CacheID: cache.CacheID(), ConnectionGeneration: query.ConnectionGeneration, RequestID: query.RequestID,
			Kind: MessageSerialEnd, Index: 0, Total: 1, Session: cache.Session(), Serial: target,
			FromSerial: 20, ToSerial: target, RootDigest: root,
		}
		signed, err := SignMessage(private, end)
		if err != nil {
			t.Fatal(err)
		}
		if err := router.IngestMessage(signed); !errors.Is(err, ErrSerialRegression) {
			t.Fatalf("target %d error = %v, want %v", target, err, ErrSerialRegression)
		}
	}
	view, _ := router.View(cache.CacheID())
	if view.Serial != 20 {
		t.Fatalf("regression changed serial: %d", view.Serial)
	}
}

func TestDisconnectResumesSerialSync(t *testing.T) {
	private := testPrivateKey(169)
	first := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64648}
	second := VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64649}
	cache := testCache(t, "cache-resume", private, 1, mustVRPs(t, first))
	router, err := NewRouter(RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)
	if _, err := cache.ReplaceVRPs(mustVRPs(t, second)); err != nil {
		t.Fatal(err)
	}
	if err := router.Disconnect(cache.CacheID()); err != nil {
		t.Fatal(err)
	}
	generation, err := router.BeginConnection(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	query, err := router.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	if query.ConnectionGeneration != generation || query.FromSerial != 1 {
		t.Fatalf("resumed query = %#v", query)
	}
	messages, err := cache.SerialResponse(query)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.IngestResponse(reversed(messages)); err != nil {
		t.Fatalf("resume serial sync: %v", err)
	}
	view, _ := router.View(cache.CacheID())
	if view.Serial != 2 {
		t.Fatalf("resumed serial = %d", view.Serial)
	}
	assertVRPList(t, view.VRPs, mustVRPs(t, second))
}

func TestSaveLoadStateAtBoundaries(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "router-state.json")
	private := testPrivateKey(170)
	oldSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64650})
	cache := testCache(t, "cache-file", private, 4, oldSet)
	config := RouterConfig{TrustedKeys: map[CacheID]ed25519.PublicKey{
		cache.CacheID(): cache.PublicKey(),
	}}
	router, err := NewRouter(config)
	if err != nil {
		t.Fatal(err)
	}
	_ = syncReset(t, router, cache, false)
	if err := router.SaveState(path); err != nil {
		t.Fatalf("save state: %v", err)
	}

	restored, err := NewRouter(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadState(path); err != nil {
		t.Fatalf("load state: %v", err)
	}
	view, ok := restored.View(cache.CacheID())
	if !ok || view.Serial != 4 {
		t.Fatalf("restored view = %#v ok=%v", view, ok)
	}
	assertVRPList(t, view.VRPs, oldSet)

	// A response from before the restart must not be accepted after the
	// restored router increments its transport generation.
	if _, err := restored.BeginConnection(cache.CacheID()); err != nil {
		t.Fatal(err)
	}
	query, err := restored.SerialQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	oldMessages, err := cache.SerialResponse(query)
	if err != nil {
		t.Fatalf("fresh no-op response: %v", err)
	}
	if _, err := restored.BeginConnection(cache.CacheID()); err != nil {
		t.Fatal(err)
	}
	if err := restored.IngestResponse(oldMessages); !errors.Is(err, ErrConnectionGeneration) {
		t.Fatalf("pre-reconnect response error = %v", err)
	}

	// Saving while a new session is only partially received must persist the
	// complete old view, marked for resync.
	if _, err := cache.RotateSession(); err != nil {
		t.Fatal(err)
	}
	newSet := mustVRPs(t, VRP{Prefix: "10.0.0.0/8", MaxLength: 8, OriginAS: 64651})
	if _, err := cache.ReplaceVRPs(newSet); err != nil {
		t.Fatal(err)
	}
	router2, err := NewRouter(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := router2.RestoreState(restored.ExportState()); err != nil {
		t.Fatal(err)
	}
	query2, err := router2.ResetQuery(cache.CacheID())
	if err != nil {
		t.Fatal(err)
	}
	messages, err := cache.ResetResponse(query2)
	if err != nil {
		t.Fatal(err)
	}
	if err := router2.IngestMessage(messages[0]); err != nil {
		t.Fatal(err)
	}
	if err := router2.SaveState(path); err != nil {
		t.Fatal(err)
	}

	fresh, err := NewRouter(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.LoadState(path); err != nil {
		t.Fatal(err)
	}
	view, ok = fresh.View(cache.CacheID())
	if !ok || view.Serial != 4 || view.Status != ViewStale {
		t.Fatalf("partial switch state = %#v ok=%v", view, ok)
	}
	assertVRPList(t, view.VRPs, oldSet)
}
