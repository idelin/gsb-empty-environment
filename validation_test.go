package netsec

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestRejectsCycle(t *testing.T) {
	// a -> b -> a, detected purely by the DFS coloring (no id-order rule).
	a := sizedBlock(1, 4, Reference(2), Literal([]byte("aa")))
	b := sizedBlock(2, 4, Reference(1), Literal([]byte("bb")))
	store := storeOf(a, b)

	_, err := Open(context.Background(), store, 1, Limits{})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("open error = %v, want ErrCycle", err)
	}
}

func TestRejectsSelfCycle(t *testing.T) {
	blk := sizedBlock(1, 4, Reference(1), Literal([]byte("abcd")))
	_, err := Open(context.Background(), storeOf(blk), 1, Limits{})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("open error = %v, want ErrCycle", err)
	}
}

func TestRejectsOverlongDeclaration(t *testing.T) {
	// The leaf is only 3 bytes but block 2 declares a larger logical size.
	sizes := map[int64]int64{}
	leaf := block(1, sizes, Literal([]byte("abc")))
	cheat := sizedBlock(2, 999, Literal([]byte(">>")), Reference(1))
	root := block(3, sizes, Reference(2))
	_, err := Open(context.Background(), storeOf(leaf, cheat, root), 3, Limits{})
	if !errors.Is(err, ErrOverlongDecl) {
		t.Fatalf("open error = %v, want ErrOverlongDecl", err)
	}
}

func TestRejectsUnderlongDeclaration(t *testing.T) {
	sizes := map[int64]int64{}
	leaf := block(1, sizes, Literal([]byte("abc")))
	cheat := sizedBlock(2, 1, Reference(1))
	_, err := Open(context.Background(), storeOf(leaf, cheat), 2, Limits{})
	if !errors.Is(err, ErrOverlongDecl) {
		t.Fatalf("open error = %v, want ErrOverlongDecl", err)
	}
}

func TestRejectsUnknownReference(t *testing.T) {
	sizes := map[int64]int64{}
	root := block(1, sizes, Literal([]byte("x")), Reference(42))
	_, err := Open(context.Background(), storeOf(root), 1, Limits{})
	if !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("open error = %v, want ErrUnknownBlock", err)
	}
}

func TestRejectsDepthLimit(t *testing.T) {
	// Chain 1 -> 2 -> 3 -> 4 with root at depth 1.
	sizes := map[int64]int64{}
	b4 := block(4, sizes, Literal([]byte("d")))
	b3 := block(3, sizes, Reference(4), Literal([]byte("c")))
	b2 := block(2, sizes, Reference(3), Literal([]byte("b")))
	b1 := block(1, sizes, Reference(2), Literal([]byte("a")))
	_, err := Open(context.Background(), storeOf(b1, b2, b3, b4), 1, Limits{MaxDepth: 3})
	if !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("open error = %v, want ErrDepthLimit", err)
	}
}

func TestRejectsBlockLimit(t *testing.T) {
	sizes := map[int64]int64{}
	b2 := block(2, sizes, Literal([]byte("b")))
	b3 := block(3, sizes, Literal([]byte("c")))
	b1 := block(1, sizes, Reference(2), Reference(3), Literal([]byte("a")))
	_, err := Open(context.Background(), storeOf(b1, b2, b3), 1, Limits{MaxBlocks: 2})
	if !errors.Is(err, ErrBlockLimit) {
		t.Fatalf("open error = %v, want ErrBlockLimit", err)
	}
}

func TestRejectsCacheLimitAtValidation(t *testing.T) {
	sizes := map[int64]int64{}
	leaf := block(1, sizes, Literal([]byte("0123456789")))
	root := block(2, sizes, Reference(1))
	_, err := Open(context.Background(), storeOf(leaf, root), 2, Limits{MaxCacheBytes: 4})
	if !errors.Is(err, ErrCacheLimit) {
		t.Fatalf("open error = %v, want ErrCacheLimit", err)
	}
}

// countingStore records how many times each block is fetched, proving that
// blocks are pulled on demand and that cached blocks are not re-fetched.
type countingStore struct {
	inner MapStore
	mu    sync.Mutex
	calls map[int64]int
}

func (s *countingStore) Get(_ context.Context, id int64) (*Block, error) {
	s.mu.Lock()
	s.calls[id]++
	s.mu.Unlock()
	return s.inner[id], nil
}

func TestOnDemandFetchAndNoRefetch(t *testing.T) {
	sizes := map[int64]int64{}
	leaf := block(1, sizes, Literal([]byte("LEAF")))
	root := block(2, sizes, Reference(1), Literal([]byte("|")), Reference(1))
	store := &countingStore{inner: storeOf(leaf, root), calls: map[int64]int{}}

	r, err := Open(context.Background(), store, 2, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	if got := readAll(t, r); got != "LEAF|LEAF" {
		t.Fatalf("output = %q, want %q", got, "LEAF|LEAF")
	}
	// Validation touches both blocks once; decoding fetches the root once more
	// and the leaf once (the second reuse is served from the cache).
	if store.calls[1] != 2 {
		t.Fatalf("leaf fetches = %d, want 2 (validate + first decode)", store.calls[1])
	}
	if acct := r.Account(); acct.CacheHits != 1 || acct.Expansions != 2 {
		t.Fatalf("account = %+v, want 1 cache hit and 2 expansions", acct)
	}
}

func TestCancelThenReenterMatchesUncancelledRead(t *testing.T) {
	store, root := diamondStore()

	// Baseline: the complete logical output and final resource bill.
	base, err := Open(context.Background(), store, root, Limits{})
	if err != nil {
		t.Fatalf("open baseline: %v", err)
	}
	baseWant := readAll(t, base)
	baseAcct := base.Account()
	base.Close()

	ctx, cancel := context.WithCancel(context.Background())
	r, err := Open(ctx, store, root, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	// Cancel deterministically right after the first expansion is charged,
	// before the referenced block is resolved.
	var once sync.Once
	r.testHook = func(int64) {
		once.Do(cancel)
	}

	prefix := make([]byte, 4)
	n, err := r.Read(prefix)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error = %v, want context.Canceled", err)
	}
	if n != 0 {
		t.Fatalf("cancelled read returned %d bytes, want 0 (cancel before resolve)", n)
	}

	// The rolled-back expansion must not be left on the meter.
	if r.Account().Expansions != 0 {
		t.Fatalf("expansions after cancel = %d, want 0", r.Account().Expansions)
	}
	if r.Account().OutputBytes != 0 {
		t.Fatalf("output bytes after cancel = %d, want 0", r.Account().OutputBytes)
	}

	// Re-enter with a live context and consume the whole stream.
	r.testHook = nil
	var sb strings.Builder
	p := make([]byte, 3)
	for {
		got, readErr := r.ReadContext(context.Background(), p)
		sb.Write(p[:got])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatalf("re-enter read: %v", readErr)
		}
	}

	if sb.String() != baseWant {
		t.Fatalf("re-entered output = %q, want %q", sb.String(), baseWant)
	}
	gotAcct := r.Account()
	if gotAcct.OutputBytes != baseAcct.OutputBytes || gotAcct.Expansions != baseAcct.Expansions {
		t.Fatalf("re-entered bill = %+v, want output=%d expansions=%d",
			gotAcct, baseAcct.OutputBytes, baseAcct.Expansions)
	}
}

func TestCancelAfterSomeBytesKeepsPrefix(t *testing.T) {
	store, root := diamondStore()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := Open(ctx, store, root, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	// Read a real prefix first; cancellation only affects subsequent reads.
	head := make([]byte, 5)
	if _, err := r.Read(head); err != nil {
		t.Fatalf("prefix read: %v", err)
	}
	if string(head) != diamondWant[:5] {
		t.Fatalf("prefix = %q, want %q", head, diamondWant[:5])
	}

	cancel()
	rest := make([]byte, 16)
	if _, err := r.Read(rest); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-cancel error = %v, want context.Canceled", err)
	}

	// Resuming with a fresh context continues exactly after the prefix.
	var sb strings.Builder
	sb.Write(head)
	p := make([]byte, 2)
	for {
		got, readErr := r.ReadContext(context.Background(), p)
		sb.Write(p[:got])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatalf("resume read: %v", readErr)
		}
	}
	if sb.String() != diamondWant {
		t.Fatalf("resumed output = %q, want %q", sb.String(), diamondWant)
	}
}

func TestZeroLengthReferenceStillChargesExpansion(t *testing.T) {
	sizes := map[int64]int64{}
	empty := block(1, sizes) // declared size 0
	root := block(2, sizes, Literal([]byte("a")), Reference(1), Literal([]byte("b")), Reference(1))
	r, err := Open(context.Background(), storeOf(empty, root), 2, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	if got := readAll(t, r); got != "ab" {
		t.Fatalf("output = %q, want %q", got, "ab")
	}
	acct := r.Account()
	if acct.Expansions != 2 {
		t.Fatalf("expansions = %d, want 2 (zero-length references still count)", acct.Expansions)
	}
	if acct.OutputBytes != 2 {
		t.Fatalf("output bytes = %d, want 2", acct.OutputBytes)
	}
}

func TestDefaultLimitsApply(t *testing.T) {
	sizes := map[int64]int64{}
	leaf := block(1, sizes, Literal([]byte("ok")))
	root := block(2, sizes, Reference(1))
	r, err := Open(context.Background(), storeOf(leaf, root), 2, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	if r.lim.MaxOutputBytes != defaultMaxOutputBytes ||
		r.lim.MaxExpansions != defaultMaxExpansions ||
		r.lim.MaxDepth != defaultMaxDepth ||
		r.lim.MaxBlocks != defaultMaxBlocks ||
		r.lim.MaxCacheBytes != defaultMaxCacheBytes {
		t.Fatalf("defaults not applied: %+v", r.lim)
	}
	if got := readAll(t, r); got != "ok" {
		t.Fatalf("output = %q, want %q", got, "ok")
	}
}

func TestStoreErrorPropagates(t *testing.T) {
	boom := errors.New("store unavailable")
	store := errStore{id: 7, err: boom}
	_, err := Open(context.Background(), store, 7, Limits{})
	if !errors.Is(err, boom) {
		t.Fatalf("open error = %v, want store error", err)
	}
}

type errStore struct {
	id  int64
	err error
}

func (s errStore) Get(_ context.Context, id int64) (*Block, error) {
	if id == s.id {
		return nil, s.err
	}
	return nil, nil
}
