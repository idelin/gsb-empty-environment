package symbolbind

type Strength string

const (
	Weak   Strength = "weak"
	Strong Strength = "strong"
)

type Status string

const (
	StatusAttack   Status = "attack"
	StatusNoAttack Status = "no_attack"
	StatusUnknown  Status = "unknown"
)

type Module struct {
	ID           string
	Trusted      bool
	Dependencies []string
	Exports      []Export
}

type DefinitionRef struct {
	ModuleID string
	Name     string
	Version  string
}

type Export struct {
	Name       string
	Version    string
	Strength   Strength
	Definition *DefinitionRef
}

type Relocation struct {
	OriginID           string
	Name               string
	Version            string
	AllowInterposition bool
	RequireTrusted     bool
}

type Input struct {
	Modules     []Module
	GlobalOrder []string
	Plugins     []Module
}

type SequenceEntry struct {
	ModuleID string
	Source   string
}

type Candidate struct {
	Step            int
	ExporterID      string
	DefinerID       string
	DefinerTrusted  bool
	Name            string
	Version         string
	Strength        Strength
	AllowInterposed bool
}

type Inspection struct {
	Step       int
	ModuleID   string
	Name       string
	Version    string
	Matched    bool
	Strength   Strength
	ExporterID string
	DefinerID  string
	SkipReason string
	Available  []string
}

type Trace struct {
	Relocation  Relocation
	Sequence    []SequenceEntry
	Inspections []Inspection
	Candidates  []Candidate
	Winner      *Candidate
	Resolved    bool
	Untrusted   bool
	Reason      string
}

type Insertion struct {
	PluginID string
	// Position is the zero-based slot before a host module; len(GlobalOrder)
	// means insertion after every host module.
	Position int
}

type Scheme struct {
	Insertions  []Insertion
	GlobalOrder []string
	Trace       *Trace
}

type AttackOptions struct {
	// MaxSchemes bounds baseline checks and generated plugin placements. Zero
	// uses the package default. Exhaustion makes the answer unknown.
	MaxSchemes int64
	// Limit is the maximum number of equal-length schemes retained. Zero uses
	// the required default of 20.
	Limit int
}

type AttackResult struct {
	Status      Status
	PluginCount int
	Schemes     []Scheme
	Limit       int
	Truncated   bool
	Reason      string
}
