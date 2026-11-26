package crush

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

const DefaultMaxAttempts = 64

// Mapper maps PG identifiers to OSD sets using Straw2 buckets and CRUSH rules.
type Mapper struct {
	root *Node
	hash Hash64Func

	mu          sync.RWMutex
	rules       map[string]*Rule
	ruleOrder   []string
	defaultRule string
	maxAttempts int
	overrides   map[int]runtimeOverride
	initErr     error
	stableIndep bool
	ignoreState bool
	implicitRW1 bool
}

type runtimeOverride struct {
	up       *bool
	weight   *float64
	reweight *float64
}

// Config is an optional structured constructor argument.
type Config struct {
	Root        *Node
	Topology    *Node
	Tree        *Node
	Hasher      any
	HashFunc    any
	Rules       []*Rule
	Rule        *Rule
	MaxAttempts int
	StableIndep *bool
}

// NewMapper creates a mapper from optional root node, hash, Config, and rules.
func NewMapper(args ...any) *Mapper {
	root, hasher, ruleArgs, config := parseConstructorArgs(args)
	h, hashErr := adaptHasher(hasher)
	m := &Mapper{
		root:        root,
		hash:        h,
		rules:       make(map[string]*Rule),
		maxAttempts: DefaultMaxAttempts,
		overrides:   make(map[int]runtimeOverride),
		stableIndep: true,
	}
	if hashErr != nil {
		m.initErr = hashErr
	}
	if config != nil {
		if config.MaxAttempts > 0 {
			m.maxAttempts = config.MaxAttempts
		}
		if config.StableIndep != nil {
			m.stableIndep = *config.StableIndep
		}
	}
	m.implicitRW1 = m.root != nil && !topologyHasPositiveReweight(m.root)
	for _, rule := range ruleArgs {
		if addErr := m.AddRule(rule); addErr != nil && m.initErr == nil {
			m.initErr = addErr
		}
	}
	if len(m.ruleOrder) > 0 {
		m.defaultRule = m.ruleOrder[0]
	}
	return m
}

func parseConstructorArgs(args []any) (*Node, any, []any, *Config) {
	var root *Node
	var hasher any
	var rules []any
	var config *Config

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue
		case *Node:
			root = value
		case Node:
			nodeCopy := value
			root = &nodeCopy
		case *Rule:
			rules = append(rules, value)
		case Rule:
			rules = append(rules, value)
		case Config:
			cfg := value
			config = &cfg
		case *Config:
			config = value
		case HashFunc, func(uint64) uint64, Hash64Func, func(...uint64) uint64, Hasher:
			hasher = value
		default:
			if _, adaptErr := adaptHasher(value); adaptErr == nil {
				hasher = value
			} else {
				rules = append(rules, value)
			}
		}
	}

	if config != nil {
		if root == nil {
			root = firstNonNilNode(config.Root, config.Topology, config.Tree)
		}
		if hasher == nil {
			if config.Hasher != nil {
				hasher = config.Hasher
			} else if config.HashFunc != nil {
				hasher = config.HashFunc
			}
		}
		if config.Rule != nil {
			rules = append(rules, config.Rule)
		}
		if len(config.Rules) > 0 {
			rules = append(rules, config.Rules)
		}
	}

	return root, hasher, rules, config
}

func firstNonNilNode(nodes ...*Node) *Node {
	for _, node := range nodes {
		if node != nil {
			return node
		}
	}
	return nil
}

func topologyHasPositiveReweight(root *Node) bool {
	positive := false
	root.Walk(func(node *Node) {
		if node.ReweightRatio() > 0 {
			positive = true
		}
	})
	return positive
}

// NewMapperE is the error-returning constructor variant.
func NewMapperE(args ...any) (*Mapper, error) {
	m := NewMapper(args...)
	if m.initErr != nil {
		return m, m.initErr
	}
	return m, nil
}

// New is an alias for NewMapper.
func New(args ...any) *Mapper {
	return NewMapper(args...)
}

// MustNewMapper panics if construction encounters an invalid hasher/rule.
func MustNewMapper(args ...any) *Mapper {
	m, err := NewMapperE(args...)
	if err != nil {
		panic(err)
	}
	return m
}

// SetMaxAttempts configures the fallback used when a Step omits attempts.
func (m *Mapper) SetMaxAttempts(n int) *Mapper {
	if n > 0 {
		m.maxAttempts = n
	}
	return m
}

// SetStableIndep toggles anchor-preserving indep mappings. When enabled
// (default), healthy slots retain their exact deterministic placement while a
// failed slot is replaced without consuming another slot's anchor.
func (m *Mapper) SetStableIndep(enabled bool) *Mapper {
	m.stableIndep = enabled
	return m
}

func (m *Mapper) canonicalView() *Mapper {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	overrides := make(map[int]runtimeOverride, len(m.overrides))
	for id, override := range m.overrides {
		// Weight changes belong to the deterministic projection; operational
		// up/down overrides do not.
		overrides[id] = runtimeOverride{weight: override.weight, reweight: override.reweight}
	}
	maxAttempts := m.maxAttempts
	hash := m.hash
	root := m.root
	m.mu.RUnlock()
	return &Mapper{
		root:        root,
		hash:        hash,
		maxAttempts: maxAttempts,
		stableIndep: false,
		ignoreState: true,
		implicitRW1: m.implicitRW1,
		overrides:   overrides,
	}
}

// AddRule adds a Rule or *Rule.
func (m *Mapper) AddRule(rule any) error {
	if m == nil {
		return errors.New("crush: nil mapper")
	}

	switch rules := rule.(type) {
	case []Rule:
		for i := range rules {
			if err := m.AddRule(&rules[i]); err != nil {
				return err
			}
		}
		return nil
	case []*Rule:
		for _, r := range rules {
			if err := m.AddRule(r); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for _, r := range rules {
			if err := m.AddRule(r); err != nil {
				return err
			}
		}
		return nil
	case map[string]Rule:
		for name, r := range rules {
			r.Name = firstNonEmpty(r.Name, name)
			if err := m.AddRule(&r); err != nil {
				return err
			}
		}
		return nil
	case map[string]*Rule:
		for name, r := range rules {
			if r != nil && r.Name == "" {
				r.Name = name
			}
			if err := m.AddRule(r); err != nil {
				return err
			}
		}
		return nil
	}

	rv := reflect.ValueOf(rule)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		for i := 0; i < rv.Len(); i++ {
			if err := m.AddRule(rv.Index(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	if rv.Kind() == reflect.Map {
		iter := rv.MapRange()
		for iter.Next() {
			if err := m.AddRule(iter.Value().Interface()); err != nil {
				return err
			}
		}
		return nil
	}

	var r *Rule
	switch v := rule.(type) {
	case nil:
		return errors.New("crush: nil rule")
	case *Rule:
		r = v
	case Rule:
		r = &v
	default:
		return fmt.Errorf("crush: unsupported rule type %T", rule)
	}
	if r.Name == "" {
		r.Name = fmt.Sprintf("rule-%d", len(m.ruleOrder))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rules[r.Name]; !exists {
		m.ruleOrder = append(m.ruleOrder, r.Name)
	}
	m.rules[r.Name] = r
	if m.defaultRule == "" {
		m.defaultRule = r.Name
	}
	return nil
}

// MustAddRule panics if rule cannot be added.
func (m *Mapper) MustAddRule(rule any) *Mapper {
	if err := m.AddRule(rule); err != nil {
		panic(err)
	}
	return m
}

// AddRules adds several rules.
func (m *Mapper) AddRules(rules ...*Rule) *Mapper {
	for _, rule := range rules {
		_ = m.AddRule(rule)
	}
	return m
}

// SetDefaultRule selects the rule used when MapPG has no explicit rule.
func (m *Mapper) SetDefaultRule(name string) *Mapper {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultRule = name
	return m
}

// Rule returns a named rule.
func (m *Mapper) Rule(name string) *Rule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rules[name]
}

// Rules returns rule names in insertion order.
func (m *Mapper) Rules() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.ruleOrder...)
}

// Root returns the topology root.
func (m *Mapper) Root() *Node { return m.root }

// SetUp overrides the up state of id.
func (m *Mapper) SetUp(id int, up bool) *Mapper {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.overrides[id]
	o.up = &up
	m.overrides[id] = o
	return m
}

// SetDown marks id down or clears a previous down override when down is false.
func (m *Mapper) SetDown(id int, down bool) *Mapper {
	return m.SetUp(id, !down)
}

// SetWeight overrides the static weight of id.
func (m *Mapper) SetWeight(id int, weight float64) *Mapper {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.overrides[id]
	o.weight = &weight
	m.overrides[id] = o
	return m
}

// SetReweight sets an explicit runtime reweight. In particular, zero drains id.
func (m *Mapper) SetReweight(id int, reweight float64) *Mapper {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.overrides[id]
	o.reweight = &reweight
	m.overrides[id] = o
	return m
}

// ResetRuntime removes all runtime overrides.
func (m *Mapper) ResetRuntime() *Mapper {
	m.mu.Lock()
	m.overrides = make(map[int]runtimeOverride)
	m.mu.Unlock()
	return m
}

func (m *Mapper) overrideFor(id int) runtimeOverride {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.overrides[id]
}

func (m *Mapper) nodeUp(n *Node) bool {
	if n == nil {
		return false
	}
	if m.ignoreState {
		return true
	}
	o := m.overrideFor(n.ID)
	if o.up != nil {
		return *o.up
	}
	return n.IsUp()
}

func (m *Mapper) nodeWeight(n *Node) float64 {
	if n == nil {
		return 0
	}
	o := m.overrideFor(n.ID)
	w := n.StaticWeightValue()
	if o.weight != nil {
		w = *o.weight
	}
	r := n.ReweightRatio()
	if o.reweight != nil {
		r = *o.reweight
	}
	if r == 0 && o.reweight == nil && m.implicitRW1 {
		r = 1
	}
	if w <= 0 || r <= 0 {
		return 0
	}
	return w * r
}

func (m *Mapper) hashTuple(parts ...uint64) uint64 {
	if m != nil && m.hash != nil {
		return m.hash(parts...)
	}
	return DefaultHash(parts...)
}

// Map is an alias for MapPG.
func (m *Mapper) Map(pgID any, replicas int, options ...any) (*Result, error) {
	return m.MapPG(pgID, replicas, options...)
}

// Place is an alias for MapPG.
func (m *Mapper) Place(pgID any, replicas int, options ...any) (*Result, error) {
	return m.MapPG(pgID, replicas, options...)
}
