package statelearning

import "fmt"

// wordKey encodes an index-based input word as a collision-free string key.
func wordKey(word []int) string {
	key := make([]byte, 0, len(word)*2)
	for _, idx := range word {
		if idx >= 0 && idx < 255 {
			key = append(key, byte(idx))
			continue
		}
		key = append(key, 0xFF)
		key = append(key, byte(idx>>8), byte(idx))
	}
	return string(key)
}

// learner holds the observation table and bookkeeping for one run.
type learner struct {
	sul        SUL
	alphabet   []Input
	maxDepth   int
	maxQueries int
	queries    int
	cache      map[string][]Output

	// short and long are access sequences (rows); suffixes are the
	// distinguishing suffixes (columns). All are index-based words.
	short  [][]int
	long   [][]int
	suffix [][]int

	rowSeen map[string]int
}

// query performs one membership query: reset the SUL, replay the word, and
// return the per-step outputs. Results are cached so each distinct word costs
// at most one query.
func (l *learner) query(word []int) ([]Output, error) {
	key := wordKey(word)
	if out, ok := l.cache[key]; ok {
		return out, nil
	}
	if l.maxQueries > 0 && l.queries >= l.maxQueries {
		return nil, budgetError(l.queries)
	}

	if err := l.sul.Reset(); err != nil {
		return nil, fmt.Errorf("statelearning: SUL reset failed: %w", err)
	}
	l.queries++

	out := make([]Output, 0, len(word))
	for _, idx := range word {
		o, err := l.sul.Step(l.alphabet[idx])
		if err != nil {
			return nil, fmt.Errorf("statelearning: SUL step %q failed: %w", l.alphabet[idx].Name, err)
		}
		out = append(out, o)
	}
	l.cache[key] = out
	return out, nil
}

func budgetError(queries int) error {
	return fmt.Errorf("%w after %d queries", ErrBudgetExhausted, queries)
}

// cell returns the output observed when suffix is applied after prefix, that
// is the last len(suffix) outputs of query(prefix+suffix).
func (l *learner) cell(prefix, suffix []int) ([]Output, error) {
	word := make([]int, 0, len(prefix)+len(suffix))
	word = append(word, prefix...)
	word = append(word, suffix...)
	out, err := l.query(word)
	if err != nil {
		return nil, err
	}
	return append([]Output(nil), out[len(prefix):]...), nil
}

// rowSignature computes the full row of a prefix across all suffixes.
func (l *learner) rowSignature(prefix []int) (string, error) {
	var sig string
	for _, suffix := range l.suffix {
		c, err := l.cell(prefix, suffix)
		if err != nil {
			return "", err
		}
		sig += "|" + outputsKey(c)
	}
	return sig, nil
}

// outputsKey renders an output sequence as a comparable signature fragment.
func outputsKey(out []Output) string {
	key := make([]byte, 0, len(out)*8)
	for _, o := range out {
		if o.Authorized {
			key = append(key, '1', ':')
		} else {
			key = append(key, '0', ':')
		}
		key = append(key, o.Response...)
		key = append(key, ';')
	}
	return string(key)
}

func outputsEqual(a, b []Output) bool {
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

func appendWord(prefix []int, sym int) []int {
	next := make([]int, 0, len(prefix)+1)
	next = append(next, prefix...)
	return append(next, sym)
}

func wordsEqual(a, b []int) bool {
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

func (l *learner) hasSuffix(s []int) bool {
	for _, existing := range l.suffix {
		if wordsEqual(existing, s) {
			return true
		}
	}
	return false
}

// fillTable ensures every short and long row has a recorded signature, and
// builds the signature-to-short-row index used by closure/consistency checks.
func (l *learner) fillTable() error {
	l.rowSeen = make(map[string]int)
	for stateID, prefix := range l.short {
		sig, err := l.rowSignature(prefix)
		if err != nil {
			return err
		}
		l.rowSeen[sig] = stateID
	}
	for _, prefix := range l.long {
		if _, err := l.rowSignature(prefix); err != nil {
			return err
		}
	}
	return nil
}

// makeClosed promotes a long row whose signature matches no short row.
func (l *learner) makeClosed() (bool, error) {
	for _, prefix := range l.long {
		sig, err := l.rowSignature(prefix)
		if err != nil {
			return false, err
		}
		if _, ok := l.rowSeen[sig]; !ok {
			l.promote(prefix)
			return true, nil
		}
	}
	return false, nil
}

// makeConsistent splits states whose one-symbol extensions disagree on some
// suffix; the distinguishing suffix is added as a new column.
func (l *learner) makeConsistent() (bool, error) {
	for i := 0; i < len(l.short); i++ {
		for j := i + 1; j < len(l.short); j++ {
			sigI, err := l.rowSignature(l.short[i])
			if err != nil {
				return false, err
			}
			sigJ, err := l.rowSignature(l.short[j])
			if err != nil {
				return false, err
			}
			if sigI != sigJ {
				continue
			}
			for sym := range l.alphabet {
				for _, suffix := range l.suffix {
					extI := appendWord(l.short[i], sym)
					extJ := appendWord(l.short[j], sym)
					cellI, err := l.cell(extI, suffix)
					if err != nil {
						return false, err
					}
					cellJ, err := l.cell(extJ, suffix)
					if err != nil {
						return false, err
					}
					if !outputsEqual(cellI, cellJ) {
						distinguishing := append([]int{sym}, suffix...)
						if !l.hasSuffix(distinguishing) {
							l.suffix = append(l.suffix, distinguishing)
						}
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}

// promote moves prefix from the long rows to the short rows and registers
// its one-symbol extensions as long rows. The promoted prefix is retained in
// the long-row set as well so the L* invariant S union SA stays complete:
// every one-symbol extension of a short row is always present in S union SA.
func (l *learner) promote(prefix []int) {
	l.short = append(l.short, append([]int(nil), prefix...))
	for sym := range l.alphabet {
		ext := appendWord(prefix, sym)
		if !l.hasLong(ext) {
			l.long = append(l.long, ext)
		}
	}
}

func (l *learner) hasShort(word []int) bool {
	for _, s := range l.short {
		if wordsEqual(s, word) {
			return true
		}
	}
	return false
}

func (l *learner) hasLong(word []int) bool {
	for _, s := range l.long {
		if wordsEqual(s, word) {
			return true
		}
	}
	return false
}

// addSuffixes registers every suffix of a counterexample as a distinguishing
// suffix. The empty suffix carries no observation and is skipped.
func (l *learner) addSuffixes(counterexample []int) {
	for start := 0; start < len(counterexample); start++ {
		suffix := append([]int(nil), counterexample[start:]...)
		if !l.hasSuffix(suffix) {
			l.suffix = append(l.suffix, suffix)
		}
	}
}
