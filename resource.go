package netsec

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// Resource is the minimal representation needed to evaluate a scoped search.
// A health data service can adapt its own records to this shape.
type Resource struct {
	Type     string
	ID       string
	TenantID string

	// PatientID is the owning patient. For Patient resources the ID itself is
	// authoritative, so this field may be left empty.
	PatientID string

	// Attributes contains non-reference search values by parameter name.
	Attributes map[string][]string

	// References contains literal reference strings by parameter name,
	// for example {"subject": {"Patient/p1"}, "has-member": {"Observation/o2"}}.
	References map[string][]string

	// Shared explicitly marks a tenant-wide resource that has no patient
	// owner. Shared resources may be included when reached from an authorized
	// source, but they never make an unrelated patient resource visible.
	Shared bool
}

// Reference is a parsed FHIR-style reference.
type Reference struct {
	Type string
	ID   string
}

func (r Reference) String() string {
	switch {
	case r.Type == "" && r.ID == "":
		return ""
	case r.Type == "":
		return r.ID
	default:
		return r.Type + "/" + r.ID
	}
}

// NewResource builds a resource with initialized maps.
func NewResource(tenantID, typ, id, patientID string) *Resource {
	return &Resource{
		Type:       typ,
		ID:         id,
		TenantID:   tenantID,
		PatientID:  patientID,
		Attributes: make(map[string][]string),
		References: make(map[string][]string),
	}
}

// AddAttribute appends values to a search parameter.
func (r *Resource) AddAttribute(param string, values ...string) *Resource {
	if r.Attributes == nil {
		r.Attributes = make(map[string][]string)
	}
	r.Attributes[param] = append(r.Attributes[param], values...)
	return r
}

// AddReference appends references to a search parameter.
func (r *Resource) AddReference(param string, references ...string) *Resource {
	if r.References == nil {
		r.References = make(map[string][]string)
	}
	r.References[param] = append(r.References[param], references...)
	return r
}

// Key returns the tenant-local resource identity.
func (r *Resource) Key() string {
	if r == nil {
		return ""
	}
	return r.Type + "/" + r.ID
}

// PatientIDs returns every patient identifier associated with the resource.
// In addition to PatientID, all references to Patient resources are included
// so that a resource linking to an unauthorized patient is denied even if its
// owning patient field claims otherwise.
func (r *Resource) PatientIDs() []string {
	if r == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	add := func(raw string) {
		id := normalizePatientID(raw)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	if r.Type == "Patient" {
		add(r.ID)
	}
	add(r.PatientID)
	for _, refs := range []map[string][]string{r.Attributes, r.References} {
		for _, values := range refs {
			for _, raw := range values {
				ref, err := ParseReference(raw)
				if err == nil && ref.Type == "Patient" {
					add(ref.ID)
				}
			}
		}
	}
	return ids
}

func normalizePatientID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "Patient/") {
		raw = strings.TrimPrefix(raw, "Patient/")
	}
	return raw
}

// ParseReference parses relative or absolute FHIR references and returns the
// resource type and logical ID. Version suffixes such as /_history/3 are
// removed. A bare ID is accepted with an empty Type.
func ParseReference(raw string) (Reference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Reference{}, fmt.Errorf("%w: empty reference", ErrInvalidQuery)
	}
	if strings.HasPrefix(strings.ToLower(raw), "urn:") {
		// Opaque URNs cannot be resolved without a server-specific index.
		return Reference{}, fmt.Errorf("%w: opaque reference %q", ErrUnsupported, raw)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return Reference{}, fmt.Errorf("%w: malformed reference", ErrInvalidQuery)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return Reference{}, fmt.Errorf("%w: reference has query or fragment", ErrInvalidQuery)
	}
	path := parsed.EscapedPath()
	if path == "" {
		return Reference{}, fmt.Errorf("%w: empty reference", ErrInvalidQuery)
	}

	parts := strings.Split(path, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return Reference{}, fmt.Errorf("%w: reference path traversal", ErrInvalidQuery)
		}
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return Reference{}, fmt.Errorf("%w: malformed reference", ErrInvalidQuery)
		}
		segments = append(segments, decoded)
	}
	if len(segments) == 0 {
		return Reference{}, fmt.Errorf("%w: empty reference", ErrInvalidQuery)
	}

	for i, segment := range segments {
		if segment == "_history" {
			segments = segments[:i]
			break
		}
	}
	if len(segments) == 0 {
		return Reference{}, fmt.Errorf("%w: reference has no ID", ErrInvalidQuery)
	}
	if len(segments) == 1 {
		id := segments[0]
		if err := validateReferenceID(id); err != nil {
			return Reference{}, err
		}
		return Reference{ID: id}, nil
	}

	typ := segments[len(segments)-2]
	id := segments[len(segments)-1]
	if !validResourceType(typ) {
		return Reference{}, fmt.Errorf("%w: invalid reference type %q", ErrInvalidQuery, typ)
	}
	if err := validateReferenceID(id); err != nil {
		return Reference{}, err
	}
	return Reference{Type: typ, ID: id}, nil
}

func validateReferenceID(id string) error {
	if id == "" || id == "." || id == ".." {
		return fmt.Errorf("%w: invalid reference ID", ErrInvalidQuery)
	}
	if strings.ContainsAny(id, "/\\\x00") {
		return fmt.Errorf("%w: invalid reference ID", ErrInvalidQuery)
	}
	return nil
}

func validResourceType(typ string) bool {
	if typ == "" {
		return false
	}
	for i, r := range typ {
		if i == 0 {
			if !unicode.IsUpper(r) {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func validSearchParam(param string) bool {
	if param == "" {
		return false
	}
	for i, r := range param {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

// authorized reports whether r can be read under the snapshot scope. Non-patient
// resources with patient ownership fields must not conflict with the scope.
func (s scopeState) authorized(r *Resource) bool {
	if r == nil || r.TenantID != s.tenantID || !s.allowsType(r.Type) {
		return false
	}
	patientIDs := r.ownershipPatientIDs()
	if len(patientIDs) == 0 && r.Type != "Patient" {
		// Non-patient resources must have a patient owner unless they are an
		// explicitly recognized tenant-level resource type.
		if r.Shared || sharedResourceType(r.Type) {
			return true
		}
		return false
	}
	if r.Type == "Patient" && len(patientIDs) == 0 {
		return false
	}
	for _, patientID := range patientIDs {
		if !s.allowsPatient(patientID) {
			return false
		}
	}
	return true
}

// ownershipPatientIDs returns identifiers that determine who owns a resource.
// For a Patient resource, its own ID is authoritative; links to other
// patients are relationships, and those targets are checked separately if a
// search actually follows the link. For all other resource types every
// Patient reference is treated as an ownership assertion so conflicting
// owner fields fail closed.
func (r *Resource) ownershipPatientIDs() []string {
	if r == nil {
		return nil
	}
	if r.Type == "Patient" {
		id := normalizePatientID(r.ID)
		if id == "" {
			return nil
		}
		return []string{id}
	}
	return r.PatientIDs()
}

// sharedResourceType recognizes tenant-level resource types that do not carry
// a patient owner. Unknown types without an owner are denied by default.
func sharedResourceType(typ string) bool {
	switch typ {
	case "Practitioner", "PractitionerRole", "Organization", "Location",
		"HealthcareService", "Device", "Medication", "Substance", "Endpoint":
		return true
	default:
		return false
	}
}

func (r *Resource) values(param string) []string {
	if r == nil {
		return nil
	}
	switch param {
	case "_id":
		return []string{r.ID}
	case "_type":
		return []string{r.Type}
	}
	var values []string
	values = append(values, r.Attributes[param]...)
	values = append(values, r.References[param]...)
	return values
}

// referenceValues returns candidate reference strings for a parameter. Both
// the dedicated References map and Attributes are considered so adapters can
// store FHIR values uniformly.
func (r *Resource) referenceValues(param string) []string {
	if r == nil {
		return nil
	}
	values := make([]string, 0, len(r.References[param])+len(r.Attributes[param]))
	values = append(values, r.References[param]...)
	values = append(values, r.Attributes[param]...)
	return values
}

// referenceParams returns all parameters that may contain references.
func (r *Resource) referenceParams() []string {
	if r == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(r.References)+len(r.Attributes))
	for param := range r.References {
		seen[param] = struct{}{}
	}
	for param := range r.Attributes {
		seen[param] = struct{}{}
	}
	params := make([]string, 0, len(seen))
	for param := range seen {
		params = append(params, param)
	}
	sort.Strings(params)
	return params
}

func (r *Resource) hasParam(param string) bool {
	if r == nil {
		return false
	}
	if param == "_id" || param == "_type" {
		return true
	}
	return len(r.Attributes[param]) > 0 || len(r.References[param]) > 0
}
