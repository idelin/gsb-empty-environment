package netsec_test

import (
	"bytes"
	"context"
	"testing"

	netsec "example.org/netsec/q0257"
)

// This test only touches exported identifiers, proving the package is
// importable and usable purely through its public API.
func TestImportableCopyOnWriteFlow(t *testing.T) {
	const pageSize = 64
	zeroer := netsec.NewPausableZeroer()
	pool, err := netsec.NewPool(pageSize, 2, zeroer)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer func() {
		zeroer.Resume()
		pool.Close()
	}()
	host := netsec.NewHost(pool)
	ctx := context.Background()

	root := host.NewSnapshot()
	secret := bytes.Repeat([]byte{0xDE}, pageSize)
	if err := host.Grant(ctx, root, netsec.Key("vault"), func(p []byte) {
		copy(p, secret)
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	child, err := host.Fork(root)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	sharedID, err := root.PageID(netsec.Key("vault"))
	if err != nil {
		t.Fatalf("PageID: %v", err)
	}
	childID, err := child.PageID(netsec.Key("vault"))
	if err != nil {
		t.Fatalf("PageID: %v", err)
	}
	if sharedID != childID {
		t.Fatal("forked snapshots must initially share one physical page")
	}

	if err := child.Write(ctx, netsec.Key("vault"), func(p []byte) { p[0] = 0x01 }); err != nil {
		t.Fatalf("Write: %v", err)
	}
	childID, err = child.PageID(netsec.Key("vault"))
	if err != nil {
		t.Fatalf("PageID after write: %v", err)
	}
	if childID == sharedID {
		t.Fatal("write must detach a private copy")
	}

	rootBytes, err := root.Read(netsec.Key("vault"))
	if err != nil {
		t.Fatalf("Read root: %v", err)
	}
	if !bytes.Equal(rootBytes, secret) {
		t.Fatal("root secret was corrupted by the child's write")
	}
	childBytes, err := child.Read(netsec.Key("vault"))
	if err != nil {
		t.Fatalf("Read child: %v", err)
	}
	if childBytes[0] != 0x01 || !bytes.Equal(childBytes[1:], secret[1:]) {
		t.Fatal("child copy does not contain the expected modified secret")
	}

	// Revoking the child cannot touch the root's page; revoking the root
	// queues the final page for zeroing and the pool eventually drains.
	child.Revoke()
	if got, err := root.Read(netsec.Key("vault")); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("root altered after child revoke: %v %v", err, got)
	}
	root.Revoke()

	drainCtx, cancel := context.WithTimeout(ctx, 5_000_000_000)
	defer cancel()
	if err := pool.WaitDrained(drainCtx); err != nil {
		t.Fatalf("WaitDrained: %v", err)
	}
	stats := pool.Stats()
	if stats.Live != 0 || stats.PendingZero != 0 || stats.Quarantined != 0 || stats.Free != stats.Created {
		t.Fatalf("unexpected stats after shutdown: %+v", stats)
	}
}
