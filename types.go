// Package controlflow recovers an intra-binary control-flow graph from a
// stream of bytes whose instruction boundaries are not trusted.
//
// The package never decodes bytes itself: callers inject a Decoder, which
// reports the length, the successors and the terminator status of the
// instruction starting at a given address. Recovery expands from a trusted
// set of entry addresses and keeps every competing byte interpretation.
// When two decoded instruction intervals overlap partially (for example when
// a jump lands in the middle of another instruction), both interpretations
// and the source paths that reach them are retained and the overlap is
// reported as a Conflict. A later interpretation never overwrites an earlier
// one.
package controlflow

// EdgeKind classifies a successor relation between two decoded instructions.
type EdgeKind int

const (
	// EdgeFallthrough is the implicit successor at Address+Length taken when
	// the instruction does not terminate the flow.
	EdgeFallthrough EdgeKind = iota + 1
	// EdgeDirect is a statically resolved branch or call target.
	EdgeDirect
	// EdgeIndirect is a branch whose target was resolved by the decoder to a
	// concrete address (for example via a jump table).
	EdgeIndirect
)

func (k EdgeKind) String() string {
	switch k {
	case EdgeFallthrough:
		return "fallthrough"
	case EdgeDirect:
		return "direct"
	case EdgeIndirect:
		return "indirect"
	default:
		return "unknown"
	}
}

// Edge is one successor relation of a decoded instruction.
type Edge struct {
	// From is the start address of the source instruction.
	From uint64
	// To is the resolved destination address.
	To uint64
	// Kind classifies how the relation was derived.
	Kind EdgeKind
}

// Target asks the engine to enqueue a successor address.
type Target struct {
	// Address is the destination instruction start address.
	Address uint64
	// Kind classifies the successor; use EdgeIndirect for targets resolved
	// from an indirect control-flow construct.
	Kind EdgeKind
}

// Result is what an injected Decoder returns for one address.
type Result struct {
	// Length is the number of bytes occupied by the instruction. It must be
	// positive; a zero value makes the engine record an ErrZeroLength error.
	Length int
	// Successors enumerates every resolved successor. A terminating
	// instruction with a fall-through must list it explicitly.
	Successors []Target
	// Terminates marks instructions that never fall through (ret,
	// unconditional trap, ...). When it is false the engine synthesises a
	// fall-through successor at Address+Length.
	Terminates bool
	// UnknownIndirect reports an indirect transfer whose concrete target
	// cannot be resolved. Each distinct site is listed in the report instead
	// of being followed.
	UnknownIndirect bool
	// Err lets the decoder reject a decode (for example a short read or an
	// illegal encoding). The address is still recorded as an Error node so
	// the failure cannot silently truncate the graph.
	Err error
}

// Decoder is the injected decoding strategy. Implementations must be safe to
// call with any address the engine reaches, including addresses that lie
// inside another instruction.
type Decoder interface {
	Decode(addr uint64) Result
}

// DecoderFunc adapts a plain function to the Decoder interface.
type DecoderFunc func(addr uint64) Result

// Decode implements Decoder.
func (f DecoderFunc) Decode(addr uint64) Result { return f(addr) }

// Conflict describes two decoded instructions whose byte ranges overlap
// without being identical. Same-start, same-length decodes of the same bytes
// are merges, not conflicts.
type Conflict struct {
	// First is the interval that was decoded first; it is never overwritten.
	First Interval
	// Second is the interval that arrived later and is kept separately.
	Second Interval
	// Shared is the half-open byte range covered by both interpretations.
	Shared [2]uint64
}

// Interval is the half-open byte range [Start, End) of one interpretation.
type Interval struct {
	Start uint64
	End   uint64
}

// Node is one instruction interpretation at one start address.
type Node struct {
	// Address is the instruction start address.
	Address uint64
	// Length is the decoder-reported size in bytes.
	Length int
	// End is Address+Length and the start of the implicit fall-through.
	End uint64
	// Terminates mirrors the decoder result.
	Terminates bool
	// EntryRoots are the trusted entry addresses from which this node is
	// reachable.
	EntryRoots []uint64
	// Incoming are the edges that first reach this interpretation, in
	// discovery order. Competing interpretations of overlapping bytes keep
	// their own incoming lists.
	Incoming []Edge
	// Paths are deduplicated chains of addresses (entry first, node last)
	// reaching this interpretation. Retained for every node and capped by
	// Engine.MaxPathsPerNode.
	Paths [][]uint64
	// PathsTruncated is set when more distinct paths existed than the cap.
	PathsTruncated bool
	// Conflicts references every later overlap involving this node.
	Conflicts []Conflict
	// Err holds the decoder failure for an Error node.
	Err error
}

// IsError reports whether the node records a decoder failure.
func (n *Node) IsError() bool { return n != nil && n.Err != nil }

// UnknownIndirectSite is one unresolved indirect control-flow transfer.
type UnknownIndirectSite struct {
	// At is the address of the instruction containing the transfer.
	At uint64
	// Path is one source path reaching the site.
	Path []uint64
}

// Report is the complete recovery output.
type Report struct {
	// Nodes holds every distinct interpretation in first-discovery order.
	Nodes []*Node
	// Edges is the deduplicated successor relation set, discovery ordered.
	Edges []Edge
	// BackEdges are edges whose destination already occurs on the source
	// path, i.e. loop back-edges under reachability from the trusted roots.
	BackEdges []Edge
	// Conflicts are partial interval overlaps, discovery ordered.
	Conflicts []Conflict
	// UnknownIndirect lists every unresolved indirect transfer site.
	UnknownIndirect []UnknownIndirectSite
	// UnmappedTargets are resolved successors that fall outside [Base, Base+
	// Size) and therefore cannot be decoded.
	UnmappedTargets []Edge
	// Errors are decoder failures keyed by address, discovery ordered.
	Errors []*Node
	// Decoded is the number of Decode calls actually performed.
	Decoded int
	// BudgetExceeded is set once the instruction budget is exhausted.
	BudgetExceeded bool
	// BudgetLimit is the configured cap.
	BudgetLimit int
}

// NodeAt returns the interpretation decoded at addr, or nil.
func (r *Report) NodeAt(addr uint64) *Node {
	for _, n := range r.Nodes {
		if n.Address == addr {
			return n
		}
	}
	return nil
}

// Engine configures and runs one recovery pass.
type Engine struct {
	// Decoder is the injected instruction decoder (required).
	Decoder Decoder
	// Base is the lowest decodable address.
	Base uint64
	// Size is the decodable span; addresses outside [Base, Base+Size) are
	// reported as unmapped rather than decoded.
	Size uint64
	// MaxInstructions bounds the number of Decode calls. Zero selects a
	// conservative default.
	MaxInstructions int
	// MaxPathsPerNode caps the retained source paths per interpretation.
	// Zero selects a conservative default.
	MaxPathsPerNode int
}
