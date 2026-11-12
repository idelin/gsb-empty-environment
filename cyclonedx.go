package netsec

import (
	"encoding/json"
	"fmt"
)

// SpecVersion is the CycloneDX spec version this package understands.
const SpecVersion = "1.5"

// BOM is the subset of a CycloneDX document needed for reachability and
// license gating. Unknown fields are ignored so later spec versions can still
// be consumed.
type BOM struct {
	BOMFormat    string       `json:"bomFormat"`
	SpecVersion  string       `json:"specVersion"`
	Metadata     Metadata     `json:"metadata"`
	Components   []Component  `json:"components"`
	Dependencies []Dependency `json:"dependencies"`
}

// Metadata carries the component describing the thing the SBOM is about.
type Metadata struct {
	Component *Component `json:"component,omitempty"`
}

// Component mirrors a CycloneDX component element. Scope is parsed but never
// trusted: reachability, not the self-reported scope, decides inclusion.
type Component struct {
	BOMRef     string    `json:"bom-ref,omitempty"`
	Type       string    `json:"type,omitempty"`
	Name       string    `json:"name"`
	Version    string    `json:"version,omitempty"`
	PackageURL string    `json:"purl,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Licenses   []License `json:"licenses,omitempty"`
}

// Dependency is one edge in the dependency graph: Ref depends on each entry
// in Dependencies.
type Dependency struct {
	Ref          string   `json:"ref"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// License is one entry of a component's licenses array. Exactly one of the
// sub-fields is expected to be populated in a valid document.
type License struct {
	Expression string         `json:"expression,omitempty"`
	License    *LicenseChoice `json:"license,omitempty"`
}

// LicenseChoice holds either an SPDX license id/name or a free-form text.
type LicenseChoice struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Text string `json:"text,omitempty"`
}

// ParseBOM decodes a CycloneDX JSON document and checks the envelope.
func ParseBOM(data []byte) (*BOM, error) {
	var bom BOM
	if err := json.Unmarshal(data, &bom); err != nil {
		return nil, fmt.Errorf("cyclonedx: invalid JSON: %w", err)
	}
	if bom.BOMFormat != "CycloneDX" {
		return nil, fmt.Errorf("cyclonedx: unsupported bomFormat %q, want \"CycloneDX\"", bom.BOMFormat)
	}
	if bom.SpecVersion == "" {
		return nil, fmt.Errorf("cyclonedx: missing specVersion")
	}
	return &bom, nil
}

// allComponents returns the metadata component (if any) followed by the
// components array. Both share the same bom-ref and purl identity spaces.
func (b *BOM) allComponents() []Component {
	comps := make([]Component, 0, len(b.Components)+1)
	if b.Metadata.Component != nil {
		comps = append(comps, *b.Metadata.Component)
	}
	return append(comps, b.Components...)
}
