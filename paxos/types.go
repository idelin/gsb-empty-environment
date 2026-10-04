package paxos

// Ballot is a Paxos proposal number: (N, ID) ordered lexicographically.
type Ballot struct {
	N  uint64
	ID uint32
}

// Compare returns -1, 0 or 1.
func (b Ballot) Compare(o Ballot) int {
	switch {
	case b.N != o.N:
		if b.N < o.N {
			return -1
		}
		return 1
	case b.ID != o.ID:
		if b.ID < o.ID {
			return -1
		}
		return 1
	}
	return 0
}

func (b Ballot) IsZero() bool { return b.N == 0 && b.ID == 0 }

func entryBallot(e Entry) Ballot { return Ballot{N: e.Term, ID: e.Node} }

func entryBallotLess(a, b Entry) bool {
	return entryBallot(a).Compare(entryBallot(b)) < 0
}

// MsgType enumerates the protocol messages.
type MsgType uint8

const (
	MsgPrepare  MsgType = iota + 1 // Phase 1a
	MsgPromise                     // Phase 1b
	MsgAccept                      // Phase 2a
	MsgAccepted                    // Phase 2b
	MsgCommit                      // commit notification / gap fill
	MsgFetch                       // follower requests missing committed entries
)

func (t MsgType) String() string {
	switch t {
	case MsgPrepare:
		return "Prepare"
	case MsgPromise:
		return "Promise"
	case MsgAccept:
		return "Accept"
	case MsgAccepted:
		return "Accepted"
	case MsgCommit:
		return "Commit"
	case MsgFetch:
		return "Fetch"
	}
	return "Unknown"
}

// Msg is the single wire message type exchanged between nodes.
type Msg struct {
	From, To    int
	Type        MsgType
	Ballot      Ballot
	Entries     []Entry  // Promise: acceptor log; Accept: to accept; Commit: committed
	Slots       []Ballot // Commit: fingerprint (ballot) of every slot 1..CommitIndex
	CommitIndex uint64
	OK          bool
}

// Transport delivers messages between nodes. Implementations must be safe
// for concurrent use and must not block the caller indefinitely.
type Transport interface {
	Send(to int, m Msg)
}
