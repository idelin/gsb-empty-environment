package binding

// Strength describes whether an export is a strong or weak definition.
type Strength string

const (
	Weak   Strength = "weak"
	Strong Strength = "strong"
)

// MatchStatus records why a module did or did not match at one search step.
type MatchStatus string

const (
	Matched         MatchStatus = "matched"
	NoSymbol        MatchStatus = "no_symbol"
	VersionMismatch MatchStatus = "version_mismatch"
)

// AttackStatus is the final result of searching an insertion attack.
type AttackStatus string

const (
	AttackSolved  AttackStatus = "solved"
	AttackNoPlan  AttackStatus = "no_plan"
	AttackUnknown AttackStatus = "unknown"
)

// SymbolRef identifies an export by module, name, and exact version.
type SymbolRef struct {
	ModuleID string
	Name     string
	Version  string
}

// Export is one entry in a module's uniquely keyed export table. A non-nil
// AliasOf makes this entry an alias; trust is still taken from the final
// definer reached by the alias chain.
type Export struct {
	Name     string
	Version  string
	Strength Strength
	AliasOf  *SymbolRef
}

// Module describes one loadable module and its ordered dependencies.
type Module struct {
	ID           string
	Trusted      bool
	Dependencies []string
	Exports      []Export
}

// Relocation describes one symbol lookup initiated by a module.
type Relocation struct {
	SourceID        string
	Name            string
	Version         string
	AllowPreemption bool
	RequireTrusted  bool
}

// SymbolBinding records the visible export and the module that actually
// defines it after alias resolution.
type SymbolBinding struct {
	ModuleID       string
	DefinerID      string
	Export         Export
	DefinerTrusted bool
}

// SearchEvent is one step in the deduplicated complete lookup sequence.
type SearchEvent struct {
	ModuleID          string
	ModuleTrusted     bool
	Status            MatchStatus
	AvailableVersions []string
	Strength          Strength
	Alias             bool
	AliasTarget       *SymbolRef
	DefinerID         string
	DefinerTrusted    bool
}

// BindingResult contains the complete lookup trace and the winning reason.
type BindingResult struct {
	Relocation       Relocation
	Found            bool
	WinnerModuleID   string
	DefinerModuleID  string
	Export           Export
	DefinerTrusted   bool
	TrustViolation   bool
	SearchOrder      []string
	Events           []SearchEvent
	StrongCandidates []SymbolBinding
	WeakCandidates   []SymbolBinding
	Reason           string
}

// PluginPlacement is a zero-based insertion position in the host's base list.
type PluginPlacement struct {
	PluginID string
	Position int
}

// AttackPlan is one minimum-size plugin insertion that causes a trust
// violation, together with the resulting complete binding trace.
type AttackPlan struct {
	Placements  []PluginPlacement
	GlobalOrder []string
	Binding     BindingResult
}

// AttackOptions controls the insertion search. Zero or negative values use
// the package defaults; MaxPlugins is still capped at three.
type AttackOptions struct {
	MaxPlugins   int
	SearchBudget int
	MaxPlans     int
}

// AttackReport contains the minimum attack plans, or an unknown result when
// the search budget cannot prove a minimum.
type AttackReport struct {
	Status         AttackStatus
	MinimumPlugins int
	Plans          []AttackPlan
	Truncated      bool
	BudgetUsed     int
	Reason         string
}
