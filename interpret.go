package iflow

import (
	"fmt"
	"sort"
)

// Run executes the program concretely with the given input values and returns
// the value observed at each sink. Declassification nodes are transparent at
// runtime (they only copy the value); they matter to the static analysis.
func Run(prog *Program, inputs map[string]int64) (map[string]int64, error) {
	env := make(map[string]int64, len(inputs))
	for k, v := range inputs {
		env[k] = v
	}
	out := map[string]int64{}
	if err := runBlock(prog.Body, env, out); err != nil {
		return nil, err
	}
	return out, nil
}

func runBlock(body []Instr, env, out map[string]int64) error {
	for _, in := range body {
		switch in.Kind {
		case OpAssign:
			v, err := evalConcrete(env, in.Expr)
			if err != nil {
				return err
			}
			env[in.Target] = v
		case OpIf:
			c, err := evalConcrete(env, in.Cond)
			if err != nil {
				return err
			}
			if c != 0 {
				if err := runBlock(in.Then, env, out); err != nil {
					return err
				}
			} else {
				if err := runBlock(in.Else, env, out); err != nil {
					return err
				}
			}
		case OpDeclass:
			v, ok := env[in.Source]
			if !ok {
				return fmt.Errorf("declassify of unassigned variable %q", in.Source)
			}
			env[in.Target] = v
		case OpSink:
			v, err := evalConcrete(env, in.Expr)
			if err != nil {
				return err
			}
			out[in.SinkName] = v
		}
	}
	return nil
}

func evalConcrete(env map[string]int64, e *Expr) (int64, error) {
	switch e.Kind {
	case ConstExpr:
		return e.Value, nil
	case VarExpr:
		v, ok := env[e.Var]
		if !ok {
			return 0, fmt.Errorf("unassigned variable %q", e.Var)
		}
		return v, nil
	default:
		l, err := evalConcrete(env, e.L)
		if err != nil {
			return 0, err
		}
		r, err := evalConcrete(env, e.R)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case "add":
			return l + r, nil
		case "sub":
			return l - r, nil
		case "mul":
			return l * r, nil
		case "eq":
			return boolInt(l == r), nil
		case "ne":
			return boolInt(l != r), nil
		case "lt":
			return boolInt(l < r), nil
		case "le":
			return boolInt(l <= r), nil
		case "gt":
			return boolInt(l > r), nil
		case "ge":
			return boolInt(l >= r), nil
		case "and":
			return boolInt(l != 0 && r != 0), nil
		case "or":
			return boolInt(l != 0 || r != 0), nil
		}
		return 0, fmt.Errorf("unknown operator %q", e.Op)
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// SinkDiff is one observable difference between two concrete runs.
type SinkDiff struct {
	Sink string
	Run1 int64
	Run2 int64
}

// CompareRuns reports the sinks whose observed values differ between two
// runs. A non-empty result is a dynamic witness that the pair of input
// values is distinguishable at those sinks; an empty result proves nothing
// beyond these two runs.
func CompareRuns(run1, run2 map[string]int64) []SinkDiff {
	keys := map[string]bool{}
	for k := range run1 {
		keys[k] = true
	}
	for k := range run2 {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var diffs []SinkDiff
	for _, k := range names {
		if run1[k] != run2[k] {
			diffs = append(diffs, SinkDiff{Sink: k, Run1: run1[k], Run2: run2[k]})
		}
	}
	return diffs
}
