package netsec

import (
	"errors"
	"fmt"
)

const (
	// MaxModules is the maximum number of declared modules, including plugins.
	MaxModules = 16

	// MaxPlugins is the maximum number of plugin modules an attacker may place.
	MaxPlugins = 3

	// MaxReportedPlans is the maximum number of equal-size plans returned by
	// an audit. Audit.TotalPlans still reports the full count.
	MaxReportedPlans = 20

	// UnlimitedBudget disables the insertion-layout budget. Any negative
	// budget has the same effect.
	UnlimitedBudget int64 = -1
)

// ErrInvalidInput identifies malformed module graphs, global orders, and
// relocation requests. Errors returned for these conditions wrap this value.
var ErrInvalidInput = errors.New("netsec: invalid input")

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

// Export is a symbol definition. Name and Version together form the export's
// identity within a module. Strong distinguishes a strong definition from a
// weak definition.
type Export struct {
	Name    string
	Version string
	Strong  bool
}

// Module is one loadable module.
//
// ID must be unique across all declared modules. Dependencies are searched in
// the given order. Exports must have a unique (Name, Version) pair within the
// module. Trusted describes the module, and therefore all exports it supplies.
type Module struct {
	ID           string
	Trusted      bool
	Dependencies []string
	Exports      []Export
}

// Relocation describes a symbol reference made by Origin.
//
// AllowPreemption selects between the preemptible and non-preemptible lookup
// orders. RequireTrusted records the caller's security requirement; Resolve
// reports whether the selected target satisfies it rather than silently
// rejecting an unsafe binding.
type Relocation struct {
	Origin          string
	Name            string
	Version         string
	AllowPreemption bool
	RequireTrusted  bool
}

// Selection describes how the winning definition was chosen.
type Selection string

const (
	SelectionNone        Selection = "none"
	SelectionFirstStrong Selection = "first-strong"
	SelectionFirstWeak   Selection = "first-weak"
)

// LookupEntry records one module visited in a complete lookup. HasMatch is
// true when the module exports the requested name and exact version. Strong is
// meaningful only when HasMatch is true. Selected marks the winning entry.
type LookupEntry struct {
	ModuleID string
	Trusted  bool
	HasMatch bool
	Strong   bool
	Selected bool
}

// Binding is the result of resolving one relocation.
//
// Sequence is the deduplicated module search order. Lookup contains one entry
// for every module in Sequence, including modules that did not match. When
// Found is true, ModuleID identifies the winning module. TargetTrusted is the
// trust bit of that module. TrustSatisfied is true when either no trust was
// required or the winning module is trusted.
type Binding struct {
	Found          bool
	ModuleID       string
	Name           string
	Version        string
	Strong         bool
	TargetTrusted  bool
	RequiresTrust  bool
	TrustSatisfied bool
	Selection      Selection
	Sequence       []string
	Lookup         []LookupEntry
	Explanation    string
}

// Insertion is one plugin placement. Position is the zero-based index of the
// plugin in the resulting global order.
type Insertion struct {
	ModuleID string
	Position int
}

// Plan is one minimum-size attacker plan.
//
// Insertions are sorted by ModuleID and then Position. FinalOrder is the
// complete global order after insertion, and Binding is the full resolution
// trace for the relocation under that order.
type Plan struct {
	Insertions []Insertion
	FinalOrder []string
	Binding    Binding
}

// AuditStatus is the outcome of an insertion search.
type AuditStatus string

const (
	// AuditFound means the search completed and Plans contains the minimum
	// number of plugins needed.
	AuditFound AuditStatus = "found"

	// AuditImpossible means the exhaustive search completed and no number of
	// the supplied plugins can cause the unsafe binding.
	AuditImpossible AuditStatus = "impossible"

	// AuditUnknown means the budget expired before the search could prove a
	// minimum or prove impossibility.
	AuditUnknown AuditStatus = "unknown"
)

// Audit is the result of Analyze or AnalyzeBudget.
//
// MinPlugins is -1 when Status is AuditImpossible or AuditUnknown. TotalPlans
// counts all equal-size plans found at the minimum size; Plans contains at
// most MaxReportedPlans of them. Examined is the number of distinct final
// global orders checked, including the zero-plugin order.
type Audit struct {
	Status     AuditStatus
	MinPlugins int
	Plans      []Plan
	TotalPlans int
	Truncated  bool
	Examined   int64
	Budget     int64
	Relocation Relocation
}
