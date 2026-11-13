package netsec

import (
	"fmt"
	"sort"
	"strings"
)

type exportKey struct {
	name    string
	version string
}

type moduleInfo struct {
	module  Module
	exports map[exportKey]Export
}

type catalog struct {
	modules map[string]*moduleInfo
}

func newCatalog(modules []Module, globalOrder []string) (*catalog, error) {
	if len(modules) > MaxModules {
		return nil, invalidf("%d modules declared; maximum is %d", len(modules), MaxModules)
	}

	c := &catalog{modules: make(map[string]*moduleInfo, len(modules))}
	for i := range modules {
		m := modules[i]
		if m.ID == "" {
			return nil, invalidf("module at index %d has an empty ID", i)
		}
		if _, exists := c.modules[m.ID]; exists {
			return nil, invalidf("duplicate module ID %q", m.ID)
		}

		info := &moduleInfo{
			module: Module{
				ID:           m.ID,
				Trusted:      m.Trusted,
				Dependencies: append([]string(nil), m.Dependencies...),
				Exports:      append([]Export(nil), m.Exports...),
			},
			exports: make(map[exportKey]Export, len(m.Exports)),
		}
		for j, export := range m.Exports {
			if export.Name == "" {
				return nil, invalidf("module %q export at index %d has an empty name", m.ID, j)
			}
			key := exportKey{name: export.Name, version: export.Version}
			if _, exists := info.exports[key]; exists {
				return nil, invalidf("module %q has duplicate export %q version %q", m.ID, export.Name, export.Version)
			}
			info.exports[key] = export
		}
		c.modules[m.ID] = info
	}

	for _, id := range sortedModuleIDs(c.modules) {
		m := c.modules[id].module
		for i, dependency := range m.Dependencies {
			if dependency == "" {
				return nil, invalidf("module %q dependency at index %d is empty", id, i)
			}
			if _, exists := c.modules[dependency]; !exists {
				return nil, invalidf("module %q has missing dependency %q", id, dependency)
			}
		}
	}

	if err := c.validateGlobalOrder(globalOrder); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *catalog) validateGlobalOrder(order []string) error {
	seen := make(map[string]bool, len(order))
	for i, id := range order {
		if _, exists := c.modules[id]; !exists {
			return invalidf("global order index %d references unknown module %q", i, id)
		}
		if seen[id] {
			return invalidf("global order contains duplicate module %q", id)
		}
		seen[id] = true
	}
	return nil
}

func (c *catalog) validateRelocation(relocation Relocation) error {
	if _, exists := c.modules[relocation.Origin]; !exists {
		return invalidf("relocation origin %q is not a declared module", relocation.Origin)
	}
	if relocation.Name == "" {
		return invalidf("relocation symbol name is empty")
	}
	return nil
}

func sortedModuleIDs(modules map[string]*moduleInfo) []string {
	ids := make([]string, 0, len(modules))
	for id := range modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Resolve searches one relocation in the host's initial global order.
//
// With preemption enabled, the order is the global list followed by the
// origin's breadth-first dependency sequence. Without preemption, the origin
// is visited first, followed by the same global-then-dependencies order. The
// sequence is deduplicated by module ID, retaining the first occurrence.
//
// Once the complete sequence has been inspected, the first strong exact
// (name, version) definition wins. If there is no strong definition, the first
// weak exact definition wins. Resolve returns an error only for malformed
// input; an unresolved symbol, an unsafe target, and a valid binding are all
// represented in Binding.
func Resolve(modules []Module, globalOrder []string, relocation Relocation) (Binding, error) {
	c, err := newCatalog(modules, globalOrder)
	if err != nil {
		return Binding{}, err
	}
	if err := c.validateRelocation(relocation); err != nil {
		return Binding{}, err
	}
	return c.resolve(globalOrder, relocation), nil
}

func (c *catalog) resolve(globalOrder []string, relocation Relocation) Binding {
	sequence := c.searchSequence(globalOrder, relocation)
	binding := Binding{
		Name:          relocation.Name,
		Version:       relocation.Version,
		RequiresTrust: relocation.RequireTrusted,
		Selection:     SelectionNone,
		Sequence:      append([]string(nil), sequence...),
		Lookup:        make([]LookupEntry, 0, len(sequence)),
	}

	key := exportKey{name: relocation.Name, version: relocation.Version}
	firstWeak := -1
	firstStrong := -1

	for _, id := range sequence {
		info := c.modules[id]
		export, matched := info.exports[key]
		entry := LookupEntry{
			ModuleID: id,
			Trusted:  info.module.Trusted,
			HasMatch: matched,
		}
		if matched {
			entry.Strong = export.Strong
			if export.Strong {
				if firstStrong < 0 {
					firstStrong = len(binding.Lookup)
				}
			} else if firstWeak < 0 {
				firstWeak = len(binding.Lookup)
			}
		}
		binding.Lookup = append(binding.Lookup, entry)
	}

	switch {
	case firstStrong >= 0:
		binding.Found = true
		binding.Selection = SelectionFirstStrong
		binding.Explanation = fmt.Sprintf("按查找顺序选择首个强定义：模块 %s", sequence[firstStrong])
		markSelected(binding.Lookup, firstStrong)
	case firstWeak >= 0:
		binding.Found = true
		binding.Selection = SelectionFirstWeak
		binding.Explanation = fmt.Sprintf("查找序列中没有强定义，选择首个弱定义：模块 %s", sequence[firstWeak])
		markSelected(binding.Lookup, firstWeak)
	default:
		binding.Explanation = "未找到名称与版本完全一致的定义"
	}

	if binding.Found {
		selected := firstWeak
		if firstStrong >= 0 {
			selected = firstStrong
		}
		info := c.modules[sequence[selected]]
		binding.ModuleID = info.module.ID
		binding.Strong = firstStrong >= 0
		binding.TargetTrusted = info.module.Trusted
	}
	binding.TrustSatisfied = !relocation.RequireTrusted ||
		(binding.Found && binding.TargetTrusted)
	if relocation.RequireTrusted && binding.Found && !binding.TargetTrusted {
		binding.Explanation += "；目标模块不可信，不满足可信要求"
	}
	return binding
}

func markSelected(entries []LookupEntry, selected int) {
	if selected >= 0 && selected < len(entries) {
		entries[selected].Selected = true
	}
}

func (c *catalog) searchSequence(globalOrder []string, relocation Relocation) []string {
	sequence := make([]string, 0, len(c.modules))
	seen := make(map[string]bool, len(c.modules))

	appendModule := func(id string) {
		if !seen[id] {
			seen[id] = true
			sequence = append(sequence, id)
		}
	}

	if !relocation.AllowPreemption {
		appendModule(relocation.Origin)
	}
	for _, id := range globalOrder {
		appendModule(id)
	}
	// The origin is the root of the dependency walk, even when it is absent
	// from the host's global list. A cycle back to it is not a dependency
	// candidate.
	expanded := map[string]bool{relocation.Origin: true}

	queue := append([]string(nil), c.modules[relocation.Origin].module.Dependencies...)
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		if expanded[id] {
			continue
		}
		// A module already present in the global list keeps its first
		// position, but its dependency edges must still be expanded.
		expanded[id] = true
		appendModule(id)
		queue = append(queue, c.modules[id].module.Dependencies...)
	}
	return sequence
}

// Report renders a complete, human-readable lookup and selection reason.
func (b Binding) Report() string {
	var out strings.Builder
	fmt.Fprintf(&out, "重定位 %s@%s\n", b.Name, b.Version)
	if len(b.Lookup) == 0 {
		out.WriteString("查找序列为空\n")
	}
	for i, entry := range b.Lookup {
		strength := "-"
		if entry.HasMatch {
			strength = "weak"
			if entry.Strong {
				strength = "strong"
			}
		}
		selected := " "
		if entry.Selected {
			selected = "*"
		}
		trust := "untrusted"
		if entry.Trusted {
			trust = "trusted"
		}
		fmt.Fprintf(&out, "  %s %2d. %s [%s] match=%t strength=%s\n",
			selected, i+1, entry.ModuleID, trust, entry.HasMatch, strength)
	}
	fmt.Fprintf(&out, "胜出理由：%s\n", b.Explanation)
	return out.String()
}
