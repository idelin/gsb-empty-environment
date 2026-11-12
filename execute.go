package netsec

import (
	"fmt"
)

// EventKind identifies an observable event in the concrete execution model.
type EventKind string

const (
	// EventAddress is a table lookup address.
	EventAddress EventKind = "address"
	// EventBranch is a control-flow condition.
	EventBranch EventKind = "branch"
	// EventCallTiming is one summarized call's timing-relevant parameter bits.
	EventCallTiming EventKind = "call-timing"
	// EventCallAddress is one summarized call's address-relevant parameter bits.
	EventCallAddress EventKind = "call-address"
)

// Event is one observable operation.  Differing events are the differential
// execution counterpart of a static finding.  This is a software model only:
// it does not measure real hardware timing.
type Event struct {
	Kind    EventKind
	Node    string
	Library string
	Param   int
	Value   uint64
	Values  []uint64
}

func (e Event) String() string {
	switch e.Kind {
	case EventAddress:
		return fmt.Sprintf("address(%s)=%#x", e.Node, e.Value)
	case EventBranch:
		return fmt.Sprintf("branch(%s)=%#x", e.Node, e.Value)
	case EventCallTiming, EventCallAddress:
		return fmt.Sprintf("%s(%s, %s)=%v", e.Kind, e.Node, e.Library, e.Values)
	default:
		return fmt.Sprintf("%s(%s)=%#x", e.Kind, e.Node, e.Value)
	}
}

// Observation is the deterministic event trace of one concrete execution.
type Observation struct {
	Events []Event
	Values map[string]uint64
}

// Equal reports whether two executions have the same observable events.
func (o Observation) Equal(other Observation) bool {
	if len(o.Events) != len(other.Events) {
		return false
	}
	for i := range o.Events {
		if !eventsEqual(o.Events[i], other.Events[i]) {
			return false
		}
	}
	return true
}

// DifferentEventIndices returns the positions at which traces differ.
func (o Observation) DifferentEventIndices(other Observation) []int {
	var indices []int
	limit := len(o.Events)
	if len(other.Events) < limit {
		limit = len(other.Events)
	}
	for i := 0; i < limit; i++ {
		if !eventsEqual(o.Events[i], other.Events[i]) {
			indices = append(indices, i)
		}
	}
	maximum := len(o.Events)
	if len(other.Events) > maximum {
		maximum = len(other.Events)
	}
	for i := limit; i < maximum; i++ {
		indices = append(indices, i)
	}
	return indices
}

func eventsEqual(a, b Event) bool {
	if a.Kind != b.Kind || a.Node != b.Node || a.Library != b.Library || a.Param != b.Param || a.Value != b.Value {
		return false
	}
	if len(a.Values) != len(b.Values) {
		return false
	}
	for i := range a.Values {
		if a.Values[i] != b.Values[i] {
			return false
		}
	}
	return true
}

// Execute runs the concrete model for one assignment.  Inputs are keyed by
// the names of secret/public input nodes.
func Execute(program *Program, libraries Libraries, inputs map[string]uint64) (*Observation, error) {
	built, err := program.Build()
	if err != nil {
		return nil, err
	}
	check := &analysis{program: built, libraries: libraries}
	if err := check.validate(); err != nil {
		return nil, err
	}

	values := make([]uint64, len(built.Nodes))
	widths := make([]int, len(built.Nodes))
	observation := &Observation{Values: make(map[string]uint64)}
	for i := range built.Nodes {
		node := built.Nodes[i]
		switch node.Op {
		case OpSecretInput, OpPublicInput:
			input, ok := inputs[node.Name]
			if !ok {
				return nil, fmt.Errorf("missing input %q", node.Name)
			}
			values[i] = input & uint64(widthMask(node.Width))
			observation.Values[node.Name] = values[i]
		case OpConst:
			values[i] = node.Value & uint64(widthMask(node.Width))
			observation.Values[node.Name] = values[i]
		case OpAnd:
			values[i] = (values[node.Args[0]] & values[node.Args[1]]) & uint64(widthMask(node.Width))
		case OpOr:
			values[i] = (values[node.Args[0]] | values[node.Args[1]]) & uint64(widthMask(node.Width))
		case OpXor:
			values[i] = (values[node.Args[0]] ^ values[node.Args[1]]) & uint64(widthMask(node.Width))
		case OpNot:
			values[i] = ^values[node.Args[0]] & uint64(widthMask(node.Width))
		case OpShl:
			values[i] = (values[node.Args[0]] << node.Shift) & uint64(widthMask(node.Width))
		case OpShr:
			values[i] = (values[node.Args[0]] >> node.Shift) & uint64(widthMask(node.Width))
		case OpAdd:
			values[i] = (values[node.Args[0]] + values[node.Args[1]]) & uint64(widthMask(node.Width))
		case OpSub:
			values[i] = (values[node.Args[0]] - values[node.Args[1]]) & uint64(widthMask(node.Width))
		case OpMul:
			values[i] = (values[node.Args[0]] * values[node.Args[1]]) & uint64(widthMask(node.Width))
		case OpSelect:
			if values[node.Args[0]] != 0 {
				values[i] = values[node.Args[1]]
			} else {
				values[i] = values[node.Args[2]]
			}
			values[i] &= uint64(widthMask(node.Width))
		case OpPublicMask:
			values[i] = values[node.Args[0]] & uint64(node.Public) & uint64(widthMask(node.Width))
		case OpLookup:
			index := values[node.Args[len(node.Args)-1]]
			observation.Events = append(observation.Events, Event{
				Kind:  EventAddress,
				Node:  node.Name,
				Value: index,
			})
			result := mix64(index)
			if len(node.Args) == 2 {
				result ^= values[node.Args[0]]
			}
			values[i] = result & uint64(widthMask(node.Width))
		case OpBranch:
			observation.Events = append(observation.Events, Event{
				Kind:  EventBranch,
				Node:  node.Name,
				Value: values[node.Args[0]],
			})
			continue
		case OpCall:
			summary, err := libraries.lookup(node.Call.Library)
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", node.Name, err)
			}
			args := make([]uint64, len(node.Args))
			var timingValues, addressValues []uint64
			hasTiming, hasAddress := false, false
			for param, arg := range node.Args {
				args[param] = values[arg]
				info := summary.Params[param]
				if info.TimingBits != 0 {
					hasTiming = true
				}
				timingValues = append(timingValues, args[param]&uint64(info.TimingBits))
				if info.AddressBits != 0 {
					hasAddress = true
				}
				addressValues = append(addressValues, args[param]&uint64(info.AddressBits))
			}
			if hasTiming {
				observation.Events = append(observation.Events, Event{
					Kind:    EventCallTiming,
					Node:    node.Name,
					Library: summary.Name,
					Values:  timingValues,
				})
			}
			if hasAddress {
				observation.Events = append(observation.Events, Event{
					Kind:    EventCallAddress,
					Node:    node.Name,
					Library: summary.Name,
					Values:  addressValues,
				})
			}
			if summary.Model != nil {
				values[i] = summary.Model(args)
			} else {
				values[i] = mix64(xorWords(args))
			}
			resultWidth := summary.ResultWidth
			if resultWidth == 0 {
				resultWidth = node.Width
			}
			if resultWidth == 0 {
				for _, arg := range node.Args {
					if widths[arg] > resultWidth {
						resultWidth = widths[arg]
					}
				}
			}
			if resultWidth > 0 {
				values[i] &= uint64(widthMask(resultWidth))
			}
			widths[i] = resultWidth
		default:
			return nil, fmt.Errorf("node %q: unsupported op %s", node.Name, node.Op)
		}
		if node.Op != OpCall && node.Op != OpBranch {
			widths[i] = node.Width
		}
		observation.Values[node.Name] = values[i]
	}
	return observation, nil
}

// Diff describes a differential execution of two assignments that differ in
// one secret bit.
type Diff struct {
	Secret          string
	Bit             int
	Before          *Observation
	After           *Observation
	Leaked          bool
	DifferingEvents []int
}

// Differential flips one secret input bit and compares the two event traces.
func Differential(program *Program, libraries Libraries, inputs map[string]uint64, secret string, bit int) (Diff, error) {
	built, err := program.Build()
	if err != nil {
		return Diff{}, err
	}
	node, ok := built.Node(secret)
	if !ok {
		return Diff{}, fmt.Errorf("unknown input %q", secret)
	}
	if node.Op != OpSecretInput {
		return Diff{}, fmt.Errorf("input %q is not secret", secret)
	}
	if bit < 0 || bit >= node.Width {
		return Diff{}, fmt.Errorf("bit %d outside secret input %q width %d", bit, secret, node.Width)
	}
	base, ok := inputs[secret]
	if !ok {
		return Diff{}, fmt.Errorf("missing input %q", secret)
	}

	before, err := Execute(built, libraries, inputs)
	if err != nil {
		return Diff{}, err
	}
	flippedInputs := make(map[string]uint64, len(inputs))
	for name, input := range inputs {
		flippedInputs[name] = input
	}
	flippedInputs[secret] = base ^ uint64(Bit(bit))
	after, err := Execute(built, libraries, flippedInputs)
	if err != nil {
		return Diff{}, err
	}

	differing := before.DifferentEventIndices(*after)
	return Diff{
		Secret:          secret,
		Bit:             bit,
		Before:          before,
		After:           after,
		Leaked:          len(differing) > 0,
		DifferingEvents: differing,
	}, nil
}

// DifferentialAll flips every bit of the named secret input.  The returned
// slice is indexed by bit position.
func DifferentialAll(program *Program, libraries Libraries, inputs map[string]uint64, secret string) ([]Diff, error) {
	built, err := program.Build()
	if err != nil {
		return nil, err
	}
	node, ok := built.Node(secret)
	if !ok {
		return nil, fmt.Errorf("unknown input %q", secret)
	}
	if node.Op != OpSecretInput {
		return nil, fmt.Errorf("input %q is not secret", secret)
	}
	diffs := make([]Diff, node.Width)
	for bit := 0; bit < node.Width; bit++ {
		diff, err := Differential(built, libraries, inputs, secret, bit)
		if err != nil {
			return nil, err
		}
		diffs[bit] = diff
	}
	return diffs, nil
}

func xorWords(values []uint64) uint64 {
	var result uint64
	for _, v := range values {
		result ^= v
	}
	return result
}

func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
