package netsec

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

type manualClock struct {
	now time.Time
}

func (c *manualClock) Now() time.Time {
	return c.now
}

func signature(oldKey, newKey string) []byte {
	return []byte("signature:" + oldKey + ":" + newKey)
}

func verifySignature(oldKey, newKey string, sig []byte) bool {
	return string(sig) == string(signature(oldKey, newKey))
}

func mustAdd(t *testing.T, store *Store, node Node) {
	t.Helper()
	if err := store.AddNode(node); err != nil {
		t.Fatalf("AddNode(%q): %v", node.ID, err)
	}
}

func graphKeys(graph Graph) []string {
	keys := make([]string, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		keys = append(keys, node.Key)
	}
	sort.Strings(keys)
	return keys
}

func TestExpiryBoundaryExcludesNodeAtExactInstant(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	expiry := base.Add(time.Hour)
	clock := &manualClock{now: base}
	store := NewStore(Options{Now: clock.Now})

	mustAdd(t, store, Node{ID: "node-a", Key: "key-a", ExpiresAt: &expiry})

	tests := []struct {
		name    string
		now     time.Time
		expired bool
		nodes   int
	}{
		{name: "before boundary", now: expiry.Add(-time.Nanosecond), expired: false, nodes: 1},
		{name: "exact boundary", now: expiry, expired: true, nodes: 0},
		{name: "after boundary", now: expiry.Add(time.Nanosecond), expired: true, nodes: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ExpiredAt(test.now, &expiry); got != test.expired {
				t.Fatalf("ExpiredAt() = %v, want %v", got, test.expired)
			}
			if got := len(store.GraphAt(test.now).Nodes); got != test.nodes {
				t.Fatalf("GraphAt() contains %d nodes, want %d", got, test.nodes)
			}
		})
	}
}

func TestDeltaAfterCachedGraphCarriesExpiryDeletion(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	shortExpiry := base.Add(time.Minute)
	longExpiry := base.Add(24 * time.Hour)
	clock := &manualClock{now: base}
	store := NewStore(Options{Now: clock.Now})

	mustAdd(t, store, Node{ID: "node-short", Key: "key-short", ExpiresAt: &shortExpiry})
	mustAdd(t, store, Node{ID: "node-long", Key: "key-long", ExpiresAt: &longExpiry})

	cached := store.Graph()
	if len(cached.Nodes) != 2 {
		t.Fatalf("cached graph contains %d nodes, want 2", len(cached.Nodes))
	}

	clock.now = shortExpiry
	delta := store.DeltaSince(cached)
	current := store.Graph()

	if got, want := graphKeys(current), []string{"key-long"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("current graph keys = %v, want %v", got, want)
	}
	// Expiry is time-derived, so the mutation version is intentionally the
	// same as the cached graph even though the effective graph changed.
	if current.Version != cached.Version {
		t.Fatalf("version changed on expiry: got %d, want %d", current.Version, cached.Version)
	}
	if len(delta) != 1 {
		t.Fatalf("delta = %#v, want exactly one delete", delta)
	}
	if delta[0].Op != DeltaDelete {
		t.Fatalf("delta op = %q, want %q", delta[0].Op, DeltaDelete)
	}
	if delta[0].NodeID != "node-short" || delta[0].Key != "key-short" {
		t.Fatalf("delete = %#v, want node-short/key-short", delta[0])
	}
	if delta[0].Node != nil {
		t.Fatalf("delete must not carry an upsert node: %#v", delta[0].Node)
	}
}

func TestRotationRetiresOldKeyAndPreservesPolicy(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	expiry := base.Add(24 * time.Hour)
	clock := &manualClock{now: base}
	verifyCalls := 0
	store := NewStore(Options{
		Now: clock.Now,
		Verify: func(oldKey, newKey string, sig []byte) bool {
			verifyCalls++
			return verifySignature(oldKey, newKey, sig)
		},
	})
	policy := map[string]any{"allow": []string{"10.0.0.0/24"}}
	mustAdd(t, store, Node{
		ID:        "node-a",
		Key:       "key-old",
		ExpiresAt: &expiry,
		Policy:    policy,
	})
	cached := store.Graph()

	request := RotationRequest{
		NodeID:    "node-a",
		OldKey:    "key-old",
		NewKey:    "key-new",
		Signature: signature("key-old", "key-new"),
	}
	rotated, err := store.Rotate(request)
	if err != nil {
		t.Fatalf("Rotate(): %v", err)
	}
	if rotated.Key != "key-new" {
		t.Fatalf("rotated key = %q, want key-new", rotated.Key)
	}
	if !reflect.DeepEqual(rotated.Policy, policy) {
		t.Fatalf("policy = %#v, want %#v", rotated.Policy, policy)
	}
	if rotated.ExpiresAt == nil || !rotated.ExpiresAt.Equal(expiry) {
		t.Fatalf("expiry = %v, want inherited %v", rotated.ExpiresAt, expiry)
	}

	current := store.Graph()
	if got, want := graphKeys(current), []string{"key-new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("current graph keys = %v, want %v", got, want)
	}
	if current.Nodes[0].Policy == nil {
		t.Fatal("rotated node lost its access policy")
	}

	delta := store.DeltaSince(cached)
	if len(delta) != 2 {
		t.Fatalf("rotation delta = %#v, want delete plus upsert", delta)
	}
	if delta[0].Op != DeltaDelete || delta[0].Key != "key-old" {
		t.Fatalf("first delta = %#v, want delete key-old", delta[0])
	}
	if delta[1].Op != DeltaUpsert || delta[1].Key != "key-new" {
		t.Fatalf("second delta = %#v, want upsert key-new", delta[1])
	}

	// Replaying the exact request, or any request signed by the same old key,
	// must not rotate again.
	if _, err := store.Rotate(request); !errors.Is(err, ErrOldKeyAlreadyRotated) {
		t.Fatalf("replayed Rotate() error = %v, want %v", err, ErrOldKeyAlreadyRotated)
	}
	replayWithNewKey := RotationRequest{
		NodeID:    "node-a",
		OldKey:    "key-old",
		NewKey:    "key-newer",
		Signature: signature("key-old", "key-newer"),
	}
	if _, err := store.Rotate(replayWithNewKey); !errors.Is(err, ErrOldKeyAlreadyRotated) {
		t.Fatalf("old-key replay with different new key error = %v, want %v", err, ErrOldKeyAlreadyRotated)
	}
	if verifyCalls != 1 {
		t.Fatalf("signature verifier called %d times, want 1 (replays rejected before verification)", verifyCalls)
	}
}

func TestRotationAtAndAfterExpiryIsRejected(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	expiry := base.Add(time.Hour)

	tests := []struct {
		name    string
		now     time.Time
		wantErr bool
	}{
		{name: "just before expiry", now: expiry.Add(-time.Nanosecond)},
		{name: "at expiry boundary", now: expiry, wantErr: true},
		{name: "after expiry", now: expiry.Add(time.Nanosecond), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &manualClock{now: test.now}
			store := NewStore(Options{Now: clock.Now, Verify: verifySignature})
			mustAdd(t, store, Node{ID: "node-a", Key: "key-old", ExpiresAt: &expiry})

			_, err := store.Rotate(RotationRequest{
				NodeID:    "node-a",
				OldKey:    "key-old",
				NewKey:    "key-new",
				Signature: signature("key-old", "key-new"),
			})
			if test.wantErr {
				if !errors.Is(err, ErrOldKeyExpired) {
					t.Fatalf("Rotate() error = %v, want %v", err, ErrOldKeyExpired)
				}
				if got := len(store.Graph().Nodes); got != 0 {
					t.Fatalf("graph contains %d expired nodes, want 0", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Rotate() before expiry: %v", err)
			}
			if got, want := graphKeys(store.Graph()), []string{"key-new"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("graph keys = %v, want %v", got, want)
			}
		})
	}
}

func TestEmptyExpiryRequiresAdministratorExemption(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	clock := &manualClock{now: base}
	store := NewStore(Options{Now: clock.Now, Verify: verifySignature})

	err := store.AddNode(Node{ID: "node-a", Key: "key-a"})
	if !errors.Is(err, ErrExpiryWithoutExemption) {
		t.Fatalf("AddNode() error = %v, want %v", err, ErrExpiryWithoutExemption)
	}

	mustAdd(t, store, Node{ID: "node-b", Key: "key-b", AdminExempt: true})
	clock.now = base.Add(100 * 365 * 24 * time.Hour)
	if got := graphKeys(store.Graph()); !reflect.DeepEqual(got, []string{"key-b"}) {
		t.Fatalf("exempt graph keys = %v, want [key-b]", got)
	}

	rotated, err := store.Rotate(RotationRequest{
		NodeID:    "node-b",
		OldKey:    "key-b",
		NewKey:    "key-b2",
		Signature: signature("key-b", "key-b2"),
	})
	if err != nil {
		t.Fatalf("Rotate() exempt node: %v", err)
	}
	if rotated.ExpiresAt != nil || !rotated.AdminExempt {
		t.Fatalf("rotation lost exemption state: %#v", rotated)
	}
}

func TestRotationRequiresInjectedSignatureVerification(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	expiry := base.Add(time.Hour)

	t.Run("missing verifier", func(t *testing.T) {
		clock := &manualClock{now: base}
		store := NewStore(Options{Now: clock.Now})
		mustAdd(t, store, Node{ID: "node-a", Key: "key-old", ExpiresAt: &expiry})

		request := RotationRequest{
			NodeID:    "node-a",
			OldKey:    "key-old",
			NewKey:    "key-new",
			Signature: signature("key-old", "key-new"),
		}
		_, err := store.Rotate(request)
		if !errors.Is(err, ErrSignatureVerifierMissing) {
			t.Fatalf("Rotate() error = %v, want %v", err, ErrSignatureVerifierMissing)
		}

		store.SetSignatureVerifier(verifySignature)
		if _, err := store.Rotate(request); err != nil {
			t.Fatalf("Rotate() after installing verifier: %v", err)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		clock := &manualClock{now: base}
		store := NewStore(Options{Now: clock.Now, Verify: verifySignature})
		mustAdd(t, store, Node{ID: "node-a", Key: "key-old", ExpiresAt: &expiry})

		_, err := store.Rotate(RotationRequest{
			NodeID:    "node-a",
			OldKey:    "key-old",
			NewKey:    "key-new",
			Signature: []byte("forged"),
		})
		if !errors.Is(err, ErrInvalidRotationSignature) {
			t.Fatalf("Rotate() error = %v, want %v", err, ErrInvalidRotationSignature)
		}
		if got := graphKeys(store.Graph()); !reflect.DeepEqual(got, []string{"key-old"}) {
			t.Fatalf("graph keys = %v, want unchanged [key-old]", got)
		}
	})
}

func TestRetiredAndInUseKeysCannotBeReintroduced(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	expiry := base.Add(time.Hour)
	clock := &manualClock{now: base}
	store := NewStore(Options{Now: clock.Now, Verify: verifySignature})

	mustAdd(t, store, Node{ID: "node-a", Key: "key-a", ExpiresAt: &expiry})
	mustAdd(t, store, Node{ID: "node-b", Key: "key-b", ExpiresAt: &expiry})
	if _, err := store.Rotate(RotationRequest{
		NodeID:    "node-a",
		OldKey:    "key-a",
		NewKey:    "key-a2",
		Signature: signature("key-a", "key-a2"),
	}); err != nil {
		t.Fatalf("Rotate(): %v", err)
	}

	if err := store.AddNode(Node{ID: "node-c", Key: "key-a", ExpiresAt: &expiry}); !errors.Is(err, ErrKeyRetired) {
		t.Fatalf("reintroducing rotated key error = %v, want %v", err, ErrKeyRetired)
	}
	if err := store.AddNode(Node{ID: "node-c", Key: "key-b", ExpiresAt: &expiry}); !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("reusing active key error = %v, want %v", err, ErrKeyInUse)
	}
	if _, err := store.Rotate(RotationRequest{
		NodeID:    "node-b",
		OldKey:    "key-b",
		NewKey:    "key-a2",
		Signature: signature("key-b", "key-a2"),
	}); !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("rotating into active key error = %v, want %v", err, ErrKeyInUse)
	}
}

func TestRemovalAndPolicyUpdatesAreReflectedInDelta(t *testing.T) {
	base := time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)
	expiry := base.Add(24 * time.Hour)
	clock := &manualClock{now: base}
	store := NewStore(Options{Now: clock.Now, Verify: verifySignature})

	mustAdd(t, store, Node{ID: "node-a", Key: "key-a", ExpiresAt: &expiry})
	mustAdd(t, store, Node{ID: "node-b", Key: "key-b", ExpiresAt: &expiry})
	cached := store.Graph()

	if err := store.SetPolicy("node-a", "policy-v2"); err != nil {
		t.Fatalf("SetPolicy(): %v", err)
	}
	delta := store.DeltaSince(cached)
	if len(delta) != 1 || delta[0].Op != DeltaUpsert || delta[0].Key != "key-a" {
		t.Fatalf("policy delta = %#v, want one upsert for key-a", delta)
	}
	if delta[0].Node == nil || delta[0].Node.Policy != "policy-v2" {
		t.Fatalf("policy upsert does not carry the new policy: %#v", delta[0].Node)
	}

	beforeRemoval := store.Graph()
	if err := store.RemoveNode("node-b"); err != nil {
		t.Fatalf("RemoveNode(): %v", err)
	}
	delta = store.DeltaSince(beforeRemoval)
	if len(delta) != 1 || delta[0].Op != DeltaDelete || delta[0].Key != "key-b" {
		t.Fatalf("removal delta = %#v, want one delete for key-b", delta)
	}

	if err := store.AddNode(Node{ID: "node-c", Key: "key-b", ExpiresAt: &expiry}); !errors.Is(err, ErrKeyRetired) {
		t.Fatalf("reintroducing removed key error = %v, want %v", err, ErrKeyRetired)
	}
}
