package obsmerge

import (
	"errors"
	"fmt"
)

// PriorityFunc returns the trust priority of sourceID for field.
// Higher numbers mean more trusted. Priority is compared per field, so a
// source may be authoritative for one field and weak for another.
type PriorityFunc func(sourceID, field string) int

// StaticPriority builds a PriorityFunc from a fixed per-source priority.
// Every field gets the same priority for a given source. Sources absent from
// the map fall back to defaultPriority.
func StaticPriority(defaultPriority int, sources map[string]int) PriorityFunc {
	return func(sourceID, _ string) int {
		if p, ok := sources[sourceID]; ok {
			return p
		}
		return defaultPriority
	}
}

// FieldPriority builds a per-field, per-source PriorityFunc.
// fieldSources maps a field name to source priorities for that field;
// sourceDefaults applies when a source is not listed for a field, and
// defaultPriority is the final fallback.
func FieldPriority(defaultPriority int, sourceDefaults map[string]int, fieldSources map[string]map[string]int) PriorityFunc {
	return func(sourceID, field string) int {
		if bySource, ok := fieldSources[field]; ok {
			if p, ok := bySource[sourceID]; ok {
				return p
			}
		}
		if p, ok := sourceDefaults[sourceID]; ok {
			return p
		}
		return defaultPriority
	}
}

// Nullable is a field value that distinguishes "no value was provided" from
// "the page explicitly provided an empty value".
//
// When Null is true the field was covered and the page reported an empty
// (deleted) value; Value is ignored. When Null is false Value is the observed
// value, including the legitimate zero value of T.
type Nullable[T any] struct {
	Value T
	Null  bool
}

// Of wraps a concrete value.
func Of[T any](v T) Nullable[T] { return Nullable[T]{Value: v} }

// Empty returns an explicit empty/null observation for a covered field.
func Empty[T any]() Nullable[T] { return Nullable[T]{Null: true} }

// IsNull reports whether the observation is an explicit empty value.
func (n Nullable[T]) IsNull() bool { return n.Null }

// Observation is one scrape of a page for one business key.
type Observation[T any] struct {
	// ID uniquely identifies the scrape (for example source + fetch timestamp).
	// It is used for retraction and duplicate detection.
	ID string

	// SourceID names the upstream system/page owner that produced the scrape.
	SourceID string

	// SourceVersion is the version of the source at scrape time. It is carried
	// through to provenance so every output field can be traced.
	SourceVersion string

	// PageType identifies the kind of page that was scraped.
	PageType string

	// BusinessKey identifies the entity the observation belongs to.
	BusinessKey string

	// CoveredFields is the set of fields this page actually provides.
	// Fields not in this set are not observed and must remain unknown.
	CoveredFields []string

	// Fields holds the values of CoveredFields. A field present in
	// CoveredFields but absent from Fields is treated as an explicit empty
	// (deleting) value, matching a Nullable with Null set.
	Fields map[string]Nullable[T]

	// Seq orders ingestion. It only supersedes earlier observations from the
	// same source and makes replay deterministic; it never lets a later
	// observation override an equal-priority peer from another source.
	Seq int64
}

// Status is the resolved state of an output field.
type Status string

const (
	// StatusUnknown: no retained observation covers the field.
	StatusUnknown Status = "unknown"
	// StatusResolved: exactly one top-priority piece of evidence.
	StatusResolved Status = "resolved"
	// StatusDeleted: the top-priority evidence is an explicit empty value.
	StatusDeleted Status = "deleted"
	// StatusConflict: equal-priority evidence disagrees; all of it is retained.
	StatusConflict Status = "conflict"
)

// Evidence is one retained observation's contribution to a field.
type Evidence[T any] struct {
	SourceID      string
	SourceVersion string
	PageType      string
	ObservationID string
	Seq           int64
	Priority      int
	Null          bool
	Value         T
}

// FieldView is the resolved output for one field with full provenance.
type FieldView[T any] struct {
	Field  string
	Status Status
	Value  T
	// Winners holds the top-priority evidence, sorted by Seq. For resolved and
	// deleted fields it has one entry; for conflicts it holds every tied
	// piece of evidence so no side is silently dropped.
	Winners []Evidence[T]
	// Sources is every retained piece of evidence for the field (including
	// lower-priority ones), sorted by priority desc then Seq.
	Sources []Evidence[T]
	// Candidates is an alias for Winners on conflict fields and nil otherwise,
	// giving callers a self-documenting accessor for conflicting evidence.
	Candidates []Evidence[T]
}

// RecordView is the resolved output for one business key.
type RecordView[T any] struct {
	BusinessKey string
	Fields      map[string]FieldView[T]
}

// ErrNotFound indicates that the retracted observation or queried key/field
// is not present in the engine.
var ErrNotFound = errors.New("obsmerge: not found")

func validate[T any](obs Observation[T]) error {
	if obs.ID == "" {
		return errors.New("obsmerge: observation ID is required")
	}
	if obs.SourceID == "" {
		return fmt.Errorf("obsmerge: observation %q: source ID is required", obs.ID)
	}
	if obs.BusinessKey == "" {
		return fmt.Errorf("obsmerge: observation %q: business key is required", obs.ID)
	}

	seen := make(map[string]struct{}, len(obs.CoveredFields))
	for _, f := range obs.CoveredFields {
		if f == "" {
			return fmt.Errorf("obsmerge: observation %q: covered field names must be non-empty", obs.ID)
		}
		if _, dup := seen[f]; dup {
			return fmt.Errorf("obsmerge: observation %q: covered field %q listed more than once", obs.ID, f)
		}
		seen[f] = struct{}{}
	}
	for f := range obs.Fields {
		if _, ok := seen[f]; !ok {
			return fmt.Errorf("obsmerge: observation %q: field %q is in Fields but not in CoveredFields", obs.ID, f)
		}
	}
	return nil
}
