package netsec

// FunctionID identifies a function in the host's trusted function table.
type FunctionID uint32

// TypeID identifies a language-specific exception type. The host, not the
// module, gives these IDs their meaning through the function type table.
type TypeID uint32

// PersonalityID names a personality routine in the host whitelist. It is an
// identifier only: exception metadata never contains a function pointer.
type PersonalityID string

// ModuleDigest binds a validated exception table to the exact immutable code
// bytes and trusted decoder output from which it was built.
type ModuleDigest [32]byte

// NoAction is the terminal action-chain sentinel. Action table indexes are
// zero based; -1 therefore cannot be a valid node index.
const NoAction = -1

// Instruction is one interval produced by the host's trusted decoder. The
// decoder marks instructions that are legal landing-pad targets.
type Instruction struct {
	Start      uint64 `json:"start"`
	End        uint64 `json:"end"`
	LandingPad bool   `json:"landingPad"`
}

// Function is one function interval from the trusted decoder. TypeIDs is the
// function's type table used to resolve action-chain type filters.
//
// EntryPrefixEnd optionally marks the exclusive end of the function entry
// prefix. Landing pads below that address are rejected. Zero means that only
// the function entry address itself is forbidden.
type Function struct {
	ID             FunctionID `json:"id"`
	Start          uint64     `json:"start"`
	End            uint64     `json:"end"`
	TypeIDs        []TypeID   `json:"typeIds"`
	EntryPrefixEnd uint64     `json:"entryPrefixEnd,omitempty"`
}

// ActionNode is one node of the untrusted exception action chain.
//
// Next is either NoAction or an index strictly greater than this node's index.
// The monotonic requirement makes cycles structurally impossible.
type ActionNode struct {
	TypeID  TypeID `json:"typeId"`
	Next    int    `json:"next"`
	Cleanup bool   `json:"cleanup"`
}

// ExceptionEntry is one untrusted protected-range entry. Start and End are a
// half-open protected interval [Start, End). All addresses are relative to the
// start of the module.
type ExceptionEntry struct {
	FunctionID    FunctionID    `json:"functionId"`
	Start         uint64        `json:"start"`
	End           uint64        `json:"end"`
	LandingPad    uint64        `json:"landingPad"`
	ActionStart   int           `json:"actionStart"`
	PersonalityID PersonalityID `json:"personalityId"`
}

// Metadata is the complete untrusted exception-table payload.
type Metadata struct {
	Entries []ExceptionEntry `json:"entries"`
	Actions []ActionNode     `json:"actions"`
}

// Module is the host's immutable view of a module before validation. Compile
// snapshots these slices; later mutation by the caller cannot alter a
// published table.
type Module struct {
	Code         []byte        `json:"-"`
	Instructions []Instruction `json:"instructions"`
	Functions    []Function    `json:"functions"`
}

// Limits bounds work performed on untrusted metadata. Zero fields select
// defaults. Negative fields are rejected during compilation.
type Limits struct {
	// MaxActionChain is the maximum number of action nodes reachable from one
	// entry's ActionStart.
	MaxActionChain int
	// MaxEntries is the maximum number of exception entries.
	MaxEntries int
	// MaxActionNodes is the maximum total number of action-table nodes.
	MaxActionNodes int
}

// Default limits are deliberately small for module metadata.
const (
	DefaultMaxActionChain = 64
	DefaultMaxEntries     = 4096
	DefaultMaxActionNodes = 65536
)

func (l Limits) normalize() (Limits, error) {
	if l.MaxActionChain < 0 || l.MaxEntries < 0 || l.MaxActionNodes < 0 {
		return Limits{}, &ValidationError{
			Kind:   ErrLimits,
			Detail: "limits must not be negative",
		}
	}
	if l.MaxActionChain == 0 {
		l.MaxActionChain = DefaultMaxActionChain
	}
	if l.MaxEntries == 0 {
		l.MaxEntries = DefaultMaxEntries
	}
	if l.MaxActionNodes == 0 {
		l.MaxActionNodes = DefaultMaxActionNodes
	}
	return l, nil
}

// ActionEvidence is one node of an action chain in traversal order.
type ActionEvidence struct {
	Index   int    `json:"index"`
	TypeID  TypeID `json:"typeId"`
	Cleanup bool   `json:"cleanup"`
}

// Resolution is the result of a runtime throw query.
//
// When Unwind is true no handler may be entered: LandingPad is zero and
// Actions is empty. Otherwise LandingPad names a validated landing-pad
// instruction start and Actions is a copy of the complete action chain,
// including the matching node at MatchedAction.
type Resolution struct {
	Unwind         bool
	ThrowingPC     uint64
	TypeID         TypeID
	LandingPad     uint64
	FunctionID     FunctionID
	EntryIndex     int
	ProtectedStart uint64
	ProtectedEnd   uint64
	Personality    Personality
	ActionStart    int
	Actions        []ActionEvidence
	MatchedAction  int
}
