// Package methodgate provides a whitelist-based method gate for Go XML-RPC
// servers. It enforces exact (case-sensitive, un-normalized) methodName
// matching, safe system.multicall handling with per-subcall authorization
// and rate limiting, and caller-scoped system.listMethods responses.
package methodgate

import (
	"context"
	"errors"
	"sort"
	"sync"
	"unicode"
)

// Well-known system method names handled by the gate itself.
const (
	MethodMulticall   = "system.multicall"
	MethodListMethods = "system.listMethods"
)

// DefaultMaxSubcalls is the maximum number of subcalls allowed in a single
// system.multicall request.
const DefaultMaxSubcalls = 20

// Fault codes returned by the gate.
const (
	FaultInvalidRequest  = -32600
	FaultMethodNotFound  = -32601
	FaultInternal        = -32603
	FaultForbidden       = 4
	FaultNestedMulticall = 5
	FaultTooManySubcalls = 6
	FaultRateLimited     = 7
)

// Fault is an XML-RPC fault.
type Fault struct {
	Code   int
	String string
}

func (f *Fault) Error() string { return f.String }

// FaultError lets a Handler control the fault code returned to the client.
type FaultError struct {
	Code int
	Msg  string
}

func (e *FaultError) Error() string { return e.Msg }

// Handler implements an XML-RPC method.
type Handler func(ctx context.Context, params []any) (any, error)

// Method is a whitelisted XML-RPC method.
type Method struct {
	// Name is matched byte-for-byte against the incoming methodName:
	// case-sensitive, no Unicode normalization, no whitespace trimming.
	Name string
	// Perm is the permission required to see and invoke the method.
	// Empty means any caller (including anonymous) may use it.
	Perm    string
	Handler Handler
}

// Caller identifies the authenticated principal of a request.
type Caller struct {
	ID    string
	Perms []string
}

// Has reports whether the caller holds the given permission.
func (c *Caller) Has(perm string) bool {
	if c == nil {
		return false
	}
	for _, p := range c.Perms {
		if p == perm {
			return true
		}
	}
	return false
}

type callerKey struct{}

// WithCaller attaches a Caller to the context.
func WithCaller(ctx context.Context, c *Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom extracts the Caller from the context, or nil for anonymous.
func CallerFrom(ctx context.Context) *Caller {
	c, _ := ctx.Value(callerKey{}).(*Caller)
	return c
}

// RateLimiter deducts tokens from a per-key bucket. Take reports whether
// n tokens were available (and deducted).
type RateLimiter interface {
	Take(key string, n int) bool
}

// Bucket is a simple thread-safe in-memory RateLimiter with a fixed
// capacity per key and no refill. It is suitable for tests and for
// deployments that reset buckets out of band.
type Bucket struct {
	mu       sync.Mutex
	capacity int
	tokens   map[string]int
}

// NewBucket returns a Bucket where every key starts with capacity tokens.
func NewBucket(capacity int) *Bucket {
	return &Bucket{capacity: capacity, tokens: make(map[string]int)}
}

// Take deducts n tokens from key's bucket, reporting success.
func (b *Bucket) Take(key string, n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok := b.tokens[key]
	if !ok {
		cur = b.capacity
	}
	if cur < n {
		return false
	}
	b.tokens[key] = cur - n
	return true
}

// Remaining reports how many tokens key has left.
func (b *Bucket) Remaining(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok := b.tokens[key]
	if !ok {
		return b.capacity
	}
	return cur
}

// Gate is the method whitelist gate. It is safe for concurrent use.
type Gate struct {
	maxSubcalls int
	limiter     RateLimiter

	mu      sync.RWMutex
	methods map[string]Method
}

// Option configures a Gate.
type Option func(*Gate)

// WithMaxSubcalls overrides the multicall subcall limit.
func WithMaxSubcalls(n int) Option {
	return func(g *Gate) {
		if n > 0 {
			g.maxSubcalls = n
		}
	}
}

// WithRateLimiter sets the limiter used for authentication failures.
func WithRateLimiter(l RateLimiter) Option {
	return func(g *Gate) { g.limiter = l }
}

// New creates a Gate.
func New(opts ...Option) *Gate {
	g := &Gate{maxSubcalls: DefaultMaxSubcalls, methods: make(map[string]Method)}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Register adds a method to the whitelist. Names are stored verbatim and
// matched exactly; names containing whitespace, empty names, duplicates,
// and the reserved system.* names are rejected.
func (g *Gate) Register(m Method) error {
	if m.Name == "" {
		return errors.New("methodgate: empty method name")
	}
	if m.Name == MethodMulticall || m.Name == MethodListMethods {
		return errors.New("methodgate: reserved method name: " + m.Name)
	}
	for _, r := range m.Name {
		if unicode.IsSpace(r) {
			return errors.New("methodgate: whitespace in method name: " + m.Name)
		}
	}
	if m.Handler == nil {
		return errors.New("methodgate: nil handler for " + m.Name)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, dup := g.methods[m.Name]; dup {
		return errors.New("methodgate: duplicate method: " + m.Name)
	}
	g.methods[m.Name] = m
	return nil
}

// lookup finds a method by exact byte equality. Case variants, Unicode
// compatibility variants (fullwidth characters, composed vs. decomposed
// accents) and whitespace-padded names never match.
func (g *Gate) lookup(name string) (Method, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	m, ok := g.methods[name]
	return m, ok
}

// Dispatch executes a single top-level XML-RPC call through the gate.
func (g *Gate) Dispatch(ctx context.Context, methodName string, params []any) (any, *Fault) {
	switch methodName {
	case MethodMulticall:
		return g.multicall(ctx, params)
	case MethodListMethods:
		return g.listMethods(ctx), nil
	}
	m, ok := g.lookup(methodName)
	if !ok {
		return nil, &Fault{Code: FaultMethodNotFound, String: "method not allowed: " + methodName}
	}
	if f := g.authorize(ctx, m); f != nil {
		return nil, f
	}
	return g.invoke(ctx, m, params)
}

// multicall implements system.multicall. params must be a single array of
// structs {methodName, params}. Exceeding the subcall limit rejects the
// whole request; individual subcall failures become fault structs while
// the remaining subcalls still execute.
func (g *Gate) multicall(ctx context.Context, params []any) (any, *Fault) {
	if len(params) != 1 {
		return nil, &Fault{Code: FaultInvalidRequest, String: "system.multicall takes exactly one array parameter"}
	}
	calls, ok := params[0].([]any)
	if !ok {
		return nil, &Fault{Code: FaultInvalidRequest, String: "system.multicall parameter must be an array"}
	}
	if len(calls) > g.maxSubcalls {
		return nil, &Fault{Code: FaultTooManySubcalls, String: "too many subcalls"}
	}
	results := make([]any, 0, len(calls))
	for _, raw := range calls {
		results = append(results, g.subcall(ctx, raw))
	}
	return results, nil
}

// subcall executes one multicall entry, always returning either
// []any{result} or a fault struct.
func (g *Gate) subcall(ctx context.Context, raw any) any {
	spec, ok := raw.(map[string]any)
	if !ok {
		return faultValue(FaultInvalidRequest, "subcall must be a struct")
	}
	name, _ := spec["methodName"].(string)
	params, _ := spec["params"].([]any)

	if name == MethodMulticall {
		return faultValue(FaultNestedMulticall, "nested system.multicall is forbidden")
	}
	if name == MethodListMethods {
		return []any{g.listMethods(ctx)}
	}
	m, ok := g.lookup(name)
	if !ok {
		return faultValue(FaultMethodNotFound, "method not allowed: "+name)
	}
	if f := g.authorize(ctx, m); f != nil {
		return faultValue(f.Code, f.String)
	}
	v, ferr := g.invoke(ctx, m, params)
	if ferr != nil {
		return faultValue(ferr.Code, ferr.String)
	}
	return []any{v}
}

// authorize checks the caller's permission for m. Authentication failures
// are charged one token against the caller's rate bucket; when the bucket
// is empty the call is rejected as rate limited.
func (g *Gate) authorize(ctx context.Context, m Method) *Fault {
	if m.Perm == "" {
		return nil
	}
	caller := CallerFrom(ctx)
	if caller != nil && caller.Has(m.Perm) {
		return nil
	}
	if g.limiter != nil && !g.limiter.Take(rateKey(caller), 1) {
		return &Fault{Code: FaultRateLimited, String: "rate limit exceeded"}
	}
	return &Fault{Code: FaultForbidden, String: "authentication required for " + m.Name}
}

func rateKey(c *Caller) string {
	if c == nil || c.ID == "" {
		return "anonymous"
	}
	return c.ID
}

func (g *Gate) invoke(ctx context.Context, m Method, params []any) (any, *Fault) {
	v, err := m.Handler(ctx, params)
	if err == nil {
		return v, nil
	}
	var fe *FaultError
	if errors.As(err, &fe) {
		return nil, &Fault{Code: fe.Code, String: fe.Msg}
	}
	return nil, &Fault{Code: FaultInternal, String: err.Error()}
}

// listMethods implements system.listMethods: it returns only the methods
// the caller is permitted to invoke, plus the system methods themselves.
func (g *Gate) listMethods(ctx context.Context) []any {
	caller := CallerFrom(ctx)
	g.mu.RLock()
	names := make([]string, 0, len(g.methods)+2)
	for _, m := range g.methods {
		if m.Perm == "" || caller.Has(m.Perm) {
			names = append(names, m.Name)
		}
	}
	g.mu.RUnlock()
	names = append(names, MethodListMethods, MethodMulticall)
	sort.Strings(names)
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return out
}

func faultValue(code int, msg string) any {
	return map[string]any{"faultCode": code, "faultString": msg}
}
