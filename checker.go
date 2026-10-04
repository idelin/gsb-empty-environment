package ambcheck

import (
	"fmt"
	"sort"
	"strings"
)

// Options controls a Check run.
type Options struct {
	// MaxLength is the maximum string length N considered.
	MaxLength int `json:"maxLength"`
	// Budget is the maximum number of expanded states (candidate
	// (nonterminal, string, tree) insertions). Zero or negative means
	// unlimited.
	Budget int `json:"budget"`
}

// Result is the outcome of a Check run.
type Result struct {
	// Ambiguous is true when a string with two distinct parse trees was
	// found within MaxLength.
	Ambiguous bool `json:"ambiguous"`
	// BudgetExceeded is true when the run stopped early because the state
	// budget was exhausted; the absence of a reported ambiguity is then
	// inconclusive.
	BudgetExceeded bool `json:"budgetExceeded"`
	// String is the shortest ambiguous string (ties broken by terminal
	// order), as a sequence of terminal symbols.
	String []string `json:"string,omitempty"`
	// Tree1 and Tree2 are two structurally distinct parse trees for
	// String, returned for independent verification.
	Tree1          *Tree `json:"tree1,omitempty"`
	Tree2          *Tree `json:"tree2,omitempty"`
	StatesExpanded int   `json:"statesExpanded"`
}

// Verify independently validates a positive result: both trees must be
// valid parse trees of g, both must derive String, and they must be
// structurally distinct.
func (r *Result) Verify(g *Grammar) error {
	if !r.Ambiguous {
		return fmt.Errorf("result reports no ambiguity")
	}
	if r.Tree1 == nil || r.Tree2 == nil {
		return fmt.Errorf("result is missing parse trees")
	}
	y1, err := VerifyTree(g, r.Tree1)
	if err != nil {
		return fmt.Errorf("tree1 invalid: %v", err)
	}
	y2, err := VerifyTree(g, r.Tree2)
	if err != nil {
		return fmt.Errorf("tree2 invalid: %v", err)
	}
	if !equalStrings(y1, r.String) {
		return fmt.Errorf("tree1 yields %q, not %q", y1, r.String)
	}
	if !equalStrings(y2, r.String) {
		return fmt.Errorf("tree2 yields %q, not %q", y2, r.String)
	}
	if r.Tree1.Canonical() == r.Tree2.Canonical() {
		return fmt.Errorf("tree1 and tree2 are structurally identical")
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cell holds the shared-forest entries for one (nonterminal, string) pair:
// up to two structurally distinct parse trees. Keeping two is sufficient:
// any ambiguity at a constituent propagates to the parent, and any
// ambiguity introduced at the parent is discovered there directly.
type cell struct {
	str   []string
	trees []*Tree
	seen  map[string]struct{} // canonical forms already stored
}

// checker accumulates, for every nonterminal A and length L, the set of
// terminal strings of length L derivable from A together with up to two
// distinct parse trees per string (the shared parse forest).
type checker struct {
	g        *Grammar
	budget   int
	states   int
	terminal map[string]bool
	rank     map[string]int
	rules    []Rule
	unit     []Rule // rules of the form A -> B with B a nonterminal
	// derived[A][L] maps a string key to its cell.
	derived map[string]map[int]map[string]*cell
}

// Check searches strings of length 1..opts.MaxLength, shortest first, for
// one with two structurally distinct parse trees. Strings of equal length
// are considered in the grammar's terminal order. Construction paths that
// build the same tree (e.g. via duplicate rules) are deduplicated in the
// shared forest and never reported as ambiguity.
func Check(g *Grammar, opts Options) (*Result, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if opts.MaxLength < 1 {
		return nil, fmt.Errorf("MaxLength must be >= 1")
	}
	c := &checker{
		g:        g,
		budget:   opts.Budget,
		terminal: make(map[string]bool),
		rank:     g.terminalRank(),
		derived:  make(map[string]map[int]map[string]*cell),
	}
	for _, t := range g.Terminals {
		c.terminal[t] = true
	}
	for _, r := range g.Rules {
		if len(r.RHS) == 1 && !c.terminal[r.RHS[0]] {
			c.unit = append(c.unit, r)
		} else {
			c.rules = append(c.rules, r)
		}
	}
	for length := 1; length <= opts.MaxLength; length++ {
		if c.expandLength(length) || c.unitClosure(length) {
			return &Result{BudgetExceeded: true, StatesExpanded: c.states}, nil
		}
		if res := c.checkStart(length); res != nil {
			res.StatesExpanded = c.states
			return res, nil
		}
	}
	return &Result{StatesExpanded: c.states}, nil
}

// insert adds (str, tree) to the cell of (lhs, len(str)), deduplicated by
// the tree's canonical form. It reports whether the tree was new and
// whether the state budget is now exceeded. Every call counts as one
// expanded state.
func (c *checker) insert(lhs string, str []string, tree *Tree) (inserted, overBudget bool) {
	c.states++
	if c.budget > 0 && c.states > c.budget {
		return false, true
	}
	byLen := c.derived[lhs]
	if byLen == nil {
		byLen = make(map[int]map[string]*cell)
		c.derived[lhs] = byLen
	}
	key := strKey(str)
	cells := byLen[len(str)]
	if cells == nil {
		cells = make(map[string]*cell)
		byLen[len(str)] = cells
	}
	cl := cells[key]
	if cl == nil {
		cells[key] = &cell{str: str, trees: []*Tree{tree}, seen: map[string]struct{}{tree.Canonical(): {}}}
		return true, false
	}
	canon := tree.Canonical()
	if _, dup := cl.seen[canon]; dup {
		return false, false // same tree via another construction path
	}
	if len(cl.trees) >= 2 {
		return false, false // two distinct trees already suffice
	}
	cl.seen[canon] = struct{}{}
	cl.trees = append(cl.trees, tree)
	return true, false
}

// expandLength generates all (string, tree) candidates of the given length
// from non-unit rules. Because there are no epsilon productions, every
// nonterminal child of a non-unit rule derives a strictly shorter string,
// so all required sub-results are already complete.
func (c *checker) expandLength(length int) (overBudget bool) {
	for _, r := range c.rules {
		if len(r.RHS) > length {
			continue // every RHS symbol contributes at least one terminal
		}
		if c.expandRule(r, length) {
			return true
		}
	}
	return false
}

func (c *checker) expandRule(r Rule, length int) (overBudget bool) {
	rhs := r.RHS
	var rec func(pos, rem int, str []string, children []*Tree) bool
	rec = func(pos, rem int, str []string, children []*Tree) bool {
		if pos == len(rhs) {
			if rem != 0 {
				return false
			}
			_, over := c.insert(r.LHS, str, Node(r.LHS, rhs, children...))
			return over
		}
		sym := rhs[pos]
		if c.terminal[sym] {
			if rem < 1 {
				return false
			}
			nextStr := append(append([]string{}, str...), sym)
			nextChildren := append(append([]*Tree{}, children...), Leaf(sym))
			return rec(pos+1, rem-1, nextStr, nextChildren)
		}
		// Reserve at least one terminal for each remaining RHS symbol.
		minRest := len(rhs) - pos - 1
		for childLen := 1; childLen+minRest <= rem; childLen++ {
			for _, cl := range c.derived[sym][childLen] {
				for _, sub := range cl.trees {
					nextStr := append(append([]string{}, str...), cl.str...)
					nextChildren := append(append([]*Tree{}, children...), sub)
					if rec(pos+1, rem-childLen, nextStr, nextChildren) {
						return true
					}
				}
			}
		}
		return false
	}
	return rec(0, length, nil, nil)
}

// unitClosure propagates strings and trees through unit productions
// (A -> B) within the current length, iterating to a fixpoint so that
// cycles such as A -> A are handled. Each cell holds at most two distinct
// trees, so the fixpoint always terminates.
func (c *checker) unitClosure(length int) (overBudget bool) {
	for changed := true; changed; {
		changed = false
		for _, r := range c.unit {
			lhs, body := r.LHS, r.RHS[0]
			// Snapshot before inserting: lhs and body may coincide.
			type entry struct {
				str   []string
				trees []*Tree
			}
			var entries []entry
			for _, cl := range c.derived[body][length] {
				entries = append(entries, entry{cl.str, append([]*Tree{}, cl.trees...)})
			}
			for _, e := range entries {
				for _, sub := range e.trees {
					inserted, over := c.insert(lhs, e.str, Node(lhs, r.RHS, sub))
					if over {
						return true
					}
					if inserted {
						changed = true
					}
				}
			}
		}
	}
	return false
}

// checkStart inspects all strings of the given length derivable from the
// start symbol, in terminal order, and returns a positive result for the
// first one with two distinct parse trees.
func (c *checker) checkStart(length int) *Result {
	cells := c.derived[c.g.Start][length]
	strs := make([][]string, 0, len(cells))
	for _, cl := range cells {
		strs = append(strs, cl.str)
	}
	sort.Slice(strs, func(i, j int) bool {
		return lessByTerminalOrder(strs[i], strs[j], c.rank)
	})
	for _, s := range strs {
		cl := cells[strKey(s)]
		if len(cl.trees) >= 2 {
			return &Result{
				Ambiguous: true,
				String:    s,
				Tree1:     cl.trees[0],
				Tree2:     cl.trees[1],
			}
		}
	}
	return nil
}

func strKey(str []string) string { return strings.Join(str, "\x00") }
