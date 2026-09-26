package majorityregister

import (
	"errors"
	"strconv"
)

var (
	// ErrEmptyLabel is returned when an operation does not have a unique label.
	ErrEmptyLabel = errors.New("operation label must not be empty")
	// ErrDuplicateLabel is returned when an operation id is already in use.
	ErrDuplicateLabel = errors.New("duplicate operation label")
	// ErrInvalidNode is returned when a node id is outside the cluster.
	ErrInvalidNode = errors.New("invalid node id")
)

// Tag is the version part of a register value.
//
// Tags are ordered first by Version, then by the unique Label. Labels are
// globally unique, so tags form a total order even when concurrent writes
// choose the same Version.
type Tag struct {
	Version uint64
	Label   string
}

func (t Tag) String() string {
	return "v" + strconv.FormatUint(t.Version, 10) + ":" + t.Label
}

// Compare returns -1, 0, or 1 when t is lower than, equal to, or higher than
// other.
func (t Tag) Compare(other Tag) int {
	if t.Version < other.Version {
		return -1
	}
	if t.Version > other.Version {
		return 1
	}
	if t.Label < other.Label {
		return -1
	}
	if t.Label > other.Label {
		return 1
	}
	return 0
}

// ReplicaState is the durable state of one replicated register copy.
type ReplicaState struct {
	Tag   Tag
	Value []byte
}

type stableStore struct {
	tag   Tag
	value []byte
}

func newStableStore() *stableStore {
	return &stableStore{}
}

func (s *stableStore) state() ReplicaState {
	value := make([]byte, len(s.value))
	copy(value, s.value)
	return ReplicaState{Tag: s.tag, Value: value}
}

func (s *stableStore) write(tag Tag, value []byte) {
	if s.tag.Compare(tag) >= 0 {
		return
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	s.tag = tag
	s.value = clone
}

// Replica is one register replica. Down replicas do not process messages;
// their stableStore still represents durable disk state across a restart.
type Replica struct {
	id      int
	stable  *stableStore
	running bool
}

func newReplica(id int) *Replica {
	return &Replica{id: id, stable: newStableStore(), running: true}
}

// ID returns the replica id.
func (r *Replica) ID() int {
	return r.id
}

// State returns a defensive copy of the replica's durable state.
func (r *Replica) State() ReplicaState {
	return r.stable.state()
}

func (r *Replica) crash() {
	r.running = false
}

func (r *Replica) restart() {
	r.running = true
}

// RequestKind identifies a client-to-replica phase message.
type RequestKind int

const (
	QueryRequest RequestKind = iota
	WriteRequest
)

// Request is a client-to-replica phase message.
type Request struct {
	Kind  RequestKind
	Round uint64
	OpID  string
	Tag   Tag
	Value []byte
}

// Reply is a replica-to-client response.
type Reply struct {
	Node     int
	Round    uint64
	OpID     string
	Tag      Tag
	Value    []byte
	Accepted bool
}

func (r *Replica) handle(req Request) (Reply, bool) {
	if !r.running {
		return Reply{}, false
	}

	reply := Reply{
		Node:  r.id,
		Round: req.Round,
		OpID:  req.OpID,
		Tag:   r.stable.tag,
		Value: append([]byte(nil), r.stable.value...),
	}

	if req.Kind == WriteRequest {
		if r.stable.tag.Compare(req.Tag) <= 0 {
			r.stable.write(req.Tag, req.Value)
			reply.Accepted = true
		}
		reply.Tag = r.stable.tag
		reply.Value = append([]byte(nil), r.stable.value...)
	}
	return reply, true
}
