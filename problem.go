// Package main implements an exact solver for finite-horizon decision
// problems with partial observability.
//
// A problem consists of finitely many worlds with rational probabilities,
// an observable signal per world, and a rational loss for every
// (world, action) pair. A policy maps each signal to a single action:
// worlds that emit the same signal are indistinguishable and must therefore
// share the same action (the non-anticipativity rule). Actions may carry a
// shared capacity: the total probability mass of the worlds mapped to an
// action must not exceed its capacity. The objective is lexicographic:
// first minimize the worst-case loss, then the expected (average) loss.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"os"
)

// World is one possible state of nature.
type World struct {
	Name   string
	Prob   *big.Rat
	Signal string
}

// Problem is the fully parsed decision problem.
type Problem struct {
	Worlds  []World
	Actions []string
	// Loss[w][a] is the loss of action a in world w.
	Loss [][]*big.Rat
	// Capacity[a] bounds the total probability mass assigned to action a.
	// A nil entry means the action is uncapacitated.
	Capacity []*big.Rat

	signals        []string
	signalIndex    map[string]int
	worldSignal    []int // world -> signal index
	worldsBySignal [][]int
}

// Signals returns the distinct signal names in first-seen order.
func (p *Problem) Signals() []string { return p.signals }

// NumSignals returns the number of distinct signals.
func (p *Problem) NumSignals() int { return len(p.signals) }

// SignalOf returns the signal index emitted by world w.
func (p *Problem) SignalOf(w int) int { return p.worldSignal[w] }

// WorldsWithSignal returns the indices of worlds emitting signal s.
func (p *Problem) WorldsWithSignal(s int) []int { return p.worldsBySignal[s] }

// --- JSON input format -----------------------------------------------------

type jsonWorld struct {
	Name   string `json:"name"`
	Prob   string `json:"prob"`
	Signal string `json:"signal"`
}

type jsonProblem struct {
	Worlds   []jsonWorld                  `json:"worlds"`
	Actions  []string                     `json:"actions"`
	Losses   map[string]map[string]string `json:"losses"`
	Capacity map[string]string            `json:"capacity"`
}

func parseRat(s, what string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid rational %q for %s", s, what)
	}
	return r, nil
}

// LoadProblem reads a JSON problem definition from r.
func LoadProblem(r io.Reader) (*Problem, error) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var jp jsonProblem
	if err := json.Unmarshal(data, &jp); err != nil {
		return nil, fmt.Errorf("parsing problem JSON: %v", err)
	}
	if len(jp.Worlds) == 0 {
		return nil, fmt.Errorf("problem has no worlds")
	}
	if len(jp.Actions) == 0 {
		return nil, fmt.Errorf("problem has no actions")
	}
	actionIndex := make(map[string]int, len(jp.Actions))
	for i, a := range jp.Actions {
		if _, dup := actionIndex[a]; dup {
			return nil, fmt.Errorf("duplicate action %q", a)
		}
		actionIndex[a] = i
	}

	p := &Problem{
		Actions:     append([]string(nil), jp.Actions...),
		signalIndex: make(map[string]int),
	}

	totalProb := new(big.Rat)
	for i, jw := range jp.Worlds {
		prob, err := parseRat(jw.Prob, fmt.Sprintf("probability of world %q", jw.Name))
		if err != nil {
			return nil, err
		}
		if prob.Sign() < 0 {
			return nil, fmt.Errorf("world %q has negative probability", jw.Name)
		}
		if jw.Signal == "" {
			return nil, fmt.Errorf("world %q has empty signal", jw.Name)
		}
		w := World{Name: jw.Name, Prob: prob, Signal: jw.Signal}
		if w.Name == "" {
			w.Name = fmt.Sprintf("w%d", i)
		}
		p.Worlds = append(p.Worlds, w)
		totalProb.Add(totalProb, prob)

		si, ok := p.signalIndex[w.Signal]
		if !ok {
			si = len(p.signals)
			p.signalIndex[w.Signal] = si
			p.signals = append(p.signals, w.Signal)
			p.worldsBySignal = append(p.worldsBySignal, nil)
		}
		p.worldSignal = append(p.worldSignal, si)
		p.worldsBySignal[si] = append(p.worldsBySignal[si], i)
	}
	if totalProb.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, fmt.Errorf("world probabilities sum to %s, want 1", totalProb.RatString())
	}

	p.Loss = make([][]*big.Rat, len(p.Worlds))
	for i, w := range p.Worlds {
		row, ok := jp.Losses[w.Name]
		if !ok {
			return nil, fmt.Errorf("missing losses for world %q", w.Name)
		}
		p.Loss[i] = make([]*big.Rat, len(p.Actions))
		for a, an := range p.Actions {
			s, ok := row[an]
			if !ok {
				return nil, fmt.Errorf("missing loss for world %q action %q", w.Name, an)
			}
			l, err := parseRat(s, fmt.Sprintf("loss of world %q action %q", w.Name, an))
			if err != nil {
				return nil, err
			}
			p.Loss[i][a] = l
		}
	}

	p.Capacity = make([]*big.Rat, len(p.Actions))
	for an, cs := range jp.Capacity {
		ai, ok := actionIndex[an]
		if !ok {
			return nil, fmt.Errorf("capacity given for unknown action %q", an)
		}
		c, err := parseRat(cs, fmt.Sprintf("capacity of action %q", an))
		if err != nil {
			return nil, err
		}
		if c.Sign() < 0 {
			return nil, fmt.Errorf("action %q has negative capacity", an)
		}
		p.Capacity[ai] = c
	}
	return p, nil
}

// LoadProblemFile reads a JSON problem definition from a file path,
// or from standard input when path is "-".
func LoadProblemFile(path string) (*Problem, error) {
	if path == "-" {
		return LoadProblem(os.Stdin)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadProblem(f)
}

// --- Policy evaluation -----------------------------------------------------

// Policy maps signal index -> action index. It has one entry per signal,
// which structurally enforces the non-anticipativity rule: worlds sharing
// a signal always receive the same action.
type Policy []int

// Eval is the exact evaluation of a policy.
type Eval struct {
	// Usage[a] is the total probability mass of worlds assigned action a.
	Usage []*big.Rat
	// Worst is the maximum loss over all worlds.
	Worst *big.Rat
	// Avg is the probability-weighted average loss.
	Avg *big.Rat
	// Feasible reports whether every capacity constraint holds.
	Feasible bool
}

// Evaluate computes the exact objective values of a policy.
func (p *Problem) Evaluate(pol Policy) (*Eval, error) {
	if len(pol) != p.NumSignals() {
		return nil, fmt.Errorf("policy has %d entries, want %d (one per signal)", len(pol), p.NumSignals())
	}
	e := &Eval{Usage: make([]*big.Rat, len(p.Actions)), Worst: new(big.Rat), Avg: new(big.Rat), Feasible: true}
	for a := range p.Actions {
		e.Usage[a] = new(big.Rat)
	}
	first := true
	for w := range p.Worlds {
		a := pol[p.worldSignal[w]]
		if a < 0 || a >= len(p.Actions) {
			return nil, fmt.Errorf("policy assigns invalid action index %d", a)
		}
		e.Usage[a].Add(e.Usage[a], p.Worlds[w].Prob)
		loss := p.Loss[w][a]
		if first || loss.Cmp(e.Worst) > 0 {
			e.Worst.Set(loss)
			first = false
		}
		e.Avg.Add(e.Avg, new(big.Rat).Mul(p.Worlds[w].Prob, loss))
	}
	for a := range p.Actions {
		if p.Capacity[a] != nil && e.Usage[a].Cmp(p.Capacity[a]) > 0 {
			e.Feasible = false
		}
	}
	return e, nil
}

// betterLex reports whether (w1, a1) is lexicographically strictly better
// than (w2, a2): smaller worst case first, then smaller average.
func betterLex(w1, a1, w2, a2 *big.Rat) bool {
	if c := w1.Cmp(w2); c != 0 {
		return c < 0
	}
	return a1.Cmp(a2) < 0
}
