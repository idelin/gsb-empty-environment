package bptree

import (
	"math/rand"
	"strings"
	"testing"
)

// --- independent pointer-tree reference (no shared code with Index) ---

type pnode struct {
	parent   *pnode
	children []*pnode
	depth    int
	id       int // preorder
	size     int // subtree size
}

// parseRef builds a pointer tree from a validated BP string, iteratively.
func parseRef(bp string) ([]*pnode, *pnode) {
	var root *pnode
	var stack []*pnode
	var order []*pnode
	for _, c := range bp {
		if c == '(' {
			n := &pnode{depth: len(stack), id: len(order)}
			if len(stack) > 0 {
				n.parent = stack[len(stack)-1]
				n.parent.children = append(n.parent.children, n)
			} else {
				root = n
			}
			order = append(order, n)
			stack = append(stack, n)
		} else {
			stack = stack[:len(stack)-1]
		}
	}
	for _, n := range order {
		n.size = 1
	}
	for i := len(order) - 1; i > 0; i-- {
		order[i].parent.size += order[i].size
	}
	return order, root
}

func refLCA(a, b *pnode) *pnode {
	for a.depth > b.depth {
		a = a.parent
	}
	for b.depth > a.depth {
		b = b.parent
	}
	for a != b {
		a = a.parent
		b = b.parent
	}
	return a
}

func refPrevSibling(n *pnode) *pnode {
	if n.parent == nil {
		return nil
	}
	for i, c := range n.parent.children {
		if c == n {
			if i == 0 {
				return nil
			}
			return n.parent.children[i-1]
		}
	}
	return nil
}

func refNextSibling(n *pnode) *pnode {
	if n.parent == nil {
		return nil
	}
	for i, c := range n.parent.children {
		if c == n {
			if i+1 < len(n.parent.children) {
				return n.parent.children[i+1]
			}
			return nil
		}
	}
	return nil
}

func idOf(n *pnode) int {
	if n == nil {
		return -1
	}
	return n.id
}

// checkTree compares every Index query against the pointer reference.
func checkTree(t *testing.T, bp string) {
	t.Helper()
	ix, err := Parse(bp)
	if err != nil {
		t.Fatalf("Parse(%q): %v", bp, err)
	}
	order, _ := parseRef(bp)
	n := len(order)
	if ix.NumNodes() != n {
		t.Fatalf("bp=%q: NumNodes=%d want %d", bp, ix.NumNodes(), n)
	}
	for v := 0; v < n; v++ {
		rn := order[v]
		// node numbering mapping: Select/Rank round trip
		p := ix.Select(v)
		if p < 0 || ix.Bit(p) != 1 {
			t.Fatalf("bp=%q v=%d: Select=%d not an open", bp, v, p)
		}
		if ix.Rank(p) != v {
			t.Fatalf("bp=%q v=%d: Rank(Select)=%d", bp, v, ix.Rank(p))
		}
		if ix.Depth(v) != rn.depth {
			t.Fatalf("bp=%q v=%d: Depth=%d want %d", bp, v, ix.Depth(v), rn.depth)
		}
		if ix.Parent(v) != idOf(rn.parent) {
			t.Fatalf("bp=%q v=%d: Parent=%d want %d", bp, v, ix.Parent(v), idOf(rn.parent))
		}
		var fc int = -1
		if len(rn.children) > 0 {
			fc = rn.children[0].id
		}
		if ix.FirstChild(v) != fc {
			t.Fatalf("bp=%q v=%d: FirstChild=%d want %d", bp, v, ix.FirstChild(v), fc)
		}
		if ix.NextSibling(v) != idOf(refNextSibling(rn)) {
			t.Fatalf("bp=%q v=%d: NextSibling=%d want %d", bp, v, ix.NextSibling(v), idOf(refNextSibling(rn)))
		}
		if ix.PrevSibling(v) != idOf(refPrevSibling(rn)) {
			t.Fatalf("bp=%q v=%d: PrevSibling=%d want %d", bp, v, ix.PrevSibling(v), idOf(refPrevSibling(rn)))
		}
		if ix.Degree(v) != len(rn.children) {
			t.Fatalf("bp=%q v=%d: Degree=%d want %d", bp, v, ix.Degree(v), len(rn.children))
		}
		for k, c := range rn.children {
			if ix.Child(v, k) != c.id {
				t.Fatalf("bp=%q v=%d k=%d: Child=%d want %d", bp, v, k, ix.Child(v, k), c.id)
			}
		}
		if ix.SubtreeSize(v) != rn.size {
			t.Fatalf("bp=%q v=%d: SubtreeSize=%d want %d", bp, v, ix.SubtreeSize(v), rn.size)
		}
		// subtree interval must match the reference: close = open + 2*size - 1
		o, c := ix.SubtreeInterval(v)
		if o != p || c != p+2*rn.size-1 {
			t.Fatalf("bp=%q v=%d: interval=[%d,%d] want [%d,%d]", bp, v, o, c, p, p+2*rn.size-1)
		}
		if ix.FindClosePos(p) != c {
			t.Fatalf("bp=%q v=%d: FindClosePos=%d want %d", bp, v, ix.FindClosePos(p), c)
		}
		if ix.FindOpenPos(c) != p {
			t.Fatalf("bp=%q v=%d: FindOpenPos=%d want %d", bp, v, ix.FindOpenPos(c), p)
		}
		if rn.parent != nil && ix.EnclosePos(p) != ix.Select(rn.parent.id) {
			t.Fatalf("bp=%q v=%d: EnclosePos mismatch", bp, v)
		}
	}
	for u := 0; u < n; u++ {
		for v := 0; v < n; v++ {
			want := refLCA(order[u], order[v]).id
			if got := ix.LCA(u, v); got != want {
				t.Fatalf("bp=%q: LCA(%d,%d)=%d want %d", bp, u, v, got, want)
			}
		}
	}
}

// genAllBP enumerates all balanced sequences of n opens, iteratively.
func genAllBP(n int) []string {
	var out []string
	type state struct {
		pos, opens, bal int
		buf             []byte
	}
	stack := []state{{buf: make([]byte, 2*n)}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s.pos == 2*n {
			out = append(out, string(s.buf))
			continue
		}
		if s.opens < n { // place '('
			b := append([]byte(nil), s.buf...)
			b[s.pos] = '('
			stack = append(stack, state{s.pos + 1, s.opens + 1, s.bal + 1, b})
		}
		if s.bal > 0 { // place ')'
			b := append([]byte(nil), s.buf...)
			b[s.pos] = ')'
			stack = append(stack, state{s.pos + 1, s.opens, s.bal - 1, b})
		}
	}
	return out
}

func TestExhaustiveSmallTrees(t *testing.T) {
	total := 0
	for n := 1; n <= 8; n++ {
		// ordered trees with n nodes == balanced sequences of n-1 pairs
		// wrapped in one extra root pair (Catalan(n-1) many)
		for _, inner := range genAllBP(n - 1) {
			checkTree(t, "("+inner+")")
			total++
		}
	}
	t.Logf("verified %d trees (all ordered trees with 1..8 nodes)", total)
}

func TestSingleNode(t *testing.T) {
	ix, err := Parse("()")
	if err != nil {
		t.Fatalf("single node must be valid: %v", err)
	}
	if ix.NumNodes() != 1 || ix.Depth(0) != 0 || ix.Parent(0) != -1 ||
		ix.FirstChild(0) != -1 || ix.SubtreeSize(0) != 1 || ix.LCA(0, 0) != 0 {
		t.Fatal("single-node tree queries wrong")
	}
}

func TestValidation(t *testing.T) {
	valid := []string{"()", "(())", "()()", "(()())", "(((())))", "(()(()())(()))"}
	for _, s := range valid {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q) = %v, want nil", s, err)
		}
	}
	cases := map[string]error{
		"":          ErrEmpty,
		"(":         ErrOddLength,
		")(":        ErrNegativePrefix,
		"())(":      ErrNegativePrefix,
		"(()":       ErrOddLength,
		"((()":      ErrUnbalanced,
		"((())":     ErrOddLength,
		"(()))(":    ErrNegativePrefix,
		"((())(":    ErrUnbalanced,
		"((()))()(": ErrOddLength,
		"(a)":       ErrBadCharacter,
	}
	for s, want := range cases {
		if _, err := Parse(s); err != want {
			t.Errorf("Parse(%q) = %v, want %v", s, err, want)
		}
	}
}

// randomBP builds a random ordered tree by random parent attachment and
// emits its BP encoding with an explicit stack (no recursion).
func randomBP(rng *rand.Rand, n int) string {
	children := make([][]int, n)
	for v := 1; v < n; v++ {
		p := rng.Intn(v)
		children[p] = append(children[p], v)
	}
	var sb strings.Builder
	type frame struct{ v, next int }
	stack := []frame{{0, 0}}
	sb.WriteByte('(')
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.next < len(children[f.v]) {
			c := children[f.v][f.next]
			f.next++
			sb.WriteByte('(')
			stack = append(stack, frame{c, 0})
		} else {
			sb.WriteByte(')')
			stack = stack[:len(stack)-1]
		}
	}
	return sb.String()
}

func TestRandomTrees(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, n := range []int{9, 17, 33, 100, 257, 1000} {
		for i := 0; i < 20; i++ {
			checkTree(t, randomBP(rng, n))
		}
	}
}

func TestDeepChain(t *testing.T) {
	const n = 1 << 20 // 1M nested nodes: recursion would blow the stack
	bp := strings.Repeat("(", n) + strings.Repeat(")", n)
	ix, err := Parse(bp)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Depth(n-1) != n-1 {
		t.Fatalf("depth of deepest = %d, want %d", ix.Depth(n-1), n-1)
	}
	if c := ix.FindClosePos(0); c != 2*n-1 {
		t.Fatalf("FindClosePos(0) = %d, want %d", c, 2*n-1)
	}
	if ix.SubtreeSize(0) != n {
		t.Fatalf("SubtreeSize(0) = %d, want %d", ix.SubtreeSize(0), n)
	}
	// walk from the deepest node to the root, iteratively
	steps := 0
	for v := n - 1; v != 0; v = ix.Parent(v) {
		steps++
	}
	if steps != n-1 {
		t.Fatalf("parent walk took %d steps, want %d", steps, n-1)
	}
	if ix.LCA(5, 7) != 5 || ix.LCA(n-1, 3) != 3 {
		t.Fatal("LCA on chain wrong")
	}
	// serialization round trip on the deep tree
	ix2, err := Unmarshal(ix.MarshalBinary())
	if err != nil {
		t.Fatal(err)
	}
	if ix2.Depth(n-1) != n-1 || ix2.SubtreeSize(0) != n {
		t.Fatal("deep chain corrupted by round trip")
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		bp := randomBP(rng, 1+rng.Intn(500))
		ix, err := Parse(bp)
		if err != nil {
			t.Fatal(err)
		}
		ix2, err := Unmarshal(ix.MarshalBinary())
		if err != nil {
			t.Fatalf("round trip: %v", err)
		}
		if ix2.NumNodes() != ix.NumNodes() || ix2.SubtreeSize(0) != ix.SubtreeSize(0) {
			t.Fatal("round trip mismatch")
		}
	}
}

func TestUnmarshalRejectsCorruption(t *testing.T) {
	ix, err := Parse("(()(()())(()(())))")
	if err != nil {
		t.Fatal(err)
	}
	good := ix.MarshalBinary()
	if _, err := Unmarshal(good); err != nil {
		t.Fatalf("clean data rejected: %v", err)
	}
	// flip one bit inside a stored superblock min-excess field
	bad := append([]byte(nil), good...)
	bad[4+8+8+8*len(ix.words)+8+8] ^= 0x01
	if _, err := Unmarshal(bad); err == nil {
		t.Fatal("corrupted superblock min-excess accepted")
	}
	// flip one bit inside a stored block relMin field
	bad2 := append([]byte(nil), good...)
	off := 4 + 8 + 8 + 8*len(ix.words) + 8 + 32*len(ix.supers) + 8 + 6
	bad2[off] ^= 0x01
	if _, err := Unmarshal(bad2); err == nil {
		t.Fatal("corrupted block relMin accepted")
	}
	// flip one bit in the bitvector itself
	bad3 := append([]byte(nil), good...)
	bad3[4+8+8] ^= 0x01
	if _, err := Unmarshal(bad3); err == nil {
		t.Fatal("corrupted bitvector accepted")
	}
	// bad magic / truncation
	if _, err := Unmarshal([]byte("XXXX")); err != ErrBadMagic {
		t.Fatalf("bad magic: %v", err)
	}
	if _, err := Unmarshal(good[:len(good)/2]); err == nil {
		t.Fatal("truncated data accepted")
	}
}

func TestTrailingBitsRejected(t *testing.T) {
	// "()" occupies 2 bits; a stray 1 bit beyond nbits must be rejected
	if _, err := Build([]uint64{0b101}, 2); err != ErrTrailingBits {
		t.Fatalf("got %v, want ErrTrailingBits", err)
	}
}
