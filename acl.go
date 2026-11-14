package netsec

import (
	"fmt"
	"sync"
)

// Effect is the disposition of an ACL entry.
type Effect uint8

const (
	// EffectDeny wins over every allow entry that matches the same request.
	EffectDeny Effect = iota + 1
	// EffectAllow grants a request only when no matching deny entry exists.
	EffectAllow
)

// Valid reports whether e is a known effect.
func (e Effect) Valid() bool {
	return e == EffectDeny || e == EffectAllow
}

// Action is a bit mask of broker operations. A request must be covered by an
// entry whose action mask includes every requested bit.
type Action uint8

const (
	// ActionRead covers subscribing / fetching from a resource.
	ActionRead Action = 1 << iota
	// ActionWrite covers publishing / producing to a resource.
	ActionWrite
	// ActionAdmin covers administrative operations such as group lifecycle.
	ActionAdmin
)

const validActionMask = ActionRead | ActionWrite | ActionAdmin

// Entry is one ACL rule bound to a principal (identity) and a typed pattern.
type Entry struct {
	// Principal is the authenticated identity the rule applies to.
	Principal string
	// Effect decides allow or deny; deny always wins.
	Effect Effect
	// Actions is the bit mask this entry covers.
	Actions Action
	// Pattern is the typed, kind-bound resource pattern.
	Pattern Pattern
}

func (e Entry) validate(delim byte) error {
	if e.Principal == "" {
		return ErrEmptyPrincipal
	}
	if !e.Effect.Valid() {
		return ErrUnknownEffect
	}
	if e.Actions == 0 || e.Actions&^validActionMask != 0 {
		return ErrUnknownAction
	}
	if err := e.Pattern.Valid(delim); err != nil {
		return err
	}
	return nil
}

// indexedEntry is a compiled entry stored inside an immutable snapshot.
type indexedEntry struct {
	principal string
	effect    Effect
	actions   Action
	kind      ResourceKind
	match     Matcher
	pattern   Pattern
}

// ruleSet keeps compiled entries partitioned by resource kind. Snapshot
// structures are never mutated in place: every replacement builds a new one.
type ruleSet struct {
	topics       []indexedEntry
	groups       []indexedEntry
	transactions []indexedEntry
}

func (r *ruleSet) entries(kind ResourceKind) []indexedEntry {
	switch kind {
	case KindTopic:
		return r.topics
	case KindGroup:
		return r.groups
	case KindTransaction:
		return r.transactions
	default:
		return nil
	}
}

// snapshot is the immutable, versioned authorization state.
type snapshot struct {
	generation uint64
	rules      ruleSet
}

// cacheKey identifies an authorization decision within one generation.
type cacheKey struct {
	principal string
	kind      ResourceKind
	name      string
	actions   Action
}

// cacheValue stores the decision together with the generation it was made
// under. Storing the generation makes a stale entry self-describing even
// though the cache is cleared on every commit.
type cacheValue struct {
	allowed    bool
	generation uint64
}

// Authorizer is the importable component a Go message broker embeds. It is
// safe for concurrent use: ACL replacement, group ownership changes and
// authorization checks may run from different goroutines.
type Authorizer struct {
	mu    sync.RWMutex
	delim byte

	current snapshot

	// groups binds a consumer group name to its owning principal so that a
	// cross-tenant name cannot be reused via a rename.
	groups map[string]string

	// cache holds per-generation allow/deny outcomes. It is replaced on every
	// generation bump, so it can never extend an old decision past its
	// generation.
	cache map[cacheKey]cacheValue
}

// NewAuthorizer builds an authorizer. delim is the boundary character used
// for prefix and wildcard matching (typically '-' or '/'). Use '-' to model
// names such as "tenant-a-orders".
func NewAuthorizer(delim byte) *Authorizer {
	if delim == 0 {
		delim = '-'
	}
	return &Authorizer{
		delim:  delim,
		groups: make(map[string]string),
		cache:  make(map[cacheKey]cacheValue),
	}
}

// Delim returns the boundary character the authorizer was configured with.
func (a *Authorizer) Delim() byte {
	return a.delim
}

// Generation returns the current ACL generation. It starts at zero and
// increments on every committed ACL replacement.
func (a *Authorizer) Generation() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.current.generation
}

// ReplaceACL validates and atomically commits a new rule set, assigning it
// the next generation. On any validation error the existing ACL is left
// untouched and the generation is unchanged. The decision cache is dropped as
// part of the commit so cached answers can never outlive the rules that
// produced them.
func (a *Authorizer) ReplaceACL(entries []Entry) (uint64, error) {
	compiled := ruleSet{}
	for _, entry := range entries {
		if err := entry.validate(a.delim); err != nil {
			return 0, fmt.Errorf("%w: %v", ErrInvalidEntry, err)
		}
		matcher, err := entry.Pattern.Compile(a.delim)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrInvalidEntry, err)
		}
		compiledEntry := indexedEntry{
			principal: entry.Principal,
			effect:    entry.Effect,
			actions:   entry.Actions,
			kind:      entry.Pattern.Kind,
			match:     matcher,
			pattern:   entry.Pattern,
		}
		switch entry.Pattern.Kind {
		case KindTopic:
			compiled.topics = append(compiled.topics, compiledEntry)
		case KindGroup:
			compiled.groups = append(compiled.groups, compiledEntry)
		case KindTransaction:
			compiled.transactions = append(compiled.transactions, compiledEntry)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	next := snapshot{
		generation: a.current.generation + 1,
		rules:      compiled,
	}
	a.current = next
	a.invalidateCacheLocked()
	return next.generation, nil
}

// Authorize evaluates one request against the current generation. It returns
// nil when the request is allowed and an error wrapping ErrNotAuthorized
// otherwise. The resource pattern type never participates here: only the
// resource kind selects which entries are eligible, so a topic rule can never
// be applied to a group or transaction id.
func (a *Authorizer) Authorize(principal string, resource Resource, actions Action) error {
	if principal == "" {
		return fmt.Errorf("%w: %v", ErrNotAuthorized, ErrEmptyPrincipal)
	}
	if actions == 0 || actions&^validActionMask != 0 {
		return fmt.Errorf("%w: %v", ErrNotAuthorized, ErrUnknownAction)
	}
	if err := resource.Valid(a.delim); err != nil {
		return fmt.Errorf("%w: %v", ErrNotAuthorized, err)
	}

	key := cacheKey{principal: principal, kind: resource.Kind, name: resource.Name, actions: actions}

	a.mu.RLock()
	generation := a.current.generation
	if cached, ok := a.cache[key]; ok && cached.generation == generation {
		allowed := cached.allowed
		a.mu.RUnlock()
		if allowed {
			return nil
		}
		return fmt.Errorf("%w: principal %q denied %s %q", ErrNotAuthorized, principal, resource.Kind, resource.Name)
	}
	allowed := evaluate(a.current, principal, resource, actions)
	a.mu.RUnlock()

	// Re-take the lock as a writer to record the outcome. The re-read of the
	// generation guarantees the decision is never cached against a newer ACL
	// if a replacement raced with this evaluation.
	a.mu.Lock()
	if a.current.generation == generation {
		a.cache[key] = cacheValue{allowed: allowed, generation: generation}
	}
	a.mu.Unlock()

	if allowed {
		return nil
	}
	return fmt.Errorf("%w: principal %q denied %s %q", ErrNotAuthorized, principal, resource.Kind, resource.Name)
}

// invalidateCacheLocked drops every cached decision. The caller must hold the
// write lock.
func (a *Authorizer) invalidateCacheLocked() {
	a.cache = make(map[cacheKey]cacheValue)
}
