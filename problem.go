// Package policytree solves finite-horizon decision problems under partial
// observability: up to 16 possible worlds, up to 8 terminal actions, and at
// most 2 adaptive probes before acting. It minimizes the worst-case total
// loss (probe costs plus action loss), breaking ties by expected total loss
// under rational per-world probabilities.
package policytree

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
)

const (
	// MaxWorlds is the largest supported number of possible worlds.
	MaxWorlds = 16
	// MaxActions is the largest supported number of terminal actions.
	MaxActions = 8
	// MaxProbeDepth is the largest supported number of probes before acting.
	MaxProbeDepth = 2
)

// Action is a terminal decision. Taking it ends the episode: the agent pays
// Losses[w] in world w and then observes Symbols[w] (the symbol is observed
// after the decision, so it cannot influence the policy itself).
type Action struct {
	Name    string
	Losses  []int64
	Symbols []string
}

// Probe is a non-terminal test. Performing it costs Cost (paid once, not per
// world) and reveals Outcomes[w] in world w. Probes may be chosen adaptively:
// which probe to use next may depend on earlier probe outcomes.
type Probe struct {
	Name     string
	Cost     int64
	Outcomes []string
}

// Outcome returns the symbol the probe reveals in world w.
func (pr Probe) Outcome(w int) string { return pr.Outcomes[w] }

// Problem is a fully specified decision problem. Weights holds a positive
// rational weight per world; it is interpreted as a probability up to
// normalization, so it need not sum to one.
type Problem struct {
	Worlds    []string
	Weights   []*big.Rat
	Actions   []Action
	Probes    []Probe
	MaxProbes int
}

// NumWorlds returns the number of possible worlds.
func (p *Problem) NumWorlds() int { return len(p.Worlds) }

// TotalWeight returns the sum of all world weights.
func (p *Problem) TotalWeight() *big.Rat {
	total := new(big.Rat)
	for _, w := range p.Weights {
		total.Add(total, w)
	}
	return total
}

// Probability returns the normalized probability of world w.
func (p *Problem) Probability(w int) *big.Rat {
	return new(big.Rat).Quo(p.Weights[w], p.TotalWeight())
}

// Validate checks structural constraints of the problem.
func (p *Problem) Validate() error {
	n := len(p.Worlds)
	if n == 0 || n > MaxWorlds {
		return fmt.Errorf("need 1..%d worlds, got %d", MaxWorlds, n)
	}
	if len(p.Weights) != n {
		return fmt.Errorf("need %d weights, got %d", n, len(p.Weights))
	}
	total := new(big.Rat)
	for i, w := range p.Weights {
		if w == nil || w.Sign() < 0 {
			return fmt.Errorf("world %d (%s): weight must be a non-negative rational", i, p.Worlds[i])
		}
		total.Add(total, w)
	}
	if total.Sign() == 0 {
		return fmt.Errorf("world weights must not all be zero")
	}
	if len(p.Actions) == 0 || len(p.Actions) > MaxActions {
		return fmt.Errorf("need 1..%d actions, got %d", MaxActions, len(p.Actions))
	}
	for i, a := range p.Actions {
		if len(a.Losses) != n {
			return fmt.Errorf("action %d (%s): need %d losses, got %d", i, a.Name, n, len(a.Losses))
		}
		if len(a.Symbols) != n {
			return fmt.Errorf("action %d (%s): need %d symbols, got %d", i, a.Name, n, len(a.Symbols))
		}
	}
	for i, pr := range p.Probes {
		if len(pr.Outcomes) != n {
			return fmt.Errorf("probe %d (%s): need %d outcomes, got %d", i, pr.Name, n, len(pr.Outcomes))
		}
	}
	if p.MaxProbes < 0 || p.MaxProbes > MaxProbeDepth {
		return fmt.Errorf("MaxProbes must be in 0..%d, got %d", MaxProbeDepth, p.MaxProbes)
	}
	return nil
}

type jsonProblem struct {
	Worlds []struct {
		Name string `json:"name"`
		Prob string `json:"prob"`
	} `json:"worlds"`
	Actions []struct {
		Name    string   `json:"name"`
		Losses  []int64  `json:"losses"`
		Symbols []string `json:"symbols"`
	} `json:"actions"`
	Probes []struct {
		Name     string   `json:"name"`
		Cost     int64    `json:"cost"`
		Outcomes []string `json:"outcomes"`
	} `json:"probes"`
	MaxProbes int `json:"maxProbes"`
}

// Load parses a problem from JSON. World probabilities are rational strings
// such as "1/3", "2/7" or plain integers/decimals; they are normalized
// internally, so they only need to be proportional to the true probabilities.
func Load(r io.Reader) (*Problem, error) {
	var jp jsonProblem
	if err := json.NewDecoder(r).Decode(&jp); err != nil {
		return nil, fmt.Errorf("decoding problem: %w", err)
	}
	p := &Problem{MaxProbes: jp.MaxProbes}
	for i, w := range jp.Worlds {
		weight, ok := new(big.Rat).SetString(w.Prob)
		if !ok {
			return nil, fmt.Errorf("world %d: invalid rational probability %q", i, w.Prob)
		}
		name := w.Name
		if name == "" {
			name = fmt.Sprintf("w%d", i)
		}
		p.Worlds = append(p.Worlds, name)
		p.Weights = append(p.Weights, weight)
	}
	for i, a := range jp.Actions {
		name := a.Name
		if name == "" {
			name = fmt.Sprintf("a%d", i)
		}
		p.Actions = append(p.Actions, Action{Name: name, Losses: a.Losses, Symbols: a.Symbols})
	}
	for i, pr := range jp.Probes {
		name := pr.Name
		if name == "" {
			name = fmt.Sprintf("p%d", i)
		}
		p.Probes = append(p.Probes, Probe{Name: name, Cost: pr.Cost, Outcomes: pr.Outcomes})
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}
