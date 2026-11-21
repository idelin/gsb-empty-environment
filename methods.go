package sliceauth

import (
	"errors"
	"fmt"
)

// AuthorizeRegistration builds the allowed NSSAI from the requested NSSAI by
// verifying every S-NSSAI against the subscription and the serving PLMN
// roaming rules. The input Registration must not carry a pre-populated
// AllowedNSSAI: inherited allowed lists from an old AMF are refused outright.
func (a *Authorizer) AuthorizeRegistration(reg Registration) (Registration, []RejectedSlice, error) {
	if !reg.ServingPLMN.Valid() {
		return Registration{}, nil, errors.New("sliceauth: registration has an invalid serving PLMN")
	}
	if reg.AMFID == "" {
		return Registration{}, nil, errors.New("sliceauth: registration requires an AMF ID")
	}
	if len(reg.AllowedNSSAI) != 0 {
		return Registration{}, nil, errors.New("sliceauth: AllowedNSSAI must be built by this component, not supplied")
	}
	seen := make(map[SliceID]bool)
	var granted []GrantedSlice
	var rejected []RejectedSlice
	for _, requested := range reg.RequestedNSSAI {
		if !requested.Valid() {
			return Registration{}, nil, fmt.Errorf("sliceauth: invalid requested S-NSSAI %+v", requested)
		}
		if seen[requested] {
			continue
		}
		seen[requested] = true
		grant, denial := a.resolve(requested, reg.ServingPLMN)
		if denial != nil {
			rejected = append(rejected, RejectedSlice{Slice: requested, Rejection: *denial})
			continue
		}
		granted = append(granted, grant)
	}
	sortGrants(granted)
	out := reg
	out.AllowedNSSAI = granted
	out.RequestedNSSAI = append([]SliceID(nil), reg.RequestedNSSAI...)
	return out, rejected, nil
}

// AuthorizePDUSession re-verifies a PDU session's slice, DNN and QoS policy
// generation against the subscription and the registration allowed NSSAI.
// Nothing is taken from the SMF context on trust. A nil Rejection means allow.
func (a *Authorizer) AuthorizePDUSession(reg Registration, session PDUSession) (*GrantedSlice, *Rejection) {
	if !reg.ServingPLMN.Valid() {
		return nil, ptr(reject(ReasonSliceNotInAllowedNSSAI, Layer5GSM, "registration has an invalid serving PLMN"))
	}
	if !session.ServingSlice.Valid() {
		return nil, ptr(reject(ReasonSliceNotSubscribed, Layer5GSM, "PDU session carries an invalid S-NSSAI"))
	}
	if normalizeDNN(session.DNN) == "" {
		return nil, ptr(reject(ReasonDNNNotSubscribed, Layer5GSM, "PDU session carries an empty DNN"))
	}
	if !grantList(reg.AllowedNSSAI).contains(session.ServingSlice) {
		return nil, ptr(reject(ReasonSliceNotInAllowedNSSAI, Layer5GSM,
			"S-NSSAI %s is not in the registration allowed NSSAI", session.ServingSlice))
	}
	grant, denial := a.resolve(session.ServingSlice, reg.ServingPLMN)
	if denial != nil {
		denial.Cause = mapCause(denial.Reason, Layer5GSM)
		return nil, denial
	}
	if !containsString(grant.AllowedDNNs, normalizeDNN(session.DNN)) {
		return nil, ptr(reject(ReasonDNNNotSubscribed, Layer5GSM,
			"DNN %q is not subscribed for S-NSSAI %s", session.DNN, session.ServingSlice))
	}
	if session.QoSPolicyGeneration != grant.QoSPolicyGeneration {
		return nil, ptr(reject(ReasonPolicyGenerationMismatch, Layer5GSM,
			"QoS policy generation %d for S-NSSAI %s is stale; subscription generation is %d",
			session.QoSPolicyGeneration, session.ServingSlice, grant.QoSPolicyGeneration))
	}
	return &grant, nil
}
