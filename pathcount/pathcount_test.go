package pathcount

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// brutePaths enumerates every simple s-t path covering Must by plain
// DFS in increasing-neighbor order: the ground truth and the canonical
// order that Paginate must reproduce.
func brutePaths(v *Version) [][]int {
	var res [][]int
	visited := make([]bool, v.N)
	var path []int
	var dfs func(cur int)
	dfs = func(cur int) {
		visited[cur] = true
		path = append(path, cur)
		if cur == v.T {
			ok := true
			for m := 0; m < v.N; m++ {
				if v.Must&(1<<uint(m)) != 0 && !visited[m] {
					ok = false
					break
				}
			}
			if ok {
				res = append(res, append([]int(nil), path...))
			}
		} else {
			for next := 0; next < v.N; next++ {
				if v.Adj[cur]&(1<<uint(next)) != 0 && !visited[next] {
					dfs(next)
				}
			}
		}
		path = path[:len(path)-1]
		visited[cur] = false
	}
	dfs(v.S)
	return res
}

func hasPrefix(path, prefix []int) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// equalPaths treats nil and empty listings as equal.
func equalPaths(a, b [][]int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func mustVersion(t *testing.T, n int, edges [][2]int, s, tt int, must []int) *Version {
	t.Helper()
	v, err := NewVersion(n, edges, s, tt, must)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	return v
}

func mustCount(t *testing.T, v *Version) *big.Int {
	t.Helper()
	n, err := NewCounter(v, -1).Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	return n
}

// Early arrival at t: the direct s-t edge must not count while a must
// vertex is still unvisited, and t must never be traversed mid-path.
func TestEarlyArrivalAtT(t *testing.T) {
	v := mustVersion(t, 3, [][2]int{{0, 1}, {1, 2}, {0, 2}}, 0, 2, []int{1})
	if got := mustCount(t, v); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("count = %v, want 1 (only 0-1-2)", got)
	}
	v = mustVersion(t, 3, [][2]int{{0, 1}, {1, 2}, {0, 2}}, 0, 2, nil)
	if got := mustCount(t, v); got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("count = %v, want 2", got)
	}
}

// An isolated must vertex can never be visited: count is exactly 0,
// not unknown.
func TestIsolatedMustVertex(t *testing.T) {
	v := mustVersion(t, 4, [][2]int{{0, 1}, {1, 2}}, 0, 2, []int{3})
	if got := mustCount(t, v); got.Sign() != 0 {
		t.Fatalf("count = %v, want 0", got)
	}
}

// Must vertex reachable only through t: no simple path can visit it
// before ending at t, so the count is 0.
func TestMustConflictThroughT(t *testing.T) {
	v := mustVersion(t, 4, [][2]int{{0, 1}, {1, 2}, {2, 3}}, 0, 2, []int{3})
	if got := mustCount(t, v); got.Sign() != 0 {
		t.Fatalf("count = %v, want 0", got)
	}
}

// Must set containing s and t themselves is trivially satisfiable.
func TestMustContainsEndpoints(t *testing.T) {
	v := mustVersion(t, 3, [][2]int{{0, 1}, {1, 2}, {0, 2}}, 0, 2, []int{0, 2})
	if got := mustCount(t, v); got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("count = %v, want 2", got)
	}
}

// Budget exhaustion must surface as unknown, never as 0.
func TestBudgetReturnsUnknown(t *testing.T) {
	v := mustVersion(t, 3, [][2]int{{0, 1}, {1, 2}, {0, 2}}, 0, 2, nil)
	if _, err := NewCounter(v, 0).Count(); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Count err = %v, want ErrBudgetExceeded", err)
	}
	page, err := NewCounter(v, 1).Paginate([]int{0}, big.NewInt(0), 10, v.Token())
	if err == nil || page == nil || !page.Unknown {
		t.Fatalf("Paginate = %+v, %v; want unknown page", page, err)
	}
	if page.Total != nil {
		t.Fatalf("unknown page leaked total %v", page.Total)
	}
}

// Paginate must reject a token from another version.
func TestVersionTokenMismatch(t *testing.T) {
	v1 := mustVersion(t, 3, [][2]int{{0, 1}, {1, 2}}, 0, 2, nil)
	v2 := mustVersion(t, 3, [][2]int{{0, 1}, {1, 2}, {0, 2}}, 0, 2, nil)
	if v1.Token() == v2.Token() {
		t.Fatal("distinct versions share a token")
	}
	_, err := NewCounter(v1, -1).Paginate([]int{0}, big.NewInt(0), 10, v2.Token())
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
}

// Complete graph K_n: closed-form count sum_{k=0}^{n-2} P(n-2,k),
// exercising big-integer counts beyond 64 bits worth of states.
func TestCompleteGraphFormula(t *testing.T) {
	const n = 12
	var edges [][2]int
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			edges = append(edges, [2]int{a, b})
		}
	}
	v := mustVersion(t, n, edges, 0, n-1, nil)
	want := new(big.Int)
	fact := big.NewInt(1)
	for k := 0; k <= n-2; k++ {
		if k > 0 {
			fact.Mul(fact, big.NewInt(int64(k)))
		}
		// P(n-2, k) = (n-2)! / (n-2-k)!
		term := new(big.Int)
		p := big.NewInt(1)
		for j := 0; j < n-2; j++ {
			p.Mul(p, big.NewInt(int64(j+1)))
		}
		term.Quo(p, fact)
		want.Add(want, term)
	}
	if got := mustCount(t, v); got.Cmp(want) != 0 {
		t.Fatalf("K_%d count = %v, want %v", n, got, want)
	}
}

// Random small graphs: DP count and every page must match brute force.
func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(7) // 2..8 vertices
		var edges [][2]int
		for a := 0; a < n; a++ {
			for b := a + 1; b < n; b++ {
				if rng.Float64() < 0.4 {
					edges = append(edges, [2]int{a, b})
				}
			}
		}
		s, tt := 0, n-1
		var must []int
		for m := 0; m < n; m++ {
			if rng.Float64() < 0.3 {
				must = append(must, m)
			}
		}
		v := mustVersion(t, n, edges, s, tt, must)
		want := brutePaths(v)

		c := NewCounter(v, -1)
		got, err := c.Count()
		if err != nil {
			t.Fatalf("trial %d: Count: %v", trial, err)
		}
		if got.Cmp(big.NewInt(int64(len(want)))) != 0 {
			t.Fatalf("trial %d: count = %v, want %d (n=%d edges=%v must=%v)",
				trial, got, len(want), n, edges, must)
		}

		// Full listing in one page must equal brute-force order.
		page, err := c.Paginate([]int{s}, big.NewInt(0), len(want)+1, v.Token())
		if err != nil {
			t.Fatalf("trial %d: Paginate: %v", trial, err)
		}
		if page.Total.Cmp(got) != 0 {
			t.Fatalf("trial %d: page total %v != count %v", trial, page.Total, got)
		}
		if !reflect.DeepEqual(page.Paths, want) {
			t.Fatalf("trial %d: paths = %v, want %v", trial, page.Paths, want)
		}

		// Random offsets and limits must slice the same listing.
		for k := 0; k < 4; k++ {
			off := rng.Intn(len(want) + 2)
			lim := rng.Intn(4)
			page, err := c.Paginate([]int{s}, big.NewInt(int64(off)), lim, v.Token())
			if err != nil {
				t.Fatalf("trial %d: Paginate: %v", trial, err)
			}
			end := off + lim
			if end > len(want) {
				end = len(want)
			}
			var wantSlice [][]int
			if off < len(want) {
				wantSlice = want[off:end]
			}
			if !equalPaths(page.Paths, wantSlice) {
				t.Fatalf("trial %d: page(off=%d,lim=%d) = %v, want %v",
					trial, off, lim, page.Paths, wantSlice)
			}
		}

		// Prefix-conditioned pages must match the filtered brute force.
		if len(want) > 0 {
			full := want[rng.Intn(len(want))]
			plen := 1 + rng.Intn(len(full))
			prefix := full[:plen]
			var filtered [][]int
			for _, p := range want {
				if hasPrefix(p, prefix) {
					filtered = append(filtered, p)
				}
			}
			page, err := c.Paginate(prefix, big.NewInt(0), len(filtered)+1, v.Token())
			if err != nil {
				t.Fatalf("trial %d: Paginate prefix %v: %v", trial, prefix, err)
			}
			if page.Total.Cmp(big.NewInt(int64(len(filtered)))) != 0 {
				t.Fatalf("trial %d: prefix total = %v, want %d",
					trial, page.Total, len(filtered))
			}
			if !reflect.DeepEqual(page.Paths, filtered) {
				t.Fatalf("trial %d: prefix %v paths = %v, want %v",
					trial, prefix, page.Paths, filtered)
			}
		}
	}
}

// Invalid prefixes are rejected rather than silently miscounted.
func TestInvalidPrefix(t *testing.T) {
	v := mustVersion(t, 3, [][2]int{{0, 1}, {1, 2}}, 0, 2, nil)
	bad := [][]int{
		{},           // empty
		{1},          // does not start at s
		{0, 2},       // not an edge
		{0, 1, 1},    // repeated vertex
		{0, 1, 2, 1}, // t not last / repeat
	}
	for _, p := range bad {
		if _, err := NewCounter(v, -1).Paginate(p, big.NewInt(0), 5, v.Token()); !errors.Is(err, ErrInvalidPrefix) {
			t.Errorf("prefix %v: err = %v, want ErrInvalidPrefix", p, err)
		}
	}
}

func ExampleCounter_Count() {
	v, _ := NewVersion(4, [][2]int{{0, 1}, {1, 3}, {0, 2}, {2, 3}, {1, 2}}, 0, 3, []int{2})
	n, _ := NewCounter(v, -1).Count()
	fmt.Println(n)
	// Output: 3
}
