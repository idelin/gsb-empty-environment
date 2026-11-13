package netsec

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Policy grants exact action sets by package digest.
type Policy struct {
	mu     sync.RWMutex
	grants map[Digest]ActionSet
}

// NewPolicy returns an empty, deny-by-default policy.
func NewPolicy() *Policy {
	return &Policy{grants: make(map[Digest]ActionSet)}
}

// Grant adds exact actions to a package digest's grant.
func (p *Policy) Grant(digest Digest, actions ...Action) error {
	if p == nil {
		return fmt.Errorf("netsec: nil policy")
	}
	if err := digest.Validate(); err != nil {
		return err
	}
	for _, action := range actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("netsec: cannot grant invalid action: %w", err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.grants == nil {
		p.grants = make(map[Digest]ActionSet)
	}
	p.grants[digest] = p.grants[digest].Union(NewActionSet(actions...))
	return nil
}

// Granted returns a copy of the package's grant.
func (p *Policy) Granted(digest Digest) ActionSet {
	if p == nil {
		return NewActionSet()
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.grants[digest]
}

// Allowed reports exact authorization.
func (p *Policy) Allowed(digest Digest, action Action) bool {
	return p.Granted(digest).Has(action)
}

// ToolInfo is host metadata for an exact tool ID.
type ToolInfo struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

// EndpointInfo is host metadata for an exact endpoint ID.
type EndpointInfo struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

// Catalog is the host registry of tool IDs, endpoint IDs, and environment
// values. Values are intentionally not exposed by a public getter.
type Catalog struct {
	mu        sync.RWMutex
	tools     map[string]ToolInfo
	endpoints map[string]EndpointInfo
	env       map[string]string
}

// NewCatalog returns an empty host registry.
func NewCatalog() *Catalog {
	return &Catalog{
		tools:     make(map[string]ToolInfo),
		endpoints: make(map[string]EndpointInfo),
		env:       make(map[string]string),
	}
}

// RegisterTool registers one exact tool ID. Registering the same ID twice is
// an error so that a policy cannot silently change meaning.
func (c *Catalog) RegisterTool(id, description string) error {
	if c == nil {
		return fmt.Errorf("netsec: nil catalog")
	}
	if err := validateOpaqueID("tool", id); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tools == nil {
		c.tools = make(map[string]ToolInfo)
	}
	if _, exists := c.tools[id]; exists {
		return fmt.Errorf("netsec: tool %q is already registered", id)
	}
	c.tools[id] = ToolInfo{ID: id, Description: description}
	return nil
}

// RegisterEndpoint registers one exact endpoint ID.
func (c *Catalog) RegisterEndpoint(id, description string) error {
	if c == nil {
		return fmt.Errorf("netsec: nil catalog")
	}
	if err := validateOpaqueID("endpoint", id); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.endpoints == nil {
		c.endpoints = make(map[string]EndpointInfo)
	}
	if _, exists := c.endpoints[id]; exists {
		return fmt.Errorf("netsec: endpoint %q is already registered", id)
	}
	c.endpoints[id] = EndpointInfo{ID: id, Description: description}
	return nil
}

// RegisterEnvironment registers a value that may be injected only into an
// authorized Execution.Env call.
func (c *Catalog) RegisterEnvironment(name, value string) error {
	if c == nil {
		return fmt.Errorf("netsec: nil catalog")
	}
	if err := ValidateEnvName(name); err != nil {
		return err
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("netsec: environment value for %q contains NUL", name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.env == nil {
		c.env = make(map[string]string)
	}
	if _, exists := c.env[name]; exists {
		return fmt.Errorf("netsec: environment %q is already registered", name)
	}
	c.env[name] = value
	return nil
}

// Tool returns registered tool metadata by exact ID.
func (c *Catalog) Tool(id string) (ToolInfo, bool) {
	if c == nil {
		return ToolInfo{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.tools[id]
	return info, ok
}

// Endpoint returns registered endpoint metadata by exact ID.
func (c *Catalog) Endpoint(id string) (EndpointInfo, bool) {
	if c == nil {
		return EndpointInfo{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.endpoints[id]
	return info, ok
}

// EnvironmentRegistered reports registration without revealing the value.
func (c *Catalog) EnvironmentRegistered(name string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.env[name]
	return ok
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateEnvName accepts a portable POSIX environment variable name.
func ValidateEnvName(name string) error {
	if !envNamePattern.MatchString(name) {
		return fmt.Errorf("netsec: invalid environment variable name %q", name)
	}
	return nil
}

type catalogSnapshot struct {
	tools     map[string]ToolInfo
	endpoints map[string]EndpointInfo
	env       map[string]string
}

func (c *Catalog) snapshot() catalogSnapshot {
	s := catalogSnapshot{
		tools:     make(map[string]ToolInfo),
		endpoints: make(map[string]EndpointInfo),
		env:       make(map[string]string),
	}
	if c == nil {
		return s
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for id, info := range c.tools {
		s.tools[id] = info
	}
	for id, info := range c.endpoints {
		s.endpoints[id] = info
	}
	for name, value := range c.env {
		s.env[name] = value
	}
	return s
}
