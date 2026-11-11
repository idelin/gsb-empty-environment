package netsec

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

func digestOf(name string) Digest { return Digest(name) }

// memStore is an in-memory ManifestStore for tests.
type memStore struct {
	manifests map[string]*Manifest
	fetches   map[string]int
}

func newMemStore(manifests ...*Manifest) *memStore {
	store := &memStore{
		manifests: make(map[string]*Manifest),
		fetches:   make(map[string]int),
	}
	for _, manifest := range manifests {
		store.manifests[string(manifest.Digest)] = manifest
	}
	return store
}

func (s *memStore) Manifest(_ context.Context, digest Digest) (*Manifest, error) {
	s.fetches[string(digest)]++
	manifest, ok := s.manifests[string(digest)]
	if !ok {
		return nil, nil
	}
	return manifest, nil
}

type authCall struct {
	importer TenantID
	owner    TenantID
	digest   Digest
}

// memAuth grants shares through an explicit allow list keyed by digest.
// Revoking a share means deleting the digest from the map.
type memAuth struct {
	importer TenantID
	shared   map[string]map[TenantID]struct{}
	calls    []authCall
}

func newMemAuth(importer TenantID) *memAuth {
	return &memAuth{
		importer: importer,
		shared:   make(map[string]map[TenantID]struct{}),
	}
}

func (a *memAuth) grant(digest Digest) {
	allowed, ok := a.shared[string(digest)]
	if !ok {
		allowed = make(map[TenantID]struct{})
		a.shared[string(digest)] = allowed
	}
	allowed[a.importer] = struct{}{}
}

func (a *memAuth) revoke(digest Digest) {
	delete(a.shared, string(digest))
}

func (a *memAuth) ShareAuthorized(_ context.Context, importer TenantID, owner TenantID, digest Digest) (bool, error) {
	a.calls = append(a.calls, authCall{
		importer: importer,
		owner:    owner,
		digest:   append(Digest(nil), digest...),
	})
	if owner == importer {
		return true, nil
	}
	allowed, ok := a.shared[string(digest)]
	if !ok {
		return false, nil
	}
	_, ok = allowed[importer]
	return ok, nil
}

func (a *memAuth) checked(digest Digest) bool {
	for _, call := range a.calls {
		if string(call.digest) == string(digest) {
			return true
		}
	}
	return false
}

func block(tenant TenantID, snapshot, blockID string) BlockEntry {
	return BlockEntry{
		Block: BlockID(blockID),
		Source: BlockSource{
			Tenant:   tenant,
			Snapshot: digestOf(snapshot),
		},
	}
}

func parent(digest string) ParentRef { return ParentRef{Digest: digestOf(digest)} }

func manifest(digest string, owner TenantID, blocks []BlockEntry, parents ...ParentRef) *Manifest {
	return &Manifest{
		Digest:  digestOf(digest),
		Owner:   owner,
		Blocks:  blocks,
		Parents: parents,
	}
}

func digestsAsStrings(closure *Closure) []string {
	out := make([]string, len(closure.Snapshots))
	for i, digest := range closure.Snapshots {
		out[i] = string(digest)
	}
	sort.Strings(out)
	return out
}

// A diamond-shaped cross-tenant chain must be validated all the way to the
// shared ancestor, not just against the top-level manifest.
func TestVerifySharedAncestorClosure(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer,
		[]BlockEntry{block(importer, "root", "b1")},
		parent("base-b"), parent("base-c"))
	baseB := manifest("base-b", "tenant-b",
		[]BlockEntry{block("tenant-b", "base-b", "b2")},
		parent("ancestor"))
	baseC := manifest("base-c", "tenant-c",
		[]BlockEntry{block("tenant-c", "base-c", "b3")},
		parent("ancestor"))
	ancestor := manifest("ancestor", "tenant-b",
		[]BlockEntry{block("tenant-b", "ancestor", "b4")})

	store := newMemStore(root, baseB, baseC, ancestor)
	auth := newMemAuth(importer)
	auth.grant(digestOf("base-b"))
	auth.grant(digestOf("base-c"))
	auth.grant(digestOf("ancestor"))

	closure, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	if err != nil {
		t.Fatalf("Verify returned unexpected error: %v", err)
	}

	got := digestsAsStrings(closure)
	want := []string{"ancestor", "base-b", "base-c", "root"}
	if len(got) != len(want) {
		t.Fatalf("closure snapshots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("closure snapshots = %v, want %v", got, want)
		}
	}

	for _, digest := range want {
		if !auth.checked(digestOf(digest)) {
			t.Errorf("authorization never checked for %q; hidden base skipped", digest)
		}
		if store.fetches[digest] != 1 {
			t.Errorf("manifest %q fetched %d times, want exactly once", digest, store.fetches[digest])
		}
	}

	if closure.Blocks[BlockID("b1")].Depth != 0 {
		t.Errorf("b1 depth = %d, want 0", closure.Blocks[BlockID("b1")].Depth)
	}
	if closure.Blocks[BlockID("b4")].Depth != 2 {
		t.Errorf("shared ancestor b4 depth = %d, want 2", closure.Blocks[BlockID("b4")].Depth)
	}
}

// A revoked share on a hidden incremental base must fail the import even
// though the top-level manifest is still authorized.
func TestVerifyRevokedShareOnHiddenBase(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer,
		[]BlockEntry{block(importer, "root", "b1")},
		parent("base"))
	base := manifest("base", "tenant-b",
		[]BlockEntry{block("tenant-b", "base", "b2")},
		parent("deep-base"))
	deepBase := manifest("deep-base", "tenant-b",
		[]BlockEntry{block("tenant-b", "deep-base", "b3")})

	store := newMemStore(root, base, deepBase)
	auth := newMemAuth(importer)
	auth.grant(digestOf("base"))
	// The deep base's share has been revoked: deep-base is not granted.

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var unauthorized *UnauthorizedError
	if !errors.As(err, &unauthorized) {
		t.Fatalf("error = %v, want *UnauthorizedError", err)
	}
	if string(unauthorized.Snapshot) != "deep-base" {
		t.Fatalf("unauthorized snapshot = %q, want deep-base", string(unauthorized.Snapshot))
	}
	if unauthorized.Owner != "tenant-b" || unauthorized.Importer != importer {
		t.Fatalf("unauthorized error parties = %+v", unauthorized)
	}
	if !auth.checked(digestOf("root")) || !auth.checked(digestOf("base")) {
		t.Fatalf("verification stopped before walking the full chain: %+v", auth.calls)
	}
}

// A revocation after an earlier successful import is enforced on the next
// verification because authorization is consulted for every snapshot each
// time.
func TestVerifyRevokePreviouslyGrantedSnapshot(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer,
		[]BlockEntry{block(importer, "root", "b1")},
		parent("base"))
	base := manifest("base", "tenant-b",
		[]BlockEntry{block("tenant-b", "base", "b2")})

	store := newMemStore(root, base)
	auth := newMemAuth(importer)
	auth.grant(digestOf("base"))

	if _, err := Verify(context.Background(), importer, digestOf("root"), store, auth); err != nil {
		t.Fatalf("first Verify returned unexpected error: %v", err)
	}

	auth.revoke(digestOf("base"))
	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var unauthorized *UnauthorizedError
	if !errors.As(err, &unauthorized) {
		t.Fatalf("after revoke error = %v, want *UnauthorizedError", err)
	}
	if string(unauthorized.Snapshot) != "base" {
		t.Fatalf("unauthorized snapshot = %q, want base", string(unauthorized.Snapshot))
	}
}

// Cyclic parent references must be rejected rather than causing infinite
// traversal.
func TestVerifyCyclicParentReference(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer, nil, parent("x"))
	x := manifest("x", "tenant-b", nil, parent("y"))
	y := manifest("y", "tenant-b", nil, parent("x")) // y -> x closes the cycle

	store := newMemStore(root, x, y)
	auth := newMemAuth(importer)
	auth.grant(digestOf("x"))
	auth.grant(digestOf("y"))

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var cycle *CycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("error = %v, want *CycleError", err)
	}
	if string(cycle.On) != "y" || string(cycle.Parent) != "x" {
		t.Fatalf("cycle edge = %q -> %q, want y -> x", string(cycle.On), string(cycle.Parent))
	}
}

// Equal-depth distinct sources for one logical block cannot be prioritized
// deterministically and must be rejected.
func TestVerifyBlockConflictSameDepth(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer, nil, parent("x"), parent("y"))
	x := manifest("x", "tenant-b", []BlockEntry{block("tenant-b", "x", "shared-block")})
	y := manifest("y", "tenant-c", []BlockEntry{block("tenant-c", "y", "shared-block")})

	store := newMemStore(root, x, y)
	auth := newMemAuth(importer)
	auth.grant(digestOf("x"))
	auth.grant(digestOf("y"))

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var conflict *BlockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want *BlockConflictError", err)
	}
	if conflict.Block != "shared-block" || conflict.Depth != 1 {
		t.Fatalf("conflict = %+v, want shared-block at depth 1", conflict)
	}
	if !strings.Contains(err.Error(), "shared-block") || !strings.Contains(err.Error(), "depth 1") {
		t.Fatalf("conflict message = %q, missing block id or depth", err.Error())
	}
}

// The same physical source offered from two equal-depth paths is one
// provider, not a conflict.
func TestVerifyEqualDepthIdenticalSourceDeduplicates(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer, nil, parent("x"), parent("y"))
	x := manifest("x", "tenant-b",
		[]BlockEntry{block("tenant-b", "shared-phys", "b")},
		parent("shared-phys"))
	y := manifest("y", "tenant-c",
		[]BlockEntry{block("tenant-b", "shared-phys", "b")},
		parent("shared-phys"))
	shared := manifest("shared-phys", "tenant-b", nil)

	store := newMemStore(root, x, y, shared)
	auth := newMemAuth(importer)
	auth.grant(digestOf("x"))
	auth.grant(digestOf("y"))
	auth.grant(digestOf("shared-phys"))

	closure, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	if err != nil {
		t.Fatalf("Verify returned unexpected error: %v", err)
	}
	resolved, ok := closure.Blocks["b"]
	if !ok {
		t.Fatalf("block b missing from resolved closure: %+v", closure.Blocks)
	}
	if resolved.Depth != 1 || resolved.Source.Tenant != "tenant-b" ||
		string(resolved.Source.Snapshot) != "shared-phys" {
		t.Fatalf("resolved block = %+v, want shared-phys at depth 1", resolved)
	}
}

// Nearer-to-root snapshots override deeper incremental bases for the same
// logical block.
func TestVerifyNearestSourceWins(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer,
		[]BlockEntry{block(importer, "root", "overridden")},
		parent("mid"))
	mid := manifest("mid", "tenant-b",
		[]BlockEntry{
			block("tenant-b", "mid", "overridden"),
			block("tenant-b", "mid", "from-mid"),
		},
		parent("base"))
	base := manifest("base", "tenant-b",
		[]BlockEntry{
			block("tenant-b", "base", "overridden"),
			block("tenant-b", "base", "from-base"),
		})

	store := newMemStore(root, mid, base)
	auth := newMemAuth(importer)
	auth.grant(digestOf("mid"))
	auth.grant(digestOf("base"))

	closure, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	if err != nil {
		t.Fatalf("Verify returned unexpected error: %v", err)
	}

	overridden := closure.Blocks["overridden"]
	if string(overridden.Source.Snapshot) != "root" || overridden.Depth != 0 {
		t.Fatalf("overridden = %+v, want root at depth 0", overridden)
	}
	fromMid := closure.Blocks["from-mid"]
	if string(fromMid.Source.Snapshot) != "mid" || fromMid.Depth != 1 {
		t.Fatalf("from-mid = %+v, want mid at depth 1", fromMid)
	}
	fromBase := closure.Blocks["from-base"]
	if string(fromBase.Source.Snapshot) != "base" || fromBase.Depth != 2 {
		t.Fatalf("from-base = %+v, want base at depth 2", fromBase)
	}
}

// A block source that points outside the verified closure cannot be trusted.
func TestVerifyBlockSourceOutsideClosure(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer,
		[]BlockEntry{block("tenant-b", "phantom", "b1")})
	store := newMemStore(root)
	auth := newMemAuth(importer)

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var invalid *InvalidSourceError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *InvalidSourceError", err)
	}
}

// A source claiming a different owner than the snapshot's manifest is a
// forgery attempt and must be rejected.
func TestVerifyBlockSourceTenantMismatch(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer,
		[]BlockEntry{block("tenant-c", "base-b", "b1")},
		parent("base-b"))
	baseB := manifest("base-b", "tenant-b", nil)

	store := newMemStore(root, baseB)
	auth := newMemAuth(importer)
	auth.grant(digestOf("base-b"))

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var invalid *InvalidSourceError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *InvalidSourceError", err)
	}
	if invalid.Block != "b1" {
		t.Fatalf("invalid source error = %+v, want block b1", invalid)
	}
}

// A missing parent manifest fails the closure instead of being skipped.
func TestVerifyMissingParentManifest(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer, nil, parent("missing"))
	store := newMemStore(root)
	auth := newMemAuth(importer)

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var notFound *SnapshotNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want *SnapshotNotFoundError", err)
	}
	if string(notFound.Digest) != "missing" {
		t.Fatalf("not found digest = %q, want missing", string(notFound.Digest))
	}
}

// A manifest whose declared digest does not match its storage key is
// rejected so digests cannot be spoofed by the store.
func TestVerifyDigestMismatch(t *testing.T) {
	const importer TenantID = "tenant-a"

	declared := manifest("other-digest", importer, nil)
	store := newMemStore(declared)
	store.manifests["root"] = declared
	auth := newMemAuth(importer)

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var mismatch *DigestMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want *DigestMismatchError", err)
	}
	if string(mismatch.Requested) != "root" || string(mismatch.Declared) != "other-digest" {
		t.Fatalf("mismatch = %+v", mismatch)
	}
}

// A foreign top-level snapshot without a current share is rejected too.
func TestVerifyForeignRootWithoutShare(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", "tenant-b",
		[]BlockEntry{block("tenant-b", "root", "b1")})
	store := newMemStore(root)
	auth := newMemAuth(importer)

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var unauthorized *UnauthorizedError
	if !errors.As(err, &unauthorized) {
		t.Fatalf("error = %v, want *UnauthorizedError", err)
	}
	if string(unauthorized.Snapshot) != "root" {
		t.Fatalf("unauthorized snapshot = %q, want root", string(unauthorized.Snapshot))
	}
}

// Duplicate block entries inside one manifest are invalid regardless of
// whether their sources agree.
func TestVerifyDuplicateBlockInManifest(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer, []BlockEntry{
		block(importer, "root", "dup"),
		block(importer, "root", "dup"),
	})
	store := newMemStore(root)
	auth := newMemAuth(importer)

	_, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	var invalid *InvalidManifestError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *InvalidManifestError", err)
	}
}

// Cancelation is observed between manifest fetches.
func TestVerifyContextCanceled(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer, nil, parent("base"))
	base := manifest("base", "tenant-b", nil)
	store := newMemStore(root, base)
	auth := newMemAuth(importer)
	auth.grant(digestOf("base"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Verify(ctx, importer, digestOf("root"), store, auth)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("error = %v, want ErrCanceled", err)
	}
}

func TestVerifyRejectsBadArguments(t *testing.T) {
	store := newMemStore()
	auth := newMemAuth("tenant-a")
	ctx := context.Background()

	cases := []struct {
		name     string
		importer TenantID
		root     Digest
		store    ManifestStore
		auth     Authorizer
	}{
		{"empty importer", "", digestOf("root"), store, auth},
		{"empty root", "tenant-a", nil, store, auth},
		{"nil store", "tenant-a", digestOf("root"), nil, auth},
		{"nil authorizer", "tenant-a", digestOf("root"), store, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Verify(ctx, tc.importer, tc.root, tc.store, tc.auth); err == nil {
				t.Fatalf("Verify(%s) returned nil error", tc.name)
			}
		})
	}
}

// The function adapters expose the same interfaces as concrete types.
func TestFuncAdapters(t *testing.T) {
	root := manifest("root", "tenant-a", nil)
	store := ManifestStoreFunc(func(_ context.Context, digest Digest) (*Manifest, error) {
		if string(digest) == "root" {
			return root, nil
		}
		return nil, nil
	})
	auth := AuthorizerFunc(func(_ context.Context, importer TenantID, owner TenantID, _ Digest) (bool, error) {
		return owner == importer, nil
	})

	closure, err := Verify(context.Background(), "tenant-a", digestOf("root"), store, auth)
	if err != nil {
		t.Fatalf("Verify with func adapters returned error: %v", err)
	}
	if len(closure.Snapshots) != 1 || string(closure.Snapshots[0]) != "root" {
		t.Fatalf("closure = %+v, want only root", closure.Snapshots)
	}
}

// Structural validation rejects malformed manifests and reports typed errors.
func TestVerifyInvalidManifestBranches(t *testing.T) {
	ctx := context.Background()
	importer := TenantID("tenant-a")

	cases := []struct {
		name     string
		manifest *Manifest
		want     any
	}{
		{
			name:     "empty declared digest",
			manifest: &Manifest{Digest: nil, Owner: importer},
			want:     &InvalidManifestError{},
		},
		{
			name: "empty owner",
			manifest: &Manifest{
				Digest: digestOf("root"),
			},
			want: &InvalidManifestError{},
		},
		{
			name: "empty block id",
			manifest: &Manifest{
				Digest: digestOf("root"),
				Owner:  importer,
				Blocks: []BlockEntry{{Block: "", Source: BlockSource{
					Tenant: importer, Snapshot: digestOf("root"),
				}}},
			},
			want: &InvalidManifestError{},
		},
		{
			name: "incomplete block source",
			manifest: &Manifest{
				Digest: digestOf("root"),
				Owner:  importer,
				Blocks: []BlockEntry{{Block: "b", Source: BlockSource{Tenant: importer}}},
			},
			want: &InvalidSourceError{},
		},
		{
			name: "empty parent digest",
			manifest: &Manifest{
				Digest:  digestOf("root"),
				Owner:   importer,
				Parents: []ParentRef{{Digest: nil}},
			},
			want: &InvalidManifestError{},
		},
		{
			name: "duplicate parent",
			manifest: &Manifest{
				Digest: digestOf("root"),
				Owner:  importer,
				Parents: []ParentRef{
					{Digest: digestOf("p")},
					{Digest: digestOf("p")},
				},
			},
			want: &InvalidManifestError{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore(tc.manifest)
			store.manifests["root"] = tc.manifest
			auth := newMemAuth(importer)

			_, err := Verify(ctx, importer, digestOf("root"), store, auth)
			if err == nil {
				t.Fatalf("Verify returned nil error for %s", tc.name)
			}
			if !errors.As(err, &tc.want) {
				t.Fatalf("error = %T %v, want %T", err, err, tc.want)
			}
			if err.Error() == "" {
				t.Fatalf("error produced empty message")
			}
		})
	}
}

// Every typed error renders a non-empty, stable diagnostic message.
func TestTypedErrorMessages(t *testing.T) {
	errs := []error{
		&SnapshotNotFoundError{Digest: digestOf("d")},
		&DigestMismatchError{Requested: digestOf("a"), Declared: digestOf("b")},
		&UnauthorizedError{Snapshot: digestOf("d"), Owner: "b", Importer: "a"},
		&CycleError{On: digestOf("x"), Parent: digestOf("y")},
		&BlockConflictError{
			Block:  "blk",
			First:  BlockSource{Tenant: "b", Snapshot: digestOf("s1")},
			Second: BlockSource{Tenant: "c", Snapshot: digestOf("s2")},
			Depth:  3,
		},
		&InvalidSourceError{
			Block:  "blk",
			Source: BlockSource{Tenant: "b", Snapshot: digestOf("s")},
			Reason: "test reason",
		},
		&InvalidManifestError{Snapshot: digestOf("d"), Reason: "test reason"},
	}
	for _, err := range errs {
		if err.Error() == "" {
			t.Fatalf("%T rendered an empty error message", err)
		}
	}

	// Empty digest still renders a placeholder rather than panicking.
	if got := (&SnapshotNotFoundError{Digest: nil}).Error(); got == "" {
		t.Fatal("empty digest error rendered empty message")
	}
}

// Host errors are propagated unchanged so the import host can react to
// storage or authorization-subsystem failures.
func TestVerifyPropagatesHostErrors(t *testing.T) {
	wantStoreErr := errors.New("store unavailable")
	store := ManifestStoreFunc(func(_ context.Context, _ Digest) (*Manifest, error) {
		return nil, wantStoreErr
	})
	auth := newMemAuth("tenant-a")

	_, err := Verify(context.Background(), "tenant-a", digestOf("root"), store, auth)
	if !errors.Is(err, wantStoreErr) {
		t.Fatalf("store error = %v, want %v", err, wantStoreErr)
	}

	wantAuthErr := errors.New("authz service down")
	goodStore := newMemStore(manifest("root", "tenant-a", nil))
	failingAuth := AuthorizerFunc(func(_ context.Context, _ TenantID, _ TenantID, _ Digest) (bool, error) {
		return false, wantAuthErr
	})
	_, err = Verify(context.Background(), "tenant-a", digestOf("root"), goodStore, failingAuth)
	if !errors.Is(err, wantAuthErr) {
		t.Fatalf("authorizer error = %v, want %v", err, wantAuthErr)
	}
}

// A node reached through a short path and then a longer path keeps the
// minimum depth, which drives the nearest-source priority rule.
func TestVerifyDiamondKeepsMinimumDepth(t *testing.T) {
	const importer TenantID = "tenant-a"

	root := manifest("root", importer, nil, parent("near"), parent("far"))
	near := manifest("near", "tenant-b", nil, parent("shared"))
	far := manifest("far", "tenant-c",
		[]BlockEntry{block(importer, "root", "root-only")},
		parent("mid"))
	mid := manifest("mid", "tenant-c", nil, parent("shared"))
	shared := manifest("shared", "tenant-b", nil)

	store := newMemStore(root, near, far, mid, shared)
	auth := newMemAuth(importer)
	for _, name := range []string{"near", "far", "mid", "shared"} {
		auth.grant(digestOf(name))
	}

	closure, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	if err != nil {
		t.Fatalf("Verify returned unexpected error: %v", err)
	}

	// shared is reachable at depth 2 (root -> near -> shared) and depth 3
	// (root -> far -> mid -> shared); the minimum depth must win.
	if got := closure.Depths["shared"]; got != 2 {
		t.Fatalf("shared depth = %d, want 2", got)
	}
	if got := closure.Depths["mid"]; got != 2 {
		t.Fatalf("mid depth = %d, want 2", got)
	}
	if got := closure.Depths["near"]; got != 1 {
		t.Fatalf("near depth = %d, want 1", got)
	}
	if got := closure.Depths["far"]; got != 1 {
		t.Fatalf("far depth = %d, want 1", got)
	}
	if got := closure.Depths["root"]; got != 0 {
		t.Fatalf("root depth = %d, want 0", got)
	}
}

// A shared node reached through multiple unequal-length paths records the
// BFS minimum depth regardless of parent listing order.
func TestVerifyDiamondDepthWithReversedParentOrder(t *testing.T) {
	const importer TenantID = "tenant-a"

	// The store returns parents in an order that forces the long path to be
	// walked before the short path reaches the shared snapshot.
	root := manifest("root", importer, nil, parent("far"), parent("near"))
	near := manifest("near", "tenant-b", nil, parent("shared"))
	far := manifest("far", "tenant-c", nil, parent("mid"))
	mid := manifest("mid", "tenant-c", nil, parent("shared"))
	shared := manifest("shared", "tenant-b", nil)

	store := newMemStore(root, near, far, mid, shared)
	auth := newMemAuth(importer)
	for _, name := range []string{"near", "far", "mid", "shared"} {
		auth.grant(digestOf(name))
	}

	closure, err := Verify(context.Background(), importer, digestOf("root"), store, auth)
	if err != nil {
		t.Fatalf("Verify returned unexpected error: %v", err)
	}
	if got := closure.Depths["shared"]; got != 2 {
		t.Fatalf("shared depth = %d, want 2", got)
	}
}

func TestItoa(t *testing.T) {
	cases := map[int]string{
		0:   "0",
		1:   "1",
		9:   "9",
		10:  "10",
		42:  "42",
		100: "100",
	}
	for input, want := range cases {
		if got := itoa(input); got != want {
			t.Errorf("itoa(%d) = %q, want %q", input, got, want)
		}
	}
}
