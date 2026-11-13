package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// SchemaID is the stable identity of a schema. It is deliberately distinct
// from Schema.Name: a search path must consist of these IDs, never of mutable
// schema names.
type SchemaID string

// ObjectID is the stable identity of a catalog object.
type ObjectID string

// FunctionID is the stable identity of a published function definition.
type FunctionID string

// PrincipalID identifies a database role or user.
type PrincipalID string

// PublicPrincipal is the pseudo-role used for grants to every caller.
const PublicPrincipal PrincipalID = "PUBLIC"

// Public is a short alias for PublicPrincipal.
const Public = PublicPrincipal

// CallerSearchPath is a sentinel that must never be accepted in a frozen
// search path. It models PostgreSQL's caller-controlled "$user" entry.
const CallerSearchPath SchemaID = "$user"

// ObjectKind is one of the three object classes whose resolution can affect
// the security of a SECURITY DEFINER function.
type ObjectKind string

const (
	KindTable    ObjectKind = "table"
	KindFunction ObjectKind = "function"
	KindOperator ObjectKind = "operator"

	// Short aliases for callers that model object classes directly.
	Table    = KindTable
	Function = KindFunction
	Operator = KindOperator
)

// Schema describes one catalog schema.
//
// System identifies a read-only system catalog. Temporary identifies
// pg_temp-like namespaces. PublicWritable means every role can create
// objects; Writers lists additional roles that can create objects. The Owner
// always has create privilege unless ReadOnly is set.
type Schema struct {
	ID             SchemaID
	Name           string
	Owner          PrincipalID
	ReadOnly       bool
	System         bool
	Temporary      bool
	PublicWritable bool
	Writers        map[PrincipalID]bool
}

// CanWrite reports whether p can create or replace objects in s.
func (s Schema) CanWrite(p PrincipalID) bool {
	if s.ReadOnly {
		return false
	}
	if s.PublicWritable {
		return true
	}
	if p != "" && s.Owner == p {
		return true
	}
	if s.Writers == nil {
		return false
	}
	return s.Writers[p] || s.Writers[PublicPrincipal]
}

// hasNonDefinerWriter reports whether any role other than definer can write
// the schema. A read-only schema is safe regardless of its nominal owner.
func (s Schema) hasNonDefinerWriter(definer PrincipalID) bool {
	if s.ReadOnly {
		return false
	}
	if s.PublicWritable {
		return true
	}
	if s.Owner != "" && s.Owner != definer {
		return true
	}
	for p, allowed := range s.Writers {
		if allowed && p != definer {
			return true
		}
	}
	return false
}

func (s Schema) isTemporary() bool {
	if s.Temporary {
		return true
	}
	n := strings.ToLower(strings.TrimSpace(s.Name))
	return n == "pg_temp" || n == "temp" || n == "temporary" ||
		strings.HasPrefix(n, "pg_temp_") || n == string(CallerSearchPath)
}

// Object is a catalog object. Signature is used for function and operator
// overloads (for example "(integer,integer)"). ArgTypes is a convenience
// alternative to Signature.
type Object struct {
	ID        ObjectID
	SchemaID  SchemaID
	Kind      ObjectKind
	Name      string
	Signature string
	ArgTypes  []string
	Owner     PrincipalID
	Version   int64
	Dropped   bool
}

// SignatureKey returns the canonical overload signature represented by the
// object.
func (o Object) SignatureKey() string {
	return signatureKey(o.Signature, o.ArgTypes)
}

// Catalog is the authoritative schema/object directory. The maps are
// exported so callers can build fixtures directly, but AddSchema and AddObject
// are the preferred constructors.
type Catalog struct {
	Schemas map[SchemaID]Schema
	Objects map[ObjectID]Object
}

// NewCatalog creates an empty catalog.
func NewCatalog() *Catalog {
	return &Catalog{
		Schemas: make(map[SchemaID]Schema),
		Objects: make(map[ObjectID]Object),
	}
}

// AddSchema inserts a schema. It rejects duplicate IDs and duplicate names so
// that resolving a search path by ID can never silently alias another schema.
func (c *Catalog) AddSchema(s Schema) error {
	if c == nil {
		return fmt.Errorf("%w: nil catalog", ErrInvalidCatalog)
	}
	if s.ID == "" {
		return fmt.Errorf("%w: empty schema ID", ErrInvalidCatalog)
	}
	if c.Schemas == nil {
		c.Schemas = make(map[SchemaID]Schema)
	}
	if _, exists := c.Schemas[s.ID]; exists {
		return fmt.Errorf("%w: duplicate schema ID %q", ErrInvalidCatalog, s.ID)
	}
	if s.Name != "" {
		for _, existing := range c.Schemas {
			if existing.Name == s.Name {
				return fmt.Errorf("%w: duplicate schema name %q", ErrInvalidCatalog, s.Name)
			}
		}
	}
	c.Schemas[s.ID] = s
	return nil
}

// AddObject inserts an object. Version and identity validation is performed
// when a reference is frozen, not here; catalogs may intentionally contain
// duplicate names to model overload resolution and shadowing.
func (c *Catalog) AddObject(o Object) error {
	if c == nil {
		return fmt.Errorf("%w: nil catalog", ErrInvalidCatalog)
	}
	if o.ID == "" {
		return fmt.Errorf("%w: empty object ID", ErrInvalidCatalog)
	}
	if o.SchemaID == "" {
		return fmt.Errorf("%w: object %q has no schema", ErrInvalidCatalog, o.ID)
	}
	if c.Objects == nil {
		c.Objects = make(map[ObjectID]Object)
	}
	if _, exists := c.Objects[o.ID]; exists {
		return fmt.Errorf("%w: duplicate object ID %q", ErrInvalidCatalog, o.ID)
	}
	c.Objects[o.ID] = o
	return nil
}

// DropObject removes an object from the live catalog.
func (c *Catalog) DropObject(id ObjectID) {
	if c != nil {
		delete(c.Objects, id)
	}
}

// Schema looks up a schema by ID.
func (c *Catalog) Schema(id SchemaID) (Schema, bool) {
	if c == nil {
		return Schema{}, false
	}
	s, ok := c.Schemas[id]
	return s, ok
}

// SchemaByName looks up a schema's stable ID by its mutable name. This helper
// is useful when constructing fixtures; frozen search paths never use it.
func (c *Catalog) SchemaByName(name string) (SchemaID, bool) {
	if c == nil {
		return "", false
	}
	for id, s := range c.Schemas {
		if s.Name == name {
			return id, true
		}
	}
	return "", false
}

// Object looks up an object by stable ID.
func (c *Catalog) Object(id ObjectID) (Object, bool) {
	if c == nil {
		return Object{}, false
	}
	o, ok := c.Objects[id]
	if !ok || o.Dropped {
		return Object{}, false
	}
	return o, true
}

// objectsIn returns the live objects in a schema in deterministic order.
func (c *Catalog) objectsIn(schema SchemaID) []Object {
	if c == nil {
		return nil
	}
	out := make([]Object, 0)
	for _, o := range c.Objects {
		if o.SchemaID == schema && !o.Dropped {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// matchingObjects returns all live objects in a schema that match a reference.
func (c *Catalog) matchingObjects(schema SchemaID, kind ObjectKind, name, signature string) []Object {
	var out []Object
	for _, o := range c.objectsIn(schema) {
		if o.Kind != kind {
			continue
		}
		if !objectNameMatches(o, kind, name) {
			continue
		}
		if signature != "" && o.SignatureKey() != signature {
			continue
		}
		out = append(out, o)
	}
	return out
}

func objectNameMatches(o Object, kind ObjectKind, name string) bool {
	if o.Name == name {
		return true
	}
	if kind != KindOperator {
		return false
	}
	// Operator constructors commonly accept either "+" with a separate
	// signature or the combined form "+(integer,integer)". Accept both while
	// preserving overload identity through the signature comparison.
	if o.SignatureKey() == name {
		return true
	}
	return o.Name+o.SignatureKey() == name
}

func signatureKey(signature string, argTypes []string) string {
	if signature != "" {
		return signature
	}
	if len(argTypes) == 0 {
		return ""
	}
	return "(" + strings.Join(argTypes, ",") + ")"
}

func normalizeKind(k ObjectKind) ObjectKind {
	return ObjectKind(strings.ToLower(strings.TrimSpace(string(k))))
}
