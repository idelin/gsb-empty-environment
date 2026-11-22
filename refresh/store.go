package refresh

import (
	"errors"
	"math/big"
)

// Crash points understood by PausableStore.
const (
	// CrashAfterStage fails after the staged row is durable, before the
	// recipient's current share has been replaced.
	CrashAfterStage = "after_stage"
	// CrashAfterReplace fails after current share replacement but before the
	// durable commit marker is cleared.
	CrashAfterReplace = "after_replace"
)

// StoredShare is one custodian's current share for a round.
type StoredShare struct {
	Round       uint64   `json:"round"`
	RecipientID string   `json:"recipient_id"`
	Scalar      *big.Int `json:"scalar"`
}

// StagedState is the idempotent two-phase staging row. Its presence means an
// apply for (Round, RecipientID) is in flight; recovery resumes rather than
// re-adding the increment.
type StagedState struct {
	Round        uint64   `json:"round"`
	RecipientID  string   `json:"recipient_id"`
	OldShare     *big.Int `json:"old_share"`
	NewShare     *big.Int `json:"new_share"`
	AppliedShare bool     `json:"applied_share"`
	DeltaDigest  []byte   `json:"delta_digest"`
}

// ShareCommitRequest is a host request to fold accepted increments into one
// recipient's share. The delta list corresponds to decision.AcceptedPairs for
// the recipient and must already have passed triage.
type ShareCommitRequest struct {
	Round       uint64
	RecipientID string
	OldShare    StoredShare
	Deltas      []*big.Int
	DeltaDigest []byte
}

// CommitResult reports how an apply call concluded.
type CommitResult struct {
	// Outcome is "committed", "recovered" or "noop".
	Outcome      string
	RecipientID  string
	Round        uint64
	CurrentShare StoredShare
}

const (
	OutcomeCommitted = "committed"
	OutcomeRecovered = "recovered"
	OutcomeNoop      = "noop"
)

// Store is the persistence contract a key host implements. The engine never
// writes shares anywhere else.
type Store interface {
	Current(recipientID string) (StoredShare, bool, error)
	PutCurrent(share StoredShare) error
	LoadStage(recipientID string) (StagedState, bool, error)
	SaveStage(stage StagedState) error
	ClearStage(recipientID string) error
}

// PausableStore is a transparent in-memory Store with fault injection for
// power-loss testing. A crash leaves all rows exactly as they were at the
// named crash point; Snapshot/Restore reproduce arbitrary restart scenarios.
type PausableStore struct {
	current    map[string]StoredShare
	staged     map[string]StagedState
	crashPoint string
}

// NewPausableStore builds an empty store.
func NewPausableStore() *PausableStore {
	return &PausableStore{
		current: map[string]StoredShare{},
		staged:  map[string]StagedState{},
	}
}

// ArmCrash arms a one-shot fault at the named point.
func (s *PausableStore) ArmCrash(point string) { s.crashPoint = point }

// ClearCrash disarms a previously armed fault.
func (s *PausableStore) ClearCrash() { s.crashPoint = "" }

// Snapshot is a deep copy that survives a later simulated crash.
type Snapshot struct {
	current map[string]StoredShare
	staged  map[string]StagedState
}

// Snapshot captures durable state.
func (s *PausableStore) Snapshot() Snapshot {
	c := map[string]StoredShare{}
	for k, v := range s.current {
		c[k] = cloneShare(v)
	}
	st := map[string]StagedState{}
	for k, v := range s.staged {
		st[k] = cloneStage(v)
	}
	return Snapshot{current: c, staged: st}
}

// Restore rewinds durable state to a previously taken snapshot.
func (s *PausableStore) Restore(snap Snapshot) {
	s.current = map[string]StoredShare{}
	for k, v := range snap.current {
		s.current[k] = cloneShare(v)
	}
	s.staged = map[string]StagedState{}
	for k, v := range snap.staged {
		s.staged[k] = cloneStage(v)
	}
}

func (s *PausableStore) Current(recipientID string) (StoredShare, bool, error) {
	v, ok := s.current[recipientID]
	return v, ok, nil
}

func (s *PausableStore) PutCurrent(share StoredShare) error {
	s.current[share.RecipientID] = cloneShare(share)
	if s.crashPoint == CrashAfterReplace {
		s.crashPoint = ""
		return errors.New("refresh: simulated power loss after share replacement")
	}
	return nil
}

func (s *PausableStore) LoadStage(recipientID string) (StagedState, bool, error) {
	v, ok := s.staged[recipientID]
	return v, ok, nil
}

func (s *PausableStore) SaveStage(stage StagedState) error {
	s.staged[stage.RecipientID] = cloneStage(stage)
	if s.crashPoint == CrashAfterStage {
		s.crashPoint = ""
		return errors.New("refresh: simulated power loss after staging")
	}
	return nil
}

func (s *PausableStore) ClearStage(recipientID string) error {
	delete(s.staged, recipientID)
	return nil
}

func cloneShare(s StoredShare) StoredShare {
	out := s
	if s.Scalar != nil {
		out.Scalar = new(big.Int).Set(s.Scalar)
	}
	return out
}

func cloneStage(s StagedState) StagedState {
	out := s
	if s.OldShare != nil {
		out.OldShare = new(big.Int).Set(s.OldShare)
	}
	if s.NewShare != nil {
		out.NewShare = new(big.Int).Set(s.NewShare)
	}
	if s.DeltaDigest != nil {
		out.DeltaDigest = append([]byte(nil), s.DeltaDigest...)
	}
	return out
}
