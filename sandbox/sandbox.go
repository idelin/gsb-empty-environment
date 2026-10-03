// Package sandbox provides a multi-tenant rule sandbox with execution
// budget guards. Tenants register named rules (boolean / arithmetic
// expressions). Every expression is parsed into an AST, statically
// validated (function whitelist, variable whitelist, depth/size limits)
// and then evaluated under a strict budget of instruction steps, memory
// allocation units and a wall-clock timeout.
package sandbox

import (
	"fmt"
	"sync"
	"time"
)

// Func is a host-provided function a tenant is allowed to call.
type Func func(args []any) (any, error)

// Limits caps the resources a single rule execution may consume.
type Limits struct {
	MaxSteps  uint64        // max AST-node evaluations (instruction steps)
	MaxAllocs uint64        // max allocation units (values + string bytes)
	Timeout   time.Duration // wall-clock deadline per execution
	MaxDepth  int           // max AST nesting depth
	MaxNodes  int           // max AST node count
}

func (l Limits) withDefaults() Limits {
	if l.MaxSteps == 0 {
		l.MaxSteps = 10000
	}
	if l.MaxAllocs == 0 {
		l.MaxAllocs = 100000
	}
	if l.Timeout == 0 {
		l.Timeout = 5 * time.Second
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = 200
	}
	if l.MaxNodes == 0 {
		l.MaxNodes = 10000
	}
	return l
}

// Tenant is an isolated rule namespace with its own limits and whitelists.
type Tenant struct {
	ID        string
	Limits    Limits
	Functions map[string]Func   // whitelisted host functions
	Variables []string          // whitelisted variable names
	Rules     map[string]string // rule name -> expression source
}

// Sandbox holds all tenants. It is safe for concurrent use.
type Sandbox struct {
	mu      sync.RWMutex
	tenants map[string]*Tenant
}

func New() *Sandbox {
	return &Sandbox{tenants: make(map[string]*Tenant)}
}

func (s *Sandbox) AddTenant(t *Tenant) error {
	if t == nil || t.ID == "" {
		return fmt.Errorf("tenant with non-empty ID required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[t.ID] = t
	return nil
}

func (s *Sandbox) tenant(id string) (*Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tenants[id]
	if !ok {
		return nil, fmt.Errorf("unknown tenant %q", id)
	}
	return t, nil
}

// Run executes the named rule of a tenant with the given variables.
func (s *Sandbox) Run(tenantID, rule string, vars map[string]any) (any, error) {
	t, err := s.tenant(tenantID)
	if err != nil {
		return nil, err
	}
	if _, ok := t.Rules[rule]; !ok {
		return nil, fmt.Errorf("unknown rule %q for tenant %q", rule, tenantID)
	}
	return run(t, rule, vars)
}

// RunExpr executes an ad-hoc expression under the tenant's limits and
// whitelists, without registering it as a named rule.
func (s *Sandbox) RunExpr(tenantID, expr string, vars map[string]any) (any, error) {
	t, err := s.tenant(tenantID)
	if err != nil {
		return nil, err
	}
	tmp := *t
	tmp.Rules = map[string]string{"__expr__": expr}
	return run(&tmp, "__expr__", vars)
}

func run(t *Tenant, rule string, vars map[string]any) (any, error) {
	limits := t.Limits.withDefaults()

	allowedVars := map[string]bool{"self": true}
	for _, v := range t.Variables {
		allowedVars[v] = true
	}
	for k := range vars {
		allowedVars[k] = true
	}
	allowedFuncs := make(map[string]bool, len(t.Functions))
	for name := range t.Functions {
		allowedFuncs[name] = true
	}

	// Parse and statically validate every rule up front, so that rules
	// reachable through eval() are checked before any execution starts.
	parsed := make(map[string]*node, len(t.Rules))
	for name, src := range t.Rules {
		n, err := parse(src)
		if err != nil {
			return nil, fmt.Errorf("rule %q: parse error: %w", name, err)
		}
		if err := validate(n, allowedFuncs, allowedVars, limits); err != nil {
			return nil, fmt.Errorf("rule %q: %w", name, err)
		}
		parsed[name] = n
	}

	b := &budget{
		maxSteps:  limits.MaxSteps,
		maxAllocs: limits.MaxAllocs,
		deadline:  time.Now().Add(limits.Timeout),
	}
	ctx := &evalCtx{
		b:     b,
		vars:  vars,
		funcs: t.Functions,
		rules: parsed,
		self:  rule,
	}

	// Evaluate in a worker goroutine so a blocked execution can always be
	// abandoned at the timeout; the host never deadlocks.
	type outcome struct {
		v   any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		v, err := ctx.eval(parsed[rule])
		done <- outcome{v, err}
	}()

	select {
	case o := <-done:
		return o.v, o.err
	case <-time.After(limits.Timeout):
		return nil, ErrTimeout
	}
}
