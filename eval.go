package configeval

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Dependency is one node a successful value transitively depended on.
type Dependency struct {
	Ref   string // "var:<name>" or "env:<key>"
	Value string // canonical fingerprint of the value seen at evaluation time
}

// Result is a successful evaluation: the value plus a digest summarizing
// every dependency (variable and environment snapshot entry) it used.
type Result struct {
	Value  Value
	Deps   []Dependency // sorted by Ref, covers the full transitive closure
	Digest string       // hex sha256 over Deps; changes iff any dependency changed
}

type cacheEntry struct {
	generation uint64
	result     Result
	direct     map[string]string // direct dep ref -> fingerprint at compute time
}

// Evaluator evaluates variables against definitions and an explicit
// environment snapshot. It caches results and recomputes incrementally:
// after SetEnv only variables whose transitive dependencies changed are
// re-evaluated. Freeze invalidates the whole cache; cached values never
// cross a freeze boundary.
//
// Evaluator is not safe for concurrent use.
type Evaluator struct {
	defs       map[string]Expr
	env        map[string]string
	generation uint64
	cache      map[string]*cacheEntry
	computed   int
	reused     int
}

// New builds an Evaluator. The env snapshot is copied; the process
// environment is never consulted.
func New(defs map[string]Expr, env map[string]string) *Evaluator {
	e := &Evaluator{
		defs:  defs,
		env:   make(map[string]string, len(env)),
		cache: make(map[string]*cacheEntry),
	}
	for k, v := range env {
		e.env[k] = v
	}
	return e
}

// SetEnv replaces the environment snapshot (copied). Cached entries stay,
// but are re-validated against their recorded dependencies on next access,
// so only affected variables are recomputed.
func (e *Evaluator) SetEnv(env map[string]string) {
	next := make(map[string]string, len(env))
	for k, v := range env {
		next[k] = v
	}
	e.env = next
}

// SetDef adds or replaces a variable definition. Existing cache entries are
// kept but re-validated; entries computed from a stale definition are
// dropped for the changed name.
func (e *Evaluator) SetDef(name string, x Expr) {
	e.defs[name] = x
	delete(e.cache, name)
}

// Freeze cuts a boundary: all cached results are invalidated and will be
// recomputed on demand. No cached value crosses a freeze boundary.
func (e *Evaluator) Freeze() {
	e.generation++
}

// Stats reports how many variables were computed vs served from cache.
func (e *Evaluator) Stats() (computed, reused int) {
	return e.computed, e.reused
}

// Get evaluates the named variable.
func (e *Evaluator) Get(name string) (Result, error) {
	return e.evalVar(name, nil)
}

func varRef(name string) string { return "var:" + name }
func envRef(key string) string  { return "env:" + key }

func varFingerprint(name, digest string) string { return varRef(name) + "@" + digest }
func envFingerprint(key, value string) string   { return envRef(key) + "=" + value }

func (e *Evaluator) evalVar(name string, chain []string) (Result, error) {
	ref := varRef(name)
	for _, c := range chain {
		if c == ref {
			return Result{}, newError(CycleError, appendChain(chain, ref),
				"variable %q references itself", name)
		}
	}
	def, ok := e.defs[name]
	if !ok {
		return Result{}, newError(MissingError, appendChain(chain, ref),
			"variable %q is not defined", name)
	}
	next := appendChain(chain, ref)
	if ent, ok := e.cache[name]; ok && ent.generation == e.generation {
		valid, err := e.entryValid(ent, next)
		if err != nil {
			return Result{}, err
		}
		if valid {
			e.reused++
			return ent.result, nil
		}
	}
	c, err := e.evalExpr(def, next)
	if err != nil {
		return Result{}, err
	}
	res := resultFrom(c)
	e.cache[name] = &cacheEntry{generation: e.generation, result: res, direct: c.direct}
	e.computed++
	return res, nil
}

// entryValid reports whether every direct dependency of a cached entry still
// resolves to the fingerprint recorded when the entry was computed.
func (e *Evaluator) entryValid(ent *cacheEntry, chain []string) (bool, error) {
	for ref, fp := range ent.direct {
		if strings.HasPrefix(ref, "env:") {
			key := ref[len("env:"):]
			v, ok := e.env[key]
			if !ok || envFingerprint(key, v) != fp {
				return false, nil
			}
			continue
		}
		name := ref[len("var:"):]
		res, err := e.evalVar(name, chain)
		if err != nil {
			return false, err
		}
		if varFingerprint(name, res.Digest) != fp {
			return false, nil
		}
	}
	return true, nil
}

// computed is the intermediate evaluation outcome of one expression.
type computed struct {
	val    Value
	deps   map[string]string // transitive dependency ref -> fingerprint
	direct map[string]string // direct dependency ref -> fingerprint
}

func resultFrom(c computed) Result {
	refs := make([]string, 0, len(c.deps))
	for ref := range c.deps {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	h := sha256.New()
	deps := make([]Dependency, 0, len(refs))
	for _, ref := range refs {
		fp := c.deps[ref]
		deps = append(deps, Dependency{Ref: ref, Value: fp})
		h.Write([]byte(ref))
		h.Write([]byte{0})
		h.Write([]byte(fp))
		h.Write([]byte{'\n'})
	}
	return Result{Value: c.val, Deps: deps, Digest: hex.EncodeToString(h.Sum(nil))}
}

func appendChain(chain []string, ref string) []string {
	next := make([]string, len(chain)+1)
	copy(next, chain)
	next[len(chain)] = ref
	return next
}

func mergeDeps(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

func (e *Evaluator) evalExpr(x Expr, chain []string) (computed, error) {
	switch n := x.(type) {
	case litExpr:
		return computed{val: n.v, deps: map[string]string{}, direct: map[string]string{}}, nil
	case refExpr:
		res, err := e.evalVar(n.name, chain)
		if err != nil {
			return computed{}, err
		}
		fp := varFingerprint(n.name, res.Digest)
		deps := make(map[string]string, len(res.Deps)+1)
		for _, d := range res.Deps {
			deps[d.Ref] = d.Value
		}
		deps[varRef(n.name)] = fp
		return computed{
			val:    res.Value,
			deps:   deps,
			direct: map[string]string{varRef(n.name): fp},
		}, nil
	case envExpr:
		v, ok := e.env[n.key]
		if !ok {
			return computed{}, newError(MissingError, appendChain(chain, envRef(n.key)),
				"environment key %q is not in the snapshot", n.key)
		}
		fp := envFingerprint(n.key, v)
		return computed{
			val:    StringValue(v),
			deps:   map[string]string{envRef(n.key): fp},
			direct: map[string]string{envRef(n.key): fp},
		}, nil
	case concatExpr:
		var sb strings.Builder
		deps := map[string]string{}
		direct := map[string]string{}
		for _, part := range n.parts {
			c, err := e.evalExpr(part, chain)
			if err != nil {
				return computed{}, err
			}
			if c.val.Kind != StringKind {
				return computed{}, newError(TypeError, chain,
					"concat requires string operands, got %s", c.val.fingerprint())
			}
			sb.WriteString(c.val.Str)
			mergeDeps(deps, c.deps)
			mergeDeps(direct, c.direct)
		}
		return computed{val: StringValue(sb.String()), deps: deps, direct: direct}, nil
	case addExpr:
		sum := 0
		deps := map[string]string{}
		direct := map[string]string{}
		for _, term := range n.terms {
			c, err := e.evalExpr(term, chain)
			if err != nil {
				return computed{}, err
			}
			if c.val.Kind != IntKind {
				return computed{}, newError(TypeError, chain,
					"add requires int operands, got %s", c.val.fingerprint())
			}
			sum += c.val.Int
			mergeDeps(deps, c.deps)
			mergeDeps(direct, c.direct)
		}
		return computed{val: IntValue(sum), deps: deps, direct: direct}, nil
	}
	return computed{}, newError(TypeError, chain, "unknown expression node %T", x)
}
