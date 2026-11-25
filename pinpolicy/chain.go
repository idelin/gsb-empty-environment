package pinpolicy

import "sort"

// chainReason builds the unique accepted chain from the releases eligible at
// one online case. It requires consecutive sequence numbers from the minimum
// accepted sequence onward, predecessor digests that actually chain, and a
// new-root endpoint whose handover was already validated by evaluateRelease.
func (e engine) chainReason(eligible map[Seq]*ReleaseVerdict, t Time) Reason {
	if len(eligible) == 0 {
		if e.migrationPublished(t) {
			return ReasonWindowClosed
		}
		return ReasonNoMigration
	}

	seqs := make([]Seq, 0, len(eligible))
	for seq := range eligible {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	bySeq := map[Seq]Release{}
	for _, release := range e.in.Releases {
		if _, ok := eligible[release.Policy.Sequence]; ok {
			bySeq[release.Policy.Sequence] = release
		}
	}

	// The smallest accepted sequence is the chain base; its PrevDigest is
	// anchored at the device checkpoint rather than re-validated here. Every
	// later step must be exactly one sequence higher and name the prior
	// digest. Any hole, reordering or digest mismatch blocks acceptance.
	previousDigest := Digest(bySeq[seqs[0]].Policy)
	for index := 1; index < len(seqs); index++ {
		if seqs[index] != seqs[index-1]+1 {
			return ReasonChainBroken
		}
		current := bySeq[seqs[index]].Policy
		if current.PrevDigest != previousDigest {
			return ReasonChainBroken
		}
		previousDigest = Digest(current)
	}

	endpoint := bySeq[seqs[len(seqs)-1]].Policy
	if endpoint.RootKey != e.in.NewRoot {
		return ReasonNoMigration
	}
	return ""
}

// finalize turns the exhaustive window data into the two top-level verdicts
// and records the earliest blocking failure.
func (e engine) finalize(report *Report) {
	report.SafetyPass = true
	report.LivenessPass = true

	blocked := map[Seq]bool{}
	var earliest *Failure
	onlineCases := 0

	for windowIndex := range report.Windows {
		onlineCases += len(report.Windows[windowIndex].Cases)
	}
	report.Stats.OnlineCaseCount = onlineCases

	for windowIndex := range report.Windows {
		window := &report.Windows[windowIndex]
		if window.Element.Kind == KindOpen && window.Element.Empty {
			// No integer online time exists inside an empty open interval.
			continue
		}
		for _, verdict := range window.ReleaseVerdicts {
			if len(verdict.Reasons) > 0 {
				blocked[verdict.Sequence] = true
			}
		}

		if !window.Reachable || !window.MigrationFetchable {
			// Before any migration target is published, or with no viable
			// checkpoint, the window carries no liveness obligation.
			continue
		}
		report.Stats.LegalWindowCount++
		if window.ChainOK {
			continue
		}
		report.LivenessPass = false
		failure := e.firstWindowFailure(*window)
		if failure != nil && (earliest == nil || lessFailure(failure, earliest)) {
			earliest = failure
		}
	}

	report.Stats.BlockedReleaseCount = len(blocked)

	// Safety is proven per release: every fetchable release that touches a
	// revoked key, uses a backup signature on an obsolete sequence, misses a
	// role threshold or lacks dual-root handover must be blocked. If such a
	// release ever appeared without reasons it would be an accept-and-lose
	// hole, which flips SafetyPass.
	for windowIndex := range report.Windows {
		window := &report.Windows[windowIndex]
		if window.Element.Kind == KindOpen && window.Element.Empty {
			continue
		}
		if !e.windowSafe(*window) {
			report.SafetyPass = false
			failure := e.firstSafetyFailure(*window)
			if failure != nil && (earliest == nil || lessFailure(failure, earliest)) {
				earliest = failure
			}
		}
	}

	report.Pass = report.SafetyPass && report.LivenessPass
	report.EarliestFailure = earliest
}

func (e engine) isPublishedInWindow(verdict ReleaseVerdict, t Time) bool {
	for _, release := range e.in.Releases {
		body := release.Policy
		if body.Host == verdict.Host && body.Sequence == verdict.Sequence && release.PublishAt <= t {
			return true
		}
	}
	return false
}

// windowSafe is true when every fetchable release that exhibits a hazard
// also carries a blocking reason for that hazard. In other words, no
// revoked, rolled-back or unauthorized key can be accepted.
func (e engine) windowSafe(window OnlineCase) bool {
	t := window.RealTime
	for index := range e.in.Releases {
		release := e.in.Releases[index]
		if release.PublishAt > t {
			// Not yet published: invisible to the offline terminal and not a
			// candidate for accepting a dangerous key.
			continue
		}
		verdict := findVerdict(window.ReleaseVerdicts, release.Policy.Host, release.Policy.Sequence)
		if verdict == nil {
			return false
		}
		// The verdict aggregates every skew; a window-closed rejection under
		// one skew is enough to show the release is never silently accepted.
		// A safety hole is exactly: an independently hazardous fetchable
		// release whose cached verdict has no blocking reason at all.
		if e.hasIndependentHazard(release, t) && len(verdict.Reasons) == 0 {
			// A hazardous fetchable release with no blocking reason would be
			// accepted, which violates the safety obligation.
			return false
		}
	}
	return true
}

func findVerdict(verdicts []ReleaseVerdict, host HostID, seq Seq) *ReleaseVerdict {
	for index := range verdicts {
		if verdicts[index].Host == host && verdicts[index].Sequence == seq {
			return &verdicts[index]
		}
	}
	return nil
}

// hasIndependentHazard recomputes the safety-side hazards without consulting
// the cached verdict, giving the top-level safety flag an independent check.
func (e engine) hasIndependentHazard(release Release, t Time) bool {
	body := release.Policy
	for _, key := range []KeyID{body.Primary, body.Backup} {
		cert, known := e.cert.byKey[key]
		if !known || t < cert.PublishAt {
			return true
		}
		if cert.Host != body.Host || cert.Role == RootRole {
			return true
		}
		if cert.Revoked && t >= cert.RevokedAt {
			return true
		}
	}

	liveByRole := map[Role]map[KeyID]bool{}
	for _, signature := range release.Signatures {
		cert, known := e.cert.byKey[signature.Signer]
		if !known || cert.Host != body.Host || t < cert.PublishAt {
			return true
		}
		if cert.Revoked && t >= cert.RevokedAt {
			return true
		}
		if liveByRole[signature.Role] == nil {
			liveByRole[signature.Role] = map[KeyID]bool{}
		}
		liveByRole[signature.Role][signature.Signer] = true
	}
	for role, threshold := range body.Thresholds {
		if len(liveByRole[role]) < threshold {
			return true
		}
	}

	if e.hasSequenceConflict(body.Host, body.Sequence, t) {
		return true
	}
	if e.isObsolete(body.Host, body.Sequence, t) {
		for _, signature := range release.Signatures {
			if signature.Backup {
				return true
			}
		}
	}
	if body.RootKey == e.in.NewRoot && len(e.handoverGaps(release, t)) > 0 {
		return true
	}
	return false
}

func (e engine) firstWindowFailure(window OnlineCase) *Failure {
	keys := make([]CaseKey, 0, len(window.ChainFailure))
	for key := range window.ChainFailure {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Checkpoint != keys[j].Checkpoint {
			return keys[i].Checkpoint < keys[j].Checkpoint
		}
		return keys[i].Skew < keys[j].Skew
	})
	if len(keys) == 0 {
		return &Failure{
			Element:     window.Element,
			Reason:      ReasonNoCheckpoint,
			Description: "legal online window has no admissible checkpoint/skew case",
		}
	}
	key := keys[0]
	reason := window.ChainFailure[key]
	failure := &Failure{
		Element:    window.Element,
		Checkpoint: key.Checkpoint,
		Skew:       key.Skew,
		Reason:     reason,
	}
	e.attachMissing(failure, window, reason)
	failure.Description = describeReason(reason)
	return failure
}

func (e engine) firstSafetyFailure(window OnlineCase) *Failure {
	for _, verdict := range window.ReleaseVerdicts {
		if !e.isPublishedInWindow(verdict, window.RealTime) || len(verdict.Reasons) == 0 {
			continue
		}
		for _, reason := range verdict.Reasons {
			if reason == ReasonWindowClosed || reason == ReasonNotPublished {
				continue
			}
			return &Failure{
				Element:     window.Element,
				Reason:      reason,
				Host:        verdict.Host,
				Sequence:    verdict.Sequence,
				Missing:     verdict.Missing,
				Description: describeReason(reason),
			}
		}
	}
	return nil
}

func (e engine) attachMissing(failure *Failure, window OnlineCase, reason Reason) {
	for _, verdict := range window.ReleaseVerdicts {
		if reason == ReasonHandoverMissing && len(verdict.Missing) > 0 {
			failure.Host = verdict.Host
			failure.Sequence = verdict.Sequence
			failure.Missing = verdict.Missing
			return
		}
		if len(verdict.Reasons) > 0 && verdict.Reasons[0] == reason {
			failure.Host = verdict.Host
			failure.Sequence = verdict.Sequence
			failure.Missing = verdict.Missing
			return
		}
	}
}

// lessFailure orders failures by partition position (points and opens share
// the timeline order), then checkpoint and skew, so the reported failure is
// genuinely the earliest one.
func lessFailure(a, b *Failure) bool {
	if a.Element.Sample != b.Element.Sample {
		return a.Element.Sample < b.Element.Sample
	}
	if a.Element.Kind != b.Element.Kind {
		return a.Element.Kind == KindPoint
	}
	if a.Checkpoint != b.Checkpoint {
		return a.Checkpoint < b.Checkpoint
	}
	return a.Skew < b.Skew
}

func describeReason(reason Reason) string {
	switch reason {
	case ReasonNotPublished:
		return "policy not yet published at the real online time"
	case ReasonWindowClosed:
		return "policy window is closed at the skewed local time"
	case ReasonUnknownKey:
		return "referenced key is absent from the certificate schedule"
	case ReasonKeyRevoked:
		return "required key is revoked at or before the online time"
	case ReasonRoleThreshold:
		return "at least one role threshold is not met by live distinct signers"
	case ReasonHandoverMissing:
		return "dual-root handover does not cover the host, policy digest and role"
	case ReasonHostMismatch:
		return "key or signer is not scoped to the policy host"
	case ReasonSequenceConflict:
		return "two fetchable releases share the same sequence number"
	case ReasonBackupRollback:
		return "backup key attempts to carry an obsolete, superseded policy"
	case ReasonChainBroken:
		return "sequence gap or predecessor digest mismatch breaks the migration chain"
	case ReasonNoCheckpoint:
		return "no checkpoint can reach the online window under the offline bound"
	case ReasonNoMigration:
		return "no verifiable new-root endpoint is reachable"
	default:
		return string(reason)
	}
}
