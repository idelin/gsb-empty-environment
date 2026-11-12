package netsec

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// block builds a *Block, deriving its exact logical size from its literals and
// the already-known sizes of referenced blocks, so well-formed fixtures stay
// consistent with the declarations Open checks.
func block(id int64, sizeOf map[int64]int64, segs ...Segment) *Block {
	b := &Block{ID: id, Segments: segs}
	var size int64
	for _, s := range segs {
		if s.IsLiteral() {
			size += int64(len(s.Data()))
		} else {
			size += sizeOf[s.Ref()]
		}
	}
	b.Declared = size
	sizeOf[id] = size
	return b
}

// sizedBlock builds a block with an explicit (possibly wrong) declaration,
// used for the over-long declaration tests.
func sizedBlock(id int64, declared int64, segs ...Segment) *Block {
	return &Block{ID: id, Declared: declared, Segments: segs}
}

func storeOf(blocks ...*Block) MapStore {
	m := make(MapStore, len(blocks))
	for _, b := range blocks {
		m[b.ID] = b
	}
	return m
}

func readAll(t *testing.T, r *Reader) string {
	t.Helper()
	var sb strings.Builder
	p := make([]byte, 3)
	for {
		n, err := r.Read(p)
		sb.Write(p[:n])
		if errors.Is(err, io.EOF) {
			return sb.String()
		}
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
	}
}

// diamondStore builds:
//
//	root -> ref1 -> leaf
//	    \-> ref2 -> leaf
//
// so the shared leaf is decoded once but emitted through two parents.
func diamondStore() (MapStore, int64) {
	sizes := map[int64]int64{}
	leaf := block(1, sizes, Literal([]byte("LEAF")))
	ref1 := block(2, sizes, Literal([]byte("[1:")), Reference(1), Literal([]byte("]")))
	ref2 := block(3, sizes, Literal([]byte("[2:")), Reference(1), Literal([]byte("]")))
	root := block(4, sizes, Reference(2), Literal([]byte("|")), Reference(3))
	return storeOf(leaf, ref1, ref2, root), 4
}

const diamondWant = "[1:LEAF]|[2:LEAF]"

func TestDiamondDependency(t *testing.T) {
	store, root := diamondStore()
	r, err := Open(context.Background(), store, root, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	if got := readAll(t, r); got != diamondWant {
		t.Fatalf("output = %q, want %q", got, diamondWant)
	}

	acct := r.Account()
	// root references ref1 and ref2; each parent references leaf once.
	if acct.Expansions != 4 {
		t.Fatalf("expansions = %d, want 4", acct.Expansions)
	}
	if acct.OutputBytes != int64(len(diamondWant)) {
		t.Fatalf("output bytes = %d, want %d", acct.OutputBytes, len(diamondWant))
	}
	// Every non-root block is decoded exactly once despite leaf sharing.
	if acct.CacheEntries != 3 {
		t.Fatalf("cache entries = %d, want 3", acct.CacheEntries)
	}
	// Only the second leaf expansion is a cache hit (via ref2).
	if acct.CacheHits != 1 {
		t.Fatalf("cache hits = %d, want 1", acct.CacheHits)
	}
	if acct.CachedBytes != 4+8+8 {
		t.Fatalf("cached bytes = %d, want %d", acct.CachedBytes, 4+8+8)
	}
}

func TestSharedBlockCacheDoesNotBypassOutputLimit(t *testing.T) {
	store, root := diamondStore()
	// The first two reads consume 5 of the 10 permitted bytes; caching the
	// shared leaf must not make the remaining emissions free.
	r, err := Open(context.Background(), store, root, Limits{
		MaxOutputBytes: 10,
		MaxExpansions:  100,
		MaxCacheBytes:  1024,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	got, n, err := readN(r, 10)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if got != diamondWant[:10] {
		t.Fatalf("prefix = %q, want %q", got, diamondWant[:10])
	}
	if n != 10 {
		t.Fatalf("n = %d, want 10", n)
	}

	buf := make([]byte, 8)
	if _, err := r.Read(buf); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("after cap error = %v, want ErrOutputLimit", err)
	}
	if r.Account().OutputBytes != 10 {
		t.Fatalf("output bytes = %d, want 10", r.Account().OutputBytes)
	}
	// The limit stays terminal.
	if _, err := r.Read(buf); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("re-read error = %v, want ErrOutputLimit", err)
	}
}

func TestExpansionLimitCountsCachedReuse(t *testing.T) {
	// leaf reused four times: each reuse still counts as an expansion.
	sizes := map[int64]int64{}
	leaf := block(1, sizes, Literal([]byte("x")))
	root := block(2, sizes, Reference(1), Reference(1), Reference(1), Reference(1))
	store := storeOf(leaf, root)

	r, err := Open(context.Background(), store, 2, Limits{
		MaxOutputBytes: 1024,
		MaxExpansions:  2,
		MaxCacheBytes:  1024,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	buf := make([]byte, 16)
	n, err := r.Read(buf)
	if !errors.Is(err, ErrExpansionLimit) {
		t.Fatalf("error = %v, want ErrExpansionLimit", err)
	}
	// Two expansions of the one-byte leaf are emitted before the cap trips.
	if n != 2 || string(buf[:n]) != "xx" {
		t.Fatalf("prefix = %q (n=%d), want %q", buf[:n], n, "xx")
	}
	if r.Account().Expansions != 2 {
		t.Fatalf("expansions = %d, want 2", r.Account().Expansions)
	}
}

// readN performs small reads until n bytes are collected or an error occurs.
func readN(r *Reader, n int) (string, int, error) {
	var out []byte
	p := make([]byte, 2)
	for len(out) < n {
		got, err := r.Read(p)
		out = append(out, p[:got]...)
		if err != nil {
			return string(out), len(out), err
		}
	}
	return string(out[:n]), len(out), nil
}

func TestReadAllByteForByte(t *testing.T) {
	store, root := diamondStore()
	r, err := Open(context.Background(), store, root, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	var sb strings.Builder
	for {
		one := make([]byte, 1)
		n, err := r.Read(one)
		if n == 1 {
			sb.WriteByte(one[0])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if sb.String() != diamondWant {
		t.Fatalf("byte-for-byte = %q, want %q", sb.String(), diamondWant)
	}
}
