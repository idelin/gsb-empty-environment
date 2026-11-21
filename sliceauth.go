// Package sliceauth is an importable S-NSSAI authorization component for 5G
// core session management (AMF/SMF).
//
// It centralizes the security property that a UE can only use S-NSSAIs that
// are simultaneously (1) subscribed, (2) allowed for the serving PLMN under
// the roaming rules and (3) supported by the target RAN/AMF. Handover never
// copies a source allowed NSSAI: every slice is re-verified against the
// subscription and intersected with the target node NSSAI. Slices carried by
// AMF reselection, session continuity or an old SMF context are never trusted
// as inherited authorization. Policy denials are returned as Rejection values
// mapped to standard TS 24.501 5GMM/5GSM cause codes; only malformed input
// produces a Go error.
//
// An Authorizer is immutable after New and safe for concurrent use.
package sliceauth

import (
	"fmt"
	"sort"
)

// Authorizer evaluates S-NSSAI requests against the provisioned subscription.
type Authorizer struct {
	homePLMN    PLMNID
	subs        map[SliceID]SliceSubscription
	orderedSubs []SliceID
}

// New builds an Authorizer for the home PLMN from the subscribed slices.
// Slices and DNNs are copied and normalized, so later caller mutation cannot
// change the authorization basis.
func New(homePLMN PLMNID, subscriptions []SliceSubscription) (*Authorizer, error) {
	if !homePLMN.Valid() {
		return nil, fmt.Errorf("sliceauth: invalid home PLMN %q/%q", homePLMN.MCC, homePLMN.MNC)
	}
	a := &Authorizer{
		homePLMN: homePLMN,
		subs:     make(map[SliceID]SliceSubscription, len(subscriptions)),
	}
	for _, sub := range subscriptions {
		if !sub.Slice.Valid() {
			return nil, fmt.Errorf("sliceauth: invalid subscribed S-NSSAI %+v", sub.Slice)
		}
		if _, exists := a.subs[sub.Slice]; exists {
			return nil, fmt.Errorf("sliceauth: duplicate subscribed S-NSSAI %s", sub.Slice)
		}
		if sub.QoSPolicyGeneration == 0 {
			return nil, fmt.Errorf("sliceauth: slice %s requires a non-zero QoS policy generation", sub.Slice)
		}
		if len(sub.AllowedDNNs) == 0 {
			return nil, fmt.Errorf("sliceauth: slice %s must bind at least one DNN", sub.Slice)
		}
		dnnSet := make(map[string]bool, len(sub.AllowedDNNs))
		dnns := make([]string, 0, len(sub.AllowedDNNs))
		for _, dnn := range sub.AllowedDNNs {
			normalized := normalizeDNN(dnn)
			if normalized == "" {
				return nil, fmt.Errorf("sliceauth: slice %s has an empty DNN", sub.Slice)
			}
			if dnnSet[normalized] {
				return nil, fmt.Errorf("sliceauth: duplicate DNN %q on slice %s", dnn, sub.Slice)
			}
			dnnSet[normalized] = true
			dnns = append(dnns, normalized)
		}
		mapped := make(map[PLMNID]map[SliceID]bool)
		rules := make([]RoamingRule, 0, len(sub.Roaming))
		for _, rule := range sub.Roaming {
			if !rule.VisitedPLMN.Valid() {
				return nil, fmt.Errorf("sliceauth: slice %s has an invalid visited PLMN", sub.Slice)
			}
			if rule.VisitedPLMN == homePLMN {
				return nil, fmt.Errorf("sliceauth: slice %s cannot carry a roaming rule for the home PLMN", sub.Slice)
			}
			if !rule.MappedSlice.Valid() {
				return nil, fmt.Errorf("sliceauth: slice %s has an invalid mapped S-NSSAI", sub.Slice)
			}
			if _, ok := mapped[rule.VisitedPLMN]; !ok {
				mapped[rule.VisitedPLMN] = make(map[SliceID]bool)
			}
			if mapped[rule.VisitedPLMN][rule.MappedSlice] {
				return nil, fmt.Errorf("sliceauth: duplicate roaming mapping %s in PLMN %s for slice %s",
					rule.MappedSlice, rule.VisitedPLMN, sub.Slice)
			}
			mapped[rule.VisitedPLMN][rule.MappedSlice] = true
			rules = append(rules, rule)
		}
		a.subs[sub.Slice] = SliceSubscription{
			Slice:               sub.Slice,
			AllowedDNNs:         dnns,
			QoSPolicyGeneration: sub.QoSPolicyGeneration,
			Roaming:             rules,
		}
		a.orderedSubs = append(a.orderedSubs, sub.Slice)
	}
	return a, nil
}

// HomePLMN returns the home PLMN the authorizer was provisioned for.
func (a *Authorizer) HomePLMN() PLMNID {
	return a.homePLMN
}

// resolve verifies one serving S-NSSAI against the subscription for the
// serving PLMN and returns a freshly derived grant. A policy failure is
// reported as Rejection (mappable to a standard cause), never as a Go error.
func (a *Authorizer) resolve(serving SliceID, servingPLMN PLMNID) (GrantedSlice, *Rejection) {
	if servingPLMN == a.homePLMN {
		sub, ok := a.subs[serving]
		if !ok {
			return GrantedSlice{}, a.homeReject(serving)
		}
		return grantFrom(sub, serving, sub.Slice), nil
	}
	for _, homeSlice := range a.orderedSubs {
		sub := a.subs[homeSlice]
		for _, rule := range sub.Roaming {
			if rule.VisitedPLMN == servingPLMN && rule.MappedSlice == serving {
				if !rule.Allowed {
					return GrantedSlice{}, ptr(reject(
						ReasonRoamingRestricted, Layer5GMM,
						"mapped S-NSSAI %s is explicitly barred in PLMN %s", serving, servingPLMN))
				}
				return grantFrom(sub, serving, homeSlice), nil
			}
		}
	}
	return GrantedSlice{}, ptr(a.roamingReject(serving, servingPLMN))
}

func (a *Authorizer) homeReject(serving SliceID) *Rejection {
	for homeSlice := range a.subs {
		if homeSlice.SST == serving.SST {
			return ptr(reject(ReasonSDMismatch, Layer5GMM,
				"SST %d is subscribed but SD %06X is not (subscribed SD %06X)",
				serving.SST, serving.SD, homeSlice.SD))
		}
	}
	return ptr(reject(ReasonSliceNotSubscribed, Layer5GMM,
		"S-NSSAI %s is not part of the subscriber data", serving))
}

func (a *Authorizer) roamingReject(serving SliceID, servingPLMN PLMNID) Rejection {
	for _, homeSlice := range a.orderedSubs {
		sub := a.subs[homeSlice]
		for _, rule := range sub.Roaming {
			if rule.VisitedPLMN != servingPLMN {
				continue
			}
			if rule.MappedSlice == serving && !rule.Allowed {
				return reject(ReasonRoamingRestricted, Layer5GMM,
					"mapped S-NSSAI %s is barred in PLMN %s", serving, servingPLMN)
			}
			if rule.MappedSlice.SST == serving.SST {
				return reject(ReasonSDMismatch, Layer5GMM,
					"SST %d has a mapping in PLMN %s but SD %06X does not match",
					serving.SST, servingPLMN, serving.SD)
			}
		}
		if homeSlice.SST == serving.SST {
			return reject(ReasonRoamingRestricted, Layer5GMM,
				"home slice %s has no allowed mapping for PLMN %s", homeSlice, servingPLMN)
		}
	}
	return reject(ReasonSliceNotSubscribed, Layer5GMM,
		"S-NSSAI %s is not part of the subscriber data", serving)
}

func grantFrom(sub SliceSubscription, serving, home SliceID) GrantedSlice {
	dnns := append([]string(nil), sub.AllowedDNNs...)
	return GrantedSlice{
		Slice:               serving,
		HomeSlice:           home,
		AllowedDNNs:         dnns,
		QoSPolicyGeneration: sub.QoSPolicyGeneration,
	}
}

func ptr(r Rejection) *Rejection {
	return &r
}

func rejectedSession(id uint32, r Rejection) RejectedSession {
	return RejectedSession{PDUSessionID: id, Rejection: r}
}

type grantList []GrantedSlice

func (grants grantList) contains(slice SliceID) bool {
	for _, grant := range grants {
		if grant.Slice == slice {
			return true
		}
	}
	return false
}

func (grants grantList) find(slice SliceID) (GrantedSlice, bool) {
	for _, grant := range grants {
		if grant.Slice == slice {
			return grant, true
		}
	}
	return GrantedSlice{}, false
}

func sortGrants(grants []GrantedSlice) {
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Slice.SST != grants[j].Slice.SST {
			return grants[i].Slice.SST < grants[j].Slice.SST
		}
		return grants[i].Slice.SD < grants[j].Slice.SD
	})
}
