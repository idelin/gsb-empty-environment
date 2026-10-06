package rulexplain

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
)

// MaxVars is the maximum number of finite-domain variables an Explainer
// accepts; the full-enumeration guarantees rely on the space staying
// small enough to enumerate exhaustively.
const MaxVars = 12

// maxAssignments caps the enumerated assignment space (product of all
// domain sizes).
const maxAssignments = 1 << 20

// ErrTooManyVars is returned when more than MaxVars variables are given.
var ErrTooManyVars = errors.New("rulexplain: more than 12 variables")

// ErrTooManyAssignments is returned when the joint domain is too large
// to enumerate exhaustively.
var ErrTooManyAssignments = errors.New("rulexplain: assignment space too large to enumerate")

// Explanation is a minimal set of variables that, pinned to their
// current values, guarantees the rule keeps producing Outcome no matter
// how the remaining variables range over their domains. Vars is sorted
// by name; among minimal explanations of the same size the
// lexicographically smallest variable list is chosen.
type Explanation struct {
	Vars    []string
	Outcome Outcome
}

// Explainer computes and caches explanations for one rule over a fixed
// set of finite variable domains. The cache is bound to the rule
// version and the domain signature: editing the rule via SetRoot or
// shrinking domains via SetDomains forces recomputation, so an
// explanation can be re-shortened once a domain shrinks.
//
// An Explainer is not safe for concurrent use.
type Explainer struct {
	rule    *Rule
	names   []string // sorted
	domains map[string][]int
	sizes   []int
	strides []int
	total   int

	table  []Outcome // outcome of every enumerated assignment
	tabVer uint64
	tabSig uint64

	cacheValid bool
	cacheVer   uint64
	cacheSig   uint64
	cacheKey   string
	cacheExpl  Explanation
}

// NewExplainer builds an Explainer for rule over the given finite
// domains. Domains are copied, sorted and deduplicated.
func NewExplainer(rule *Rule, domains map[string][]int) (*Explainer, error) {
	e := &Explainer{rule: rule}
	if err := e.configure(domains); err != nil {
		return nil, err
	}
	return e, nil
}

// SetDomains replaces the variable domains, invalidating every cached
// outcome and explanation. After a domain shrinks, Explain may return a
// strictly shorter variable set than before.
func (e *Explainer) SetDomains(domains map[string][]int) error {
	return e.configure(domains)
}

func (e *Explainer) configure(domains map[string][]int) error {
	if len(domains) > MaxVars {
		return ErrTooManyVars
	}
	e.names = e.names[:0]
	e.domains = make(map[string][]int, len(domains))
	total := 1
	for name, dom := range domains {
		if len(dom) == 0 {
			return fmt.Errorf("rulexplain: empty domain for variable %q", name)
		}
		cp := append([]int(nil), dom...)
		sort.Ints(cp)
		uniq := cp[:1]
		for _, v := range cp[1:] {
			if v != uniq[len(uniq)-1] {
				uniq = append(uniq, v)
			}
		}
		e.domains[name] = uniq
		e.names = append(e.names, name)
		total *= len(uniq)
		if total > maxAssignments {
			return ErrTooManyAssignments
		}
	}
	sort.Strings(e.names)
	e.sizes = make([]int, len(e.names))
	e.strides = make([]int, len(e.names))
	stride := 1
	for i, name := range e.names {
		e.sizes[i] = len(e.domains[name])
		e.strides[i] = stride
		stride *= e.sizes[i]
	}
	e.total = total
	e.table = nil
	e.cacheValid = false
	return nil
}

// Explain returns a minimal explanation for the current assignment env.
// env must pin every domain variable to a value inside its domain; extra
// keys are allowed and are held fixed as part of the evaluation context.
// Sufficiency and minimality are established by exhaustively enumerating
// all assignments of the unpinned variables, never by tracing a single
// evaluation path.
func (e *Explainer) Explain(env Env) (Explanation, error) {
	pos, err := e.envPositions(env)
	if err != nil {
		return Explanation{}, err
	}
	e.ensureTable()
	key := encodePos(pos)
	if e.cacheValid && e.cacheVer == e.tabVer && e.cacheSig == e.tabSig && e.cacheKey == key {
		return copyExplanation(e.cacheExpl), nil
	}

	n := len(e.names)
	full := 1<<uint(n) - 1
	baseline := e.table[e.indexOf(pos)]

	// For every assignment whose outcome differs from the baseline,
	// record the bitmask of variables on which it disagrees with env.
	// A candidate set S is sufficient iff it intersects every such mask.
	blocked := make([]bool, 1<<uint(n))
	for idx := 0; idx < e.total; idx++ {
		if e.table[idx] == baseline {
			continue
		}
		mask := 0
		for v := 0; v < n; v++ {
			if (idx/e.strides[v])%e.sizes[v] != pos[v] {
				mask |= 1 << uint(v)
			}
		}
		blocked[mask] = true
	}
	// Superset closure: blocked[m] becomes true iff some bad mask is a
	// subset of m, i.e. iff pinning only the complement of m is unsafe.
	for bit := 0; bit < n; bit++ {
		for m := 0; m <= full; m++ {
			if m&(1<<uint(bit)) != 0 && blocked[m^(1<<uint(bit))] {
				blocked[m] = true
			}
		}
	}

	var picked []int
	for size := 0; size <= n && picked == nil; size++ {
		picked = firstCombination(n, size, func(comb []int) bool {
			mask := 0
			for _, v := range comb {
				mask |= 1 << uint(v)
			}
			return !blocked[full^mask]
		})
	}
	expl := Explanation{Outcome: baseline}
	for _, v := range picked {
		expl.Vars = append(expl.Vars, e.names[v])
	}

	e.cacheValid = true
	e.cacheVer = e.tabVer
	e.cacheSig = e.tabSig
	e.cacheKey = key
	e.cacheExpl = copyExplanation(expl)
	return expl, nil
}

// Verify reports, by exhaustive enumeration, whether pinning vars to
// their values in env guarantees the rule outcome stays equal to the
// outcome under env.
func (e *Explainer) Verify(env Env, vars []string) (bool, error) {
	pos, err := e.envPositions(env)
	if err != nil {
		return false, err
	}
	set, err := e.varIndices(vars)
	if err != nil {
		return false, err
	}
	e.ensureTable()
	baseline := e.table[e.indexOf(pos)]
	for idx := 0; idx < e.total; idx++ {
		agree := true
		for _, v := range set {
			if (idx/e.strides[v])%e.sizes[v] != pos[v] {
				agree = false
				break
			}
		}
		if agree && e.table[idx] != baseline {
			return false, nil
		}
	}
	return true, nil
}

// IsMinimal reports whether vars is a sufficient explanation for env and
// no proper subset of vars is sufficient. Every subset is checked by
// full enumeration.
func (e *Explainer) IsMinimal(env Env, vars []string) (bool, error) {
	ok, err := e.Verify(env, vars)
	if err != nil || !ok {
		return false, err
	}
	n := len(vars)
	for mask := 0; mask < 1<<uint(n)-1; mask++ {
		sub := make([]string, 0, n)
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				sub = append(sub, vars[i])
			}
		}
		holds, err := e.Verify(env, sub)
		if err != nil {
			return false, err
		}
		if holds {
			return false, nil
		}
	}
	return true, nil
}

// ensureTable (re)builds the exhaustive outcome table when the rule
// version or the domain signature changed.
func (e *Explainer) ensureTable() {
	sig := e.domainSig()
	if e.table != nil && e.tabVer == e.rule.Version() && e.tabSig == sig {
		return
	}
	e.table = make([]Outcome, e.total)
	env := make(Env, len(e.names))
	var walk func(v, idx int)
	walk = func(v, idx int) {
		if v == len(e.names) {
			e.table[idx] = Evaluate(e.rule.Root(), env)
			return
		}
		name := e.names[v]
		for p, val := range e.domains[name] {
			env[name] = val
			walk(v+1, idx+p*e.strides[v])
		}
	}
	walk(0, 0)
	e.tabVer = e.rule.Version()
	e.tabSig = sig
	e.cacheValid = false
}

func (e *Explainer) domainSig() uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, name := range e.names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		for _, v := range e.domains[name] {
			binary.LittleEndian.PutUint64(buf[:], uint64(int64(v)))
			h.Write(buf[:])
		}
		h.Write([]byte{1})
	}
	return h.Sum64()
}

func (e *Explainer) envPositions(env Env) ([]int, error) {
	pos := make([]int, len(e.names))
	for i, name := range e.names {
		v, ok := env[name]
		if !ok {
			return nil, fmt.Errorf("rulexplain: variable %q has no current value", name)
		}
		p := sort.SearchInts(e.domains[name], v)
		if p >= e.sizes[i] || e.domains[name][p] != v {
			return nil, fmt.Errorf("rulexplain: value %d of variable %q is outside its domain", v, name)
		}
		pos[i] = p
	}
	return pos, nil
}

func (e *Explainer) varIndices(vars []string) ([]int, error) {
	seen := make(map[string]bool, len(vars))
	idx := make([]int, 0, len(vars))
	for _, name := range vars {
		if seen[name] {
			continue
		}
		seen[name] = true
		i := sort.SearchStrings(e.names, name)
		if i >= len(e.names) || e.names[i] != name {
			return nil, fmt.Errorf("rulexplain: unknown variable %q", name)
		}
		idx = append(idx, i)
	}
	return idx, nil
}

func (e *Explainer) indexOf(pos []int) int {
	idx := 0
	for i, p := range pos {
		idx += p * e.strides[i]
	}
	return idx
}

func encodePos(pos []int) string {
	parts := make([]string, len(pos))
	for i, p := range pos {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ",")
}

func copyExplanation(ex Explanation) Explanation {
	return Explanation{Vars: append([]string(nil), ex.Vars...), Outcome: ex.Outcome}
}

// firstCombination enumerates the k-subsets of {0..n-1} in
// lexicographic order and returns the first one accepted by f, or nil.
func firstCombination(n, k int, f func([]int) bool) []int {
	if k < 0 || k > n {
		return nil
	}
	comb := make([]int, k)
	for i := range comb {
		comb[i] = i
	}
	for {
		if f(comb) {
			return append([]int(nil), comb...)
		}
		i := k - 1
		for i >= 0 && comb[i] == n-k+i {
			i--
		}
		if i < 0 {
			return nil
		}
		comb[i]++
		for j := i + 1; j < k; j++ {
			comb[j] = comb[j-1] + 1
		}
	}
}
