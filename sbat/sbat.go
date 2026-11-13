// Package sbat implements SBAT (Secure Boot Advanced Targeting) validation for
// a Go boot-admission service.
//
// SBAT data lives in the .sbat section of a signed PE image as CSV text. Each
// line carries a component name and a generation number. A revocation policy
// records, per component, the lowest generation that is still allowed: a binary
// carrying an older signed component is a rollback and must be rejected.
//
// The package validates the section table and section contents together so the
// usual bypasses are caught: a missing .sbat section, a section whose raw size
// does not match the supplied contents, malformed or non-numeric generations,
// duplicated component names, and extra CSV fields used to confuse a naive
// string comparison.
package sbat

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// PESection describes one entry of a PE image section table.
type PESection struct {
	Name        string
	VirtualAddr uint32
	RawSize     uint32
}

// Entry is one parsed SBAT record: a component and its generation.
type Entry struct {
	Component  string
	Generation uint64
}

// SectionName is the PE section that carries the SBAT CSV payload.
const SectionName = ".sbat"

var (
	// ErrSectionMissing means the section table contains no .sbat section.
	ErrSectionMissing = errors.New("sbat: .sbat section missing")
	// ErrSizeMismatch means the section table raw size differs from the
	// length of the supplied section contents.
	ErrSizeMismatch = errors.New("sbat: section raw size does not match content length")
	// ErrMalformed means the CSV does not follow the SBAT layout.
	ErrMalformed = errors.New("sbat: malformed .sbat content")
	// ErrInvalidGeneration means a generation field is not a decimal integer.
	ErrInvalidGeneration = errors.New("sbat: generation is not a decimal integer")
	// ErrDuplicateComponent means the same component appears more than once.
	ErrDuplicateComponent = errors.New("sbat: duplicate component name")
	// ErrPolicyRollback means a policy update would lower a component minimum.
	ErrPolicyRollback = errors.New("sbat: policy update would roll back a revocation generation")
)

// RevocationError reports that an image component is below the minimum
// generation recorded by the revocation policy.
type RevocationError struct {
	Component  string
	Generation uint64
	Minimum    uint64
}

func (e *RevocationError) Error() string {
	return fmt.Sprintf("sbat: component %q generation %d below minimum %d",
		e.Component, e.Generation, e.Minimum)
}

// Parse parses the raw .sbat section contents into ordered entries.
//
// The first line must start with "sbat,1" (the SBAT header); every following
// line must contain a component name, a decimal generation and at least one
// vendor-information field. Non-numeric generations, duplicated component
// names, empty lines, extra/missing CSV fields and embedded whitespace in a
// component name are all rejected rather than silently ignored, so that a
// crafted string cannot smuggle a different component past the comparison.
func Parse(content []byte) ([]Entry, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("%w: empty section", ErrMalformed)
	}

	text := strings.TrimSuffix(string(content), "\n")
	lines := strings.Split(text, "\n")

	entries := make([]Entry, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))

	for lineNo, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" {
			return nil, fmt.Errorf("%w: empty line at line %d", ErrMalformed, lineNo+1)
		}

		fields := strings.Split(line, ",")
		if lineNo == 0 {
			if len(fields) < 2 || fields[0] != "sbat" || fields[1] != "1" {
				return nil, fmt.Errorf("%w: first line must start with sbat,1", ErrMalformed)
			}
			// The SBAT header is itself a revocable component ("sbat"), used
			// as a global kill switch, so keep it among the checked entries.
			fields = fields[:2]
		} else if len(fields) < 3 {
			return nil, fmt.Errorf("%w: line %d must be component,generation,vendor",
				ErrMalformed, lineNo+1)
		}

		component := fields[0]
		if !validComponentName(component) {
			return nil, fmt.Errorf("%w: invalid component name %q on line %d",
				ErrMalformed, component, lineNo+1)
		}

		generation, err := parseGeneration(fields[1])
		if err != nil {
			return nil, fmt.Errorf("%w: component %q: %v",
				ErrInvalidGeneration, component, err)
		}

		if _, ok := seen[component]; ok {
			return nil, fmt.Errorf("%w: %q on line %d",
				ErrDuplicateComponent, component, lineNo+1)
		}
		seen[component] = struct{}{}

		entries = append(entries, Entry{Component: component, Generation: generation})
	}

	return entries, nil
}

// Check rejects entries whose component generation is below the policy minimum.
// Components that have no revocation entry are unconstrained.
func Check(entries []Entry, policy map[string]uint64) error {
	for _, entry := range entries {
		minimum, ok := policy[entry.Component]
		if !ok {
			continue
		}
		if entry.Generation < minimum {
			return &RevocationError{
				Component:  entry.Component,
				Generation: entry.Generation,
				Minimum:    minimum,
			}
		}
	}
	return nil
}

// CheckSection validates a .sbat section located through the PE section table.
// It rejects a missing section, a raw size that does not equal the supplied
// content length, malformed contents, and any component below its policy
// minimum.
func CheckSection(sections []PESection, content []byte, policy map[string]uint64) error {
	section, ok := findSection(sections)
	if !ok {
		return ErrSectionMissing
	}
	if int64(section.RawSize) != int64(len(content)) {
		return fmt.Errorf("%w: table declares %d bytes, got %d bytes",
			ErrSizeMismatch, section.RawSize, len(content))
	}

	entries, err := Parse(content)
	if err != nil {
		return err
	}
	return Check(entries, policy)
}

// PolicyStore is the registered revocation policy: component name to the lowest
// generation still allowed. It is safe for concurrent use.
//
// Updates are monotonic: a component minimum may only be raised, never lowered,
// and registered components are never removed. A generation that has been
// revoked therefore stays revoked even if an attacker feeds in an older policy
// document.
type PolicyStore struct {
	mu       sync.RWMutex
	minimums map[string]uint64
}

// NewPolicyStore creates a store seeded with the given policy. The seed is
// treated as the initial trusted revision and is copied so later mutation of
// the argument cannot affect the store.
func NewPolicyStore(seed map[string]uint64) *PolicyStore {
	minimums := make(map[string]uint64, len(seed))
	for component, generation := range seed {
		minimums[component] = generation
	}
	return &PolicyStore{minimums: minimums}
}

// Update merges a new policy revision. It returns ErrPolicyRollback, without
// changing any stored value, when the revision lowers a known component or
// drops a known component. New components and equal-or-higher generations are
// accepted.
func (s *PolicyStore) Update(revision map[string]uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for component, generation := range revision {
		if !validComponentName(component) {
			return fmt.Errorf("%w: invalid component name %q", ErrPolicyRollback, component)
		}
		current, ok := s.minimums[component]
		if ok && generation < current {
			return fmt.Errorf("%w: component %q minimum %d would drop to %d",
				ErrPolicyRollback, component, current, generation)
		}
	}

	for component := range s.minimums {
		if _, ok := revision[component]; !ok {
			return fmt.Errorf("%w: component %q would be removed",
				ErrPolicyRollback, component)
		}
	}

	for component, generation := range revision {
		s.minimums[component] = generation
	}
	return nil
}

// Minimum returns the registered minimum generation for a component and
// whether the component is registered.
func (s *PolicyStore) Minimum(component string) (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	generation, ok := s.minimums[component]
	return generation, ok
}

// Snapshot returns a copy of the current policy for passing to Check.
func (s *PolicyStore) Snapshot() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]uint64, len(s.minimums))
	for component, generation := range s.minimums {
		snapshot[component] = generation
	}
	return snapshot
}

// ValidateImage runs section-table validation against the current policy. It
// is the entry point intended for image-approval callers.
func (s *PolicyStore) ValidateImage(sections []PESection, content []byte) error {
	return CheckSection(sections, content, s.Snapshot())
}

func findSection(sections []PESection) (PESection, bool) {
	for _, section := range sections {
		name := strings.TrimRight(section.Name, "\x00")
		if name == SectionName {
			return section, true
		}
	}
	return PESection{}, false
}

func validComponentName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || r == ',' {
			return false
		}
	}
	return true
}

// parseGeneration accepts only unsigned decimal integers: an optional leading
// sign, whitespace, an empty field or a value that overflows uint64 is
// rejected rather than parsed leniently.
func parseGeneration(field string) (uint64, error) {
	if field == "" {
		return 0, errors.New("empty generation")
	}
	var value uint64
	for _, r := range field {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-decimal generation %q", field)
		}
		digit := uint64(r - '0')
		if value > (^uint64(0)-digit)/10 {
			return 0, fmt.Errorf("generation %q overflows uint64", field)
		}
		value = value*10 + digit
	}
	return value, nil
}
