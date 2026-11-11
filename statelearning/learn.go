package statelearning

import (
	"errors"
	"fmt"
)

// Learn runs L* against sul and returns the inferred model together with the
// validation status. SUL failures are surfaced in Result.Err; an exhausted
// query budget yields StatusIncomplete with ErrBudgetExhausted wrapped.
func Learn(sul SUL, cfg Config) Result {
	if sul == nil {
		return Result{Err: errors.New("statelearning: nil SUL")}
	}
	if len(cfg.Alphabet) == 0 {
		return Result{Err: errors.New("statelearning: empty alphabet")}
	}
	if cfg.MaxDepth < 0 {
		return Result{Err: errors.New("statelearning: MaxDepth must be non-negative")}
	}
	seenNames := make(map[string]struct{}, len(cfg.Alphabet))
	for _, in := range cfg.Alphabet {
		if _, dup := seenNames[in.Name]; dup {
			return Result{Err: fmt.Errorf("statelearning: duplicate alphabet symbol %q", in.Name)}
		}
		seenNames[in.Name] = struct{}{}
	}

	l := &learner{
		sul:        sul,
		alphabet:   cfg.Alphabet,
		maxDepth:   cfg.MaxDepth,
		maxQueries: cfg.MaxQueries,
		cache:      make(map[string][]Output),
		short:      [][]int{{}},
		suffix:     make([][]int, 0, len(cfg.Alphabet)),
		rowSeen:    make(map[string]int),
	}
	for idx := range cfg.Alphabet {
		l.suffix = append(l.suffix, []int{idx})
	}
	// Initialize SA with the one-symbol extensions of the empty access
	// sequence, giving the closure check its initial candidate rows.
	for idx := range cfg.Alphabet {
		l.long = append(l.long, []int{idx})
	}

	for {
		if err := l.fillTable(); err != nil {
			return l.incomplete(err)
		}

		if moved, err := l.makeClosed(); err != nil {
			return l.incomplete(err)
		} else if moved {
			continue
		}
		if moved, err := l.makeConsistent(); err != nil {
			return l.incomplete(err)
		} else if moved {
			continue
		}

		model, err := l.buildHypothesis()
		if err != nil {
			return l.incomplete(err)
		}

		counterexample, depth, err := l.findCounterexample(model)
		if err != nil {
			return Result{
				Status:       StatusIncomplete,
				Model:        model,
				Queries:      l.queries,
				DepthReached: depth,
				Err:          err,
			}
		}
		if counterexample == nil {
			return Result{
				Status:       StatusComplete,
				Model:        model,
				Queries:      l.queries,
				DepthReached: l.maxDepth,
			}
		}

		// Feed the counterexample suffixes back as new distinguishing
		// suffixes so observationally equal rows split on the next round.
		l.addSuffixes(counterexample)
	}
}

// incomplete builds a result for a failure encountered while the table was
// being constructed, where no hypothesis exists yet.
func (l *learner) incomplete(err error) Result {
	return Result{Status: StatusIncomplete, Queries: l.queries, Err: err}
}

// buildHypothesis turns a closed, consistent table into a Mealy machine.
func (l *learner) buildHypothesis() (*MealyMachine, error) {
	model := &MealyMachine{
		alphabet:  l.alphabet,
		trans:     make(map[int]map[string]mealyEdge),
		stateName: make(map[int]string),
	}

	stateOf := make(map[string]int)
	for stateID, prefix := range l.short {
		sig, err := l.rowSignature(prefix)
		if err != nil {
			return nil, err
		}
		stateOf[sig] = stateID
		model.stateName[stateID] = "q" + fmt.Sprint(stateID)
		model.trans[stateID] = make(map[string]mealyEdge)
	}

	for stateID, prefix := range l.short {
		for sym, in := range l.alphabet {
			ext := appendWord(prefix, sym)
			sig, err := l.rowSignature(ext)
			if err != nil {
				return nil, err
			}
			target, ok := stateOf[sig]
			if !ok {
				return nil, errors.New("statelearning: table is not closed while building hypothesis")
			}
			out, err := l.cell(prefix, []int{sym})
			if err != nil {
				return nil, err
			}
			model.trans[stateID][in.Name] = mealyEdge{to: target, output: out[0]}
		}
	}
	return model, nil
}

// findCounterexample exhaustively replays every word up to maxDepth on both
// the SUL and the hypothesis. It returns the first disagreeing word. A budget
// failure aborts the search and is returned together with the depth reached.
func (l *learner) findCounterexample(model *MealyMachine) ([]int, int, error) {
	type frame struct {
		word  []int
		state int
	}

	frontier := []frame{{word: []int{}, state: 0}}
	depthReached := 0

	for depth := 0; depth <= l.maxDepth; depth++ {
		next := make([]frame, 0, len(frontier)*len(l.alphabet))
		for _, f := range frontier {
			if depth > 0 {
				sulTrace, err := l.query(f.word)
				if err != nil {
					return nil, depthReached, err
				}
				modelTrace, err := traceIndices(model, f.word)
				if err != nil {
					return nil, depthReached, err
				}
				if !outputsEqual(sulTrace, modelTrace) {
					return append([]int(nil), f.word...), depth, nil
				}
			}

			if depth == l.maxDepth {
				continue
			}
			for sym := range l.alphabet {
				edge, ok := model.trans[f.state][l.alphabet[sym].Name]
				if !ok {
					return nil, depthReached, fmt.Errorf("statelearning: hypothesis lacks transition for %q in state %d", l.alphabet[sym].Name, f.state)
				}
				next = append(next, frame{
					word:  appendWord(f.word, sym),
					state: edge.to,
				})
			}
		}
		depthReached = depth
		frontier = next
	}
	return nil, l.maxDepth, nil
}

// traceIndices replays an index-based word on the hypothesis.
func traceIndices(model *MealyMachine, word []int) ([]Output, error) {
	inputs := make([]Input, len(word))
	for i, idx := range word {
		inputs[i] = model.alphabet[idx]
	}
	return model.Trace(inputs)
}
