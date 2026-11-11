package netsec

import (
	"errors"
	"testing"
)

const tenant = "tenant-7"

type fixture struct {
	m      *Migrator
	src    *Pool
	dst    *Pool
	auth   *AuthLog
	router *Router
}

func newFixture() *fixture {
	auth := NewAuthLog()
	src := NewPool("pool-old", tenant, "policy-v1")
	dst := NewPool("pool-new", tenant, "policy-v1")
	router := NewRouter()
	router.set(tenant, src)
	return &fixture{
		m:      NewMigrator(tenant, src, dst, auth, router),
		src:    src,
		dst:    dst,
		auth:   auth,
		router: router,
	}
}

func (f *fixture) srcCred() Credentials { return Credentials{Tenant: tenant, Pool: "pool-old"} }
func (f *fixture) dstCred() Credentials { return Credentials{Tenant: tenant, Pool: "pool-new"} }

func mustRead(t *testing.T, b *Block, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(b.Data) != want {
		t.Fatalf("read got %q, want %q", b.Data, want)
	}
}

func mustErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

// 先撤权后复制: a revocation issued before the copy must travel with the
// manifest; after switchover neither pool serves the revoked object.
func TestRevokeBeforeCopyCarriesToTarget(t *testing.T) {
	f := newFixture()
	f.src.Write("doc-a", []byte("alpha"), f.auth.Head())
	f.src.Write("doc-b", []byte("beta"), f.auth.Head())

	// Revoke first, then copy.
	f.auth.Revoke("doc-a")
	f.src.ApplyAuth(f.auth, f.auth.Head())

	f.m.Copy(f.m.Snapshot())
	f.m.SyncAuth()
	if err := f.m.Switchover(); err != nil {
		t.Fatalf("switchover failed: %v", err)
	}

	mustErr(t, mustReadErr(f.router.Read(f.dstCred(), "doc-a")), ErrRevoked)
	mustErr(t, mustReadErr(f.src.Read(f.srcCred(), "doc-a")), ErrPoolRetired)

	// Target blocks carry the target pool key generation and policy digest.
	b, err := f.router.Read(f.dstCred(), "doc-b")
	mustRead(t, b, err, "beta")
	if b.KeyGen != f.dst.KeyGen() {
		t.Fatalf("block key gen %d, want target gen %d", b.KeyGen, f.dst.KeyGen())
	}
	if b.PolicyDigest != f.dst.PolicyDigest() {
		t.Fatal("block policy digest does not match target pool")
	}
}

// Paused between copy and switchover, a new revocation must hold the route on
// the source until the target reaches the revocation barrier.
func TestRevokeDuringPausedCopyHoldsRoute(t *testing.T) {
	f := newFixture()
	f.src.Write("doc-a", []byte("alpha"), f.auth.Head())
	f.src.Write("doc-b", []byte("beta"), f.auth.Head())

	// Pause the migration after the content copy completes.
	f.m.Copy(f.m.Snapshot())

	// Revocation lands while paused; the serving source applies it live.
	f.auth.Revoke("doc-a")
	f.src.ApplyAuth(f.auth, f.auth.Head())

	// Content copy is done, but the revocation barrier is not met: no switch.
	mustErr(t, f.m.Switchover(), ErrRevocationBarrier)
	if got := f.router.PoolFor(tenant); got != f.src {
		t.Fatalf("route moved to %s despite revocation barrier miss", got.Name())
	}
	mustErr(t, mustReadErr(f.router.Read(f.srcCred(), "doc-a")), ErrRevoked)

	// Catch the target up, then the switch is allowed and both pools keep
	// the revoked object closed.
	f.m.SyncAuth()
	if err := f.m.Switchover(); err != nil {
		t.Fatalf("switchover after sync failed: %v", err)
	}
	mustErr(t, mustReadErr(f.router.Read(f.dstCred(), "doc-a")), ErrRevoked)
	mustErr(t, mustReadErr(f.src.Read(f.srcCred(), "doc-a")), ErrPoolRetired)
	b, err := f.router.Read(f.dstCred(), "doc-b")
	mustRead(t, b, err, "beta")
}

// A source write after the snapshot raises the content barrier; the route
// must stay on the source until the target re-copies.
func TestContentBarrierUnmetKeepsSource(t *testing.T) {
	f := newFixture()
	f.src.Write("doc-a", []byte("alpha"), f.auth.Head())

	f.m.Copy(f.m.Snapshot())
	f.src.Write("doc-a", []byte("alpha-v2"), f.auth.Head())

	mustErr(t, f.m.Switchover(), ErrContentBarrier)
	if got := f.router.PoolFor(tenant); got != f.src {
		t.Fatalf("route moved to %s despite content barrier miss", got.Name())
	}

	f.m.Copy(f.m.Snapshot())
	if err := f.m.Switchover(); err != nil {
		t.Fatalf("switchover after re-copy failed: %v", err)
	}
	b, err := f.router.Read(f.dstCred(), "doc-a")
	mustRead(t, b, err, "alpha-v2")
}

// A stale copy manifest replayed after a newer revocation must not roll the
// target's revocation watermark back.
func TestStaleManifestDoesNotOverwriteRevocation(t *testing.T) {
	f := newFixture()
	f.src.Write("doc-a", []byte("alpha"), f.auth.Head())

	stale := f.m.Snapshot() // manifest taken before the revocation
	f.m.Copy(stale)

	f.auth.Revoke("doc-a")
	f.src.ApplyAuth(f.auth, f.auth.Head())
	f.m.SyncAuth()
	if got := f.dst.AuthWatermark(); got != 1 {
		t.Fatalf("target auth watermark %d, want 1", got)
	}

	// Late-arriving old manifest: watermark must not regress, revocation
	// must survive.
	f.m.Copy(stale)
	if got := f.dst.AuthWatermark(); got != 1 {
		t.Fatalf("stale manifest regressed target auth watermark to %d", got)
	}
	mustErr(t, mustReadErr(f.dst.Read(f.dstCred(), "doc-a")), ErrRevoked)
}

// A failed key rotation on the target leaves blocks sealed under a stale
// generation: the switch is refused and the route stays on the source.
func TestKeyRotationFailureKeepsSourceRoute(t *testing.T) {
	f := newFixture()
	f.src.Write("doc-a", []byte("alpha"), f.auth.Head())
	f.src.Write("doc-b", []byte("beta"), f.auth.Head())
	f.auth.Revoke("doc-a")
	f.src.ApplyAuth(f.auth, f.auth.Head())

	f.m.Copy(f.m.Snapshot())
	f.m.SyncAuth()

	f.dst.failNextRotation()
	mustErr(t, f.dst.RotateKey(), ErrKeyRotation)

	// Blocks are stale-sealed: no switch, source keeps serving, revoked data
	// stays closed on both pools.
	mustErr(t, f.m.Switchover(), ErrUnsealedBlocks)
	if got := f.router.PoolFor(tenant); got != f.src {
		t.Fatalf("route moved to %s despite unsealed target blocks", got.Name())
	}
	mustErr(t, mustReadErr(f.dst.Read(f.dstCred(), "doc-b")), ErrStaleKeyGeneration)
	mustErr(t, mustReadErr(f.router.Read(f.srcCred(), "doc-a")), ErrRevoked)

	// A successful rotation reseals everything and the switch proceeds.
	if err := f.dst.RotateKey(); err != nil {
		t.Fatalf("second rotation failed: %v", err)
	}
	if err := f.m.Switchover(); err != nil {
		t.Fatalf("switchover after reseal failed: %v", err)
	}
	mustErr(t, mustReadErr(f.router.Read(f.dstCred(), "doc-a")), ErrRevoked)
	b, err := f.router.Read(f.dstCred(), "doc-b")
	mustRead(t, b, err, "beta")
}

// After switchover, requests carrying old-pool credentials are denied
// everywhere, and revoked data stays closed on both pools.
func TestPostSwitchoverOldPoolCredentialsDenied(t *testing.T) {
	f := newFixture()
	f.src.Write("doc-a", []byte("alpha"), f.auth.Head())
	f.src.Write("doc-b", []byte("beta"), f.auth.Head())
	f.auth.Revoke("doc-a")
	f.src.ApplyAuth(f.auth, f.auth.Head())

	f.m.Copy(f.m.Snapshot())
	f.m.SyncAuth()
	if err := f.m.Switchover(); err != nil {
		t.Fatalf("switchover failed: %v", err)
	}

	// Old-pool credential against the retired source directly.
	mustErr(t, mustReadErr(f.src.Read(f.srcCred(), "doc-b")), ErrPoolRetired)
	mustErr(t, mustReadErr(f.src.Read(f.srcCred(), "doc-a")), ErrPoolRetired)

	// Old-pool credential through the router: it now lands on the target,
	// which rejects foreign credentials.
	mustErr(t, mustReadErr(f.router.Read(f.srcCred(), "doc-b")), ErrUnknownCredential)

	// Valid new-pool credential: revoked object stays closed, live object
	// is served.
	mustErr(t, mustReadErr(f.router.Read(f.dstCred(), "doc-a")), ErrRevoked)
	b, err := f.router.Read(f.dstCred(), "doc-b")
	mustRead(t, b, err, "beta")
}

func mustReadErr(_ *Block, err error) error { return err }
