// Package pathcount counts and enumerates simple paths (no repeated
// vertices) from s to t in graphs of at most 22 vertices, optionally
// requiring the path to pass through a set of must-visit vertices.
//
// Counting uses subset-state memoization and big integers. Enumeration
// is paginated and may be conditioned on a path prefix. A Counter is
// bound to one immutable Version, so counts and pages always describe
// the same graph version; callers that keep the version remotely can
// pass Version.Token to Paginate to detect stale references.
//
// Work is bounded by a budget of state evaluations. When the budget is
// exhausted the result is reported as unknown (ErrBudgetExceeded /
// Page.Unknown), never as a bogus zero.
package pathcount

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"math/bits"
)

// MaxN is the maximum number of vertices (subset states use a uint32).
const MaxN = 22

var (
	// ErrBudgetExceeded means the answer is unknown, not zero.
	ErrBudgetExceeded = errors.New("pathcount: budget exceeded, result unknown")
	// ErrVersionMismatch means the caller's token names another version.
	ErrVersionMismatch = errors.New("pathcount: version token mismatch")
	// ErrInvalidPrefix means the prefix is not a simple prefix from s.
	ErrInvalidPrefix = errors.New("pathcount: invalid prefix")
)

// Version is one immutable snapshot of the problem instance. Count and
// Paginate results are only comparable within the same Version.
type Version struct {
	N    int      // number of vertices, 2..MaxN
	S    int      // source
	T    int      // sink, != S
	Must uint32   // bitmask of must-visit vertices
	Adj  []uint32 // adjacency bitmasks, undirected

	token string
}

// NewVersion builds a Version from an undirected edge list. The must
// set may include s or t (both are trivially visited by any s-t path).
func NewVersion(n int, edges [][2]int, s, t int, must []int) (*Version, error) {
	if n < 2 || n > MaxN {
		return nil, fmt.Errorf("pathcount: n=%d out of range [2,%d]", n, MaxN)
	}
	if s < 0 || s >= n || t < 0 || t >= n {
		return nil, fmt.Errorf("pathcount: s=%d t=%d out of range", s, t)
	}
	if s == t {
		return nil, errors.New("pathcount: s and t must differ")
	}
	v := &Version{N: n, S: s, T: t, Adj: make([]uint32, n)}
	for _, e := range edges {
		a, b := e[0], e[1]
		if a < 0 || a >= n || b < 0 || b >= n || a == b {
			return nil, fmt.Errorf("pathcount: bad edge %v", e)
		}
		v.Adj[a] |= 1 << uint(b)
		v.Adj[b] |= 1 << uint(a)
	}
	for _, m := range must {
		if m < 0 || m >= n {
			return nil, fmt.Errorf("pathcount: bad must vertex %d", m)
		}
		v.Must |= 1 << uint(m)
	}
	v.token = v.computeToken()
	return v, nil
}

// Token identifies this exact version; pass it back to Paginate to
// prove the listing refers to the same version that was counted.
func (v *Version) Token() string { return v.token }

func (v *Version) computeToken() string {
	h := sha256.New()
	var buf [8]byte
	put := func(x uint64) {
		binary.LittleEndian.PutUint64(buf[:], x)
		h.Write(buf[:])
	}
	put(uint64(v.N))
	put(uint64(v.S))
	put(uint64(v.T))
	put(uint64(v.Must))
	for _, a := range v.Adj {
		put(uint64(a))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// Counter evaluates counts for one Version with subset-state
// memoization. Memoized states are shared between Count and Paginate,
// so a page taken after a count is both cheap and consistent.
type Counter struct {
	v     *Version
	memo  map[uint64]*big.Int
	limit int64 // max state evaluations; <0 means unlimited
	used  int64
}

// NewCounter binds a Counter to a version. budget caps the number of
// distinct subset states evaluated over the Counter's lifetime; a
// negative budget means unlimited.
func NewCounter(v *Version, budget int64) *Counter {
	return &Counter{v: v, memo: make(map[uint64]*big.Int), limit: budget}
}

// countFrom returns the number of simple paths from cur to t that avoid
// the visited mask and collectively visit every must vertex not already
// in visited. Reaching t with must vertices still unvisited contributes
// zero (early arrival is a dead end, and t is never passed through).
func (c *Counter) countFrom(cur int, visited uint32) (*big.Int, error) {
	if cur == c.v.T {
		if c.v.Must&^visited == 0 {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	}
	key := uint64(visited)<<5 | uint64(cur)
	if val, ok := c.memo[key]; ok {
		return val, nil
	}
	if c.limit >= 0 {
		if c.used >= c.limit {
			return nil, ErrBudgetExceeded
		}
		c.used++
	}
	sum := new(big.Int)
	rest := c.v.Adj[cur] &^ visited
	for rest != 0 {
		next := bits.TrailingZeros32(rest)
		rest &= rest - 1
		sub, err := c.countFrom(next, visited|1<<uint(next))
		if err != nil {
			return nil, err
		}
		sum.Add(sum, sub)
	}
	c.memo[key] = sum
	return sum, nil
}

// Count returns the number of simple s-t paths visiting every must
// vertex. It returns ErrBudgetExceeded (unknown) instead of a partial
// count when the budget runs out.
func (c *Counter) Count() (*big.Int, error) {
	return c.countFrom(c.v.S, 1<<uint(c.v.S))
}

// Page is one page of the lexicographic (by vertex sequence) listing of
// all matching paths that start with the requested prefix.
type Page struct {
	Total   *big.Int // total matching paths under the prefix; nil if Unknown
	Paths   [][]int  // up to Limit paths starting at Offset
	Offset  *big.Int // offset actually used
	Unknown bool     // true when the budget ran out mid-listing
}

// Paginate lists matching paths that begin with prefix, skipping offset
// of them and returning at most limit, in the same canonical order that
// Count counts. token must equal Version.Token. Subtree counts from the
// shared memo let it skip whole branches without enumerating them, so
// the page and the total always come from the same version and order.
func (c *Counter) Paginate(prefix []int, offset *big.Int, limit int, token string) (*Page, error) {
	if token != c.v.token {
		return nil, ErrVersionMismatch
	}
	if offset == nil || offset.Sign() < 0 {
		offset = new(big.Int)
	}
	visited, err := c.checkPrefix(prefix)
	if err != nil {
		return nil, err
	}
	page := &Page{Offset: new(big.Int).Set(offset)}
	if limit <= 0 {
		return page, nil
	}
	total, err := c.countFrom(prefix[len(prefix)-1], visited)
	if err != nil {
		page.Unknown = true
		return page, err
	}
	page.Total = total
	skip := new(big.Int).Set(offset)
	// Copy the prefix: enumerate appends to it and must never write
	// into the caller's backing array.
	path := append([]int(nil), prefix...)
	if err := c.enumerate(path, visited, skip, limit, page); err != nil {
		page.Unknown = true
		return page, err
	}
	return page, nil
}

// checkPrefix verifies prefix is a simple path starting at s that does
// not pass through t before its end, and returns its visited mask.
func (c *Counter) checkPrefix(prefix []int) (uint32, error) {
	if len(prefix) == 0 || prefix[0] != c.v.S {
		return 0, ErrInvalidPrefix
	}
	var visited uint32
	for i, u := range prefix {
		if u < 0 || u >= c.v.N || visited&(1<<uint(u)) != 0 {
			return 0, ErrInvalidPrefix
		}
		if u == c.v.T && i != len(prefix)-1 {
			return 0, ErrInvalidPrefix
		}
		if i > 0 && c.v.Adj[prefix[i-1]]&(1<<uint(u)) == 0 {
			return 0, ErrInvalidPrefix
		}
		visited |= 1 << uint(u)
	}
	return visited, nil
}

// enumerate performs the DFS in increasing-neighbor order, decrementing
// skip past whole subtrees via memoized counts until the page starts.
func (c *Counter) enumerate(path []int, visited uint32, skip *big.Int, limit int, page *Page) error {
	if len(page.Paths) >= limit {
		return nil
	}
	cur := path[len(path)-1]
	if cur == c.v.T {
		if c.v.Must&^visited == 0 {
			if skip.Sign() > 0 {
				skip.Sub(skip, big.NewInt(1))
			} else {
				page.Paths = append(page.Paths, append([]int(nil), path...))
			}
		}
		return nil
	}
	rest := c.v.Adj[cur] &^ visited
	for rest != 0 {
		next := bits.TrailingZeros32(rest)
		rest &= rest - 1
		if skip.Sign() > 0 {
			cnt, err := c.countFrom(next, visited|1<<uint(next))
			if err != nil {
				return err
			}
			if skip.Cmp(cnt) >= 0 {
				skip.Sub(skip, cnt)
				continue
			}
		}
		if err := c.enumerate(append(path, next), visited|1<<uint(next), skip, limit, page); err != nil {
			return err
		}
		if len(page.Paths) >= limit {
			return nil
		}
	}
	return nil
}
