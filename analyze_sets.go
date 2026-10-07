package flowsec

import "fmt"

// AnalyzeSets is an INDEPENDENT re-implementation of the flow check,
// used to cross-validate Analyze. Instead of joining lattice labels it
// tracks, per variable, the set of input names that may influence the
// value (directly or via control dependence). A sink is allowed iff
// every influencing input is individually permitted at that sink.
//
// The two runs share only the IR and Policy types; divergence between
// them is reported, not silently resolved.
func AnalyzeSets(lat *Lattice, prog *Program) (map[int]bool, []string) {
	s := &setRunner{
		env:    map[string]map[string]bool{},
		inputs: map[string]*Input{},
		result: map[int]bool{},
		lat:    lat,
		prog:   prog,
	}
	s.walk(prog.Instrs)
	return s.result, s.violations
}

type setRunner struct {
	env        map[string]map[string]bool // var -> influencing input names
	pc         []map[string]bool
	inputs     map[string]*Input
	result     map[int]bool
	violations []string
	lat        *Lattice
	prog       *Program
}

func unionInto(dst, src map[string]bool) {
	for k := range src {
		dst[k] = true
	}
}

func cloneSetEnv(env map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

func (s *setRunner) pcSet() map[string]bool {
	u := map[string]bool{}
	for _, p := range s.pc {
		unionInto(u, p)
	}
	return u
}

func (s *setRunner) walk(list []Instr) {
	for _, in := range list {
		switch t := in.(type) {
		case *Input:
			s.inputs[t.Name] = t
			s.env[t.Name] = map[string]bool{t.Name: true}
		case *Const:
			s.env[t.Name] = map[string]bool{}
		case *Assign:
			u := s.pcSet()
			for _, src := range t.Srcs {
				unionInto(u, s.env[src])
			}
			s.env[t.Dst] = u
		case *If:
			cond := map[string]bool{}
			unionInto(cond, s.env[t.Cond])
			s.pc = append(s.pc, cond)
			saved := cloneSetEnv(s.env)
			s.walk(t.Then)
			thenEnv := s.env
			s.env = cloneSetEnv(saved)
			s.walk(t.Else)
			elseEnv := s.env
			s.pc = s.pc[:len(s.pc)-1]
			merged := map[string]map[string]bool{}
			for v, set := range thenEnv {
				u := map[string]bool{}
				unionInto(u, set)
				unionInto(u, elseEnv[v])
				merged[v] = u
			}
			for v, set := range elseEnv {
				if _, ok := thenEnv[v]; !ok {
					merged[v] = set
				}
			}
			s.env = merged
		case *Declassify:
			g, ok := s.prog.Policy.Grants[t.ID]
			if !ok || g.Token != t.Token || t.ToLevel != g.ToLevel || !subsetOf(t.AddReceivers, g.AddReceivers) {
				s.violations = append(s.violations, fmt.Sprintf("declassify@%d rejected by policy", t.ID))
				s.env[t.Dst] = s.env[t.Src] // unchanged
			} else {
				// Authorized declassification is a trust boundary:
				// past influence is forgotten, exactly like the
				// lattice run replaces the label.
				s.env[t.Dst] = map[string]bool{}
			}
		case *Sink:
			u := s.pcSet()
			unionInto(u, s.env[t.Src])
			allowed := true
			for name := range u {
				src, ok := s.inputs[name]
				if !ok {
					continue
				}
				if !s.lat.Leq(src.Level, t.MaxLevel) {
					allowed = false
				}
				if len(src.Receivers) > 0 && !contains(src.Receivers, t.Receiver) {
					allowed = false
				}
			}
			s.result[t.ID] = allowed
		}
	}
}

// CrossValidate compares the sink verdicts of the two independent runs
// and returns a description per mismatch (empty = full agreement).
func CrossValidate(r *Report, sets map[int]bool) []string {
	var mismatches []string
	for _, sr := range r.Sinks {
		other, ok := sets[sr.SinkID]
		if !ok {
			mismatches = append(mismatches, fmt.Sprintf("sink %d (%s): missing in set-based run", sr.SinkID, sr.Name))
			continue
		}
		if other != sr.Allowed {
			mismatches = append(mismatches, fmt.Sprintf("sink %d (%s): lattice run=%v set run=%v",
				sr.SinkID, sr.Name, sr.Allowed, other))
		}
	}
	return mismatches
}
