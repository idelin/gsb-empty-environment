package displaycommit

import (
	"fmt"
	"math/big"
	"strings"
)

// Role marks the authorization semantics of a normalized field.
type Role string

const (
	RoleUnspecified Role = ""
	RolePayee       Role = "payee"
	RoleRecipient   Role = "recipient"
	RoleSubject     Role = "subject"
	RoleAsset       Role = "asset"
	RoleAmount      Role = "amount"
	RoleDomain      Role = "signing-domain"
	RoleSignDomain  Role = "signing-domain"
	RoleDeadline    Role = "deadline"
	RoleExpiry      Role = "deadline"
	RoleNonAuth     Role = "non-authorized"
	RoleMalleable   Role = "non-authorized"
	RoleObjectID    Role = "object-id"
)

func canonicalRole(r Role) Role {
	switch Role(strings.ToLower(strings.ReplaceAll(string(r), "_", "-"))) {
	case RoleRecipient:
		return RolePayee
	case RoleSignDomain, "domain":
		return RoleDomain
	case RoleExpiry, "expiration", "valid-until":
		return RoleDeadline
	case RoleMalleable, "unauthorized", "nonauthorized":
		return RoleNonAuth
	default:
		return r
	}
}

// FieldSpec describes one normalized type-table field.
type FieldSpec struct {
	Number      int    `json:"number,omitempty"`
	FieldNumber int    `json:"field_number,omitempty"`
	Name        string `json:"name,omitempty"`
	Wire        any    `json:"wire,omitempty"`
	WireType    any    `json:"wire_type,omitempty"`
	Kind        Kind   `json:"kind,omitempty"`
	Type        string `json:"type,omitempty"`
	Role        Role   `json:"role,omitempty"`
	Roles       []Role `json:"roles,omitempty"`

	Required bool `json:"required,omitempty"`
	Optional bool `json:"optional,omitempty"`
	Repeated bool `json:"repeated,omitempty"`
	List     bool `json:"list,omitempty"`

	MinItems int  `json:"min_items,omitempty"`
	MaxItems int  `json:"max_items,omitempty"`
	Bounded  bool `json:"bounded,omitempty"`

	Default any `json:"default,omitempty"`

	// Domain is an optional finite small-domain enumeration.
	Domain     []any `json:"domain,omitempty"`
	Candidates []any `json:"candidates,omitempty"`

	Decimals int         `json:"decimals,omitempty"`
	Format   string      `json:"format,omitempty"`
	Message  string      `json:"message,omitempty"`
	Fields   []FieldSpec `json:"fields,omitempty"`
	Element  *FieldSpec  `json:"element,omitempty"`

	// NonAuthorized can be set by the host explicitly. Rule-level
	// NonAuthorizedPaths are the preferred declaration location.
	NonAuthorized bool `json:"non_authorized,omitempty"`
}

func (s FieldSpec) number() int {
	if s.Number != 0 {
		return s.Number
	}
	return s.FieldNumber
}

func (s FieldSpec) allRoles() []Role {
	out := make([]Role, 0, len(s.Roles)+1)
	if s.Role != "" {
		out = append(out, canonicalRole(s.Role))
	}
	for _, r := range s.Roles {
		out = append(out, canonicalRole(r))
	}
	if s.NonAuthorized {
		out = append(out, RoleNonAuth)
	}
	return out
}

func (s FieldSpec) hasRole(want Role) bool {
	want = canonicalRole(want)
	for _, r := range s.allRoles() {
		if r == want {
			return true
		}
	}
	return false
}

// MessageSpec is a named normalized message type.
type MessageSpec struct {
	Name   string      `json:"name"`
	Fields []FieldSpec `json:"fields,omitempty"`
}

// TypeTable is the declared, versioned normalization table.
type TypeTable struct {
	Version         any                    `json:"version,omitempty"`
	TableVersion    any                    `json:"table_version,omitempty"`
	DeclaredVersion string                 `json:"declared_version,omitempty"`
	RootType        string                 `json:"root_type,omitempty"`
	Root            string                 `json:"root,omitempty"`
	Messages        map[string]MessageSpec `json:"messages,omitempty"`
	Specs           []MessageSpec          `json:"specs,omitempty"`
	Fields          []FieldSpec            `json:"fields,omitempty"`
}

func (t *TypeTable) versionValue() string {
	if t == nil {
		return "unknown-table"
	}
	for _, v := range []any{t.DeclaredVersion, t.TableVersion, t.Version} {
		if v != nil && fmt.Sprint(v) != "" {
			return fmt.Sprint(v)
		}
	}
	return "unversioned-table"
}

func (t *TypeTable) rootTypeName() string {
	if t == nil {
		return ""
	}
	if t.RootType != "" {
		return t.RootType
	}
	return t.Root
}

func (t *TypeTable) messageSpec(name string) (MessageSpec, bool) {
	if t == nil {
		return MessageSpec{}, false
	}
	if t.Messages != nil {
		if m, ok := t.Messages[name]; ok {
			return m, true
		}
	}
	for _, m := range t.Specs {
		if m.Name == name {
			return m, true
		}
	}
	if name == "" && len(t.Fields) > 0 {
		return MessageSpec{Name: t.rootTypeName(), Fields: t.Fields}, true
	}
	return MessageSpec{}, false
}

func (t *TypeTable) fieldSpec(typeName string, fieldNumber int) (FieldSpec, bool) {
	m, ok := t.messageSpec(typeName)
	if ok {
		for _, f := range m.Fields {
			if f.number() == fieldNumber {
				return f, true
			}
		}
	}
	for _, f := range t.Fields {
		if f.number() == fieldNumber {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// SpecAtPath resolves a possibly nested field path. Repeated/list ordinals do
// not affect schema lookup.
func (t *TypeTable) SpecAtPath(typeName string, path Path) (FieldSpec, bool) {
	if t == nil || len(path) == 0 {
		return FieldSpec{}, false
	}
	var fields []FieldSpec
	if m, ok := t.messageSpec(typeName); ok {
		fields = m.Fields
	} else {
		fields = t.Fields
	}
	var spec FieldSpec
	var ok bool
	for i, step := range path {
		spec, ok = findFieldSpec(fields, step.Field)
		if !ok {
			return FieldSpec{}, false
		}
		if i == len(path)-1 {
			return spec, true
		}
		if len(spec.Fields) > 0 {
			fields = spec.Fields
			continue
		}
		if spec.Element != nil && len(spec.Element.Fields) > 0 {
			fields = spec.Element.Fields
			continue
		}
		if spec.Message != "" {
			if m, exists := t.messageSpec(spec.Message); exists {
				fields = m.Fields
				continue
			}
		}
		return FieldSpec{}, false
	}
	return spec, ok
}

func findFieldSpec(fields []FieldSpec, number int) (FieldSpec, bool) {
	for _, f := range fields {
		if f.number() == number {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// RolePaths returns table-declared paths carrying a role. Nested specs use
// their field number followed by the nested field number.
func (t *TypeTable) RolePaths(want Role) []Path {
	if t == nil {
		return nil
	}
	var out []Path
	var walk func(typeName string, prefix Path, seen map[string]bool)
	walk = func(typeName string, prefix Path, seen map[string]bool) {
		key := typeName + ":" + prefix.Key()
		if seen[key] {
			return
		}
		seen[key] = true
		m, ok := t.messageSpec(typeName)
		if !ok {
			return
		}
		for _, f := range m.Fields {
			n := f.number()
			if n == 0 {
				continue
			}
			p := append(append(Path(nil), prefix...), PathStep{Field: n, Index: -1})
			if f.hasRole(want) {
				out = append(out, p)
			}
			nextType := f.Message
			if nextType == "" && f.Kind == KindMessage {
				nextType = f.Name
			}
			if nextType != "" {
				walk(nextType, p, seen)
			}
			for _, nested := range f.Fields {
				if nested.number() != 0 {
					np := append(append(Path(nil), p...), PathStep{Field: nested.number(), Index: -1})
					if nested.hasRole(want) {
						out = append(out, np)
					}
				}
			}
			if f.Element != nil && f.Element.Message != "" {
				walk(f.Element.Message, p, seen)
			}
		}
	}
	walk(t.rootTypeName(), nil, map[string]bool{})
	return out
}

func defaultValueEqual(spec FieldSpec, f Field) bool {
	if spec.Default == nil {
		return false
	}
	dv := spec.Default
	switch spec.Kind {
	case KindInt:
		return AsBigInt(dv).Cmp(valueInt(f.Value)) == 0
	case KindText:
		return fmt.Sprint(dv) == f.Value.Text
	case KindBool:
		b, _ := dv.(bool)
		return b == f.Value.Bool
	case KindAddress:
		return AddressValue(dv).Address.canonical() == f.Value.Address.canonical()
	default:
		return fmt.Sprint(dv) == fmt.Sprint(f.Value)
	}
}

func valueInt(v Value) *big.Int {
	if v.Int != nil {
		return v.Int
	}
	return AsBigInt(v.Text)
}

// Bounded reports whether all repeated fields in the table declare a finite
// maximum length.
func (t *TypeTable) Bounded() (bool, Path) {
	if t == nil {
		return false, nil
	}
	seen := map[string]bool{}
	for name, m := range t.Messages {
		_ = name
		if ok, p := specsBounded(m.Fields, P(), map[string]bool{}); !ok {
			return false, p
		}
	}
	if ok, p := specsBounded(t.Fields, P(), seen); !ok {
		return false, p
	}
	return true, nil
}

func specsBounded(fields []FieldSpec, prefix Path, seen map[string]bool) (bool, Path) {
	for _, f := range fields {
		p := append(append(Path(nil), prefix...), PathStep{Field: f.number(), Index: -1})
		if (f.Repeated || f.List) && f.MaxItems <= 0 {
			return false, p
		}
		if len(f.Fields) > 0 {
			if ok, bad := specsBounded(f.Fields, p, seen); !ok {
				return false, bad
			}
		}
		if f.Element != nil && f.Element.Kind == KindMessage {
			if ok, bad := specsBounded(f.Element.Fields, p, seen); !ok {
				return false, bad
			}
		}
	}
	return true, nil
}
