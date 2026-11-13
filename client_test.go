package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type testEnv struct {
	client *Client
	server *MemoryServer
	store  *MemoryStore
	cfg    Config
	leaves func() int
}

func newAEAD(t *testing.T) BucketAEAD {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return aead
}

func newTestEnv(t *testing.T, height, capacity, dataSize, stashCapacity int, leafFn func() int) *testEnv {
	t.Helper()
	server, err := NewMemoryServer("tree-test", height)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		TreeID:         "tree-test",
		Height:         height,
		BucketCapacity: capacity,
		BlockDataSize:  dataSize,
		StashCapacity:  stashCapacity,
		AEAD:           newAEAD(t),
		MACKey:         bytes.Repeat([]byte{0x24}, 32),
		RootPublicKey:  server.PublicKey(),
	}
	if leafFn != nil {
		cfg.SelectLeaf = func(int) (int, error) { return leafFn(), nil }
	}
	store := NewMemoryStore()
	client, err := NewClient(cfg, server, store)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{client: client, server: server, store: store, cfg: cfg, leaves: leafFn}
}

func dataFor(id BlockID, version uint64, size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(uint64(id)*131 + version*17 + uint64(i))
	}
	return out
}

func TestBasicReadWriteAndVersionChain(t *testing.T) {
	leaf := 0
	leafStep := func() int { leaf = (leaf + 1) % 16; return leaf }
	env := newTestEnv(t, 4, 4, 32, 128, leafStep)

	for id := BlockID(1); id <= 20; id++ {
		block, err := env.client.Write(id, dataFor(id, 1, 32))
		if err != nil {
			t.Fatalf("write %d: %v", id, err)
		}
		if block.Version != 1 {
			t.Fatalf("id %d version = %d, want 1", id, block.Version)
		}
	}
	for id := BlockID(1); id <= 20; id++ {
		block, err := env.client.Read(id)
		if err != nil {
			t.Fatalf("read %d: %v", id, err)
		}
		if block.Version != 1 || !bytes.Equal(block.Data, dataFor(id, 1, 32)) {
			t.Fatalf("id %d mismatch: v%d data %x", id, block.Version, block.Data)
		}
	}

	for version := uint64(2); version <= 6; version++ {
		block, err := env.client.Write(7, dataFor(7, version, 32))
		if err != nil {
			t.Fatalf("write version %d: %v", version, err)
		}
		if block.Version != version {
			t.Fatalf("version = %d, want %d", block.Version, version)
		}
		got, err := env.client.Read(7)
		if err != nil {
			t.Fatalf("read version %d: %v", version, err)
		}
		if got.Version != version || !bytes.Equal(got.Data, dataFor(7, version, 32)) {
			t.Fatalf("read version %d got %d/%x", version, got.Version, got.Data)
		}
	}
	if env.server.ServerTxn() != env.clientTxn(t) {
		t.Fatalf("server tx %d != client tx %d", env.server.ServerTxn(), env.clientTxn(t))
	}
}

func (e *testEnv) clientTxn(t *testing.T) uint64 {
	t.Helper()
	snap, err := e.client.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snap.Txn
}

func TestWriteNotFoundAndReadNotFound(t *testing.T) {
	env := newTestEnv(t, 2, 2, 16, 32, func() int { return 0 })
	if _, err := env.client.Read(99); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("read missing err = %v", err)
	}
	snap, _ := env.client.Snapshot()
	if snap.Txn != 0 || len(snap.Positions) != 0 {
		t.Fatalf("missing read changed state: %+v", snap)
	}
}

func TestStashOverflowAbortsWithoutCommit(t *testing.T) {
	env := newTestEnv(t, 2, 1, 16, 1, func() int { return 0 })
	if _, err := env.client.Write(1, dataFor(1, 1, 16)); err != nil {
		t.Fatal(err)
	}
	before, _ := env.client.Snapshot()
	if before.Txn != 1 {
		t.Fatalf("tx %d", before.Txn)
	}

	_, err := env.client.Write(2, dataFor(2, 1, 16))
	if !errors.Is(err, ErrStashOverflow) {
		t.Fatalf("overflow err = %v", err)
	}
	after, _ := env.client.Snapshot()
	if after.Txn != before.Txn {
		t.Fatalf("transaction committed: tx %d -> %d", before.Txn, after.Txn)
	}
	if !reflect.DeepEqual(after.Positions, before.Positions) {
		t.Fatalf("positions changed: %v -> %v", before.Positions, after.Positions)
	}
	if !reflect.DeepEqual(after.Stash, before.Stash) || after.Root != before.Root {
		t.Fatalf("stash/root changed on overflow")
	}
	if after.Pending {
		t.Fatalf("overflow left a pending journal")
	}
	if env.server.ServerTxn() != 1 {
		t.Fatalf("server tx advanced to %d", env.server.ServerTxn())
	}

	if _, err := env.client.Read(1); err != nil {
		t.Fatalf("client unusable after aborted overflow: %v", err)
	}
}

func TestMergePathBlocksVersionRules(t *testing.T) {
	const size = 8
	mk := func(id BlockID, version uint64, salt byte) Block {
		data := bytes.Repeat([]byte{salt}, size)
		return Block{ID: id, Version: version, Data: data, MAC: blockMAC([]byte("0123456789abcdef"), "t", id, version, data)}
	}

	t.Run("same version same content", func(t *testing.T) {
		a := mk(1, 3, 'a')
		stash := map[BlockID]Block{}
		if err := mergePathBlocks(stash, []Block{a, a.clone()}); err != nil {
			t.Fatal(err)
		}
		if stash[1].Version != 3 {
			t.Fatalf("version %d", stash[1].Version)
		}
	})
	t.Run("same version different content", func(t *testing.T) {
		stash := map[BlockID]Block{}
		if err := mergePathBlocks(stash, []Block{mk(1, 2, 'a'), mk(1, 2, 'b')}); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("stash equal version different content", func(t *testing.T) {
		stash := map[BlockID]Block{1: mk(1, 2, 'a')}
		if err := mergePathBlocks(stash, []Block{mk(1, 2, 'b')}); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("next consecutive version", func(t *testing.T) {
		stash := map[BlockID]Block{1: mk(1, 1, 'a')}
		if err := mergePathBlocks(stash, []Block{mk(1, 2, 'b')}); err != nil {
			t.Fatal(err)
		}
		if stash[1].Version != 2 {
			t.Fatalf("version %d", stash[1].Version)
		}
	})
	t.Run("missing intermediate version", func(t *testing.T) {
		stash := map[BlockID]Block{1: mk(1, 1, 'a')}
		if err := mergePathBlocks(stash, []Block{mk(1, 3, 'c')}); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("complete intermediate chain", func(t *testing.T) {
		stash := map[BlockID]Block{1: mk(1, 1, 'a')}
		if err := mergePathBlocks(stash, []Block{mk(1, 2, 'b'), mk(1, 3, 'c')}); err != nil {
			t.Fatal(err)
		}
		if stash[1].Version != 3 {
			t.Fatalf("version %d", stash[1].Version)
		}
	})
	t.Run("older path copy is stale", func(t *testing.T) {
		stash := map[BlockID]Block{1: mk(1, 5, 'e')}
		if err := mergePathBlocks(stash, []Block{mk(1, 2, 'b')}); err != nil {
			t.Fatal(err)
		}
		if stash[1].Version != 5 {
			t.Fatalf("version %d", stash[1].Version)
		}
	})
}

func pathOf(t *testing.T, height, leaf int) (rootToLeaf, leafToRoot []int) {
	t.Helper()
	g := newTreeGeometry(height)
	rootToLeaf, err := g.pathRootToLeaf(leaf)
	if err != nil {
		t.Fatal(err)
	}
	leafToRoot, err = g.pathLeafToRoot(leaf)
	if err != nil {
		t.Fatal(err)
	}
	return rootToLeaf, leafToRoot
}

func TestReadFailureLeavesIntentThenReplays(t *testing.T) {
	env := newTestEnv(t, 3, 3, 16, 64, func() int { return 3 })
	if _, err := env.client.Write(1, dataFor(1, 1, 16)); err != nil {
		t.Fatal(err)
	}
	snap, _ := env.client.Snapshot()
	_, leafToRoot := pathOf(t, 3, snap.Positions[1])
	node := leafToRoot[len(leafToRoot)-1]
	env.server.SetFaults(ServerFaults{FailReadNode: &node})
	before, _ := env.client.Snapshot()
	if _, err := env.client.Read(1); err == nil {
		t.Fatal("read succeeded under injected failure")
	}
	mid, _ := env.client.Snapshot()
	if mid.Txn != before.Txn || !mid.Pending || mid.Intent == nil {
		t.Fatalf("failure did not preserve intent journal: %+v", mid)
	}

	env.server.SetFaults(ServerFaults{})
	if err := env.client.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	after, _ := env.client.Snapshot()
	if after.Txn != before.Txn+1 || after.Pending {
		t.Fatalf("recovery state: tx %d pending %v", after.Txn, after.Pending)
	}
	block, err := env.client.Read(1)
	if err != nil || block.Version != 1 {
		t.Fatalf("read after recovery: %v %+v", err, block)
	}
}

func TestStageAndCommitFailuresRecoverWithSameLeaves(t *testing.T) {
	for _, faultCase := range []struct {
		name  string
		setup func(*MemoryServer, int)
	}{
		{"stage", func(s *MemoryServer, node int) { s.SetFaults(ServerFaults{FailStageNode: &node}) }},
		{"commit", func(s *MemoryServer, node int) { s.SetFaults(ServerFaults{FailCommit: true}) }},
	} {
		t.Run(faultCase.name, func(t *testing.T) {
			draws := 0
			env := newTestEnv(t, 3, 3, 16, 64, func() int {
				draws++
				return (draws * 5) % 8
			})
			if _, err := env.client.Write(10, dataFor(10, 1, 16)); err != nil {
				t.Fatal(err)
			}
			before, _ := env.client.Snapshot()
			intentTx := before.Txn + 1
			_, leafToRoot := pathOf(t, 3, before.Positions[10])
			faultCase.setup(env.server, leafToRoot[0])
			if _, err := env.client.Write(10, dataFor(10, 2, 16)); err == nil {
				t.Fatal("write succeeded under injected failure")
			}
			mid, _ := env.client.Snapshot()
			if mid.Txn != before.Txn || mid.Intent == nil || mid.Prepared == nil {
				t.Fatalf("expected prepared journal: %+v", mid)
			}
			if mid.Intent.Tx != intentTx {
				t.Fatalf("intent tx %d", mid.Intent.Tx)
			}
			drawsAtFailure := draws
			env.server.SetFaults(ServerFaults{})
			if err := env.client.Recover(); err != nil {
				t.Fatalf("recover: %v", err)
			}
			if draws != drawsAtFailure {
				t.Fatalf("recovery drew a new leaf (%d -> %d)", drawsAtFailure, draws)
			}
			after, _ := env.client.Snapshot()
			if after.Txn != intentTx || after.Pending {
				t.Fatalf("recovered tx %d pending %v", after.Txn, after.Pending)
			}
			if after.Positions[10] != mid.Intent.NewLeaf {
				t.Fatalf("position %d != pre-drawn leaf %d", after.Positions[10], mid.Intent.NewLeaf)
			}
			commits := env.server.CommitLog()
			if commits[len(commits)-1].Leaf != mid.Intent.OldLeaf {
				t.Fatalf("server commit leaf %d != old leaf %d", commits[len(commits)-1].Leaf, mid.Intent.OldLeaf)
			}
			got, err := env.client.Read(10)
			if err != nil || got.Version != 2 {
				t.Fatalf("read back: %v v%d", err, got.Version)
			}
		})
	}
}

func TestStoreFailurePoints(t *testing.T) {
	type failingStore struct {
		inner  *MemoryStore
		failAt int
		calls  int
	}
	loadFn := func(f *failingStore) (*PersistentState, error) { return f.inner.Load() }
	commitFn := func(f *failingStore, state *PersistentState) error {
		f.calls++
		if f.calls == f.failAt {
			return errors.New("injected persistence failure")
		}
		return f.inner.Commit(state)
	}

	for _, tc := range []struct {
		name            string
		failAt          int
		serverMayApply  bool
		txnAfterRecover uint64
	}{
		{"intent", 2, false, 0},
		{"prepared", 3, false, 1},
		{"final", 4, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, err := NewMemoryServer("store-test", 3)
			if err != nil {
				t.Fatal(err)
			}
			inner := NewMemoryStore()
			fs := &failingStore{inner: inner, failAt: tc.failAt}
			store := &interfaceStore{
				load:   func() (*PersistentState, error) { return loadFn(fs) },
				commit: func(s *PersistentState) error { return commitFn(fs, s) },
			}
			cfg := Config{
				TreeID:         "store-test",
				Height:         3,
				BucketCapacity: 3,
				BlockDataSize:  8,
				StashCapacity:  64,
				AEAD:           newAEAD(t),
				MACKey:         bytes.Repeat([]byte{1}, 32),
				RootPublicKey:  server.PublicKey(),
				SelectLeaf:     func(int) (int, error) { return 2, nil },
			}
			client, err := NewClient(cfg, server, store)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Write(1, dataFor(1, 1, 8))
			if err == nil {
				t.Fatal("write succeeded despite persistence failure")
			}
			before, _ := client.Snapshot()
			if before.Txn != 0 {
				t.Fatalf("state committed at tx %d", before.Txn)
			}
			if tc.serverMayApply {
				if server.ServerTxn() != 1 {
					t.Fatalf("expected server commit before final persistence failure, tx=%d", server.ServerTxn())
				}
			} else if server.ServerTxn() != 0 {
				t.Fatalf("server advanced to tx %d", server.ServerTxn())
			}

			if err := client.Recover(); err != nil {
				t.Fatalf("recover after %s persistence failure: %v", tc.name, err)
			}
			after, _ := client.Snapshot()
			if after.Txn != tc.txnAfterRecover {
				t.Fatalf("tx after recovery = %d, want %d", after.Txn, tc.txnAfterRecover)
			}
			if _, err := client.Write(2, dataFor(2, 1, 8)); err != nil {
				// The failingStore's one-shot failure has been consumed.
				t.Fatalf("client unusable after failure: %v", err)
			}
		})
	}
}

type interfaceStore struct {
	load   func() (*PersistentState, error)
	commit func(*PersistentState) error
}

func (s *interfaceStore) Load() (*PersistentState, error) { return s.load() }
func (s *interfaceStore) Commit(state *PersistentState) error {
	return s.commit(state)
}

func TestRollbackIsNeverAccepted(t *testing.T) {
	env := newTestEnv(t, 3, 3, 16, 64, func() int { return 1 })
	for id := BlockID(1); id <= 4; id++ {
		if _, err := env.client.Write(id, dataFor(id, 1, 16)); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := env.client.Snapshot()
	if before.Txn != 4 {
		t.Fatalf("tx %d", before.Txn)
	}
	if err := env.server.RollbackTo(2); err != nil {
		t.Fatal(err)
	}
	if _, err := env.client.Read(1); !errors.Is(err, ErrRollback) {
		t.Fatalf("read after rollback err = %v", err)
	}
	if _, err := env.client.Write(5, dataFor(5, 1, 16)); !errors.Is(err, ErrRollback) {
		t.Fatalf("write after rollback err = %v", err)
	}
	after, _ := env.client.Snapshot()
	if after.Txn != before.Txn || after.Pending {
		t.Fatalf("state changed after rejected rollback: %+v", after)
	}
	if _, err := NewClient(env.cfg, env.server, env.store); !errors.Is(err, ErrRollback) {
		t.Fatalf("new client accepted rollback: %v", err)
	}
}

func TestDeletedBucketIsRejected(t *testing.T) {
	env := newTestEnv(t, 3, 3, 16, 64, func() int { return 2 })
	if _, err := env.client.Write(1, dataFor(1, 1, 16)); err != nil {
		t.Fatal(err)
	}
	snap, _ := env.client.Snapshot()
	rootToLeaf, _ := pathOf(t, 3, snap.Positions[1])
	if err := env.server.DeleteBucket(rootToLeaf[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := env.client.Read(1); !errors.Is(err, ErrServerStateMismatch) {
		t.Fatalf("read after bucket deletion err = %v", err)
	}
	if _, err := env.server.ReadBucket(snap.Txn+1, rootToLeaf[1]); !errors.Is(err, ErrMissingBucket) {
		t.Fatalf("direct bucket read err = %v", err)
	}
	post, _ := env.client.Snapshot()
	if post.Txn != snap.Txn || post.Root != snap.Root {
		t.Fatalf("state changed after rejected deletion")
	}
}

func TestDroppedWriteBack(t *testing.T) {
	t.Run("returns signed forked receipt", func(t *testing.T) {
		env := newTestEnv(t, 3, 3, 16, 64, func() int { return 2 })
		if _, err := env.client.Write(1, dataFor(1, 1, 16)); err != nil {
			t.Fatal(err)
		}
		before, _ := env.client.Snapshot()
		_, leafToRoot := pathOf(t, 3, before.Positions[1])
		drop := leafToRoot[0]
		env.server.SetFaults(ServerFaults{DropWriteNode: &drop, DropWriteReturnsReceipt: true})
		if _, err := env.client.Write(1, dataFor(1, 2, 16)); !errors.Is(err, ErrServerStateMismatch) {
			t.Fatalf("write err = %v", err)
		}
		mid, _ := env.client.Snapshot()
		if mid.Txn != before.Txn || mid.Prepared == nil {
			t.Fatalf("committed a forked transaction: tx %d prepared %v", mid.Txn, mid.Prepared != nil)
		}
		if env.server.ServerTxn() != before.Txn+1 {
			t.Fatalf("server tx %d", env.server.ServerTxn())
		}
		env.server.SetFaults(ServerFaults{})
		if err := env.client.Recover(); !errors.Is(err, ErrServerStateMismatch) {
			t.Fatalf("recovery accepted forked tree: %v", err)
		}
		post, _ := env.client.Snapshot()
		if post.Txn != before.Txn {
			t.Fatalf("forked transaction committed locally: tx %d", post.Txn)
		}
	})

	t.Run("returns error before applying", func(t *testing.T) {
		env := newTestEnv(t, 3, 3, 16, 64, func() int { return 2 })
		if _, err := env.client.Write(1, dataFor(1, 1, 16)); err != nil {
			t.Fatal(err)
		}
		before, _ := env.client.Snapshot()
		_, leafToRoot := pathOf(t, 3, before.Positions[1])
		drop := leafToRoot[0]
		env.server.SetFaults(ServerFaults{DropWriteNode: &drop})
		if _, err := env.client.Write(1, dataFor(1, 2, 16)); !errors.Is(err, ErrDroppedWrite) {
			t.Fatalf("write err = %v", err)
		}
		if env.server.ServerTxn() != before.Txn {
			t.Fatalf("server tx advanced to %d", env.server.ServerTxn())
		}
		env.server.SetFaults(ServerFaults{})
		if err := env.client.Recover(); err != nil {
			t.Fatalf("recover: %v", err)
		}
		got, err := env.client.Read(1)
		if err != nil || got.Version != 2 {
			t.Fatalf("read after recovery: %v v%d", err, got.Version)
		}
	})
}

func TestReplayedReceiptIsRejectedThenRecovered(t *testing.T) {
	env := newTestEnv(t, 3, 3, 16, 64, func() int { return 2 })
	if _, err := env.client.Write(1, dataFor(1, 1, 16)); err != nil {
		t.Fatal(err)
	}
	before, _ := env.client.Snapshot()
	oldTx := uint64(0)
	env.server.SetFaults(ServerFaults{ReplayReceiptTx: &oldTx})
	if _, err := env.client.Write(1, dataFor(1, 2, 16)); !errors.Is(err, ErrReceipt) {
		t.Fatalf("write err = %v", err)
	}
	mid, _ := env.client.Snapshot()
	if mid.Txn != before.Txn || mid.Prepared == nil {
		t.Fatalf("committed on replayed receipt: %+v", mid)
	}
	if env.server.ServerTxn() != before.Txn+1 {
		t.Fatalf("server failed to commit under replay fault")
	}
	env.server.SetFaults(ServerFaults{})
	if err := env.client.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if _, err := env.client.Read(1); err != nil {
		t.Fatal(err)
	}
}

type tamperingReceiptHost struct {
	Host
	tamper bool
}

func (h *tamperingReceiptHost) CommitPath(tx uint64, leaf int, nodes []int) (RootReceipt, error) {
	receipt, err := h.Host.CommitPath(tx, leaf, nodes)
	if err == nil && h.tamper {
		receipt.Signature[0] ^= 0x40
	}
	return receipt, err
}

func TestForgedReceiptSignatureIsRejected(t *testing.T) {
	server, err := NewMemoryServer("sig-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	host := &tamperingReceiptHost{Host: server, tamper: true}
	store := NewMemoryStore()
	cfg := Config{
		TreeID:         "sig-test",
		Height:         2,
		BucketCapacity: 2,
		BlockDataSize:  8,
		StashCapacity:  32,
		AEAD:           newAEAD(t),
		MACKey:         bytes.Repeat([]byte{7}, 32),
		RootPublicKey:  server.PublicKey(),
		SelectLeaf:     func(int) (int, error) { return 0, nil },
	}
	client, err := NewClient(cfg, host, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(1, dataFor(1, 1, 8)); !errors.Is(err, ErrReceipt) {
		t.Fatalf("forged receipt err = %v", err)
	}
	mid, _ := client.Snapshot()
	if mid.Txn != 0 || mid.Prepared == nil {
		t.Fatalf("forged receipt committed state: %+v", mid)
	}
	host.tamper = false
	if err := client.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	after, _ := client.Snapshot()
	if after.Txn != 1 {
		t.Fatalf("recovered tx %d", after.Txn)
	}
}

func TestRootAndInitializeFailures(t *testing.T) {
	server, err := NewMemoryServer("root-test", 2)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	cfg := Config{
		TreeID:         "root-test",
		Height:         2,
		BucketCapacity: 2,
		BlockDataSize:  8,
		StashCapacity:  32,
		AEAD:           newAEAD(t),
		MACKey:         bytes.Repeat([]byte{3}, 32),
		RootPublicKey:  server.PublicKey(),
		SelectLeaf:     func(int) (int, error) { return 0, nil },
	}
	server.SetFaults(ServerFaults{FailInitialize: true})
	if _, err := NewClient(cfg, server, store); err == nil {
		t.Fatal("initialization failure was ignored")
	}
	server.SetFaults(ServerFaults{})
	client, err := NewClient(cfg, server, store)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := client.Snapshot()
	server.SetFaults(ServerFaults{FailRoot: true})
	if _, err := client.Read(1); err == nil {
		t.Fatal("root failure was ignored")
	}
	after, _ := client.Snapshot()
	if after.Txn != before.Txn || after.Pending {
		t.Fatalf("root failure changed state")
	}
	server.SetFaults(ServerFaults{})
}

func TestSuccessfulPathsUseOnlyPreDrawnLeaves(t *testing.T) {
	env := newTestEnv(t, 4, 3, 16, 128, func() int { return 6 })
	for tx := uint64(1); tx <= 6; tx++ {
		if _, err := env.client.Write(BlockID(tx), dataFor(BlockID(tx), 1, 16)); err != nil {
			t.Fatal(err)
		}
	}
	history := env.store.History()
	for tx := uint64(1); tx <= 6; tx++ {
		var intent *Intent
		for _, state := range history {
			if state.Intent != nil && state.Intent.Tx == tx {
				intent = state.Intent
				break
			}
		}
		if intent == nil {
			t.Fatalf("no persisted intent for tx %d", tx)
		}
		rootToLeaf, leafToRoot := pathOf(t, 4, intent.OldLeaf)
		for _, record := range env.server.AccessLog() {
			if record.Tx != tx {
				continue
			}
			switch record.Kind {
			case "read":
				found := false
				for _, node := range rootToLeaf {
					if node == record.Node {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("tx %d read node %d not on pre-drawn old path %v", tx, record.Node, rootToLeaf)
				}
			case "commit":
				if record.Leaf != intent.OldLeaf || !equalIntSlices(record.Nodes, leafToRoot) {
					t.Fatalf("tx %d commit path %d %v, want old leaf %d %v", tx, record.Leaf, record.Nodes, intent.OldLeaf, leafToRoot)
				}
			}
		}
		readCount := 0
		commitCount := 0
		for _, record := range env.server.AccessLog() {
			if record.Tx == tx {
				if record.Kind == "read" {
					readCount++
				} else if record.Kind == "commit" {
					commitCount++
				}
			}
		}
		if readCount != len(rootToLeaf) || commitCount != 1 {
			t.Fatalf("tx %d read/commit count %d/%d", tx, readCount, commitCount)
		}
		state := env.store.State()
		if state.Positions[intent.BlockID] != intent.NewLeaf {
			t.Fatalf("tx %d committed position %d, pre-drawn new leaf %d", tx, state.Positions[intent.BlockID], intent.NewLeaf)
		}
	}
}

func TestWrongDataLengthAndUnknownBlocks(t *testing.T) {
	env := newTestEnv(t, 2, 2, 8, 16, func() int { return 0 })
	if _, err := env.client.Write(1, []byte{1, 2, 3}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("short data err = %v", err)
	}
	if _, err := env.client.Write(1, make([]byte, 9)); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("long data err = %v", err)
	}
	if _, err := env.client.Read(12345); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("unknown read err = %v", err)
	}
}

func TestBucketAuthenticationBindsHeaderAndCiphertext(t *testing.T) {
	cfg := Config{
		TreeID:         "ad-test",
		Height:         3,
		BucketCapacity: 2,
		BlockDataSize:  8,
		AEAD:           newAEAD(t),
		MACKey:         bytes.Repeat([]byte{9}, 32),
		RootPublicKey:  make([]byte, 32),
	}
	id := BlockID(5)
	data := []byte("12345678")
	block := Block{ID: id, Version: 1, Data: data}
	block.MAC = blockMAC(cfg.MACKey, cfg.TreeID, id, 1, data)
	bucket, err := sealBucket(cfg, 4, 2, 7, []Block{block})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openBucket(cfg, bucket); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !bytes.Contains(BucketAdditionalData(cfg.TreeID, 4, 2, 7), []byte(cfg.TreeID)) {
		t.Fatal("additional data does not contain tree ID")
	}

	forged := bucket.clone()
	forged.Node = 5
	if _, err := openBucket(cfg, forged); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("node tamper err = %v", err)
	}
	forged = bucket.clone()
	forged.Generation++
	if _, err := openBucket(cfg, forged); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("generation tamper err = %v", err)
	}
	forged = bucket.clone()
	forged.Txn++
	if _, err := openBucket(cfg, forged); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("transaction tamper err = %v", err)
	}
	forged = bucket.clone()
	forged.Ciphertext[0] ^= 0x80
	if _, err := openBucket(cfg, forged); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ciphertext tamper err = %v", err)
	}
}

func TestRollbackToEveryHistoricalTreeIsRejected(t *testing.T) {
	env := newTestEnv(t, 3, 2, 8, 64, func() int { return 1 })
	for tx := uint64(1); tx <= 5; tx++ {
		if _, err := env.client.Write(BlockID(tx), dataFor(BlockID(tx), 1, 8)); err != nil {
			t.Fatal(err)
		}
	}
	committed, _ := env.client.Snapshot()
	for _, tx := range env.server.HistoryTransactions() {
		if tx == committed.Txn {
			continue
		}
		if err := env.server.RollbackTo(tx); err != nil {
			t.Fatal(err)
		}
		if _, err := env.client.Read(1); !errors.Is(err, ErrRollback) {
			t.Fatalf("rollback to tx %d accepted: %v", tx, err)
		}
		post, _ := env.client.Snapshot()
		if post.Txn != committed.Txn {
			t.Fatalf("rollback to tx %d changed client state", tx)
		}
	}
}

func TestManySameBlockAccesses(t *testing.T) {
	env := newTestEnv(t, 5, 8, 24, 256, func() int { return 11 })
	for version := uint64(1); version <= 40; version++ {
		written, err := env.client.Write(9, dataFor(9, version, 24))
		if err != nil {
			t.Fatalf("write %d: %v", version, err)
		}
		if written.Version != version {
			t.Fatalf("version %d != %d", written.Version, version)
		}
		if version%3 == 0 {
			read, err := env.client.Read(9)
			if err != nil {
				t.Fatalf("read %d: %v", version, err)
			}
			if read.Version != version || !bytes.Equal(read.Data, dataFor(9, version, 24)) {
				t.Fatalf("read version %d got %d", version, read.Version)
			}
		}
	}
	snap, _ := env.client.Snapshot()
	if snap.Txn != 40+40/3 {
		t.Fatalf("tx %d", snap.Txn)
	}
}

func TestNoDataDeliveredOnFailure(t *testing.T) {
	env := newTestEnv(t, 3, 3, 16, 64, func() int { return 1 })
	if _, err := env.client.Write(1, dataFor(1, 1, 16)); err != nil {
		t.Fatal(err)
	}
	snap, _ := env.client.Snapshot()
	_, leafToRoot := pathOf(t, 3, snap.Positions[1])
	node := leafToRoot[0]
	env.server.SetFaults(ServerFaults{FailReadNode: &node})
	data, err := env.client.Read(1)
	if err == nil {
		t.Fatal("expected failure")
	}
	if len(data.Data) != 0 || data.Version != 0 {
		t.Fatalf("failure delivered block: %+v", data)
	}
}

func TestRandomizedReadWriteWorkload(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	env := newTestEnv(t, 6, 4, 16, 512, func() int { return rng.Intn(64) })
	type want struct {
		version uint64
		data    []byte
	}
	expected := make(map[BlockID]want)
	var txn uint64
	for step := 0; step < 800; step++ {
		id := BlockID(1 + rng.Intn(120))
		if step%2 == 0 {
			version := expected[id].version + 1
			data := dataFor(id, version, 16)
			block, err := env.client.Write(id, data)
			if err != nil {
				t.Fatalf("step %d write %d: %v", step, id, err)
			}
			if block.Version != version {
				t.Fatalf("step %d write %d version %d, want %d", step, id, block.Version, version)
			}
			expected[id] = want{version: version, data: data}
			txn++
			continue
		}
		current, exists := expected[id]
		block, err := env.client.Read(id)
		if !exists {
			if !errors.Is(err, ErrBlockNotFound) {
				t.Fatalf("step %d read missing %d: %v", step, id, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("step %d read %d: %v", step, id, err)
		}
		if block.Version != current.version || !bytes.Equal(block.Data, current.data) {
			t.Fatalf("step %d read %d got v%d, want v%d", step, id, block.Version, current.version)
		}
		txn++
	}
	snap, err := env.client.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Txn != txn {
		t.Fatalf("client tx %d, want %d", snap.Txn, txn)
	}
	if env.server.ServerTxn() != txn {
		t.Fatalf("server tx %d, want %d", env.server.ServerTxn(), txn)
	}
}

func ExampleConfig() {
	fmt.Println("see TestBasicReadWriteAndVersionChain for a complete setup")
	// Output: see TestBasicReadWriteAndVersionChain for a complete setup
}
