// Package rar implements a subset policy engine for OAuth 2.0 Rich
// Authorization Requests (RFC 9396).
//
// It decides whether the authorization_details carried in a token request
// (or a token refresh) are actually covered by the details the resource owner
// previously consented to. The supported subset is limited to the common RAR
// object fields type, locations, actions, datatypes and identifier.
package rar

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Detail is one element of an authorization_details array.
//
// Identifier is a pointer so that an omitted "identifier" JSON field can be
// distinguished from an explicitly present, possibly empty, string. The nil
// state means the request does not constrain the identifier.
type Detail struct {
	Type       string   `json:"type"`
	Locations  []string `json:"locations,omitempty"`
	Actions    []string `json:"actions,omitempty"`
	Datatypes  []string `json:"datatypes,omitempty"`
	Identifier *string  `json:"identifier,omitempty"`
}

// NewDetail builds a Detail without an identifier constraint.
func NewDetail(typ string, locations, actions, datatypes []string) Detail {
	return Detail{
		Type:      typ,
		Locations: clone(locations),
		Actions:   clone(actions),
		Datatypes: clone(datatypes),
	}
}

// NewDetailWithIdentifier builds a Detail carrying an explicit identifier.
func NewDetailWithIdentifier(typ string, locations, actions, datatypes []string, identifier string) Detail {
	detail := NewDetail(typ, locations, actions, datatypes)
	detail.Identifier = &identifier
	return detail
}

// HasIdentifier reports whether the detail carries an explicit identifier.
func (d Detail) HasIdentifier() bool {
	return d.Identifier != nil
}

// IdentifierValue returns the explicit identifier and true, or empty string
// and false when the detail does not carry one.
func (d Detail) IdentifierValue() (string, bool) {
	if d.Identifier == nil {
		return "", false
	}
	return *d.Identifier, true
}

// UnmarshalJSON validates the shared fields while decoding a RAR object. It
// rejects a missing or non-string type and non-string array elements instead
// of silently coercing malformed input.
func (d *Detail) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	typeValue, ok := raw["type"]
	if !ok {
		return fmt.Errorf("rar: detail is missing required string field %q", "type")
	}
	if err := json.Unmarshal(typeValue, &d.Type); err != nil {
		return fmt.Errorf("rar: field %q must be a string: %w", "type", err)
	}

	if err := decodeStringSet(raw, "locations", &d.Locations); err != nil {
		return err
	}
	if err := decodeStringSet(raw, "actions", &d.Actions); err != nil {
		return err
	}
	if err := decodeStringSet(raw, "datatypes", &d.Datatypes); err != nil {
		return err
	}

	if identifier, ok := raw["identifier"]; ok {
		var value string
		if err := json.Unmarshal(identifier, &value); err != nil {
			return fmt.Errorf("rar: field %q must be a string: %w", "identifier", err)
		}
		d.Identifier = &value
	}

	return nil
}

func decodeStringSet(raw map[string]json.RawMessage, field string, target *[]string) error {
	value, ok := raw[field]
	if !ok {
		return nil
	}
	var values []string
	if err := json.Unmarshal(value, &values); err != nil {
		return fmt.Errorf("rar: field %q must be an array of strings: %w", field, err)
	}
	*target = values
	return nil
}

// ParseDetails decodes a bare authorization_details JSON array, for example
// the payload of the authorization_details request parameter.
func ParseDetails(data []byte) ([]Detail, error) {
	var details []Detail
	if err := json.Unmarshal(data, &details); err != nil {
		return nil, fmt.Errorf("rar: invalid authorization_details: %w", err)
	}
	return details, nil
}

// Field names the RAR object field that caused a request to be rejected.
type Field string

const (
	FieldType       Field = "type"
	FieldLocations  Field = "locations"
	FieldActions    Field = "actions"
	FieldDatatypes  Field = "datatypes"
	FieldIdentifier Field = "identifier"
)

// Violation describes the first requested detail that no granted detail can
// satisfy. Error can be unwrapped with errors.As to inspect the details.
type Violation struct {
	Field       Field
	Requested   Detail
	GrantedType string
}

func (v *Violation) Error() string {
	switch v.Field {
	case FieldType:
		return fmt.Sprintf("rar: unknown authorization detail type %q", v.Requested.Type)
	default:
		return fmt.Sprintf("rar: requested %s detail is not covered by any granted detail of type %q", v.Field, v.Requested.Type)
	}
}

// Satisfy reports whether every requested detail is covered by at least one
// granted detail. A nil error means the request may proceed.
//
// A requested detail is satisfied iff a granted detail exists such that:
//   - both details share the same type; an unknown type, meaning no granted
//     detail of that type exists, rejects the whole request;
//   - every requested location equals, byte for byte, some granted location —
//     prefix matching is never performed;
//   - granted actions and datatypes are supersets of the requested ones;
//   - the identifiers are equal, or the request does not provide one.
//
// Repeated details in either list are counted only once.
func Satisfy(requested, granted []Detail) error {
	requested = dedupe(requested)
	grantedByType := indexByType(granted)

	for _, request := range requested {
		candidates, ok := grantedByType[request.Type]
		if !ok {
			return &Violation{Field: FieldType, Requested: request}
		}
		if !coveredByAny(request, candidates) {
			return &Violation{Field: failingField(request, candidates), Requested: request, GrantedType: request.Type}
		}
	}
	return nil
}

// RefreshAllowed is Satisfy specialized for token refresh: the details
// obtained from the refresh must be a subset of the details originally
// granted, otherwise the refresh would silently broaden consent.
func RefreshAllowed(refreshed, original []Detail) error {
	return Satisfy(refreshed, original)
}

// Subset is the boolean-only form of Satisfy.
func Subset(requested, granted []Detail) bool {
	return Satisfy(requested, granted) == nil
}

// AsViolation extracts the Violation from a Satisfy error, or returns nil.
func AsViolation(err error) *Violation {
	var violation *Violation
	if errors.As(err, &violation) {
		return violation
	}
	return nil
}

func coveredByAny(request Detail, granted []Detail) bool {
	for _, grant := range granted {
		if covers(grant, request) {
			return true
		}
	}
	return false
}

// covers reports whether grant satisfies request. Type equality is guaranteed
// by the caller.
func covers(grant, request Detail) bool {
	return locationsCover(grant.Locations, request.Locations) &&
		isSuperset(grant.Actions, request.Actions) &&
		isSuperset(grant.Datatypes, request.Datatypes) &&
		identifierCovers(grant, request)
}

// locationsCover requires an exact byte-equal granted location for every
// requested URL. A granted prefix of the requested URL does not match.
func locationsCover(granted, requested []string) bool {
	for _, requestURL := range requested {
		found := false
		for _, grantedURL := range granted {
			if requestURL == grantedURL {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// isSuperset reports whether every requested value appears in granted. An
// empty request is vacuously covered by any granted set.
func isSuperset(granted, requested []string) bool {
	allowed := make(map[string]struct{}, len(granted))
	for _, value := range granted {
		allowed[value] = struct{}{}
	}
	for _, value := range requested {
		if _, ok := allowed[value]; !ok {
			return false
		}
	}
	return true
}

func identifierCovers(grant, request Detail) bool {
	if request.Identifier == nil {
		return true
	}
	if grant.Identifier == nil {
		return false
	}
	return *grant.Identifier == *request.Identifier
}

// failingField picks a concrete reason when no granted detail of the same
// type covers the request.
func failingField(request Detail, granted []Detail) Field {
	for _, grant := range granted {
		if !locationsCover(grant.Locations, request.Locations) {
			continue
		}
		if !isSuperset(grant.Actions, request.Actions) {
			return FieldActions
		}
		if !isSuperset(grant.Datatypes, request.Datatypes) {
			return FieldDatatypes
		}
		if !identifierCovers(grant, request) {
			return FieldIdentifier
		}
	}
	return FieldLocations
}

func indexByType(details []Detail) map[string][]Detail {
	indexed := make(map[string][]Detail)
	for _, detail := range dedupe(details) {
		indexed[detail.Type] = append(indexed[detail.Type], detail)
	}
	return indexed
}

// dedupe collapses identical details so repeated request items are counted
// once. Field order is irrelevant because a normalized encoding is used as
// the identity key.
func dedupe(details []Detail) []Detail {
	seen := make(map[string]struct{}, len(details))
	unique := make([]Detail, 0, len(details))
	for _, detail := range details {
		key := canonicalKey(detail)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, detail)
	}
	return unique
}

func canonicalKey(detail Detail) string {
	normalized := Detail{
		Type:       detail.Type,
		Locations:  sortedCopy(detail.Locations),
		Actions:    sortedCopy(detail.Actions),
		Datatypes:  sortedCopy(detail.Datatypes),
		Identifier: detail.Identifier,
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		// Detail only contains JSON-safe values; marshalling cannot fail.
		panic(err)
	}
	return string(encoded)
}

func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	copied := clone(values)
	sort.Strings(copied)
	return copied
}

func clone(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}
