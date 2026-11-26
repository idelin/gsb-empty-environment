package doublesign

// rule1 enforces: no two different block digests may be signed at the same
// (epoch, height, round, phase) by the same member.
func (a *Auditor) rule1(m Message, table map[uint64]phaseEntry, set EpochSet) Result {
	prior, ok := table[m.Round]
	if !ok {
		return Result{Verdict: VerdictAccepted}
	}
	if bytesEqual(prior.block, m.BlockDigest) {
		return Result{Verdict: VerdictDuplicate}
	}
	ev := a.evidence(set, m.MemberID, ViolationEquivocation, prior.msg, m)
	return Result{
		Verdict:  VerdictConvicted,
		Evidence: ev,
		Reason:   "two different block digests signed at same epoch/height/round/phase",
	}
}

// rule3 enforces the LockedQC no-rollback constraint: once the member has
// acknowledged a higher lock at this height, it cannot move to a higher round
// supporting a conflicting block with a lower lock.
func (a *Auditor) rule3(m Message, hs *heightState, set EpochSet) Result {
	if hs.maxLocked <= m.LockedRound {
		return Result{Verdict: VerdictAccepted}
	}
	if prior, ok := a.findRollbackPrior(m, hs); ok {
		ev := a.evidence(set, m.MemberID, ViolationLockedQCRollback, prior, m)
		return Result{
			Verdict:  VerdictConvicted,
			Evidence: ev,
			Reason:   "locked qc rolled back to conflicting lower-lock block",
		}
	}
	return Result{Verdict: VerdictAccepted}
}

func (a *Auditor) findRollbackPrior(m Message, hs *heightState) (Message, bool) {
	check := func(e phaseEntry) bool {
		return e.msg.LockedRound > m.LockedRound &&
			e.msg.Round < m.Round &&
			!bytesEqual(e.block, m.BlockDigest)
	}
	for _, e := range hs.prepares {
		if check(e) {
			return e.msg, true
		}
	}
	for _, e := range hs.views {
		if check(e) {
			return e.msg, true
		}
	}
	return Message{}, false
}

func anyRoundAbove(table map[uint64]phaseEntry, round uint64) bool {
	for r := range table {
		if r > round {
			return true
		}
	}
	return false
}

func (a *Auditor) applyPrepare(m Message, ms *memberState, hs *heightState, set EpochSet) Result {
	if r := a.rule1(m, hs.prepares, set); r.Verdict == VerdictConvicted {
		return r
	} else if r.Verdict == VerdictDuplicate {
		return r
	}
	if r := a.rule3(m, hs, set); r.Verdict == VerdictConvicted {
		return r
	}

	// Rule 4: a prepare at height h must extend the member's highest commit.
	if ms.hasCommitTip {
		if m.Height != ms.commitTipH+1 {
			return a.stage("prepare ahead of committed tip")
		}
		if !bytesEqual(m.ParentDigest, ms.commitTipB) {
			commit := a.commitMessage(ms, ms.commitTipH)
			ev := a.evidence(set, m.MemberID, ViolationParentMismatch, commit, m)
			return Result{Verdict: VerdictConvicted, Evidence: ev,
				Reason: "prepare parent digest does not link to last commit"}
		}
	}

	hs.prepares[m.Round] = phaseEntry{block: m.BlockDigest, msg: m}
	if m.LockedRound > hs.maxLocked {
		hs.maxLocked = m.LockedRound
	}
	return Result{Verdict: VerdictAccepted}
}

func (a *Auditor) applyCommit(m Message, ms *memberState, hs *heightState, set EpochSet) Result {
	if r := a.rule1(m, hs.commits, set); r.Verdict == VerdictConvicted {
		return r
	} else if r.Verdict == VerdictDuplicate {
		return r
	}

	// Rule 2: a qualifying prepare (same height/block, round <= commit round)
	// must already exist.
	qualifies := false
	conflicting := false
	for r, e := range hs.prepares {
		if r > m.Round {
			continue
		}
		if bytesEqual(e.block, m.BlockDigest) {
			qualifies = true
		} else {
			conflicting = true
		}
	}
	if !qualifies {
		if conflicting {
			ev := a.evidence(set, m.MemberID, ViolationCommitNoPrepare, m)
			return Result{Verdict: VerdictConvicted, Evidence: ev,
				Reason: "commit without qualifying prepare; only conflicting prepares exist"}
		}
		return a.stage("commit waiting for matching prepare")
	}

	hs.commits[m.Round] = phaseEntry{block: m.BlockDigest, msg: m}
	if !ms.hasCommitTip || m.Height > ms.commitTipH {
		ms.commitTipH = m.Height
		ms.commitTipB = m.BlockDigest
		ms.hasCommitTip = true
	}
	return Result{Verdict: VerdictAccepted}
}

func (a *Auditor) applyView(m Message, ms *memberState, hs *heightState, set EpochSet) Result {
	if r := a.rule1(m, hs.views, set); r.Verdict == VerdictConvicted {
		return r
	} else if r.Verdict == VerdictDuplicate {
		return r
	}
	if r := a.rule3(m, hs, set); r.Verdict == VerdictConvicted {
		return r
	}

	// Claimed highest prepare QC must be monotonic across view changes.
	if m.QCHeight < hs.maxQCHeight ||
		(m.QCHeight == hs.maxQCHeight && m.QCRound < hs.maxQCRound) {
		prev := a.priorClaimMessage(hs)
		ev := a.evidence(set, m.MemberID, ViolationViewQCForged, prev, m)
		return Result{Verdict: VerdictConvicted, Evidence: ev,
			Reason: "view_change regresses claimed highest prepare qc"}
	}

	// The claimed QC must be backed by the member's own prepare at the exact
	// height/round, referencing the same block.
	claimH, ok := ms.heights[m.QCHeight]
	if !ok {
		return a.stage("view_change waiting for claimed prepare qc height")
	}
	prep, ok := claimH.prepares[m.QCRound]
	if !ok {
		if anyRoundAbove(claimH.prepares, m.QCRound) {
			ev := a.evidence(set, m.MemberID, ViolationViewQCForged, m)
			return Result{Verdict: VerdictConvicted, Evidence: ev,
				Reason: "view_change claims a prepare qc round that cannot exist"}
		}
		return a.stage("view_change waiting for claimed prepare round")
	}
	if !bytesEqual(prep.block, m.BlockDigest) {
		ev := a.evidence(set, m.MemberID, ViolationViewQCForged, m, prep.msg)
		return Result{Verdict: VerdictConvicted, Evidence: ev,
			Reason: "view_change qc block does not match the prepared block"}
	}

	hs.views[m.Round] = phaseEntry{block: m.BlockDigest, msg: m}
	hs.maxQCHeight = m.QCHeight
	hs.maxQCRound = m.QCRound
	if m.LockedRound > hs.maxLocked {
		hs.maxLocked = m.LockedRound
	}
	return Result{Verdict: VerdictAccepted}
}

func (a *Auditor) commitMessage(ms *memberState, height uint64) Message {
	if hs, ok := ms.heights[height]; ok {
		for _, e := range hs.commits {
			return e.msg
		}
	}
	return Message{}
}

func (a *Auditor) priorClaimMessage(hs *heightState) Message {
	for _, e := range hs.views {
		if e.msg.QCHeight == hs.maxQCHeight && e.msg.QCRound == hs.maxQCRound {
			return e.msg
		}
	}
	return Message{}
}

// pump re-evaluates staged shares for a member after each applied message.
// Shares whose member is frozen are dropped permanently (old staging can never
// reactivate). The loop runs until no further progress is made.
func (a *Auditor) pump(memberID string) {
	ms := a.members[memberID]
	progress := true
	for progress {
		progress = false
		if ms.frozen {
			a.dropStaged(memberID)
			return
		}
		var remaining []Message
		for _, m := range a.staged {
			if m.MemberID != memberID {
				remaining = append(remaining, m)
				continue
			}
			set := a.epochs[m.Epoch]
			res := a.apply(m, ms, set)
			switch res.Verdict {
			case VerdictAccepted:
				progress = true
			case VerdictConvicted:
				// apply already froze the member and purged its staging.
				return
			default:
				remaining = append(remaining, m)
			}
		}
		a.staged = remaining
	}
}

func (a *Auditor) dropStaged(memberID string) {
	var kept []Message
	for _, m := range a.staged {
		if m.MemberID != memberID {
			kept = append(kept, m)
		}
	}
	a.staged = kept
}

// freeze permanently freezes the member and durably records the conviction.
func (a *Auditor) freeze(memberID string, r Result) {
	ms := a.members[memberID]
	if ms.frozen {
		return
	}
	ms.frozen = true
	if a.store != nil && r.Evidence != nil {
		_ = a.store.appendEvidence(*r.Evidence)
	}
	a.dropStaged(memberID)
}

// Conviction is recorded inside observeLocked when apply returns it directly;
// wrap apply call sites to freeze. This helper centralizes that transition.
func (a *Auditor) apply(m Message, ms *memberState, set EpochSet) Result {
	hs := a.height(ms, m.Height)
	var r Result
	switch m.Phase {
	case PhasePrepare:
		r = a.applyPrepare(m, ms, hs, set)
	case PhaseCommit:
		r = a.applyCommit(m, ms, hs, set)
	default:
		r = a.applyView(m, ms, hs, set)
	}
	if r.Verdict == VerdictConvicted {
		a.freeze(m.MemberID, r)
	}
	return r
}
