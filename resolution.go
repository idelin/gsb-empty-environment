package netsec

import (
	"fmt"
	"strings"
)

// ResolutionCandidate records a position in the frozen search path. Matched
// is true when that schema contained an object matching the reference. A
// later candidate can only be reached when all earlier schemas missed.
type ResolutionCandidate struct {
	SchemaID    SchemaID
	SearchIndex int
	ObjectID    ObjectID
	Version     int64
	Matched     bool
}

// FrozenRef is the publish-time resolution of a security-critical AST
// reference. Execution code and callbacks use ObjectID; the original names
// are retained only for diagnostics and dependency auditing.
type FrozenRef struct {
	Path        string
	Kind        ObjectKind
	ObjectID    ObjectID
	Version     int64
	SchemaID    SchemaID
	Name        string
	Signature   string
	SearchIndex int
	Qualified   bool
}

// Dependency is one entry in a published function's resolution manifest.
type Dependency struct {
	Ref        FrozenRef
	Candidates []ResolutionCandidate
}

// DynamicSite records a dynamic-SQL site that was not resolved at publish
// time. It must be supplied a trusted AST and re-resolved at every execution.
type DynamicSite struct {
	Path string
	SQL  string
	Key  string
}

// DependencyManifest is the complete, immutable audit trail for a published
// function.
type DependencyManifest struct {
	FunctionID   FunctionID
	DefinerID    PrincipalID
	SearchPath   []SchemaID
	Dependencies []Dependency
	DynamicSites []DynamicSite
}

type resolvedReference struct {
	object     Object
	searchIdx  int
	qualified  bool
	candidates []ResolutionCandidate
}

// CompileSearchPath validates and returns a frozen search path. Only stable
// schema IDs are accepted: a mutable schema name (or the caller-controlled
// "$user" marker / a temp schema) is rejected.
func (c *Catalog) CompileSearchPath(path []SchemaID, definer PrincipalID) ([]SchemaID, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrInvalidCatalog)
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("%w: empty search path", ErrInvalidSearchPath)
	}
	if definer == "" {
		return nil, fmt.Errorf("%w: empty definer ID", ErrInvalidDefinition)
	}

	seen := make(map[SchemaID]bool, len(path))
	out := make([]SchemaID, 0, len(path))
	for i, id := range path {
		if id == "" {
			return nil, fmt.Errorf("%w: empty schema ID at position %d", ErrInvalidSearchPath, i)
		}
		if id == CallerSearchPath || strings.EqualFold(strings.TrimSpace(string(id)), string(CallerSearchPath)) {
			return nil, fmt.Errorf("%w: caller search path marker at position %d", ErrTemporarySchema, i)
		}
		if seen[id] {
			return nil, fmt.Errorf("%w: duplicate schema ID %q", ErrInvalidSearchPath, id)
		}
		seen[id] = true

		schema, ok := c.Schemas[id]
		if !ok {
			// A schema name is not a schema ID. Do not fall back to
			// SchemaByName here: names are mutable and are exactly what an
			// attacker can influence.
			return nil, fmt.Errorf("%w: %q is not a schema ID in the catalog", ErrInvalidSearchPath, id)
		}
		if schema.isTemporary() {
			return nil, fmt.Errorf("%w: schema %q", ErrTemporarySchema, id)
		}
		if schema.PublicWritable || schema.Writers[PublicPrincipal] {
			return nil, fmt.Errorf("%w: schema %q", ErrPublicWritableSchema, id)
		}
		if i == 0 {
			if !schema.System {
				return nil, fmt.Errorf("%w: schema %q", ErrUnsafeSystemSchema, id)
			}
			if !schema.ReadOnly {
				return nil, fmt.Errorf("%w: schema %q is writable", ErrUnsafeSystemSchema, id)
			}
			if schema.hasNonDefinerWriter(definer) {
				return nil, fmt.Errorf("%w: schema %q", ErrWritableSchema, id)
			}
		} else if schema.hasNonDefinerWriter(definer) {
			return nil, fmt.Errorf("%w: schema %q has writer other than definer", ErrWritableSchema, id)
		}
		out = append(out, id)
	}
	return out, nil
}

func (c *Catalog) validateExecutionSecurity(path []SchemaID, definer PrincipalID) error {
	if _, err := c.CompileSearchPath(path, definer); err != nil {
		return fmt.Errorf("%w: %w", ErrUnsafeSchemaAtExecution, err)
	}
	return nil
}

func (c *Catalog) resolveReference(path []SchemaID, ref Ref, nodePath string) (resolvedReference, error) {
	kind := normalizeKind(ref.Kind)
	if kind != KindTable && kind != KindFunction && kind != KindOperator {
		return resolvedReference{}, fmt.Errorf("%w: unknown object kind %q at %s", ErrInvalidDefinition, ref.Kind, nodePath)
	}
	if ref.Name == "" {
		return resolvedReference{}, fmt.Errorf("%w: empty object name at %s", ErrInvalidDefinition, nodePath)
	}
	signature := ""
	if kind != KindTable {
		signature = signatureKey(ref.Signature, ref.ArgTypes)
	}

	if ref.SchemaID != "" {
		schema, ok := c.Schemas[ref.SchemaID]
		if !ok {
			return resolvedReference{}, fmt.Errorf("%w: schema %q at %s", ErrOutsideSearchPath, ref.SchemaID, nodePath)
		}
		if schema.isTemporary() {
			return resolvedReference{}, fmt.Errorf("%w: schema %q at %s", ErrTemporarySchema, ref.SchemaID, nodePath)
		}
		searchIdx := -1
		for i, id := range path {
			if id == ref.SchemaID {
				searchIdx = i
				break
			}
		}
		if searchIdx < 0 {
			return resolvedReference{}, fmt.Errorf("%w: schema %q at %s", ErrOutsideSearchPath, ref.SchemaID, nodePath)
		}
		matches := c.matchingObjects(ref.SchemaID, kind, ref.Name, signature)
		switch {
		case len(matches) == 0:
			return resolvedReference{}, fmt.Errorf("%w: %s %q in schema %q at %s", ErrUnresolvedReference, kind, ref.Name, ref.SchemaID, nodePath)
		case len(matches) > 1:
			return resolvedReference{}, fmt.Errorf("%w: %s %q in schema %q at %s", ErrAmbiguousReference, kind, ref.Name, ref.SchemaID, nodePath)
		}
		o := matches[0]
		return resolvedReference{
			object:    o,
			searchIdx: searchIdx,
			qualified: true,
			candidates: []ResolutionCandidate{{
				SchemaID: ref.SchemaID, SearchIndex: searchIdx,
				ObjectID: o.ID, Version: o.Version, Matched: true,
			}},
		}, nil
	}

	candidates := make([]ResolutionCandidate, 0, len(path))
	for i, schemaID := range path {
		schema, ok := c.Schemas[schemaID]
		if !ok {
			return resolvedReference{}, fmt.Errorf("%w: missing schema %q", ErrInvalidSearchPath, schemaID)
		}
		if schema.isTemporary() {
			return resolvedReference{}, fmt.Errorf("%w: schema %q", ErrTemporarySchema, schemaID)
		}
		matches := c.matchingObjects(schemaID, kind, ref.Name, signature)
		if len(matches) > 1 {
			return resolvedReference{}, fmt.Errorf("%w: %s %q in schema %q at %s", ErrAmbiguousReference, kind, ref.Name, schemaID, nodePath)
		}
		candidate := ResolutionCandidate{SchemaID: schemaID, SearchIndex: i}
		if len(matches) == 1 {
			o := matches[0]
			candidate.ObjectID = o.ID
			candidate.Version = o.Version
			candidate.Matched = true
			candidates = append(candidates, candidate)
			// The first matching schema owns the unqualified name. Later
			// schemas are shadowed and are never considered.
			return resolvedReference{
				object:     o,
				searchIdx:  i,
				candidates: candidates,
			}, nil
		}
		candidates = append(candidates, candidate)
	}
	return resolvedReference{}, fmt.Errorf("%w: %s %q at %s", ErrUnresolvedReference, kind, ref.Name, nodePath)
}

func (c *Catalog) validateFrozen(ref FrozenRef) error {
	current, ok := c.Object(ref.ObjectID)
	if !ok {
		return fmt.Errorf("%w: object %q disappeared or was dropped", ErrStaleObject, ref.ObjectID)
	}
	if current.Version != ref.Version {
		return fmt.Errorf("%w: object %q version changed from %d to %d", ErrStaleObject, ref.ObjectID, ref.Version, current.Version)
	}
	if current.Kind != ref.Kind || current.SchemaID != ref.SchemaID ||
		current.Name != ref.Name || current.SignatureKey() != ref.Signature {
		return fmt.Errorf("%w: object %q identity changed", ErrStaleObject, ref.ObjectID)
	}
	return nil
}
