package bp_test

import (
	"math/rand"
	"strings"
	"testing"

	"bptree/bp"
	"bptree/naive"
)

func TestValidation(t *testing.T) {
	invalid := []string{
		"",        // empty
		"(",       // odd length / unbalanced
		")",       // negative prefix
		"(()",     // unbalanced
		"())",     // negative prefix at the end
		"())(",    // negative prefix in the middle
		")(()",    // negative prefix first
		"()()",    // forest: two roots
		"(())()",  // forest
		"abc(",    // invalid characters
		"(a)",     // invalid character inside
		"((()))(", // odd length
	}
	for _, s := range invalid {
		if _, err := bp.Build(s); err == nil {
			t.Errorf("Build(%q) unexpectedly succeeded", s)
		}
	}
	valid := []string{"()", "(())", "(()(()()))", "((()())(()))"}
	for _, s := range valid {
		if _, err := bp.Build(s); err != nil {
			t.Errorf("Build(%q) failed: %v", s, err)
		}
	}
}

func TestSingleNode(t *testing.T) {
	ix, err := bp.Build("()")
	if err != nil {
		t.Fatalf("single node must be valid: %v", err)
	}
	if ix.NumNodes() != 1 || ix.Len() != 2 {
		t.Fatalf("NumNodes=%d Len=%d", ix.NumNodes(), ix.Len())
	}
	if d := ix.Depth(0); d != 0 {
		t.Errorf("depth=%d", d)
	}
	if _, ok := ix.Parent(0); ok {
		t.Error("root must have no parent")
	}
	if _, ok := ix.FirstChild(0); ok {
		t.Error("single node must have no child")
	}
	if _, ok := ix.NextSibling(0); ok {
		t.Error("root must have no next sibling")
	}
	if _, ok := ix.PrevSibling(0); ok {
		t.Error("root must have no prev sibling")
	}
	if s := ix.SubtreeSize(0); s != 1 {
		t.Errorf("subtree size=%d", s)
	}
	if lo, hi := ix.SubtreeRange(0); lo != 0 || hi != 1 {
		t.Errorf("subtree range=[%d,%d)", lo, hi)
	}
	if o, c := ix.SubtreeParens(0); o != 0 || c != 1 {
		t.Errorf("paren interval=[%d,%d]", o, c)
	}
	if got := ix.LCA(0, 0); got != 0 {
		t.Errorf("lca=%d", got)
	}
	if r := ix.ChildRank(0); r != 0 {
		t.Errorf("child rank=%d", r)
	}
	if ix.FindClose(0) != 1 || ix.FindOpen(1) != 0 || ix.Enclose(0) != -1 {
		t.Error("position primitives wrong on ()")
	}
}

// TestExhaustiveSmallTrees cross-checks every operation of the succinct
// index against the independent pointer tree for ALL ordered trees with up
// to 7 nodes.
func TestExhaustiveSmallTrees(t *testing.T) {
	catalan := []int{1, 1, 2, 5, 14, 42, 132}
	for n := 1; n <= 7; n++ {
		words := naive.Enumerate(n)
		if len(words) != catalan[n-1] {
			t.Fatalf("n=%d: got %d trees, want %d", n, len(words), catalan[n-1])
		}
		for _, w := range words {
			checkTree(t, w)
		}
	}
}

func checkTree(t *testing.T, w string) {
	t.Helper()
	ix, err := bp.Build(w)
	if err != nil {
		t.Fatalf("Build(%q): %v", w, err)
	}
	root, err := naive.Parse(w)
	if err != nil {
		t.Fatalf("naive.Parse(%q): %v", w, err)
	}
	if got := naive.Encode(root); got != w {
		t.Fatalf("naive round trip: %q != %q", got, w)
	}
	nodes := naive.Preorder(root)
	n := len(nodes)
	if ix.NumNodes() != n {
		t.Fatalf("%q: NumNodes=%d want %d", w, ix.NumNodes(), n)
	}
	for v, nd := range nodes {
		if nd.Id != v {
			t.Fatalf("%q: naive id %d != preorder %d", w, nd.Id, v)
		}
		if got := ix.Depth(v); got != nd.Depth() {
			t.Fatalf("%q v=%d: depth=%d want %d", w, v, got, nd.Depth())
		}
		pg, pok := ix.Parent(v)
		if nd.Parent == nil {
			if pok {
				t.Fatalf("%q v=%d: root has parent %d", w, v, pg)
			}
		} else if !pok || pg != nd.Parent.Id {
			t.Fatalf("%q v=%d: parent=(%d,%v) want %d", w, v, pg, pok, nd.Parent.Id)
		}
		fg, fok := ix.FirstChild(v)
		if fc := nd.FirstChild(); fc == nil {
			if fok {
				t.Fatalf("%q v=%d: unexpected first child %d", w, v, fg)
			}
		} else if !fok || fg != fc.Id {
			t.Fatalf("%q v=%d: first child=(%d,%v) want %d", w, v, fg, fok, fc.Id)
		}
		ng, nok := ix.NextSibling(v)
		if ns := nd.NextSibling(); ns == nil {
			if nok {
				t.Fatalf("%q v=%d: unexpected next sibling %d", w, v, ng)
			}
		} else if !nok || ng != ns.Id {
			t.Fatalf("%q v=%d: next sibling=(%d,%v) want %d", w, v, ng, nok, ns.Id)
		}
		vg, vok := ix.PrevSibling(v)
		if ps := nd.PrevSibling(); ps == nil {
			if vok {
				t.Fatalf("%q v=%d: unexpected prev sibling %d", w, v, vg)
			}
		} else if !vok || vg != ps.Id {
			t.Fatalf("%q v=%d: prev sibling=(%d,%v) want %d", w, v, vg, vok, ps.Id)
		}
		if got := ix.ChildRank(v); got != nd.ChildRank() {
			t.Fatalf("%q v=%d: child rank=%d want %d", w, v, got, nd.ChildRank())
		}
		size := nd.SubtreeSize()
		if got := ix.SubtreeSize(v); got != size {
			t.Fatalf("%q v=%d: subtree size=%d want %d", w, v, got, size)
		}
		if lo, hi := ix.SubtreeRange(v); lo != v || hi != v+size {
			t.Fatalf("%q v=%d: subtree range=[%d,%d) want [%d,%d)", w, v, lo, hi, v, v+size)
		}
		// Subtree node count must correspond to the parenthesis interval.
		o, c := ix.SubtreeParens(v)
		if (c-o+1)/2 != size {
			t.Fatalf("%q v=%d: paren interval [%d,%d] holds %d nodes, want %d",
				w, v, o, c, (c-o+1)/2, size)
		}
		if opens := ix.Rank(c+1) - ix.Rank(o); opens != size {
			t.Fatalf("%q v=%d: opens in [%d,%d] = %d, want %d", w, v, o, c, opens, size)
		}
		// Node numbering mapping.
		if ix.Open(v) != o {
			t.Fatalf("%q v=%d: Open=%d want %d", w, v, ix.Open(v), o)
		}
		if id, ok := ix.NodeAt(o); !ok || id != v {
			t.Fatalf("%q v=%d: NodeAt(%d)=(%d,%v)", w, v, o, id, ok)
		}
		if ix.Select(v) != o || ix.Rank(o) != v {
			t.Fatalf("%q v=%d: select/rank mismatch", w, v)
		}
		if ix.Excess(o) != nd.Depth() {
			t.Fatalf("%q v=%d: excess=%d want depth %d", w, v, ix.Excess(o), nd.Depth())
		}
		if got := ix.FindClose(o); got != c {
			t.Fatalf("%q v=%d: findclose=%d want %d", w, v, got, c)
		}
		if got := ix.FindOpen(c); got != o {
			t.Fatalf("%q v=%d: findopen=%d want %d", w, v, got, o)
		}
		wantEnclose := -1
		if nd.Parent != nil {
			wantEnclose = ix.Open(nd.Parent.Id)
		}
		if got := ix.Enclose(o); got != wantEnclose {
			t.Fatalf("%q v=%d: enclose=%d want %d", w, v, got, wantEnclose)
		}
	}
	checkLCA := func(u, v int) {
		want := naive.LCA(nodes[u], nodes[v]).Id
		if got := ix.LCA(u, v); got != want {
			t.Fatalf("lca(%d,%d)=%d want %d (n=%d)", u, v, got, want, n)
		}
	}
	if n <= 300 {
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				checkLCA(u, v)
			}
		}
	} else {
		rng := rand.New(rand.NewSource(int64(n)))
		for i := 0; i < 20000; i++ {
			checkLCA(rng.Intn(n), rng.Intn(n))
		}
		// Adjacent and ancestor-heavy pairs.
		for u := 0; u+1 < n; u += 97 {
			checkLCA(u, u+1)
			checkLCA(0, u)
		}
	}
}

// TestRandomTrees cross-checks larger random trees, including trees wider
// than one block so summary skipping is exercised.
func TestRandomTrees(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 3, 50, 300, 2000, 20000} {
		root := naive.Random(n, rng)
		checkTree(t, naive.Encode(root))
	}
}

// TestDeepChain ensures nothing recurses: a chain of 2^17 nodes must build,
// navigate and serialize without overflowing the goroutine stack.
func TestDeepChain(t *testing.T) {
	d := 1 << 17
	w := strings.Repeat("(", d) + strings.Repeat(")", d)
	ix, err := bp.Build(w)
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Depth(d - 1); got != d-1 {
		t.Fatalf("depth of deepest node = %d, want %d", got, d-1)
	}
	// Iterative parent walk from the deepest node to the root.
	steps, v := 0, d-1
	for {
		p, ok := ix.Parent(v)
		if !ok {
			break
		}
		steps++
		v = p
	}
	if steps != d-1 || v != 0 {
		t.Fatalf("parent walk: steps=%d end=%d, want %d and 0", steps, v, d-1)
	}
	if got := ix.SubtreeSize(0); got != d {
		t.Fatalf("root subtree size=%d want %d", got, d)
	}
	if got := ix.LCA(0, d-1); got != 0 {
		t.Fatalf("lca=%d want 0", got)
	}
	if got := ix.LCA(d/2, d-1); got != d/2 {
		t.Fatalf("lca=%d want %d", got, d/2)
	}
	if o, c := ix.SubtreeParens(0); o != 0 || c != 2*d-1 {
		t.Fatalf("root parens [%d,%d]", o, c)
	}
	// Serialize and reload the deep chain.
	ix2, err := bp.Load(ix.MarshalBinary())
	if err != nil {
		t.Fatal(err)
	}
	if got := ix2.Depth(d - 1); got != d-1 {
		t.Fatalf("reloaded depth=%d want %d", got, d-1)
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range []int{1, 2, 5, 128, 5000, 100000} {
		root := naive.Random(n, rng)
		ix, err := bp.Build(naive.Encode(root))
		if err != nil {
			t.Fatal(err)
		}
		ix2, err := bp.Load(ix.MarshalBinary())
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if ix2.String() != ix.String() || ix2.NumNodes() != n {
			t.Fatalf("n=%d: round trip mismatch", n)
		}
		// Spot-check queries on the reloaded index.
		for _, v := range []int{0, n / 3, n / 2, n - 1} {
			if ix2.Depth(v) != ix.Depth(v) || ix2.SubtreeSize(v) != ix.SubtreeSize(v) {
				t.Fatalf("n=%d v=%d: query mismatch after reload", n, v)
			}
			if ix2.LCA(0, v) != ix.LCA(0, v) {
				t.Fatalf("n=%d v=%d: lca mismatch after reload", n, v)
			}
		}
	}
}

// TestLoadRejectsCorruption flips every single byte of a serialized index
// and requires Load to reject every variant, proving the stored min prefix
// excess, counts and summaries are all verified.
func TestLoadRejectsCorruption(t *testing.T) {
	ix, err := bp.Build("(()(()())(()))")
	if err != nil {
		t.Fatal(err)
	}
	good := ix.MarshalBinary()
	if _, err := bp.Load(good); err != nil {
		t.Fatalf("clean load failed: %v", err)
	}
	for i := 0; i < len(good); i++ {
		bad := append([]byte(nil), good...)
		bad[i] ^= 0xFF
		if _, err := bp.Load(bad); err == nil {
			t.Fatalf("corruption at byte %d not detected", i)
		}
	}
	for _, cut := range []int{1, 7, len(good) / 2, len(good) - 1} {
		if _, err := bp.Load(good[:cut]); err == nil {
			t.Fatalf("truncation to %d bytes not detected", cut)
		}
	}
}

// TestQueriesSkipBlocks verifies that queries touch far fewer blocks than a
// full scan of the parenthesis string would need.
func TestQueriesSkipBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	root := naive.Random(1<<16, rng)
	ix, err := bp.Build(naive.Encode(root))
	if err != nil {
		t.Fatal(err)
	}
	st := ix.Stats()
	st.Reset()
	const q = 5000
	for i := 0; i < q; i++ {
		u := rng.Intn(ix.NumNodes())
		v := rng.Intn(ix.NumNodes())
		ix.LCA(u, v)
		ix.FindClose(ix.Open(u))
		ix.Depth(u)
		ix.SubtreeSize(v)
	}
	for _, op := range []string{bp.OpLCA, bp.OpFindClose, bp.OpDepth, bp.OpSubtreeSize} {
		avg := st.AvgBlockAccesses(op)
		if st.Queries(op) != q {
			t.Fatalf("%s: %d queries recorded, want %d", op, st.Queries(op), q)
		}
		// A full scan would need up to NumBlocks() block visits per query;
		// require at least 4x better than that worst case.
		if limit := float64(ix.NumBlocks()) / 4; avg > limit {
			t.Fatalf("%s: avg block accesses %.1f too high (limit %.1f, blocks total %d)",
				op, avg, limit, ix.NumBlocks())
		}
		t.Logf("%-14s avg block accesses: %.2f (index has %d blocks, %d superblocks)",
			op, avg, ix.NumBlocks(), ix.NumSuperblocks())
	}
}

func TestIndexOverheadReport(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	root := naive.Random(1<<20, rng)
	ix, err := bp.Build(naive.Encode(root))
	if err != nil {
		t.Fatal(err)
	}
	bitsB, sbB, blkB, rmqB := ix.SizeBytes()
	total := ix.TotalBytes()
	payload := ix.PayloadBytes()
	t.Logf("nodes=%d parens=%d", ix.NumNodes(), ix.Len())
	t.Logf("payload bits: %d B (1 bit/paren)", payload)
	t.Logf("index: bits=%d B superblocks=%d B (%d x 12 B) blocks=%d B (%d x 6 B) rmq=%d B",
		bitsB, sbB, ix.NumSuperblocks(), blkB, ix.NumBlocks(), rmqB)
	t.Logf("total=%d B, overhead vs raw payload: %.1f%%",
		total, 100*float64(total-payload)/float64(payload))
	if total <= payload {
		t.Fatal("summaries must cost something")
	}
	if float64(total) > 1.25*float64(payload) {
		t.Fatalf("overhead too high: %d vs payload %d", total, payload)
	}
}
