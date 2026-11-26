// Package reshard searches the exact-minimum-communication sequence of
// collective primitives that transforms one tensor sharding layout into
// another over a multi-dimensional device mesh.
//
// The communication volume of every primitive is a rational number of the
// form (M_a-1)/M_a * bytes * w_a (twice that for AllReduce). All costs are
// compared as scaled integers: every weight is multiplied by lcm(M_1,...,M_R)
// once, turning every primitive cost into an exact integer and removing all
// floating-point arithmetic from the search.
package reshard

import "math/big"

// OpType identifies a single-step communication primitive.
//
// The numeric values double as the canonical dictionary order of primitive
// names, so AllGather < AllReduce < AllToAll < LocalSlice < ReduceScatter.
type OpType int

const (
	// AllGather removes the sharding of one tensor dimension along mesh axis A.
	AllGather OpType = iota
	// AllReduce clears the Partial state of mesh axis A while keeping full
	// replication of every tensor dimension on that axis.
	AllReduce
	// AllToAll changes which tensor dimension is sharded along mesh axis A:
	// dimension D1 -> dimension D2.
	AllToAll
	// LocalSlice introduces sharding of a replicated tensor dimension along
	// mesh axis A without any communication.
	LocalSlice
	// ReduceScatter clears the Partial state of axis A and simultaneously
	// shards a replicated dimension along A.
	ReduceScatter
)

// String returns the canonical primitive name.
func (t OpType) String() string {
	switch t {
	case AllGather:
		return "AllGather"
	case AllReduce:
		return "AllReduce"
	case AllToAll:
		return "AllToAll"
	case LocalSlice:
		return "LocalSlice"
	case ReduceScatter:
		return "ReduceScatter"
	default:
		return "Unknown"
	}
}

// Op is one applied primitive.
//
// Field usage per primitive:
//
//	AllGather:     A, D1
//	AllReduce:     A
//	AllToAll:      A, D1, D2
//	LocalSlice:    A, D2
//	ReduceScatter: A, D2
//
// Unused fields are zero.
type Op struct {
	Type OpType
	A    int // source mesh axis (communication axis)
	B    int // destination mesh axis (AllToAll only)
	D1   int // source tensor dimension
	D2   int // destination tensor dimension
}

// String renders the primitive with the exact axis/dimension arguments.
func (o Op) String() string {
	switch o.Type {
	case AllGather:
		return "AllGather(a=" + itoa(o.A) + ",d=" + itoa(o.D1) + ")"
	case AllReduce:
		return "AllReduce(a=" + itoa(o.A) + ")"
	case AllToAll:
		return "AllToAll(a=" + itoa(o.A) + ",b=" + itoa(o.B) + ",d1=" + itoa(o.D1) + ",d2=" + itoa(o.D2) + ")"
	case LocalSlice:
		return "LocalSlice(a=" + itoa(o.A) + ",d2=" + itoa(o.D2) + ")"
	case ReduceScatter:
		return "ReduceScatter(a=" + itoa(o.A) + ",d2=" + itoa(o.D2) + ")"
	default:
		return "Unknown"
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [24]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

// Mesh describes the device grid. Ranks is the per-axis device count and
// Weights the per-byte bandwidth weight of each axis; the two slices have
// equal length, which must be 2 or 3.
type Mesh struct {
	Ranks   []int64
	Weights []int64
}

// Layout describes how a tensor is held by every device.
//
// Shard[d] is the mesh axis sharding tensor dimension d, or -1 when the
// dimension is replicated on every axis. Partial is the set of mesh axes on
// which every shard holds a local, not-yet-summed reduction contribution.
type Layout struct {
	Shard   []int
	Partial []bool
}

// Problem is one resharding request. BytesPerElement is the element size.
type Problem struct {
	Shape           []int64
	BytesPerElement int64
	Mesh            Mesh
	Source          Layout
	Target          Layout
}

// Solution is the exact optimal primitive sequence.
//
// ScaledCost is the exact total cost after multiplying every primitive cost
// by the mesh axis-size LCM; Cost holds the same value as an exact rational.
type Solution struct {
	Ops        []Op
	Steps      int
	ScaledCost *big.Int
	Cost       *big.Rat
	Scale      int64
}
