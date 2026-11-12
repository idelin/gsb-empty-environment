package netsec

import (
	"fmt"
	"strings"
)

// purlIdentity is the conflict key for a component: the package "coordinate"
// (type/namespace/name plus subpath) without its version. Two components with
// the same identity but different versions are an ambiguous, un-gateable BOM.
type purlIdentity struct {
	type_     string
	namespace string
	name      string
	subpath   string
}

// parsePURL extracts the version-independent identity and the version from a
// Package URL. A purl has the shape
// pkg:type/namespace/name@version?qualifiers#subpath.
func parsePURL(raw string) (id purlIdentity, version string, ok bool) {
	if raw == "" {
		return purlIdentity{}, "", false
	}
	body, ok := strings.CutPrefix(raw, "pkg:")
	if !ok {
		return purlIdentity{}, "", false
	}
	var fragment string
	body, fragment, _ = strings.Cut(body, "#")
	body, _, _ = strings.Cut(body, "?")

	typeAndPath, tail, found := strings.Cut(body, "/")
	if !found || typeAndPath == "" {
		return purlIdentity{}, "", false
	}
	id.type_ = typeAndPath
	id.subpath = fragment

	// A version attached directly to the type segment is malformed; strip it
	// defensively, the canonical location is the end of the name segment.
	if at := strings.IndexByte(id.type_, '@'); at >= 0 {
		id.type_ = id.type_[:at]
	}

	// The tail after the first slash carries namespace/name@version.
	path := ""
	if at := strings.IndexByte(tail, '@'); at >= 0 {
		path = tail[:at]
		version = tail[at+1:]
	} else {
		path = tail
	}
	if path == "" {
		return purlIdentity{}, "", false
	}

	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		id.namespace = path[:idx]
		id.name = path[idx+1:]
	} else {
		id.name = path
	}
	if id.name == "" {
		return purlIdentity{}, "", false
	}
	return id, version, true
}

// String renders the identity back as a version-less purl for error messages.
func (p purlIdentity) String() string {
	loc := p.name
	if p.namespace != "" {
		loc = p.namespace + "/" + p.name
	}
	s := "pkg:" + p.type_ + "/" + loc
	if p.subpath != "" {
		s += "#" + p.subpath
	}
	return s
}

// validateIdentity rejects duplicate bom-refs and the same purl identity
// carrying conflicting versions. Both conditions make graph resolution or
// license attribution ambiguous, so the gate must fail closed.
func (b *BOM) validateIdentity() error {
	seenRefs := make(map[string]string)
	seenVersions := make(map[purlIdentity]string)

	for _, comp := range b.allComponents() {
		if comp.BOMRef == "" {
			return fmt.Errorf("cyclonedx: component %q has no bom-ref", comp.Name)
		}
		if other, dup := seenRefs[comp.BOMRef]; dup {
			return fmt.Errorf("cyclonedx: duplicate bom-ref %q (components %q and %q)",
				comp.BOMRef, other, comp.Name)
		}
		seenRefs[comp.BOMRef] = comp.Name

		id, version, ok := parsePURL(comp.PackageURL)
		if !ok {
			continue
		}
		if prior, exists := seenVersions[id]; exists && prior != version {
			return fmt.Errorf("cyclonedx: purl conflict for %s: versions %q and %q",
				id, prior, version)
		}
		seenVersions[id] = version
	}
	return nil
}

// validateEdges rejects dependency edges pointing at bom-refs that no
// component defines, so reachability can never silently skip a branch.
func (b *BOM) validateEdges(refs map[string]struct{}) error {
	for _, edge := range b.Dependencies {
		if _, ok := refs[edge.Ref]; !ok {
			return fmt.Errorf("cyclonedx: dependency edge has unknown ref %q", edge.Ref)
		}
		for _, dep := range edge.Dependencies {
			if _, ok := refs[dep]; !ok {
				return fmt.Errorf("cyclonedx: ref %q depends on unknown ref %q", edge.Ref, dep)
			}
		}
	}
	return nil
}
