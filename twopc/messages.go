package twopc

import "fmt"

// Protocol message bodies. String methods make traces self-explanatory.

// Prepare opens phase 1: the participant must durably vote YES (with locks)
// or vote NO; it cannot "unvote" afterwards.
type Prepare struct {
	Txn    string
	Keys   []string
	Values []string // proposed values; nil entry means a delete
}

func (m Prepare) String() string {
	return fmt.Sprintf("PREPARE{%s writes=%v}", m.Txn, m.Keys)
}

// Prepared is a YES vote: the participant holds durable locks.
type Prepared struct {
	Txn string
}

func (m Prepared) String() string { return "PREPARED{" + m.Txn + "}" }

// NoVote is a NO vote (lock conflict, already aborted, ...). It is also a
// hint that the coordinator may finalise the abort; the decision still has
// to come from the coordinator.
type NoVote struct {
	Txn    string
	From   string
	Reason string
}

func (m NoVote) String() string { return fmt.Sprintf("NOVOTE{%s by %s: %s}", m.Txn, m.From, m.Reason) }

// Commit forces the global commit decision at an uncertain participant.
type Commit struct {
	Txn string
}

func (m Commit) String() string { return "COMMIT{" + m.Txn + "}" }

// Abort forces the global abort decision (and lock release) at a participant.
type Abort struct {
	Txn string
}

func (m Abort) String() string { return "ABORT{" + m.Txn + "}" }

// DecisionQuery is sent by a participant in the PREPARED state to a
// restarted coordinator when it does not know the outcome yet.
type DecisionQuery struct {
	Txn string
}

func (m DecisionQuery) String() string { return "DECISION?{" + m.Txn + "}" }

// Ack acknowledges execution of a decision, allowing the coordinator to
// stop retransmitting it.
type Ack struct {
	Txn      string
	From     string
	Decision Decision // DecisionCommit / DecisionAbort
}

func (m Ack) String() string {
	return fmt.Sprintf("ACK{%s %s from %s}", m.Txn, m.Decision, m.From)
}

// Decision is the global outcome of a transaction.
type Decision int

const (
	DecisionPending Decision = iota
	DecisionCommit
	DecisionAbort
)

func (d Decision) String() string {
	switch d {
	case DecisionCommit:
		return "COMMIT"
	case DecisionAbort:
		return "ABORT"
	default:
		return "PENDING"
	}
}
