package netsec

// historyLimit bounds retained terminal transactions; only stale-proof
// classification needs the most recent entries.
const historyLimit = 16

// Begin opens a rebinding transaction. The new channel gains no recovery
// power: Active is untouched while the change is pending.
//
// A second Begin while another change is in flight supersedes it: every proof
// issued for the old digest becomes unusable (ErrProofRevoked). A late Begin
// whose old channel no longer matches Active is rejected so a stale host cannot
// rebind against an outdated channel.
func (s *State) Begin(old, newChannel Channel, digest string, nowNano, expiresNano int64) (*Transaction, *Event, error) {
	if s == nil {
		return nil, nil, ErrNoState
	}
	if !old.Valid() || !newChannel.Valid() || old.Equal(newChannel) {
		return nil, nil, ErrInvalidChannel
	}
	if !old.Equal(s.Active) {
		return nil, nil, ErrInvalidChannel
	}
	if expiresNano <= nowNano {
		return nil, nil, ErrProofExpired
	}

	superseded := ""
	if s.Pending != nil {
		superseded = s.Pending.Digest
		s.archive(s.Pending.Digest, s.Pending.Old, s.Pending.New, OutcomeSuperseded, nowNano, s.Pending.ExpiresNano)
	}

	tx := &Transaction{
		Digest:      digest,
		Old:         old,
		New:         newChannel,
		ExpiresNano: expiresNano,
	}
	s.Pending = tx
	s.Version++

	ev := &Event{
		Type:        EventChangeBegun,
		AtNano:      nowNano,
		Superseded:  superseded,
		Digest:      digest,
		Old:         old,
		New:         newChannel,
		ExpiresNano: expiresNano,
	}
	return tx, ev, nil
}

// Confirm applies one side's proof to the pending transaction. It never honors
// a proof whose digest differs from the live transaction, whose side does not
// match the presented token, or whose deadline has passed.
//
// The active channel only changes when both sides are confirmed. The switch is
// atomic within a single state version bump.
func (s *State) Confirm(side Side, digest string, nowNano, expireNano int64) (ConfirmResult, *Event, error) {
	if s == nil {
		return ConfirmResult{}, nil, ErrNoState
	}
	if s.Pending != nil && s.Pending.Digest == digest {
		return s.confirmLive(side, digest, nowNano, expireNano)
	}

	// No live transaction binds this digest: classify the stale proof from the
	// terminal archive when we still retain it.
	if outcome, ok := s.lookupArchive(digest); ok {
		if outcome == OutcomeSwitched {
			return ConfirmResult{}, nil, ErrAlreadyComplete
		}
		return ConfirmResult{}, nil, ErrProofRevoked
	}
	// Unknown digest or a superseded transaction that aged out of the archive.
	return ConfirmResult{}, nil, ErrProofRevoked
}

func (s *State) confirmLive(side Side, digest string, nowNano, expireNano int64) (ConfirmResult, *Event, error) {
	tx := s.Pending
	if side != SideOld && side != SideNew {
		return ConfirmResult{}, nil, ErrSideMismatch
	}
	if nowNano >= tx.ExpiresNano || nowNano >= expireNano {
		return ConfirmResult{}, nil, ErrProofExpired
	}

	already := false
	switch side {
	case SideOld:
		if tx.OldConfirmed != nil {
			already = true
		} else {
			tx.OldConfirmed = &Confirmation{
				Digest: digest, Side: side, AtNano: nowNano, ExpireNano: expireNano,
			}
		}
	case SideNew:
		if tx.NewConfirmed != nil {
			already = true
		} else {
			tx.NewConfirmed = &Confirmation{
				Digest: digest, Side: side, AtNano: nowNano, ExpireNano: expireNano,
			}
		}
	}
	if already {
		// Idempotent retry after a lost response: no state change, no event.
		return ConfirmResult{Status: StatusAlreadyConfirmed, Digest: digest, Side: side}, nil, nil
	}

	ev := &Event{
		Type:        EventChangeConfirmed,
		AtNano:      nowNano,
		Digest:      digest,
		Old:         tx.Old,
		New:         tx.New,
		ExpiresNano: tx.ExpiresNano,
	}

	if tx.OldConfirmed == nil || tx.NewConfirmed == nil {
		s.Version++
		return ConfirmResult{Status: StatusPending, Digest: digest, Side: side}, ev, nil
	}

	// Both sides are present: perform the atomic switch. Until this point the
	// old channel remained the active recovery channel.
	switchedFrom := tx.Old
	switchedTo := tx.New
	s.Active = tx.New
	s.Pending = nil
	s.archive(digest, switchedFrom, switchedTo, OutcomeSwitched, nowNano, tx.ExpiresNano)
	s.Version++

	switchEv := &Event{
		Type:        EventChannelSwitched,
		AtNano:      nowNano,
		Digest:      digest,
		Old:         switchedFrom,
		New:         switchedTo,
		ExpiresNano: tx.ExpiresNano,
		Outcome:     OutcomeSwitched,
	}
	ev.Switch = switchEv
	return ConfirmResult{
		Status: StatusSwitched,
		Digest: digest,
		Side:   side,
		Active: switchedTo,
	}, ev, nil
}

// Cancel aborts the in-flight change. All proofs for its digest are revoked
// immediately; the active recovery channel is unchanged.
func (s *State) Cancel(nowNano int64) (string, *Event, error) {
	if s == nil {
		return "", nil, ErrNoState
	}
	if s.Pending == nil {
		return "", nil, ErrNoPending
	}
	tx := s.Pending
	digest := tx.Digest
	s.archive(digest, tx.Old, tx.New, OutcomeCanceled, nowNano, tx.ExpiresNano)
	s.Pending = nil
	s.Version++

	ev := &Event{
		Type:        EventChangeCanceled,
		AtNano:      nowNano,
		Digest:      digest,
		Old:         tx.Old,
		New:         tx.New,
		ExpiresNano: tx.ExpiresNano,
		Outcome:     OutcomeCanceled,
	}
	return digest, ev, nil
}

func (s *State) archive(digest string, old, newChannel Channel, outcome Outcome, at, expires int64) {
	s.Past = append(s.Past, Archive{
		Digest:      digest,
		Old:         old,
		New:         newChannel,
		Outcome:     outcome,
		CompletedAt: at,
		ExpiresNano: expires,
	})
	if excess := len(s.Past) - historyLimit; excess > 0 {
		s.Past = append([]Archive(nil), s.Past[excess:]...)
	}
}

func (s *State) lookupArchive(digest string) (Outcome, bool) {
	for i := len(s.Past) - 1; i >= 0; i-- {
		if s.Past[i].Digest == digest {
			return s.Past[i].Outcome, true
		}
	}
	return "", false
}

// Clone returns a deep copy so callers (and stores) cannot mutate managed state.
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}
	copied := *s
	if s.Pending != nil {
		tx := *s.Pending
		if s.Pending.OldConfirmed != nil {
			old := *s.Pending.OldConfirmed
			tx.OldConfirmed = &old
		}
		if s.Pending.NewConfirmed != nil {
			newC := *s.Pending.NewConfirmed
			tx.NewConfirmed = &newC
		}
		copied.Pending = &tx
	}
	if s.Past != nil {
		copied.Past = append([]Archive(nil), s.Past...)
	}
	return &copied
}
