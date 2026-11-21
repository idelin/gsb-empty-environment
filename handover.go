package sliceauth

import (
	"errors"
	"fmt"
)

// AuthorizeHandover evaluates a handover without trusting the source context.
// Each PDU session and each newly requested slice must pass, in order:
// membership of the current registration allowed NSSAI, a fresh subscription
// and roaming check, DNN/QoS verification, and membership of the target RAN
// allowed NSSAI (the intersection rule). Cross-AMF handover must be preceded by
// ReselectAMF; otherwise the target AMF mismatch is a programming error.
func (a *Authorizer) AuthorizeHandover(reg Registration, req HandoverRequest) (HandoverDecision, error) {
	if len(reg.AllowedNSSAI) == 0 {
		return HandoverDecision{}, errors.New("sliceauth: handover requires an authorized registration with an allowed NSSAI")
	}
	if req.TargetPLMN != reg.ServingPLMN {
		return HandoverDecision{}, fmt.Errorf("sliceauth: target PLMN %s differs from serving PLMN %s; re-register first",
			req.TargetPLMN, reg.ServingPLMN)
	}
	if req.TargetAMFID != reg.AMFID {
		return HandoverDecision{}, fmt.Errorf("sliceauth: target AMF %q differs from serving AMF %q; call ReselectAMF first",
			req.TargetAMFID, reg.AMFID)
	}
	targetSet := make(map[SliceID]bool, len(req.TargetRANAllowedNSSAI))
	for _, slice := range req.TargetRANAllowedNSSAI {
		if !slice.Valid() {
			return HandoverDecision{}, fmt.Errorf("sliceauth: invalid target RAN S-NSSAI %+v", slice)
		}
		targetSet[slice] = true
	}

	decision := HandoverDecision{}
	sessionSeen := make(map[uint32]bool)
	for _, session := range req.Sessions {
		if sessionSeen[session.ID] {
			return HandoverDecision{}, fmt.Errorf("sliceauth: duplicate PDU session ID %d in handover", session.ID)
		}
		sessionSeen[session.ID] = true
		if !session.ServingSlice.Valid() {
			decision.RejectedSessions = append(decision.RejectedSessions, rejectedSession(session.ID,
				reject(ReasonSliceNotSubscribed, Layer5GSM, "PDU session carries an invalid S-NSSAI")))
			continue
		}
		if !grantList(reg.AllowedNSSAI).contains(session.ServingSlice) {
			decision.RejectedSessions = append(decision.RejectedSessions, rejectedSession(session.ID,
				reject(ReasonSliceNotInAllowedNSSAI, Layer5GSM,
					"S-NSSAI %s from session context is not in the allowed NSSAI; inherited context rejected",
					session.ServingSlice)))
			continue
		}
		grant, denial := a.resolve(session.ServingSlice, reg.ServingPLMN)
		if denial != nil {
			denial.Cause = mapCause(denial.Reason, Layer5GSM)
			decision.RejectedSessions = append(decision.RejectedSessions, rejectedSession(session.ID, *denial))
			continue
		}
		if !containsString(grant.AllowedDNNs, normalizeDNN(session.DNN)) {
			decision.RejectedSessions = append(decision.RejectedSessions, rejectedSession(session.ID,
				reject(ReasonDNNNotSubscribed, Layer5GSM,
					"DNN %q is not subscribed for S-NSSAI %s", session.DNN, session.ServingSlice)))
			continue
		}
		if session.QoSPolicyGeneration != grant.QoSPolicyGeneration {
			decision.RejectedSessions = append(decision.RejectedSessions, rejectedSession(session.ID,
				reject(ReasonPolicyGenerationMismatch, Layer5GSM,
					"QoS policy generation %d for S-NSSAI %s is stale; subscription generation is %d",
					session.QoSPolicyGeneration, session.ServingSlice, grant.QoSPolicyGeneration)))
			continue
		}
		if !targetSet[session.ServingSlice] {
			decision.RejectedSessions = append(decision.RejectedSessions, rejectedSession(session.ID,
				reject(ReasonSliceUnsupportedByTarget, Layer5GSM,
					"S-NSSAI %s is not in the target RAN allowed NSSAI", session.ServingSlice)))
			continue
		}
		decision.AllowedSessions = append(decision.AllowedSessions, AcceptedSession{
			PDUSessionID: session.ID,
			Grant:        grant,
		})
	}

	newSeen := make(map[SliceID]bool)
	for _, candidate := range req.NewSlices {
		if !candidate.Valid() {
			return HandoverDecision{}, fmt.Errorf("sliceauth: invalid new S-NSSAI %+v", candidate)
		}
		if newSeen[candidate] {
			continue
		}
		newSeen[candidate] = true
		// A newly requested slice receives the full subscription check; it is
		// never admitted just because it appeared in an old context.
		grant, denial := a.resolve(candidate, reg.ServingPLMN)
		if denial != nil {
			decision.RejectedNewSlices = append(decision.RejectedNewSlices, RejectedSlice{
				Slice:     candidate,
				Rejection: *denial,
			})
			continue
		}
		if !targetSet[candidate] {
			decision.RejectedNewSlices = append(decision.RejectedNewSlices, RejectedSlice{
				Slice: candidate,
				Rejection: reject(ReasonSliceUnsupportedByTarget, Layer5GMM,
					"new S-NSSAI %s is not in the target RAN allowed NSSAI", candidate),
			})
			continue
		}
		decision.GrantedNewSlices = append(decision.GrantedNewSlices, grant)
	}
	sortGrants(decision.GrantedNewSlices)

	if len(decision.AllowedSessions) == 0 && len(decision.GrantedNewSlices) == 0 {
		overall := reject(ReasonNoSlicesAvailable, Layer5GMM,
			"no subscribed S-NSSAI is available at the target node")
		decision.OverallRejection = &overall
	}
	return decision, nil
}

// ReselectAMF rebuilds the registration context for a target AMF. The target
// AMF capability can only remove S-NSSAIs from the already authorized allowed
// NSSAI; it can never introduce new slices. There is no inheritance from the
// old AMF beyond what the subscription-backed allowed NSSAI contains.
func (a *Authorizer) ReselectAMF(reg Registration, target AMFCapability) (Registration, []RejectedSlice, error) {
	if target.AMFID == "" {
		return Registration{}, nil, errors.New("sliceauth: AMF reselection requires a target AMF ID")
	}
	if target.AMFID == reg.AMFID {
		return Registration{}, nil, fmt.Errorf("sliceauth: target AMF %q is the current AMF", target.AMFID)
	}
	supported := make(map[SliceID]bool, len(target.SupportedNSSAI))
	for _, slice := range target.SupportedNSSAI {
		if !slice.Valid() {
			return Registration{}, nil, fmt.Errorf("sliceauth: invalid target AMF S-NSSAI %+v", slice)
		}
		supported[slice] = true
	}
	var retained []GrantedSlice
	var rejected []RejectedSlice
	for _, grant := range reg.AllowedNSSAI {
		if !supported[grant.Slice] {
			rejected = append(rejected, RejectedSlice{
				Slice: grant.Slice,
				Rejection: reject(ReasonSliceUnsupportedByTarget, Layer5GMM,
					"S-NSSAI %s is not supported by target AMF %q", grant.Slice, target.AMFID),
			})
			continue
		}
		retained = append(retained, grant)
	}
	sortGrants(retained)
	out := reg
	out.AMFID = target.AMFID
	out.AllowedNSSAI = retained
	return out, rejected, nil
}

// AllowedTargetSlices returns the intersection of an authorized allowed NSSAI
// and the target node (RAN or AMF) supported NSSAI. It is the only primitive a
// caller needs when deriving the slice list to advertise to a target node.
func AllowedTargetSlices(reg Registration, targetNSSAI []SliceID) ([]SliceID, error) {
	supported := make(map[SliceID]bool, len(targetNSSAI))
	for _, slice := range targetNSSAI {
		if !slice.Valid() {
			return nil, fmt.Errorf("sliceauth: invalid target S-NSSAI %+v", slice)
		}
		supported[slice] = true
	}
	var intersection []SliceID
	for _, grant := range reg.AllowedNSSAI {
		if supported[grant.Slice] {
			intersection = append(intersection, grant.Slice)
		}
	}
	sortSlices(intersection)
	return intersection, nil
}
