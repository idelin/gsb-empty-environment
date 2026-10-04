package paxos

// Message is any RPC payload exchanged between nodes.
type Message interface{ message() }

// Prepare is Phase 1a: a would-be leader asks acceptors to promise not to
// accept lower ballots and to report what they have already accepted.
type Prepare struct {
	Ballot Ballot
	From   string
}

// Promise is Phase 1b. OK is false when the acceptor has already promised
// a higher ballot; Ballot then carries the promised ballot so the
// proposer can back off and retry higher.
type Promise struct {
	Ballot  Ballot
	From    string
	OK      bool
	Entries map[int]Entry // everything the acceptor has accepted
}

// Accept is Phase 2a: the leader asks acceptors to store value at slot.
type Accept struct {
	Slot   int
	Ballot Ballot
	Value  string
	From   string
}

// Accepted is Phase 2b. OK is false when the acceptor promised a higher
// ballot; Ballot then carries the promised ballot.
type Accepted struct {
	Slot   int
	Ballot Ballot
	From   string
	OK     bool
}

// Commit is the learn phase: the leader notifies followers that a slot is
// committed. It carries the full entry so followers that were outside the
// accepting quorum can upsert it idempotently.
type Commit struct {
	Slot   int
	Ballot Ballot
	Value  string
	From   string
}

func (Prepare) message()  {}
func (Promise) message()  {}
func (Accept) message()   {}
func (Accepted) message() {}
func (Commit) message()   {}
