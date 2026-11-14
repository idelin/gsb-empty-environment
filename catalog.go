package netsec

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrUnknownIndex is returned when an index or alias cannot be resolved.
	ErrUnknownIndex = errors.New("netsec: unknown index or alias")
	// ErrAliasExists is returned when create-only alias semantics are violated.
	ErrAliasExists = errors.New("netsec: alias already exists")
	// ErrAmbiguousWrite is returned when an alias has no unambiguous write index.
	ErrAmbiguousWrite = errors.New("netsec: alias has no unambiguous write index")
	// ErrInvalidCatalog is returned for malformed catalog mutations.
	ErrInvalidCatalog = errors.New("netsec: invalid catalog")
)

// Index describes one concrete index: its known fields and read field policy.
//
// Fields is the mapping schema used to expand request wildcards. Unknown
// fields are never discovered by a request wildcard; they must be named
// explicitly in a policy and request to be returned.
type Index struct {
	Name   string
	Fields []string
	Policy Policy
}

// IndexDefinition is a descriptive alias for Index.
type IndexDefinition = Index

// Alias describes an alias. Policy, when non-nil, is an additional
// restriction that applies to every backing index. Indices are always
// concrete index names.
type Alias struct {
	Name       string
	Indices    []string
	WriteIndex string
	Policy     *Policy
	Generation uint64
}

// ResolvedTarget is one concrete index after alias resolution.
type ResolvedTarget struct {
	Index            string
	Fields           []string
	Policy           Policy
	Aliases          []string
	AliasPolicies    []Policy
	AliasGenerations []uint64
}

// Resolution is a generation-consistent expansion of an index expression.
type Resolution struct {
	Expression string
	Generation uint64
	Targets    []ResolvedTarget
}

// Snapshot is an immutable point-in-time view of a Catalog. Every mutation
// creates a new generation; callers may hold a snapshot safely while the
// catalog continues to change.
type Snapshot struct {
	Generation uint64
	Indices    map[string]Index
	Aliases    map[string]Alias
}

// Catalog stores concrete index definitions, policies, and aliases. It is safe
// for concurrent use by multiple goroutines.
type Catalog struct {
	mu         sync.RWMutex
	generation uint64
	indices    map[string]Index
	aliases    map[string]Alias
}

// NewCatalog returns an empty catalog. Initial generation is 1 so a zero-value
// generation always means "no snapshot".
func NewCatalog() *Catalog {
	return &Catalog{
		generation: 1,
		indices:    make(map[string]Index),
		aliases:    make(map[string]Alias),
	}
}

// Generation returns the current catalog generation.
func (c *Catalog) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// AddIndex adds a concrete index. It fails if the name already exists as an
// index or alias.
func (c *Catalog) AddIndex(index Index) error {
	return c.addIndex(index, false)
}

// PutIndex adds or replaces a concrete index definition.
func (c *Catalog) PutIndex(index Index) error {
	return c.addIndex(index, true)
}

// DefineIndex is a convenience wrapper around AddIndex.
func (c *Catalog) DefineIndex(name string, fields []string, policy Policy) error {
	return c.AddIndex(Index{Name: name, Fields: fields, Policy: policy})
}

func (c *Catalog) addIndex(index Index, replace bool) error {
	if err := validateIndex(index); err != nil {
		return err
	}
	index = cloneIndex(index)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.indices == nil {
		c.indices = make(map[string]Index)
	}
	if _, ok := c.aliases[index.Name]; ok {
		return fmt.Errorf("%w: %q is an alias", ErrInvalidCatalog, index.Name)
	}
	if _, ok := c.indices[index.Name]; ok && !replace {
		return fmt.Errorf("%w: index %q already exists", ErrInvalidCatalog, index.Name)
	}
	c.indices[index.Name] = index
	c.generation++
	return nil
}

func validateIndex(index Index) error {
	if strings.TrimSpace(index.Name) == "" {
		return fmt.Errorf("%w: empty index name", ErrInvalidCatalog)
	}
	if strings.ContainsAny(index.Name, " ,*?") {
		return fmt.Errorf("%w: invalid index name %q", ErrInvalidCatalog, index.Name)
	}
	for _, field := range index.Fields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("%w: empty field in index %q", ErrInvalidCatalog, index.Name)
		}
	}
	return nil
}

// SetIndexPolicy replaces an index's field policy and advances the generation.
func (c *Catalog) SetIndexPolicy(name string, policy Policy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	index, ok := c.indices[name]
	if !ok {
		return fmt.Errorf("%w: index %q", ErrUnknownIndex, name)
	}
	index.Policy = policy.Clone()
	c.indices[name] = index
	c.generation++
	return nil
}

// SetIndexFields replaces an index's known field schema and advances the
// generation.
func (c *Catalog) SetIndexFields(name string, fields []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	index, ok := c.indices[name]
	if !ok {
		return fmt.Errorf("%w: index %q", ErrUnknownIndex, name)
	}
	index.Fields = append([]string(nil), fields...)
	c.indices[name] = index
	c.generation++
	return nil
}

// DeleteIndex removes an unused concrete index.
func (c *Catalog) DeleteIndex(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.indices[name]; !ok {
		return fmt.Errorf("%w: index %q", ErrUnknownIndex, name)
	}
	for aliasName, alias := range c.aliases {
		for _, index := range alias.Indices {
			if index == name {
				return fmt.Errorf("%w: index %q is used by alias %q", ErrInvalidCatalog, name, aliasName)
			}
		}
	}
	delete(c.indices, name)
	c.generation++
	return nil
}

// CreateAlias creates an alias that must not already exist.
func (c *Catalog) CreateAlias(name string, indices ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.aliases[name]; ok {
		return fmt.Errorf("%w: %q", ErrAliasExists, name)
	}
	return c.putAliasLocked(Alias{Name: name, Indices: indices}, false)
}

// PutAlias creates or replaces an alias definition atomically and advances the
// generation.
func (c *Catalog) PutAlias(alias Alias) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.putAliasLocked(alias, true)
}

func (c *Catalog) putAliasLocked(alias Alias, replace bool) error {
	alias.Name = strings.TrimSpace(alias.Name)
	if alias.Name == "" {
		return fmt.Errorf("%w: empty alias name", ErrInvalidCatalog)
	}
	if strings.ContainsAny(alias.Name, " ,*?") {
		return fmt.Errorf("%w: invalid alias name %q", ErrInvalidCatalog, alias.Name)
	}
	if _, ok := c.indices[alias.Name]; ok {
		return fmt.Errorf("%w: %q is a concrete index", ErrInvalidCatalog, alias.Name)
	}
	if _, ok := c.aliases[alias.Name]; ok && !replace {
		return fmt.Errorf("%w: %q", ErrAliasExists, alias.Name)
	}
	if len(alias.Indices) == 0 {
		return fmt.Errorf("%w: alias %q has no indices", ErrInvalidCatalog, alias.Name)
	}

	seen := make(map[string]struct{}, len(alias.Indices))
	indices := make([]string, 0, len(alias.Indices))
	for _, index := range alias.Indices {
		index = strings.TrimSpace(index)
		if _, ok := c.indices[index]; !ok {
			return fmt.Errorf("%w: alias %q references missing index %q", ErrUnknownIndex, alias.Name, index)
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		indices = append(indices, index)
	}
	sort.Strings(indices)

	writeIndex := strings.TrimSpace(alias.WriteIndex)
	if writeIndex == "" && len(indices) == 1 {
		writeIndex = indices[0]
	}
	if writeIndex != "" {
		if _, ok := seen[writeIndex]; !ok {
			return fmt.Errorf("%w: write index %q is not in alias %q", ErrInvalidCatalog, writeIndex, alias.Name)
		}
	}

	c.generation++
	if c.aliases == nil {
		c.aliases = make(map[string]Alias)
	}
	alias.Indices = indices
	alias.WriteIndex = writeIndex
	alias.Generation = c.generation
	if alias.Policy != nil {
		policy := alias.Policy.Clone()
		alias.Policy = &policy
	}
	c.aliases[alias.Name] = alias
	return nil
}

// RetargetAlias replaces the backing indices of an existing alias and advances
// the generation. In-flight snapshots keep their old resolution.
func (c *Catalog) RetargetAlias(name string, indices ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	alias, ok := c.aliases[name]
	if !ok {
		return fmt.Errorf("%w: alias %q", ErrUnknownIndex, name)
	}
	alias.Indices = indices
	if !containsString(indices, alias.WriteIndex) {
		alias.WriteIndex = ""
	}
	return c.putAliasLocked(alias, true)
}

// SetAliasWriteIndex changes the write target of an alias and advances the
// generation.
func (c *Catalog) SetAliasWriteIndex(name, index string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	alias, ok := c.aliases[name]
	if !ok {
		return fmt.Errorf("%w: alias %q", ErrUnknownIndex, name)
	}
	alias.WriteIndex = index
	return c.putAliasLocked(alias, true)
}

// SetAliasPolicy sets an additional restriction on an alias. It composes with
// every backing index's policy by intersection.
func (c *Catalog) SetAliasPolicy(name string, policy Policy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	alias, ok := c.aliases[name]
	if !ok {
		return fmt.Errorf("%w: alias %q", ErrUnknownIndex, name)
	}
	policy = policy.Clone()
	alias.Policy = &policy
	return c.putAliasLocked(alias, true)
}

// ClearAliasPolicy removes an alias-level restriction.
func (c *Catalog) ClearAliasPolicy(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	alias, ok := c.aliases[name]
	if !ok {
		return fmt.Errorf("%w: alias %q", ErrUnknownIndex, name)
	}
	alias.Policy = nil
	return c.putAliasLocked(alias, true)
}

// DeleteAlias removes an alias and advances the generation.
func (c *Catalog) DeleteAlias(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.aliases[name]; !ok {
		return fmt.Errorf("%w: alias %q", ErrUnknownIndex, name)
	}
	delete(c.aliases, name)
	c.generation++
	return nil
}

// Snapshot returns a deep, immutable point-in-time copy of the catalog.
func (c *Catalog) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot := Snapshot{
		Generation: c.generation,
		Indices:    make(map[string]Index, len(c.indices)),
		Aliases:    make(map[string]Alias, len(c.aliases)),
	}
	for name, index := range c.indices {
		snapshot.Indices[name] = cloneIndex(index)
	}
	for name, alias := range c.aliases {
		snapshot.Aliases[name] = cloneAlias(alias)
	}
	return snapshot
}

// Resolve expands an index expression using a fresh snapshot.
func (c *Catalog) Resolve(expression string) (Resolution, error) {
	return c.Snapshot().Resolve(expression)
}

// Resolve expands an index expression. Expressions may be comma-separated and
// may contain concrete indices, aliases, index patterns, or _all. The result is
// deduplicated by concrete index and is bound to the snapshot generation.
func (s Snapshot) Resolve(expression string) (Resolution, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return Resolution{}, fmt.Errorf("%w: empty index expression", ErrUnknownIndex)
	}

	targets := make(map[string]*ResolvedTarget)
	add := func(indexName, aliasName string, aliasPolicy *Policy, aliasGeneration uint64) {
		target, ok := targets[indexName]
		if !ok {
			index := s.Indices[indexName]
			target = &ResolvedTarget{
				Index:  indexName,
				Fields: append([]string(nil), index.Fields...),
				Policy: index.Policy.Clone(),
			}
			targets[indexName] = target
		}
		if aliasName != "" {
			target.Aliases = appendUnique(target.Aliases, aliasName)
			target.AliasGenerations = append(target.AliasGenerations, aliasGeneration)
			if aliasPolicy != nil {
				target.AliasPolicies = append(target.AliasPolicies, aliasPolicy.Clone())
			}
		}
	}

	for _, raw := range strings.Split(expression, ",") {
		token := strings.TrimSpace(raw)
		if token == "" {
			return Resolution{}, fmt.Errorf("%w: empty token in %q", ErrUnknownIndex, expression)
		}
		if token == "_all" {
			token = "*"
		}

		if alias, ok := s.Aliases[token]; ok {
			for _, index := range alias.Indices {
				add(index, alias.Name, alias.Policy, alias.Generation)
			}
			continue
		}
		if _, ok := s.Indices[token]; ok {
			add(token, "", nil, 0)
			continue
		}

		matched := false
		if hasPatternMeta(token) {
			for _, indexName := range sortedIndexNames(s.Indices) {
				if indexPatternMatches(token, indexName) {
					add(indexName, "", nil, 0)
					matched = true
				}
			}
			for _, aliasName := range sortedAliasNames(s.Aliases) {
				if !indexPatternMatches(token, aliasName) {
					continue
				}
				alias := s.Aliases[aliasName]
				for _, index := range alias.Indices {
					add(index, alias.Name, alias.Policy, alias.Generation)
				}
				matched = true
			}
		}
		if !matched {
			return Resolution{}, fmt.Errorf("%w: %q", ErrUnknownIndex, token)
		}
	}

	if len(targets) == 0 {
		return Resolution{}, fmt.Errorf("%w: %q resolved to no indices", ErrUnknownIndex, expression)
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	resolved := make([]ResolvedTarget, 0, len(names))
	for _, name := range names {
		resolved = append(resolved, *targets[name])
	}
	return Resolution{
		Expression: expression,
		Generation: s.Generation,
		Targets:    resolved,
	}, nil
}

func (s Snapshot) resolveWrite(aliasName string) (Alias, ResolvedTarget, error) {
	alias, ok := s.Aliases[aliasName]
	if !ok {
		return Alias{}, ResolvedTarget{}, fmt.Errorf("%w: alias %q", ErrUnknownIndex, aliasName)
	}
	writeIndex := alias.WriteIndex
	if writeIndex == "" {
		if len(alias.Indices) != 1 {
			return Alias{}, ResolvedTarget{}, fmt.Errorf("%w: %q", ErrAmbiguousWrite, aliasName)
		}
		writeIndex = alias.Indices[0]
	}
	index, ok := s.Indices[writeIndex]
	if !ok {
		return Alias{}, ResolvedTarget{}, fmt.Errorf("%w: write index %q", ErrUnknownIndex, writeIndex)
	}
	target := ResolvedTarget{
		Index:  writeIndex,
		Fields: append([]string(nil), index.Fields...),
		Policy: index.Policy.Clone(),
	}
	target.Aliases = []string{alias.Name}
	target.AliasGenerations = []uint64{alias.Generation}
	if alias.Policy != nil {
		target.AliasPolicies = []Policy{alias.Policy.Clone()}
	}
	return alias, target, nil
}

func cloneIndex(index Index) Index {
	index.Fields = append([]string(nil), index.Fields...)
	index.Policy = index.Policy.Clone()
	return index
}

func cloneAlias(alias Alias) Alias {
	alias.Indices = append([]string(nil), alias.Indices...)
	if alias.Policy != nil {
		policy := alias.Policy.Clone()
		alias.Policy = &policy
	}
	return alias
}

func sortedIndexNames(indices map[string]Index) []string {
	names := make([]string, 0, len(indices))
	for name := range indices {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedAliasNames(aliases map[string]Alias) []string {
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func indexPatternMatches(pattern, name string) bool {
	if pattern == "*" || pattern == "**" {
		return true
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	if value == "" {
		return false
	}
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
