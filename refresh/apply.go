package refresh

import (
	"errors"
	"math/big"
)

// StageAndCommit folds a recipient's accepted round increments into its share
// with a durable two-phase protocol. Re-invocation after a power loss never
// adds the increments a second time: an existing staged row is resumed instead.
func (e *Engine) StageAndCommit(store Store, req ShareCommitRequest) (*CommitResult, error) {
	if store == nil {
		return nil, errors.New("refresh: nil store")
	}
	canon, _, err := e.resolve(req.RecipientID)
	if err != nil {
		return nil, err
	}
	req.RecipientID = canon

	if stage, ok, err := store.LoadStage(canon); err != nil {
		return nil, err
	} else if ok {
		if stage.Round != req.Round {
			return nil, errors.New("refresh: staged state belongs to a different round")
		}
		return e.resume(store, stage)
	}

	current, ok, err := store.Current(canon)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("refresh: recipient has no current share")
	}
	if current.Round >= req.Round {
		return &CommitResult{
			Outcome:      OutcomeNoop,
			RecipientID:  canon,
			Round:        current.Round,
			CurrentShare: current,
		}, nil
	}
	if current.Round != req.Round-1 {
		return nil, errors.New("refresh: round gap; refusing to apply non-adjacent round")
	}
	if !e.Field.EqualScalar(current.Scalar, req.OldShare.Scalar) ||
		current.RecipientID != req.OldShare.RecipientID {
		return nil, errors.New("refresh: request old share does not match durable share")
	}

	newScalar := e.Field.Sum(append([]*big.Int{current.Scalar}, req.Deltas...)...)
	stage := StagedState{
		Round:       req.Round,
		RecipientID: canon,
		OldShare:    new(big.Int).Set(current.Scalar),
		NewShare:    newScalar,
		DeltaDigest: append([]byte(nil), req.DeltaDigest...),
	}
	if err := store.SaveStage(stage); err != nil {
		// Durable stage may have landed before the fault: recovery resumes it.
		return nil, err
	}

	newShare := StoredShare{Round: req.Round, RecipientID: canon, Scalar: newScalar}
	if err := store.PutCurrent(newShare); err != nil {
		return nil, err
	}
	stage.AppliedShare = true
	if err := store.SaveStage(stage); err != nil {
		return nil, err
	}
	if err := store.ClearStage(canon); err != nil {
		return nil, err
	}
	return &CommitResult{
		Outcome:      OutcomeCommitted,
		RecipientID:  canon,
		Round:        req.Round,
		CurrentShare: newShare,
	}, nil
}

// Recover resumes any interrupted apply for a recipient. It is idempotent: with
// no staged row it simply returns the current share.
func (e *Engine) Recover(store Store, recipientID string) (*CommitResult, error) {
	canon, _, err := e.resolve(recipientID)
	if err != nil {
		return nil, err
	}
	stage, ok, err := store.LoadStage(canon)
	if err != nil {
		return nil, err
	}
	if !ok {
		current, exists, err := store.Current(canon)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, errors.New("refresh: recipient has no current share")
		}
		return &CommitResult{
			Outcome:      OutcomeNoop,
			RecipientID:  canon,
			Round:        current.Round,
			CurrentShare: current,
		}, nil
	}
	return e.resume(store, stage)
}

func (e *Engine) resume(store Store, stage StagedState) (*CommitResult, error) {
	// Phase 1 crash: nothing applied yet. Write the precomputed new share;
	// the staged row, not the request, is the source of truth.
	if !stage.AppliedShare {
		newShare := StoredShare{
			Round:       stage.Round,
			RecipientID: stage.RecipientID,
			Scalar:      stage.NewShare,
		}
		if err := store.PutCurrent(newShare); err != nil {
			return nil, err
		}
		stage.AppliedShare = true
		if err := store.SaveStage(stage); err != nil {
			return nil, err
		}
	}
	// Phase 2 crash: current already replaced; only the marker remains.
	if err := store.ClearStage(stage.RecipientID); err != nil {
		return nil, err
	}
	current, _, err := store.Current(stage.RecipientID)
	if err != nil {
		return nil, err
	}
	return &CommitResult{
		Outcome:      OutcomeRecovered,
		RecipientID:  stage.RecipientID,
		Round:        stage.Round,
		CurrentShare: current,
	}, nil
}
