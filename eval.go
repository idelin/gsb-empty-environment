// Package configeval evaluates configuration variables whose expressions may
// reference other variables and an explicit environment snapshot.
//
// The evaluator never reads the runtime process environment: every external
// value must be supplied through an Env snapshot. Evaluation failures
// (missing references, reference cycles, type errors) report the full
// reference chain. Successful results carry a digest summarising every
// transitive dependency.
//
// Caching is scoped by freeze boundaries: Freeze installs a new snapshot and
// subsequent evaluations recompute only variables whose transitive
// environment dependencies actually changed; cached values are never reused
// across a boundary when any dependency differs.
package configeval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Env is an explicit, immutable environment snapshot. It is the only source
// of external values; the process environment is never consulted.
type Env map[string]string

// Snapshot copies pairs into a new Env snapshot.
func Snapshot(pairs map[string]string) Env {
	e := make(Env, len(pairs))
	for k, v := range pairs {
		e[k] = v
	}
	return e
}

// ErrKind classifies evaluation failures.
type ErrKind int

const (
	// ErrMissing indicates a reference to an undefined variable or an
	// environment key absent from the snapshot.
	ErrMissing ErrKind = iota
	// ErrCycle indicates a reference cycle among variables.
	ErrCycle
	// ErrType indicates an operator applied to incompatible types.
	ErrType
)

func (k ErrKind) String() string {
	switch k {
	case ErrMissing:
		return "missing reference"
	case ErrCycle:
		return "reference cycle"
	case ErrType:
		return "type error"
	}
	return "error"
}

// RefError describes a failed evaluation, including the reference chain
// from the requested variable to the point of failure.
type RefError struct {
	Kind  ErrKind
	Chain []string // e.g. ["a", "b", "c"] or ["a", "b", "a"] for a cycle
	Msg   string
}

func (e *RefError) Error() string {
	return fmt.Sprintf("configeval: %s: %s (chain: %s)", e.Kind, e.Msg, strings.Join(e.Chain, " -> "))
}

// Result is the outcome of a successful evaluation.
type Result struct {
	Value Value
	// Digest is a hex SHA-256 over the variable's own expression and the
	// canonical values of every transitive dependency (variables and
	// environment keys). Identical dependency sets yield identical digests.
	Digest string
	// Deps lists every transitive dependency, sorted. Variable names appear
	// bare; environment keys appear as "env.KEY".
	Deps []string
}

// Config is an immutable set of named variable expressions.
type Config struct {
	vars map[string]*Expr
}

// NewConfig returns an empty Config.
func NewConfig() *Config { return &Config{vars: map[string]*Expr{}} }

// Set parses src and binds it to name, replacing any previous definition.
func (c *Config) Set(name, src string) error {
	if !isIdent(name) {
		return fmt.Errorf("configeval: invalid variable name %q", name)
	}
	expr, err := ParseExpr(src)
	if err != nil {
		return err
	}
	c.vars[name] = expr
	return nil
}

// MustSet is Set that panics on error; convenient for static configs.
func (c *Config) MustSet(name, src string) {
	if err := c.Set(name, src); err != nil {
		panic(err)
	}
}

// Has reports whether name is defined.
func (c *Config) Has(name string) bool {
	_, ok := c.vars[name]
	return ok
}

type cacheEntry struct {
	result  Result
	envDeps map[string]string // transitive env keys with values at compute time
}

// Stats reports cache behaviour, useful for verifying incremental recompute.
type Stats struct {
	Epoch    uint64 // number of Freeze boundaries crossed (starts at 1)
	Computes int    // total expression evaluations performed
	Hits     int    // cache reuses
}

// Evaluator evaluates a Config against a frozen Env snapshot, caching
// results between freeze boundaries.
type Evaluator struct {
	cfg      *Config
	env      Env
	epoch    uint64
	cache    map[string]*cacheEntry
	computes int
	hits     int
}

// NewEvaluator freezes env as the initial snapshot. The env map is copied;
// later mutations of the caller's map have no effect.
func NewEvaluator(cfg *Config, env Env) *Evaluator {
	e := &Evaluator{cfg: cfg, epoch: 1, cache: map[string]*cacheEntry{}}
	e.Freeze(env)
	e.epoch = 1
	return e
}

// Freeze installs a new environment snapshot, crossing a freeze boundary.
// The snapshot is copied. Cached entries whose transitive environment
// dependencies are unchanged remain valid; everything else is recomputed
// lazily on the next Eval. A cached value is never returned when any of its
// recorded dependencies differ from the new snapshot.
func (e *Evaluator) Freeze(env Env) {
	frozen := make(Env, len(env))
	for k, v := range env {
		frozen[k] = v
	}
	e.env = frozen
	e.epoch++
}

// Stats returns a snapshot of cache counters.
func (e *Evaluator) Stats() Stats {
	return Stats{Epoch: e.epoch, Computes: e.computes, Hits: e.hits}
}

// Eval resolves name to a value, or returns a *RefError carrying the
// reference chain.
func (e *Evaluator) Eval(name string) (Result, error) {
	return e.evalVar(name, nil)
}

func (e *Evaluator) evalVar(name string, stack []string) (Result, error) {
	chain := append(append([]string{}, stack...), name)
	for _, s := range stack {
		if s == name {
			return Result{}, &RefError{Kind: ErrCycle, Chain: chain,
				Msg: fmt.Sprintf("variable %q references itself", name)}
		}
	}
	expr, ok := e.cfg.vars[name]
	if !ok {
		return Result{}, &RefError{Kind: ErrMissing, Chain: chain,
			Msg: fmt.Sprintf("variable %q is not defined", name)}
	}
	if ent, ok := e.cache[name]; ok && e.entryValid(ent) {
		e.hits++
		return ent.result, nil
	}
	e.computes++
	frame := &frame{ev: e, stack: chain, deps: map[string]Value{}, envDeps: map[string]string{}}
	v, err := e.evalNode(expr.root, frame)
	if err != nil {
		return Result{}, err
	}
	res := Result{Value: v}
	res.Deps, res.Digest = summarise(name, expr.src, frame.deps, frame.envDeps)
	e.cache[name] = &cacheEntry{result: res, envDeps: frame.envDeps}
	return res, nil
}

// entryValid reports whether a cached entry is consistent with the current
// frozen snapshot. Because variable values depend only on expressions
// (immutable per Evaluator) and the snapshot, comparing the transitive
// environment footprint is sufficient.
func (e *Evaluator) entryValid(ent *cacheEntry) bool {
	for k, v := range ent.envDeps {
		cur, ok := e.env[k]
		if !ok || cur != v {
			return false
		}
	}
	return true
}

// summarise builds the dependency list and digest for a computed variable.
func summarise(name, src string, deps map[string]Value, envDeps map[string]string) ([]string, string) {
	lines := make([]string, 0, len(deps)+len(envDeps)+1)
	lines = append(lines, "self:"+name+"="+src)
	out := make([]string, 0, len(deps)+len(envDeps))
	for dep, v := range deps {
		lines = append(lines, "var:"+dep+"="+v.canonical())
		out = append(out, dep)
	}
	for k, v := range envDeps {
		lines = append(lines, "env:"+k+"="+v)
		out = append(out, "env."+k)
	}
	sort.Strings(lines)
	sort.Strings(out)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return out, hex.EncodeToString(sum[:])
}

// frame carries per-evaluation state: the resolution stack (for error
// chains) and accumulators for transitive dependencies.
type frame struct {
	ev      *Evaluator
	stack   []string
	deps    map[string]Value  // var name -> resolved value (transitive)
	envDeps map[string]string // env key -> snapshot value (transitive)
}

func (f *frame) merge(child *frame) {
	for k, v := range child.deps {
		f.deps[k] = v
	}
	for k, v := range child.envDeps {
		f.envDeps[k] = v
	}
}

func (e *Evaluator) evalNode(n *node, f *frame) (Value, error) {
	switch n.kind {
	case nNum:
		return Num(n.num), nil
	case nStr:
		return Str(n.str), nil
	case nBool:
		return Bool(n.boolean), nil
	case nRefVar:
		res, err := e.evalVar(n.str, f.stack)
		if err != nil {
			return Value{}, err
		}
		f.deps[n.str] = res.Value
		// Absorb the child's transitive environment footprint so cache
		// validity and digests reflect the full dependency closure.
		if ent, ok := e.cache[n.str]; ok {
			for k, v := range ent.envDeps {
				f.envDeps[k] = v
			}
		}
		for _, dep := range res.Deps {
			if strings.HasPrefix(dep, "env.") {
				continue
			}
			if _, ok := f.deps[dep]; !ok {
				if sub, ok := e.cache[dep]; ok {
					f.deps[dep] = sub.result.Value
				}
			}
		}
		return res.Value, nil
	case nRefEnv:
		v, ok := e.env[n.str]
		if !ok {
			chain := append(append([]string{}, f.stack...), "env."+n.str)
			return Value{}, &RefError{Kind: ErrMissing, Chain: chain,
				Msg: fmt.Sprintf("environment key %q is not in the snapshot", n.str)}
		}
		f.envDeps[n.str] = v
		return Str(v), nil
	case nInterp:
		var sb strings.Builder
		for _, part := range n.parts {
			v, err := e.evalNode(part, f)
			if err != nil {
				return Value{}, err
			}
			s, err := v.interpolate()
			if err != nil {
				return Value{}, &RefError{Kind: ErrType, Chain: f.stack, Msg: err.Error()}
			}
			sb.WriteString(s)
		}
		return Str(sb.String()), nil
	case nBin:
		left, err := e.evalNode(n.left, f)
		if err != nil {
			return Value{}, err
		}
		right, err := e.evalNode(n.right, f)
		if err != nil {
			return Value{}, err
		}
		return applyOp(n.op, left, right, f.stack)
	}
	return Value{}, &RefError{Kind: ErrType, Chain: f.stack, Msg: "unknown expression node"}
}

func applyOp(op byte, left, right Value, stack []string) (Value, error) {
	typeErr := func() (Value, error) {
		return Value{}, &RefError{Kind: ErrType, Chain: stack, Msg: fmt.Sprintf(
			"operator %q cannot be applied to %s and %s", string(op), left.kind, right.kind)}
	}
	if op == '+' {
		if left.kind == KindStr && right.kind == KindStr {
			return Str(left.str + right.str), nil
		}
	}
	if left.kind == KindNum && right.kind == KindNum {
		switch op {
		case '+':
			return Num(left.num + right.num), nil
		case '-':
			return Num(left.num - right.num), nil
		case '*':
			return Num(left.num * right.num), nil
		case '/':
			if right.num == 0 {
				return Value{}, &RefError{Kind: ErrType, Chain: stack, Msg: "division by zero"}
			}
			return Num(left.num / right.num), nil
		}
	}
	return typeErr()
}
