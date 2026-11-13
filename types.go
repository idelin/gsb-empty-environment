package netsec

import (
	"fmt"
	"sort"
)

const (
	MaxRecords    = 12
	MaxCandidates = 64
	MaxWitnesses  = 20
)

const (
	DefaultMaxSearchNodes uint64 = 50_000_000
	UnlimitedSearchNodes  uint64 = ^uint64(0)
)

// Status describes the completeness of a recovery result.
type Status string

const (
	Satisfiable   Status = "satisfiable"
	Unsatisfiable Status = "unsatisfiable"
	Unknown       Status = "unknown"

	StatusSatisfiable   = Satisfiable
	StatusUnsatisfiable = Unsatisfiable
	StatusUnknown       = Unknown
)

// CandidateState records what the search proved about one candidate.
type CandidateState uint8

const (
	CandidateUnknown CandidateState = iota
	CandidateFeasible
	CandidateImpossible
)

func (s CandidateState) String() string {
	switch s {
	case CandidateFeasible:
		return "feasible"
	case CandidateImpossible:
		return "impossible"
	default:
		return "unknown"
	}
}

// Record is one ciphertext with its candidate plaintexts.
//
// Offset is the non-negative starting position of the record in the shared
// keystream. Every candidate must have exactly len(Ciphertext) bytes.
type Record struct {
	ID         string
	Ciphertext []byte
	Offset     int
	Candidates [][]byte
}

// Options controls bounded search and witness output.
//
// MaxSearchNodes limits deterministic search work. Zero selects the default;
// UnlimitedSearchNodes disables the limit. WitnessLimit is clamped to the
// package maximum of 20; zero selects 20.
type Options struct {
	MaxSearchNodes       uint64
	WitnessLimit         int
	SkipConflictAnalysis bool
}

// DefaultOptions returns the options used by Recover when none are supplied.
func DefaultOptions() Options {
	return Options{
		MaxSearchNodes: DefaultMaxSearchNodes,
		WitnessLimit:   MaxWitnesses,
	}
}

// KeyByte is a determined keystream byte at an absolute keystream offset.
type KeyByte struct {
	Offset int
	Value  byte
}

// Conflict describes a smallest unsatisfiable record subset.
//
// RecordIDs and RecordIndices are sorted by record ID, then input index.
// Deletions contains one satisfying assignment for the conflict subset after
// removing each of its records, extended to all remaining input records when
// possible. This is the minimality witness: every proper subset of a smallest
// conflict subset is satisfiable.
type Conflict struct {
	RecordIDs     []string
	RecordIndices []int
	Deletions     []DeletionWitness
}

// DeletionWitness is a complete assignment for the conflict subset after one
// record has been removed. CandidateIndices is aligned with RecordIndices.
// FullIndices has one entry per input record and contains -1 for inactive
// records. CoversAllInput reports that every input record except RemovedIndex
// has a candidate in this witness.
type DeletionWitness struct {
	RemovedID        string
	RemovedIndex     int
	RecordIDs        []string
	RecordIndices    []int
	CandidateIndices []int
	FullIndices      []int
	CoversAllInput   bool
}

// Result is the complete recovery report.
//
// Candidates[i] lists candidate indices for input record i that occur in at
// least one complete solution. When Status is Unknown, candidates that could
// not be resolved are reported through CandidateStates instead.
//
// Determined contains only keystream positions whose byte is identical in
// every completion consistent with everything the search has proved. It is
// conservative when Status is Unknown.
//
// Witnesses are ordered lexicographically by candidate index in input record
// order. Truncated is true only when a further witness was actually proved to
// exist beyond the reported limit.
type Result struct {
	Status              Status
	Candidates          [][]int
	CandidateStates     [][]CandidateState
	ExcludedCandidates  [][]int
	Determined          map[int]byte
	DeterminedBytes     []KeyByte
	Witnesses           [][]int
	Truncated           bool
	Conflict            *Conflict
	SearchNodes         uint64
	BudgetExhausted     bool
	ProvenUnsatisfiable bool
}

// Recover searches the joint candidate space.
func Recover(records []Record, options ...Options) (Result, error) {
	opts := DefaultOptions()
	for _, option := range options {
		if option.MaxSearchNodes != 0 {
			opts.MaxSearchNodes = option.MaxSearchNodes
		}
		if option.WitnessLimit != 0 {
			opts.WitnessLimit = option.WitnessLimit
		}
		if option.SkipConflictAnalysis {
			opts.SkipConflictAnalysis = true
		}
	}
	return recoverWithOptions(records, opts)
}

// Solve is an alias for Recover.
func Solve(records []Record, options ...Options) (Result, error) {
	return Recover(records, options...)
}

// RecoverWithBudget runs Recover with an explicit node budget.
func RecoverWithBudget(records []Record, maxSearchNodes uint64) (Result, error) {
	opts := DefaultOptions()
	opts.MaxSearchNodes = maxSearchNodes
	return recoverWithOptions(records, opts)
}

func normalizeOptions(opts Options) (Options, error) {
	defaults := DefaultOptions()
	if opts.MaxSearchNodes == 0 {
		opts.MaxSearchNodes = defaults.MaxSearchNodes
	}
	if opts.WitnessLimit == 0 {
		opts.WitnessLimit = defaults.WitnessLimit
	}
	if opts.WitnessLimit < 0 {
		return Options{}, fmt.Errorf("netsec: witness limit must not be negative")
	}
	if opts.WitnessLimit > MaxWitnesses {
		opts.WitnessLimit = MaxWitnesses
	}
	return opts, nil
}

func sortedRecordOrder(records []Record) []int {
	order := make([]int, len(records))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if records[ia].ID != records[ib].ID {
			return records[ia].ID < records[ib].ID
		}
		return ia < ib
	})
	return order
}
