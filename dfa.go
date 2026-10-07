// Package dfeq compares the accepted languages of two deterministic
// automata (protocol state machines). Missing transitions implicitly
// lead to a rejecting sink. States may carry unexpanded external
// fragments; comparison then reports Unknown together with the
// blocking fragments instead of claiming equivalence.
package dfeq

import "sort"

// sinkName is the internal name of the implicit rejecting sink.
// It cannot collide with user state names because it contains a NUL byte.
const sinkName = "\x00sink"

// Fragment marks a state whose behaviour is not fully expanded yet.
// The external piece it stands for may later be supplied via Expansion.
type Fragment struct {
	ID string
}

// Expansion is the asynchronously supplied content of a Fragment.
// It may fix the acceptance of the host state and add transitions
// (possibly to brand new states) plus acceptance for the new states.
type Expansion struct {
	HostAccept *bool                        // acceptance of the host state, optional
	Accept     map[string]bool              // acceptance of newly introduced states
	Trans      map[string]map[string]string // transitions: state -> symbol -> target
}

// DFA is a deterministic automaton with a partial transition function.
// Missing transitions implicitly go to a rejecting sink.
type DFA struct {
	Name     string
	Alphabet []string // sorted set of symbols
	Start    string
	Accept   map[string]bool
	Trans    map[string]map[string]string // state -> symbol -> target (partial)
	// Fragments maps a state name to its unexpanded external fragment.
	Fragments map[string]*Fragment
	Version   uint64
}

// NewDFA builds a DFA and normalises the alphabet (sorted, deduped).
func NewDFA(name string, alphabet []string, start string) *DFA {
	d := &DFA{
		Name:      name,
		Start:     start,
		Accept:    map[string]bool{},
		Trans:     map[string]map[string]string{},
		Fragments: map[string]*Fragment{},
	}
	d.Alphabet = normalizeAlphabet(alphabet)
	return d
}

func normalizeAlphabet(a []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// SetTrans adds a transition.
func (d *DFA) SetTrans(from, symbol, to string) {
	row, ok := d.Trans[from]
	if !ok {
		row = map[string]string{}
		d.Trans[from] = row
	}
	row[symbol] = to
}

// AttachFragment marks state as backed by an unexpanded external fragment.
func (d *DFA) AttachFragment(state, fragmentID string) {
	d.Fragments[state] = &Fragment{ID: fragmentID}
}

// opaque reports whether the state's behaviour is not fully known.
func (d *DFA) opaque(state string) bool {
	if state == sinkName {
		return false
	}
	_, ok := d.Fragments[state]
	return ok
}

// fragmentID returns the fragment attached to state, or "".
func (d *DFA) fragmentID(state string) string {
	if f, ok := d.Fragments[state]; ok {
		return f.ID
	}
	return ""
}

// Step follows one symbol; missing transitions land in the rejecting sink.
func (d *DFA) Step(state, symbol string) string {
	if state == sinkName {
		return sinkName
	}
	if row, ok := d.Trans[state]; ok {
		if to, ok := row[symbol]; ok {
			return to
		}
	}
	return sinkName
}

// Accepting reports whether a state accepts. The sink never accepts.
// Acceptance of an opaque state is its currently known value; callers
// must treat results depending on opaque states as provisional.
func (d *DFA) Accepting(state string) bool {
	if state == sinkName {
		return false
	}
	return d.Accept[state]
}

// Simulate runs a word and reports whether it is accepted.
func (d *DFA) Simulate(word []string) bool {
	state := d.Start
	for _, sym := range word {
		state = d.Step(state, sym)
	}
	return d.Accepting(state)
}

// Snapshot deep-copies the DFA so a query stays bound to the version
// it was issued at, even if fragments are resolved later.
func (d *DFA) Snapshot() *DFA {
	cp := &DFA{
		Name:      d.Name,
		Start:     d.Start,
		Version:   d.Version,
		Accept:    make(map[string]bool, len(d.Accept)),
		Trans:     make(map[string]map[string]string, len(d.Trans)),
		Fragments: make(map[string]*Fragment, len(d.Fragments)),
	}
	cp.Alphabet = append([]string(nil), d.Alphabet...)
	for k, v := range d.Accept {
		cp.Accept[k] = v
	}
	for k, row := range d.Trans {
		nrow := make(map[string]string, len(row))
		for s, to := range row {
			nrow[s] = to
		}
		cp.Trans[k] = nrow
	}
	for k, f := range d.Fragments {
		nf := *f
		cp.Fragments[k] = &nf
	}
	return cp
}

// ResolveFragment supplies the expansion for the fragment attached to
// host. It only adds information: existing transitions and acceptance
// of other states are left untouched. It bumps the DFA version.
// It returns false if the host has no such fragment.
func (d *DFA) ResolveFragment(host, fragmentID string, exp Expansion) bool {
	f, ok := d.Fragments[host]
	if !ok || f.ID != fragmentID {
		return false
	}
	if exp.HostAccept != nil {
		d.Accept[host] = *exp.HostAccept
	}
	for state, acc := range exp.Accept {
		d.Accept[state] = acc
	}
	for from, row := range exp.Trans {
		for sym, to := range row {
			d.SetTrans(from, sym, to)
			d.addSymbol(sym)
		}
	}
	delete(d.Fragments, host)
	d.Version++
	return true
}

// addSymbol inserts sym into the alphabet if missing, keeping it sorted.
func (d *DFA) addSymbol(sym string) {
	i := sort.SearchStrings(d.Alphabet, sym)
	if i < len(d.Alphabet) && d.Alphabet[i] == sym {
		return
	}
	d.Alphabet = append(d.Alphabet, "")
	copy(d.Alphabet[i+1:], d.Alphabet[i:])
	d.Alphabet[i] = sym
}
