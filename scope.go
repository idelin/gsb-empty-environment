package netsec

import (
	"fmt"
	"strings"
)

// Scope describes what a principal is allowed to read. A search never widens
// this scope, regardless of the references present in resources or the search
// parameters supplied by the caller.
//
// An empty PatientIDs slice denies access to every patient-scoped resource.
// ResourceTypes is an optional allowlist; nil or empty means all resource
// types are permitted. The tenant is always mandatory.
type Scope struct {
	TenantID      string
	PatientIDs    []string
	ResourceTypes []string
}

// AccessScope is an alias kept for callers that prefer the more explicit name.
type AccessScope = Scope

type scopeState struct {
	tenantID string
	patients map[string]struct{}
	types    map[string]struct{}
}

// NewScope returns a scope for one tenant and a set of authorized patients.
// The slices are copied into the returned value.
func NewScope(tenantID string, patientIDs ...string) Scope {
	return Scope{TenantID: tenantID, PatientIDs: append([]string(nil), patientIDs...)}
}

// NewAccessScope is an alias for NewScope.
func NewAccessScope(tenantID string, patientIDs ...string) Scope {
	return NewScope(tenantID, patientIDs...)
}

// WithResourceTypes returns a copy of s restricted to the supplied resource
// types. Passing no types leaves the allowlist empty, which permits all types.
func (s Scope) WithResourceTypes(resourceTypes ...string) Scope {
	s.ResourceTypes = append([]string(nil), resourceTypes...)
	return s
}

// AllowsPatient reports whether patientID is in the scope.
func (s Scope) AllowsPatient(patientID string) bool {
	patientID = normalizePatientID(patientID)
	for _, candidate := range s.PatientIDs {
		if normalizePatientID(candidate) == patientID {
			return true
		}
	}
	return false
}

// AllowsResourceType reports whether typ is permitted by the scope's optional
// resource-type allowlist.
func (s Scope) AllowsResourceType(typ string) bool {
	if len(s.ResourceTypes) == 0 {
		return true
	}
	for _, candidate := range s.ResourceTypes {
		if candidate == typ {
			return true
		}
	}
	return false
}

// AllowsResource applies the same tenant, patient, and resource-type checks
// used by Engine to one resource.
func (s Scope) AllowsResource(resource *Resource) bool {
	state, err := s.snapshot()
	if err != nil {
		return false
	}
	return state.authorized(resource)
}

// FilterResources returns a copy containing only resources allowed by the
// scope. It is suitable for services that need a count without running a
// search expression.
func (s Scope) FilterResources(resources []*Resource) []*Resource {
	state, err := s.snapshot()
	if err != nil {
		return nil
	}
	filtered := make([]*Resource, 0, len(resources))
	for _, resource := range resources {
		if state.authorized(resource) {
			filtered = append(filtered, resource)
		}
	}
	return filtered
}

func (s Scope) snapshot() (scopeState, error) {
	tenant := strings.TrimSpace(s.TenantID)
	if tenant == "" {
		return scopeState{}, fmt.Errorf("%w: tenant is required", ErrInvalidScope)
	}

	state := scopeState{
		tenantID: tenant,
		patients: make(map[string]struct{}, len(s.PatientIDs)),
		types:    make(map[string]struct{}, len(s.ResourceTypes)),
	}
	for _, raw := range s.PatientIDs {
		patient := normalizePatientID(raw)
		if patient == "" {
			return scopeState{}, fmt.Errorf("%w: empty patient identifier", ErrInvalidScope)
		}
		state.patients[patient] = struct{}{}
	}
	for _, raw := range s.ResourceTypes {
		typ := strings.TrimSpace(raw)
		if typ == "" {
			return scopeState{}, fmt.Errorf("%w: empty resource type", ErrInvalidScope)
		}
		state.types[typ] = struct{}{}
	}
	return state, nil
}

func (s scopeState) allowsPatient(patientID string) bool {
	_, ok := s.patients[patientID]
	return ok
}

func (s scopeState) allowsType(typ string) bool {
	if len(s.types) == 0 {
		return true
	}
	_, ok := s.types[typ]
	return ok
}
