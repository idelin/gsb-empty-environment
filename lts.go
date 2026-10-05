package main

import (
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
)

// MaxStates bounds each LTS as required (at most 30 states).
const MaxStates = 30

// IsTau reports whether a label denotes an internal (silent) action.
func IsTau(label string) bool { return label == "tau" || label == "τ" }

// Step is a single concrete transition.
type Step struct {
	From  int
	Label string
	To    int
}

// LTS is a finite labelled transition system.
type LTS struct {
	N     int
	Start int
	Out   [][]Step
}

func NewLTS(n, start int) *LTS {
	return &LTS{N: n, Start: start, Out: make([][]Step, n)}
}

func (l *LTS) Add(from int, label string, to int) {
	l.Out[from] = append(l.Out[from], Step{From: from, Label: label, To: to})
}

// TauClosure returns all states reachable from s via zero or more internal
// steps. Cycles (internal loops) are handled by the visited set and never
// count as visible progress.
func (l *LTS) TauClosure(s int) []int {
	seen := make([]bool, l.N)
	seen[s] = true
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, e := range l.Out[u] {
			if IsTau(e.Label) && !seen[e.To] {
				seen[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
	var res []int
	for i, ok := range seen {
		if ok {
			res = append(res, i)
		}
	}
	return res
}

// WeakTargets returns all t with s =label=> t, i.e. tau* . label . tau*.
// For label "tau" this is just the tau closure (zero or more steps).
func (l *LTS) WeakTargets(s int, label string) []int {
	seen := make([]bool, l.N)
	if IsTau(label) {
		for _, u := range l.TauClosure(s) {
			seen[u] = true
		}
	} else {
		for _, u := range l.TauClosure(s) {
			for _, e := range l.Out[u] {
				if e.Label == label {
					for _, v := range l.TauClosure(e.To) {
						seen[v] = true
					}
				}
			}
		}
	}
	var res []int
	for i, ok := range seen {
		if ok {
			res = append(res, i)
		}
	}
	return res
}

// FindWeakPath returns a concrete sequence of steps witnessing
// s =label=> t (tau* . label . tau*), or nil if none exists.
func (l *LTS) FindWeakPath(s, t int, label string) []Step {
	if IsTau(label) {
		return l.findTauPath(s, t)
	}
	type node struct{ state, phase int } // phase 0: before visible step, 1: after
	type parent struct {
		prev node
		st   Step
	}
	start := node{s, 0}
	goal := node{t, 1}
	visited := map[node]bool{start: true}
	prev := map[node]parent{}
	queue := []node{start}
	found := false
	for len(queue) > 0 && !found {
		u := queue[0]
		queue = queue[1:]
		for _, e := range l.Out[u.state] {
			var v node
			switch {
			case IsTau(e.Label):
				v = node{e.To, u.phase}
			case u.phase == 0 && e.Label == label:
				v = node{e.To, 1}
			default:
				continue
			}
			if visited[v] {
				continue
			}
			visited[v] = true
			prev[v] = parent{u, e}
			if v == goal {
				found = true
				break
			}
			queue = append(queue, v)
		}
	}
	if !found {
		return nil
	}
	var rev []Step
	for cur := goal; cur != start; {
		p := prev[cur]
		rev = append(rev, p.st)
		cur = p.prev
	}
	path := make([]Step, len(rev))
	for i, st := range rev {
		path[len(rev)-1-i] = st
	}
	return path
}

// findTauPath returns a concrete tau-only path from s to t (nil if s == t
// or unreachable).
func (l *LTS) findTauPath(s, t int) []Step {
	if s == t {
		return nil
	}
	prev := map[int]Step{}
	visited := map[int]bool{s: true}
	queue := []int{s}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, e := range l.Out[u] {
			if !IsTau(e.Label) || visited[e.To] {
				continue
			}
			visited[e.To] = true
			prev[e.To] = e
			if e.To == t {
				var rev []Step
				for cur := t; cur != s; {
					st := prev[cur]
					rev = append(rev, st)
					cur = st.From
				}
				path := make([]Step, len(rev))
				for i, st := range rev {
					path[len(rev)-1-i] = st
				}
				return path
			}
			queue = append(queue, e.To)
		}
	}
	return nil
}

// Parse reads an LTS from text:
//
//	<numStates> <startState>          (first non-comment line)
//	<from> <label> <to>               (one transition per line)
//
// Lines starting with '#' and blank lines are ignored. The label "tau"
// (or "τ") is the internal action; every other label is visible.
func Parse(text string) (*LTS, error) {
	var lts *LTS
	for ln, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if lts == nil {
			if len(f) != 2 {
				return nil, fmt.Errorf("line %d: want '<numStates> <startState>'", ln+1)
			}
			n, err1 := strconv.Atoi(f[0])
			st, err2 := strconv.Atoi(f[1])
			if err1 != nil || err2 != nil || n < 1 || n > MaxStates || st < 0 || st >= n {
				return nil, fmt.Errorf("line %d: invalid header (states must be 1..%d, start in range)", ln+1, MaxStates)
			}
			lts = NewLTS(n, st)
			continue
		}
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: want '<from> <label> <to>'", ln+1)
		}
		from, err1 := strconv.Atoi(f[0])
		to, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || from < 0 || from >= lts.N || to < 0 || to >= lts.N {
			return nil, fmt.Errorf("line %d: invalid transition", ln+1)
		}
		lts.Add(from, f[1], to)
	}
	if lts == nil {
		return nil, fmt.Errorf("empty input")
	}
	return lts, nil
}

func ParseFile(path string) (*LTS, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(data))
}
