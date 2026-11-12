package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrBadPrefix       = errors.New("netsec: reserved prefix must end with '.'")
	ErrPrefixOwned     = errors.New("netsec: reserved prefix already registered")
	ErrPublishRejected = errors.New("netsec: publish rejected by reserved prefix")
)

// Registry tracks reserved ID prefixes and their owners. Reserved prefixes
// end with '.'; the first registrant owns the prefix.
type Registry struct {
	mu     sync.Mutex
	owners map[string]string
}

func NewRegistry() *Registry {
	return &Registry{owners: make(map[string]string)}
}

// Reserve claims prefix for owner. Exactly one concurrent registrant wins.
func (r *Registry) Reserve(prefix, owner string) error {
	p := strings.ToLower(strings.TrimSpace(prefix))
	if !strings.HasSuffix(p, ".") {
		return ErrBadPrefix
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.owners[p]; taken {
		return ErrPrefixOwned
	}
	r.owners[p] = owner
	return nil
}

// ownerOf returns the owner of the longest reserved prefix containing id.
func (r *Registry) ownerOf(id string) (string, bool) {
	lower := strings.ToLower(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	best := -1
	owner := ""
	for p, o := range r.owners {
		if strings.HasPrefix(lower, p) && len(p) > best {
			best = len(p)
			owner = o
		}
	}
	return owner, best >= 0
}

// Index is a mutable package source. Publishing goes through Gate so
// rejected packages never enter any source's index.
type Index struct {
	mu   sync.Mutex
	name string
	pkgs map[string]map[string][]Requirement
}

func NewIndex(name string) *Index {
	return &Index{name: name, pkgs: make(map[string]map[string][]Requirement)}
}

func (i *Index) Name() string { return i.name }

func (i *Index) Versions(id string) []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	vs := make([]string, 0, len(i.pkgs[strings.ToLower(id)]))
	for v := range i.pkgs[strings.ToLower(id)] {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

func (i *Index) Deps(id, version string) []Requirement {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]Requirement(nil), i.pkgs[strings.ToLower(id)][version]...)
}

func (i *Index) add(id, version string, deps []Requirement) {
	i.mu.Lock()
	defer i.mu.Unlock()
	lid := strings.ToLower(id)
	if i.pkgs[lid] == nil {
		i.pkgs[lid] = make(map[string][]Requirement)
	}
	i.pkgs[lid][version] = append([]Requirement(nil), deps...)
}

// Gate enforces reserved-prefix admission before anything is indexed.
type Gate struct {
	reg *Registry
}

func NewGate(reg *Registry) *Gate {
	return &Gate{reg: reg}
}

// Publish adds id@version to idx unless a reserved prefix owned by someone
// else covers id. Rejected publishes never reach the index.
func (g *Gate) Publish(idx *Index, owner, id, version string, deps []Requirement) error {
	if o, ok := g.reg.ownerOf(id); ok && o != owner {
		return fmt.Errorf("%w: %s is reserved by another owner", ErrPublishRejected, id)
	}
	idx.add(id, version, deps)
	return nil
}
