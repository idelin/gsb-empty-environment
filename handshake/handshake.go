// Package handshake reconstructs the minimum-change legal ordering of a set of
// captured protocol messages whose observed sequence may contain measurement
// errors.
//
// A legal handshake explanation must preserve three kinds of causality:
//
//   - a send message is ordered before the receive message that matches it;
//   - messages observed on the same endpoint keep their relative local order;
//   - messages belonging to the same transaction keep their observed order.
//
// Every captured message participates in the solution: a message that cannot be
// placed legally is never silently dropped. The solver returns a permutation
// whose minimum number of adjacent swaps away from the observed sequence is
// smallest; ties are broken by the lexicographic order of the message IDs.
// When the constraints contain a directed cycle no legal permutation exists and
// the conflicting edge ring is returned instead.
package handshake

import "fmt"

// MaxMessages is the largest number of messages a single solve call accepts.
// The solver uses an O(n^2 2^n) bitmask dynamic program, which is practical up
// to this bound.
const MaxMessages = 18

// Kind classifies a captured message as a send or a receive.
type Kind int

const (
	// KindSend marks a message that transmits data.
	KindSend Kind = iota + 1
	// KindReceive marks a message that consumes a matching send.
	KindReceive
)

// EdgeReason states why a precedence edge was introduced.
type EdgeReason string

const (
	// EdgeSendReceive links a receive to the send that must precede it.
	EdgeSendReceive EdgeReason = "send-before-receive"
	// EdgeLocalOrder links consecutive messages observed on one endpoint.
	EdgeLocalOrder EdgeReason = "local-order"
	// EdgeTransaction links consecutive messages that share a transaction.
	EdgeTransaction EdgeReason = "transaction"
	// EdgeCustom marks an edge supplied directly to SolveWithEdges.
	EdgeCustom EdgeReason = "custom"
)

// Message is one captured protocol record, given in observed capture order.
type Message struct {
	// ID uniquely identifies the message within the call and is used to break
	// ties between equally cheap permutations.
	ID string
	// Kind is KindSend or KindReceive.
	Kind Kind
	// Endpoint identifies the local party that observed the message. Messages
	// sharing an endpoint keep their relative order. An empty endpoint means
	// the message takes part in no local-order chain.
	Endpoint string
	// Transaction groups messages of one handshake transaction. Messages
	// sharing a non-empty transaction keep their observed relative order.
	Transaction string
	// SendID is set on KindReceive messages and references the ID of the
	// matching KindSend message. It must be empty on KindSend messages.
	SendID string
}

// Edge imposes that From be ordered before To.
type Edge struct {
	From   string
	To     string
	Reason EdgeReason
}

// Result is the minimum-change legal permutation of the input messages.
type Result struct {
	// Order lists every input message ID exactly once in a legal order.
	Order []string
	// Swaps is the minimum number of adjacent swaps needed to turn the
	// observed sequence into Order. It equals the inversion count of Order
	// against the observed positions.
	Swaps int
}

// CycleError reports that the precedence edges contain a directed cycle, so no
// legal permutation exists without deleting messages (which is not allowed).
type CycleError struct {
	// Ring lists the message IDs on the conflict cycle. The first ID is
	// repeated as the last element to close the ring visually.
	Ring []string
	// Edges lists the precedence edges along Ring, in ring order. Edges[i]
	// goes from Ring[i] to Ring[i+1].
	Edges []Edge
}

// Error renders the conflicting ring as a chain of message IDs.
func (e *CycleError) Error() string {
	return fmt.Sprintf("handshake: conflicting precedence cycle: %v", e.Ring)
}

// Solve builds the causality edges implied by the messages and returns the
// minimum-change legal permutation. Messages are taken in observed capture
// order and all of them appear in the result. A *CycleError is returned when
// the implied constraints are mutually inconsistent.
func Solve(messages []Message) (*Result, error) {
	if len(messages) > MaxMessages {
		return nil, fmt.Errorf("handshake: %d messages exceed maximum of %d", len(messages), MaxMessages)
	}

	index := make(map[string]int, len(messages))
	for i, msg := range messages {
		if msg.ID == "" {
			return nil, fmt.Errorf("handshake: message at index %d has an empty ID", i)
		}
		if _, dup := index[msg.ID]; dup {
			return nil, fmt.Errorf("handshake: duplicate message ID %q", msg.ID)
		}
		switch msg.Kind {
		case KindSend:
			if msg.SendID != "" {
				return nil, fmt.Errorf("handshake: send message %q must not set SendID", msg.ID)
			}
		case KindReceive:
			if msg.SendID == "" {
				return nil, fmt.Errorf("handshake: receive message %q must reference a SendID", msg.ID)
			}
		default:
			return nil, fmt.Errorf("handshake: message %q has unknown kind %d", msg.ID, msg.Kind)
		}
		index[msg.ID] = i
	}

	for _, msg := range messages {
		if msg.Kind != KindReceive {
			continue
		}
		sendIndex, ok := index[msg.SendID]
		if !ok {
			return nil, fmt.Errorf("handshake: receive message %q references unknown send %q", msg.ID, msg.SendID)
		}
		if messages[sendIndex].Kind != KindSend {
			return nil, fmt.Errorf("handshake: receive message %q references %q which is not a send", msg.ID, msg.SendID)
		}
	}

	type edgeKey struct{ from, to int }
	reasons := make(map[edgeKey]EdgeReason)
	addEdge := func(from, to int, reason EdgeReason) {
		if from == to {
			return
		}
		key := edgeKey{from, to}
		if _, exists := reasons[key]; !exists {
			reasons[key] = reason
		}
	}

	localGroups := make(map[string][]int)
	transactionGroups := make(map[string][]int)
	for i, msg := range messages {
		if msg.Endpoint != "" {
			localGroups[msg.Endpoint] = append(localGroups[msg.Endpoint], i)
		}
		if msg.Transaction != "" {
			transactionGroups[msg.Transaction] = append(transactionGroups[msg.Transaction], i)
		}
		if msg.Kind == KindReceive {
			addEdge(index[msg.SendID], i, EdgeSendReceive)
		}
	}
	for _, group := range localGroups {
		for i := 1; i < len(group); i++ {
			addEdge(group[i-1], group[i], EdgeLocalOrder)
		}
	}
	for _, group := range transactionGroups {
		for i := 1; i < len(group); i++ {
			addEdge(group[i-1], group[i], EdgeTransaction)
		}
	}

	edges := make([]Edge, 0, len(reasons))
	for key, reason := range reasons {
		edges = append(edges, Edge{
			From:   messages[key.from].ID,
			To:     messages[key.to].ID,
			Reason: reason,
		})
	}

	return SolveWithEdges(messages, edges)
}
