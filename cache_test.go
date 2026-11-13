package netsec

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
)

func TestIdentitySeparation(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 64})
	base := testRequest("tenant-a", 10, 11, 12)
	lease := mustCreate(t, c, base, testSegments(1, 0, 3, "base"))
	defer lease.Release()

	mustLookup(t, c, base).Release()

	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{"tenant", func(r *Request) { r.TenantID = "tenant-b" }},
		{"system prompt digest", func(r *Request) { r.PromptDigest = promptHash("private system b") }},
		{"model quantization digest", func(r *Request) { r.ModelDigest = "model:weights:int8" }},
		{"adapter", func(r *Request) { r.AdapterDigest = "adapter:lora:finance" }},
		{"tokenizer", func(r *Request) { r.TokenizerDigest = "tokenizer:v2" }},
		{"decode position", func(r *Request) { r.Position = 1 }},
		{"token prefix", func(r *Request) { r.Tokens[2] = 99 }},
		{"safety policy generation", func(r *Request) { r.SafetyPolicyGeneration = 2 }},
		{"KV layout version", func(r *Request) { r.KVLayoutVersion = 2 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.Tokens = append([]Token(nil), base.Tokens...)
			tc.mutate(&req)
			if _, ok, err := c.Lookup(t.Context(), req); err != nil || ok {
				t.Fatalf("lookup ok=%v err=%v, want miss", ok, err)
			}
		})
	}
}

func TestSameTokensDifferentSystemPromptAreDistinct(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 32})
	a := testRequest("tenant-a", 5, 6)
	a.PromptDigest = promptHash("system prompt A")
	b := a
	b.PromptDigest = promptHash("system prompt B")
	if a.Identity().TokenPrefixDigest == b.Identity().TokenPrefixDigest {
		t.Fatal("system prompt digest was not folded into the token-prefix digest")
	}
	lease := mustCreate(t, c, a, testSegments(1, 0, 2, "tenant-a"))
	defer lease.Release()
	mustMiss(t, c, b)
}

func TestPublicScopeSharingAndBoundaries(t *testing.T) {
	tokens := []Token{21, 22}
	prompt := promptHash("public system")
	digest := CanonicalTokenPrefixDigest(tokens, prompt)
	c := newTestCache(t, Config{
		PoolBlocks: 64,
		PublicScopes: []PublicScope{{
			Name:               "shared-context",
			TokenPrefixDigests: [][32]byte{digest},
		}},
	})
	a := testRequest("tenant-a", tokens...)
	a.PromptDigest = prompt
	a.PublicScope = "shared-context"
	lease := mustCreate(t, c, a, testSegments(1, 0, 2, "public"))
	defer lease.Release()

	b := a
	b.TenantID = "tenant-b"
	hit := mustLookup(t, c, b)
	hit.Release()

	mutations := []struct {
		name   string
		mutate func(*Request)
	}{
		{"model quantization", func(r *Request) { r.ModelDigest = "model:weights:int8" }},
		{"adapter", func(r *Request) { r.AdapterDigest = "adapter:lora:x" }},
		{"tokenizer", func(r *Request) { r.TokenizerDigest = "tokenizer:v2" }},
		{"policy", func(r *Request) { r.SafetyPolicyGeneration = 2 }},
		{"layout", func(r *Request) { r.KVLayoutVersion = 2 }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			req := b
			tc.mutate(&req)
			mustMiss(t, c, req)
		})
	}

	otherPrompt := b
	otherPrompt.PromptDigest = promptHash("private other prompt")
	if _, _, err := c.Lookup(t.Context(), otherPrompt); !errors.Is(err, ErrPublicScope) {
		t.Fatalf("different prompt under public scope error = %v, want ErrPublicScope", err)
	}

	extra := b
	extra.Tokens = []Token{21, 22, 23}
	if _, _, err := c.Lookup(t.Context(), extra); !errors.Is(err, ErrPublicScope) {
		t.Fatalf("unregistered public prefix error = %v, want ErrPublicScope", err)
	}

	private := b
	private.PublicScope = ""
	mustMiss(t, c, private)
}

func TestPublicPrefixPrivateSuffixCopyOnWrite(t *testing.T) {
	tokens := []Token{31, 32}
	prompt := promptHash("public prefix")
	publicDigest := CanonicalTokenPrefixDigest(tokens, prompt)
	c := newTestCache(t, Config{
		PoolBlocks: 64,
		PublicScopes: []PublicScope{{
			Name:               "shared",
			TokenPrefixDigests: [][32]byte{publicDigest},
		}},
	})
	parentReq := testRequest("tenant-a", tokens...)
	parentReq.PromptDigest = prompt
	parentReq.PublicScope = "shared"
	parent := mustCreate(t, c, parentReq, testSegments(1, 0, 2, "public-prefix"))
	defer parent.Release()

	childReq := testRequest("tenant-a", 31, 32, 33)
	childReq.PromptDigest = prompt
	child, err := c.Extend(t.Context(), parent, childReq, testSegments(1, 2, 3, "private-suffix"))
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	defer child.Release()

	if got := child.Range(); got != (TokenRange{Start: 0, End: 3}) {
		t.Fatalf("child range = %+v", got)
	}
	if got := parent.Range(); got != (TokenRange{Start: 0, End: 2}) {
		t.Fatalf("parent was mutated: %+v", got)
	}
	parentLayers := parent.Layers()
	childLayers := child.Layers()
	if len(childLayers[0].Segments) != 2 {
		t.Fatalf("child segments = %+v", childLayers[0].Segments)
	}
	if childLayers[0].Segments[0] != parentLayers[0].Segments[0] {
		t.Fatal("child did not share the public prefix block")
	}
	if childLayers[0].Segments[1] == parentLayers[0].Segments[0] {
		t.Fatal("private suffix reused a prefix block")
	}

	// The parent is still independently valid and unchanged.
	parentHit := mustLookup(t, c, parentReq)
	if parentHit.Range() != (TokenRange{Start: 0, End: 2}) {
		t.Fatalf("parent range changed: %+v", parentHit.Range())
	}
	parentHit.Release()

	// Another tenant cannot see the private child.
	other := childReq
	other.TenantID = "tenant-b"
	mustMiss(t, c, other)
	// Nor may it mark the private extension public without explicit registration.
	other.PublicScope = "shared"
	if _, _, err := c.Lookup(t.Context(), other); !errors.Is(err, ErrPublicScope) {
		t.Fatalf("private suffix public lookup error = %v, want ErrPublicScope", err)
	}

	// Shared prefix blocks remain live until the child lease is gone.
	parentHandles := allBlockHandles(parentLayers)
	parent.Release()
	for _, h := range parentHandles {
		info, err := c.Pool().SlotInfo(h.Slot)
		if err != nil {
			t.Fatal(err)
		}
		if info.State != SlotReady {
			t.Fatalf("shared block was freed while child still holds it: %+v", info)
		}
	}
	child.Release()
	for _, h := range parentHandles {
		info, err := c.Pool().SlotInfo(h.Slot)
		if err != nil {
			t.Fatal(err)
		}
		if info.State != SlotFree || !info.AllZero {
			t.Fatalf("final child release did not scrub shared block: %+v", info)
		}
	}
}

func TestPrivatePrefixCannotBecomePublic(t *testing.T) {
	tokens := []Token{1, 2}
	prompt := promptHash("private")
	digest := CanonicalTokenPrefixDigest(tokens, prompt)
	extendedDigest := CanonicalTokenPrefixDigest([]Token{1, 2, 3}, prompt)
	// Rebuild with the private prefix registered so validation passes and the
	// extension-specific scope check is exercised.
	c := newTestCache(t, Config{
		PoolBlocks: 32,
		PublicScopes: []PublicScope{{
			Name:               "shared",
			TokenPrefixDigests: [][32]byte{digest, extendedDigest},
		}},
	})
	parentReq := testRequest("tenant-a", tokens...)
	parentReq.PromptDigest = prompt
	parent := mustCreate(t, c, parentReq, testSegments(1, 0, 2, "private"))
	defer parent.Release()

	childReq := testRequest("tenant-a", 1, 2, 3)
	childReq.PromptDigest = prompt
	childReq.PublicScope = "shared"
	if _, err := c.Extend(t.Context(), parent, childReq, testSegments(1, 2, 3, "suffix")); !errors.Is(err, ErrPublicScope) {
		t.Fatalf("private-to-public extension error = %v, want ErrPublicScope", err)
	}
}

func TestExtendRejectsWrongTenantOrPrefix(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 32})
	parentReq := testRequest("tenant-a", 11, 12)
	parent := mustCreate(t, c, parentReq, testSegments(1, 0, 2, "parent"))
	defer parent.Release()

	wrongTenant := testRequest("tenant-b", 11, 12, 13)
	if _, err := c.Extend(t.Context(), parent, wrongTenant, testSegments(1, 2, 3, "suffix")); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("wrong tenant extension error = %v, want ErrTenantMismatch", err)
	}

	notPrefix := testRequest("tenant-a", 11, 99, 13)
	if _, err := c.Extend(t.Context(), parent, notPrefix, testSegments(1, 2, 3, "suffix")); !errors.Is(err, ErrNotPrefix) {
		t.Fatalf("non-prefix extension error = %v, want ErrNotPrefix", err)
	}

	changedModel := testRequest("tenant-a", 11, 12, 13)
	changedModel.ModelDigest = "model:weights:int8"
	if _, err := c.Extend(t.Context(), parent, changedModel, testSegments(1, 2, 3, "suffix")); err == nil {
		t.Fatal("extension with changed model digest was accepted")
	}
}

func TestLeaseScrubsAfterLastRelease(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 16})
	req := testRequest("tenant-a", 41, 42)
	lease1 := mustCreate(t, c, req, testSegments(1, 0, 2, "secret"))
	lease2 := mustLookup(t, c, req)
	handles := allBlockHandles(lease1.Layers())

	lease1.Release()
	for _, h := range handles {
		info, err := c.Pool().SlotInfo(h.Slot)
		if err != nil {
			t.Fatal(err)
		}
		if info.State != SlotReady || info.AllZero {
			t.Fatalf("block scrubbed while another lease is active: %+v", info)
		}
	}
	lease2.Release()
	for _, h := range handles {
		info, err := c.Pool().SlotInfo(h.Slot)
		if err != nil {
			t.Fatal(err)
		}
		if info.State != SlotFree || !info.AllZero {
			t.Fatalf("final release did not scrub: %+v", info)
		}
	}
	mustMiss(t, c, req)
}

func TestBuilderAbortAndCancelScrub(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 8})
	req := testRequest("tenant-a", 51, 52)

	b, err := c.Begin(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range testSegments(1, 0, 2, "aborted") {
		if err := b.AppendSegment(seg); err != nil {
			t.Fatal(err)
		}
	}
	b.Abort()
	mustAllSlotsScrubbed(t, c.Pool())

	b, err = c.Begin(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range testSegments(1, 0, 2, "canceled") {
		if err := b.AppendSegment(seg); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Commit(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Commit canceled error = %v", err)
	}
	mustAllSlotsScrubbed(t, c.Pool())
	if _, ok, err := c.Lookup(context.Background(), req); err != nil || ok {
		t.Fatalf("canceled build is visible: ok=%v err=%v", ok, err)
	}
}

func TestContentMACAndBlockDigestTampering(t *testing.T) {
	t.Run("MAC", func(t *testing.T) {
		c := newTestCache(t, Config{PoolBlocks: 16})
		req := testRequest("tenant-a", 61, 62)
		lease := mustCreate(t, c, req, testSegments(1, 0, 2, "mac"))
		defer lease.Release()
		c.mu.Lock()
		e := c.index[string(req.Identity().Canonical())]
		e.mac[0] ^= 0xff
		c.mu.Unlock()
		mustMiss(t, c, req)
	})
	t.Run("block digest", func(t *testing.T) {
		c := newTestCache(t, Config{PoolBlocks: 16})
		req := testRequest("tenant-a", 71, 72)
		lease := mustCreate(t, c, req, testSegments(1, 0, 2, "digest"))
		defer lease.Release()
		c.mu.Lock()
		e := c.index[string(req.Identity().Canonical())]
		e.blockDigest[0] ^= 0xff
		c.mu.Unlock()
		mustMiss(t, c, req)
	})
	t.Run("block bytes", func(t *testing.T) {
		c := newTestCache(t, Config{PoolBlocks: 16})
		req := testRequest("tenant-a", 81, 82)
		lease := mustCreate(t, c, req, testSegments(1, 0, 2, "bytes"))
		defer lease.Release()
		h := lease.Layers()[0].Segments[0].K
		c.Pool().mu.Lock()
		c.Pool().slots[h.Slot].data[0] ^= 0xff
		c.Pool().mu.Unlock()
		mustMiss(t, c, req)
	})
}

func TestEntryGenerationAndEvictionReuse(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 8, MaxEntries: 1})
	a := testRequest("tenant-a", 91, 92)
	leaseA := mustCreate(t, c, a, testSegments(1, 0, 2, "a"))
	genA := leaseA.Generation()
	handlesA := allBlockHandles(leaseA.Layers())
	leaseA.Release()

	b := testRequest("tenant-b", 93, 94)
	leaseB := mustCreate(t, c, b, testSegments(1, 0, 2, "b"))
	defer leaseB.Release()
	if leaseB.Generation() <= genA {
		t.Fatalf("entry generation did not advance: a=%d b=%d", genA, leaseB.Generation())
	}
	mustMiss(t, c, a)
	mustLookup(t, c, b).Release()

	// A handle from the evicted entry must not be accepted by a reused slot.
	stale := handlesA[0]
	err := c.Pool().Fill(stale, []byte("late-fill"))
	if err == nil {
		t.Fatal("stale handle was accepted after eviction")
	}
	if !errors.Is(err, ErrStaleBlock) && !errors.Is(err, ErrBlockNotFilling) {
		t.Fatalf("stale fill error = %v", err)
	}
}

func TestConcurrentLookupAndRelease(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 32})
	req := testRequest("tenant-a", 101, 102)
	base := mustCreate(t, c, req, testSegments(1, 0, 2, "concurrent"))
	defer base.Release()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, ok, err := c.Lookup(context.Background(), req)
			if err != nil || !ok {
				t.Errorf("Lookup ok=%v err=%v", ok, err)
				return
			}
			lease.Release()
		}()
	}
	wg.Wait()
	hit := mustLookup(t, c, req)
	h := hit.Layers()[0].Segments[0].K
	if data, err := c.Pool().Read(h); err != nil || !bytes.Contains(data, []byte("concurrent-k-0")) {
		t.Fatalf("concurrent entry content = %q, %v", data, err)
	}
	hit.Release()
}

func TestBuilderCoverageValidation(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 16})
	req := testRequest("tenant-a", 1, 2, 3)
	b, err := c.Begin(req)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Abort()
	if err := b.AppendSegment(SegmentData{Layer: 0, TokenStart: 0, TokenEnd: 2, K: []byte("k"), V: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Commit(context.Background()); !errors.Is(err, ErrCoverage) {
		t.Fatalf("gap commit error = %v, want ErrCoverage", err)
	}
}

func TestAbsolutePositionAndSegmentedKV(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 32})
	req := testRequest("tenant-a", 201, 202, 203, 204)
	req.Position = 5
	req.KVLayoutVersion = 2
	segments := []SegmentData{
		{Layer: 0, TokenStart: 5, TokenEnd: 7, K: []byte("l0-k-a"), V: []byte("l0-v-a")},
		{Layer: 0, TokenStart: 7, TokenEnd: 9, K: []byte("l0-k-b"), V: []byte("l0-v-b")},
		{Layer: 1, TokenStart: 5, TokenEnd: 7, K: []byte("l1-k-a"), V: []byte("l1-v-a")},
		{Layer: 1, TokenStart: 7, TokenEnd: 9, K: []byte("l1-k-b"), V: []byte("l1-v-b")},
	}
	parent := mustCreate(t, c, req, segments)
	defer parent.Release()
	if parent.Range() != (TokenRange{Start: 5, End: 9}) {
		t.Fatalf("range = %+v", parent.Range())
	}
	if len(parent.Layers()) != 2 || len(parent.Layers()[0].Segments) != 2 {
		t.Fatalf("layers = %+v", parent.Layers())
	}

	childReq := testRequest("tenant-a", 201, 202, 203, 204, 205)
	childReq.Position = 5
	childReq.KVLayoutVersion = 2
	child, err := c.Extend(t.Context(), parent, childReq, []SegmentData{
		{Layer: 0, TokenStart: 9, TokenEnd: 10, K: []byte("l0-k-c"), V: []byte("l0-v-c")},
		{Layer: 1, TokenStart: 9, TokenEnd: 10, K: []byte("l1-k-c"), V: []byte("l1-v-c")},
	})
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	defer child.Release()
	if child.Range() != (TokenRange{Start: 5, End: 10}) {
		t.Fatalf("child range = %+v", child.Range())
	}
	if len(child.Layers()[1].Segments) != 3 {
		t.Fatalf("child layer 1 = %+v", child.Layers()[1])
	}
}

func TestConcurrentCreateDeduplicatesAndScrubs(t *testing.T) {
	c := newTestCache(t, Config{PoolBlocks: 64})
	req := testRequest("tenant-a", 301, 302)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := c.Create(context.Background(), req, testSegments(1, 0, 2, "dedup"))
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			lease.Release()
		}()
	}
	wg.Wait()
	mustAllSlotsScrubbed(t, c.Pool())
	mustMiss(t, c, req)
}
