package spp

import (
	"encoding/json"
	"fmt"
	"os"
)

// Spec is the importable JSON description of one destination-prefix problem.
//
// Relationship names: "peer", "customer-provider" (the link names customer
// first, provider second), or "unrestricted" (abstract gadget link).
type Spec struct {
	Destination int        `json:"destination"`
	Nodes       []int      `json:"nodes"`
	Links       []LinkSpec `json:"links"`
	StrictGR    bool       `json:"strict_gr"`
	// TierOverrides rewrites the local-pref tier of a named path.
	TierOverrides []TierOverride `json:"tier_overrides,omitempty"`
	// ExplicitPaths injects abstract gadget paths (best first per AS).
	ExplicitPaths map[string][][]int `json:"explicit_paths,omitempty"`
	// ExplicitOnly keeps only explicit paths and disables enumeration.
	ExplicitOnly  bool `json:"explicit_only,omitempty"`
	MaxPathLength int  `json:"max_path_length,omitempty"`
}

// LinkSpec is one undirected link in the JSON form.
type LinkSpec struct {
	A        int    `json:"a"`
	B        int    `json:"b"`
	Relation string `json:"relation"`
}

// TierOverride raises or lowers the strategy tier of one AS/path pair.
type TierOverride struct {
	AS   int   `json:"as"`
	Path []int `json:"path"`
	// Tier is "customer", "peer" or "provider".
	Tier string `json:"tier"`
}

// LoadSpec reads and decodes a JSON problem specification.
func LoadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("spp: invalid spec %s: %w", path, err)
	}
	return &s, nil
}

// BuildFromSpec converts a JSON spec into a validated Instance.
func BuildFromSpec(s *Spec) (*Instance, error) {
	links := make([]Link, 0, len(s.Links))
	for _, l := range s.Links {
		rel, err := parseRelation(l.Relation)
		if err != nil {
			return nil, err
		}
		links = append(links, Link{A: l.A, B: l.B, Relation: rel})
	}
	g, err := NewASGraph(s.Nodes, links)
	if err != nil {
		return nil, err
	}
	cfg := PolicyConfig{
		Origin:        s.Destination,
		StrictGR:      s.StrictGR,
		ExplicitOnly:  s.ExplicitOnly,
		MaxPathLength: s.MaxPathLength,
	}
	if len(s.TierOverrides) > 0 {
		cfg.TierFor = map[int]map[string]LocalPrefTier{}
		for _, o := range s.TierOverrides {
			t, err := parseTier(o.Tier)
			if err != nil {
				return nil, err
			}
			if cfg.TierFor[o.AS] == nil {
				cfg.TierFor[o.AS] = map[string]LocalPrefTier{}
			}
			cfg.TierFor[o.AS][PathKey(o.Path)] = t
		}
	}
	if len(s.ExplicitPaths) > 0 {
		cfg.ExtraPaths = map[int][][]int{}
		for key, paths := range s.ExplicitPaths {
			var as int
			if _, err := fmt.Sscanf(key, "%d", &as); err != nil {
				return nil, fmt.Errorf("spp: explicit_paths key %q must be an AS id", key)
			}
			cfg.ExtraPaths[as] = append(cfg.ExtraPaths[as], paths...)
		}
	}
	return Build(g, cfg)
}

func parseRelation(s string) (Relation, error) {
	switch s {
	case "peer":
		return RelationPeer, nil
	case "customer-provider":
		return RelationCustomerToProvider, nil
	case "unrestricted", "":
		return RelationUnknown, nil
	default:
		return RelationUnknown, fmt.Errorf("spp: unknown relation %q", s)
	}
}

func parseTier(s string) (LocalPrefTier, error) {
	switch s {
	case "customer":
		return PrefCustomer, nil
	case "peer":
		return PrefPeer, nil
	case "provider":
		return PrefProvider, nil
	default:
		return 0, fmt.Errorf("spp: unknown tier %q", s)
	}
}
