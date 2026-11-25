package pinpolicy

import "sort"

// evaluateRelease applies every acceptance predicate to one release at real
// time t, using localTime only for the closed policy window.
func (e engine) evaluateRelease(release Release, t, localTime Time) *ReleaseVerdict {
	body := release.Policy
	verdict := &ReleaseVerdict{
		Host:     body.Host,
		Sequence: body.Sequence,
	}
	addReason := func(reason Reason) {
		for _, existing := range verdict.Reasons {
			if existing == reason {
				return
			}
		}
		verdict.Reasons = append(verdict.Reasons, reason)
	}

	if release.PublishAt > t {
		addReason(ReasonNotPublished)
	}
	if localTime < body.ValidFrom || localTime > body.ValidUntil {
		addReason(ReasonWindowClosed)
	}

	// Both pinned keys must resolve to live host-scoped non-root certificates.
	for _, key := range []KeyID{body.Primary, body.Backup} {
		cert, ok := e.cert.liveAt(key, t)
		if !ok {
			if _, known := e.cert.byKey[key]; known {
				addReason(ReasonKeyRevoked)
			} else {
				addReason(ReasonUnknownKey)
			}
			continue
		}
		if cert.Host != body.Host || cert.Role == RootRole {
			addReason(ReasonHostMismatch)
		}
	}

	liveByRole := map[Role]map[KeyID]bool{}
	for _, signature := range release.Signatures {
		cert, ok := e.cert.liveAt(signature.Signer, t)
		if !ok {
			if _, known := e.cert.byKey[signature.Signer]; known {
				addReason(ReasonKeyRevoked)
			} else {
				addReason(ReasonUnknownKey)
			}
			continue
		}
		if cert.Host != body.Host {
			addReason(ReasonHostMismatch)
			continue
		}
		if liveByRole[signature.Role] == nil {
			liveByRole[signature.Role] = map[KeyID]bool{}
		}
		liveByRole[signature.Role][signature.Signer] = true
	}

	// Every required role needs enough distinct live signers.
	for role, threshold := range body.Thresholds {
		if len(liveByRole[role]) < threshold {
			addReason(ReasonRoleThreshold)
		}
	}

	if e.hasSequenceConflict(body.Host, body.Sequence, t) {
		addReason(ReasonSequenceConflict)
	}

	// Backup-key rollback: an obsolete release must never be carried by a
	// backup signature.
	if e.isObsolete(body.Host, body.Sequence, t) {
		for _, signature := range release.Signatures {
			if signature.Backup {
				addReason(ReasonBackupRollback)
				break
			}
		}
	}

	// Root-switch policies need the dual handover for the same host, the same
	// policy digest and every handover role.
	if body.RootKey == e.in.NewRoot {
		if missing := e.handoverGaps(release, t); len(missing) > 0 {
			verdict.Missing = append(verdict.Missing, missing...)
			addReason(ReasonHandoverMissing)
			for _, gap := range missing {
				if gap.Detail != "" {
					addReason(ReasonHandoverKeyDead)
					break
				}
			}
		}
	}

	sortReasons(verdict.Reasons)
	return verdict
}

func sortReasons(reasons []Reason) {
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
}

func (e engine) hasSequenceConflict(host HostID, seq Seq, t Time) bool {
	count := 0
	for _, release := range e.in.Releases {
		body := release.Policy
		if body.Host == host && body.Sequence == seq && release.PublishAt <= t {
			count++
		}
	}
	return count > 1
}

func (e engine) isObsolete(host HostID, seq Seq, t Time) bool {
	for _, release := range e.in.Releases {
		body := release.Policy
		if body.Host == host && body.Sequence > seq && release.PublishAt <= t {
			return true
		}
	}
	return false
}

// handoverGaps returns one MissingAuthorization per handover role lacking
// matching live old-root and new-root signatures over the exact policy
// digest. The policy's threshold roles are the handover roles, and each one
// must be covered by both sides.
func (e engine) handoverGaps(release Release, t Time) []MissingAuthorization {
	body := release.Policy
	digest := Digest(body)
	roles := make([]Role, 0, len(body.Thresholds))
	for role := range body.Thresholds {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })

	var gaps []MissingAuthorization
	for _, role := range roles {
		oldOK, newOK := false, false
		oldDetail, newDetail := "", ""
		for _, handover := range e.in.Handovers {
			if handover.Host != body.Host ||
				handover.PolicyDigest != digest ||
				handover.Role != role ||
				handover.PublishAt > t {
				continue
			}
			if oldCert, live := e.cert.liveAt(handover.OldSigner, t); live && oldCert.RootKey == e.in.OldRoot {
				oldOK = true
			} else if _, known := e.cert.byKey[handover.OldSigner]; known {
				oldDetail = "old root signer revoked at or before online time"
			}
			if newCert, live := e.cert.liveAt(handover.NewSigner, t); live && newCert.RootKey == e.in.NewRoot {
				newOK = true
			} else if _, known := e.cert.byKey[handover.NewSigner]; known {
				newDetail = "new root signer revoked at or before online time"
			}
		}
		if oldOK && newOK {
			continue
		}
		side := "both"
		switch {
		case oldOK && !newOK:
			side = "new"
		case !oldOK && newOK:
			side = "old"
		}
		detail := oldDetail
		if newDetail != "" {
			if detail != "" {
				detail += "; "
			}
			detail += newDetail
		}
		gaps = append(gaps, MissingAuthorization{
			Host:         body.Host,
			PolicyDigest: digest,
			Role:         role,
			MissingSide:  side,
			Detail:       detail,
		})
	}
	return gaps
}
