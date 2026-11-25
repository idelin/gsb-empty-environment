package closure

// Domain aliases for host vocabulary.
type (
	// Provenance is an attestation bound to identity, condition, and generation.
	Provenance = Attestation

	// Witness is replayable rejection evidence.
	Witness = Evidence

	// BDD is the shared ordered decision graph manager.
	BDD = Graph

	// ROBDD is a descriptive alias for the shared reduced graph manager.
	ROBDD = Graph

	// PackageTable is a finite candidate package list.
	PackageTable = []Package
)

// NewBDD creates a shared ROBDD manager. Zero/negative budget is unlimited.
func NewBDD(order []string, budget int) (*BDD, error) { return NewGraph(order, budget) }

// NewROBDD creates a shared ROBDD manager.
func NewROBDD(order []string, budget int) (*ROBDD, error) { return NewGraph(order, budget) }

// AnalyzeClosure is a descriptive alias for Analyze.
func AnalyzeClosure(input any) (*Report, error) { return Analyze(input) }

// ResolveClosure is a host-friendly alias for Analyze.
func ResolveClosure(input any) (*Report, error) { return Analyze(input) }

// Run is a short alias for Analyze.
func Run(input any) (*Report, error) { return Analyze(input) }
