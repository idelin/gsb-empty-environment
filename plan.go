package netsec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// ScriptManifest is the planner result for one script declaration.
type ScriptManifest struct {
	Name     string   `json:"name"`
	Declared []Action `json:"declared"`
	Granted  []Action `json:"granted"`
	Denied   []Action `json:"denied"`
}

// PackageManifest is the planner's per-package capability list. Actions that
// are declared but absent from Granted can be inspected without running the
// script.
type PackageManifest struct {
	Digest       Digest           `json:"digest"`
	Dependencies []Digest         `json:"dependencies,omitempty"`
	Scripts      []ScriptManifest `json:"scripts"`
	Declared     []Action         `json:"declared"`
	Granted      []Action         `json:"granted"`
	Denied       []Action         `json:"denied"`
}

// Plan is a deterministic execution and publication plan. It carries an
// unexported integrity seal; Host.Run rejects a plan whose exported capability
// fields were modified. Plans should be produced by a Planner rather than
// assembled or decoded from untrusted JSON.
type Plan struct {
	Order     []Digest                   `json:"order"`
	Manifests map[Digest]PackageManifest `json:"manifests"`
	integrity [sha256.Size]byte
}

// Manifest returns a package manifest by digest.
func (p *Plan) Manifest(digest Digest) (PackageManifest, bool) {
	if p == nil {
		return PackageManifest{}, false
	}
	manifest, ok := p.Manifests[digest]
	return manifest, ok
}

func (p *Plan) validate() error {
	if p == nil {
		return fmt.Errorf("netsec: nil plan")
	}
	if err := p.verifyIntegrity(); err != nil {
		return err
	}
	if len(p.Order) != len(p.Manifests) {
		return fmt.Errorf("netsec: plan order and manifest count differ")
	}
	seen := make(map[Digest]int, len(p.Order))
	for i, digest := range p.Order {
		if err := digest.Validate(); err != nil {
			return err
		}
		if _, exists := p.Manifests[digest]; !exists {
			return fmt.Errorf("netsec: plan order references package %s without a manifest", digest)
		}
		if _, exists := seen[digest]; exists {
			return fmt.Errorf("netsec: plan order repeats package %s", digest)
		}
		seen[digest] = i
	}
	for digest, manifest := range p.Manifests {
		if manifest.Digest != digest {
			return fmt.Errorf("netsec: manifest key %s does not match digest %s", digest, manifest.Digest)
		}
		for _, dep := range manifest.Dependencies {
			depIndex, exists := seen[dep]
			if !exists {
				return fmt.Errorf("netsec: package %s depends on unplanned package %s", digest, dep)
			}
			if depIndex >= seen[digest] {
				return fmt.Errorf("netsec: package %s appears before dependency %s", digest, dep)
			}
		}
		scriptNames := make(map[string]struct{}, len(manifest.Scripts))
		for _, script := range manifest.Scripts {
			if _, exists := scriptNames[script.Name]; exists {
				return fmt.Errorf("netsec: package %s repeats script %q", digest, script.Name)
			}
			scriptNames[script.Name] = struct{}{}
		}
	}
	return nil
}

// Planner intersects graph declarations with host policy and registry
// membership.
type Planner struct {
	policy  *Policy
	catalog *Catalog
}

// NewPlanner creates a planner. A nil policy is deny-all. Exec, Connect, and
// Env grants also require exact catalog registration.
func NewPlanner(policy *Policy, catalog *Catalog) *Planner {
	return &Planner{policy: policy, catalog: catalog}
}

// Plan validates a package graph and emits deterministic per-package capability
// manifests. Lexically unsafe or structurally invalid action declarations are
// left in Denied with the script attribution, allowing runtime traces to report
// the exact refusal rather than hiding it at planning time.
func (p *Planner) Plan(graph PackageGraph) (*Plan, error) {
	nodes, err := normalizeGraph(graph)
	if err != nil {
		return nil, err
	}
	order, err := topologicalOrder(nodes)
	if err != nil {
		return nil, err
	}
	catalog := catalogSnapshot{}
	if p != nil && p.catalog != nil {
		catalog = p.catalog.snapshot()
	}
	var policy *Policy
	if p != nil {
		policy = p.policy
	}
	plan := &Plan{
		Order:     append([]Digest(nil), order...),
		Manifests: make(map[Digest]PackageManifest, len(order)),
	}
	for _, digest := range order {
		node := nodes[digest]
		manifest := PackageManifest{
			Digest:       digest,
			Dependencies: append([]Digest(nil), node.DependsOn...),
		}
		packageDeclared := NewActionSet()
		packageGranted := NewActionSet()
		packageDenied := NewActionSet()
		for _, script := range node.Scripts {
			declared := canonicalActions(script.Actions)
			scriptGranted := NewActionSet()
			scriptDenied := NewActionSet()
			for _, action := range declared {
				allowed, _ := plannerActionAllowed(policy, digest, catalog, action)
				switch {
				case allowed:
					scriptGranted = scriptGranted.Union(NewActionSet(action))
				case action.Validate() != nil:
					scriptDenied = scriptDenied.Union(NewActionSet(action))
				case !policyActionAllowed(policy, digest, action):
					scriptDenied = scriptDenied.Union(NewActionSet(action))
				case !catalogActionAllowed(catalog, action):
					scriptDenied = scriptDenied.Union(NewActionSet(action))
				default:
					scriptDenied = scriptDenied.Union(NewActionSet(action))
				}
			}
			scriptManifest := ScriptManifest{
				Name:     script.Name,
				Declared: append([]Action(nil), declared...),
				Granted:  scriptGranted.List(),
				Denied:   scriptDenied.List(),
			}
			manifest.Scripts = append(manifest.Scripts, scriptManifest)
			packageDeclared = packageDeclared.Union(NewActionSet(declared...))
			packageGranted = packageGranted.Union(scriptGranted)
			packageDenied = packageDenied.Union(scriptDenied)
		}
		manifest.Declared = packageDeclared.List()
		manifest.Granted = packageGranted.List()
		manifest.Denied = packageDenied.List()
		plan.Manifests[digest] = manifest
	}
	plan.sealIntegrity()
	return plan, nil
}

func (p *Plan) sealIntegrity() {
	if p == nil {
		return
	}
	p.integrity = p.computeIntegrity()
}

func (p *Plan) verifyIntegrity() error {
	if p == nil {
		return fmt.Errorf("netsec: nil plan")
	}
	if p.integrity != p.computeIntegrity() {
		return fmt.Errorf("netsec: plan integrity check failed (plans must come from a planner and must not be modified)")
	}
	return nil
}

func (p *Plan) computeIntegrity() [sha256.Size]byte {
	type payload struct {
		Order     []Digest                   `json:"order"`
		Manifests map[Digest]PackageManifest `json:"manifests"`
	}
	data, _ := json.Marshal(payload{Order: p.Order, Manifests: p.Manifests})
	return sha256.Sum256(data)
}

func plannerActionAllowed(policy *Policy, digest Digest, catalog catalogSnapshot, action Action) (bool, string) {
	if err := action.Validate(); err != nil {
		return false, err.Error()
	}
	if !policyActionAllowed(policy, digest, action) {
		return false, "host policy does not grant the exact action"
	}
	if !catalogActionAllowed(catalog, action) {
		return false, "action selector is not registered by the host"
	}
	return true, ""
}

func policyActionAllowed(policy *Policy, digest Digest, action Action) bool {
	return policy != nil && policy.Allowed(digest, action)
}

func catalogActionAllowed(catalog catalogSnapshot, action Action) bool {
	switch action.Kind {
	case KindExec:
		_, ok := catalog.tools[action.ToolID]
		return ok
	case KindConnect:
		_, ok := catalog.endpoints[action.EndpointID]
		return ok
	case KindEnv:
		_, ok := catalog.env[action.Name]
		return ok
	default:
		return true
	}
}

// SortedDigests returns a sorted copy for stable reporting.
func SortedDigests(digests []Digest) []Digest {
	out := append([]Digest(nil), digests...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
