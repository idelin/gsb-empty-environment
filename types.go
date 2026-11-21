package sliceauth

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// NoSD is the default/absent SD encoding defined in TS 23.003 section 28.4.2.
// Every S-NSSAI handled by this package is normalized to NoSD when it carries
// no explicit SD, so that an absent SD and an explicit 0xFFFFFF SD compare
// equal while any other explicit SD is a distinct slice (no wildcard matching).
const NoSD uint32 = 0xFFFFFF

// SliceID is a normalized S-NSSAI: a Single Network Slice Selection Type
// (SST) paired with a Slice Differentiator (SD). The zero value is invalid;
// build values with NewSliceID.
type SliceID struct {
	SST uint8
	SD  uint32
}

// NewSliceID builds and validates an S-NSSAI. A nil sd means "no SD" and is
// normalized to NoSD (0xFFFFFF) per TS 23.003.
func NewSliceID(sst uint8, sd *uint32) (SliceID, error) {
	if sst == 0 {
		return SliceID{}, errors.New("sliceauth: SST must be in 1..255")
	}
	id := SliceID{SST: sst, SD: NoSD}
	if sd != nil {
		if *sd > NoSD {
			return SliceID{}, fmt.Errorf("sliceauth: SD %#x exceeds 0xFFFFFF", *sd)
		}
		id.SD = *sd
	}
	return id, nil
}

// MustSliceID is a test/configuration helper that panics on an invalid S-NSSAI.
func MustSliceID(sst uint8, sd uint32) SliceID {
	id, err := NewSliceID(sst, &sd)
	if err != nil {
		panic(err)
	}
	return id
}

// DefaultSliceID builds an S-NSSAI without an SD (normalized to NoSD).
func DefaultSliceID(sst uint8) (SliceID, error) {
	return NewSliceID(sst, nil)
}

// Valid reports whether the S-NSSAI is well formed.
func (s SliceID) Valid() bool {
	return s.SST != 0 && s.SD <= NoSD
}

func (s SliceID) String() string {
	if s.SD == NoSD {
		return fmt.Sprintf("SST=%d", s.SST)
	}
	return fmt.Sprintf("SST=%d,SD=%06X", s.SST, s.SD)
}

// PLMNID identifies a PLMN by MCC/MNC (2- or 3-digit MNC).
type PLMNID struct {
	MCC string
	MNC string
}

// NewPLMNID validates and builds a PLMN identity.
func NewPLMNID(mcc, mnc string) (PLMNID, error) {
	if len(mcc) != 3 || !allDigits(mcc) {
		return PLMNID{}, fmt.Errorf("sliceauth: invalid MCC %q", mcc)
	}
	if (len(mnc) != 2 && len(mnc) != 3) || !allDigits(mnc) {
		return PLMNID{}, fmt.Errorf("sliceauth: invalid MNC %q", mnc)
	}
	return PLMNID{MCC: mcc, MNC: mnc}, nil
}

// Valid reports whether the PLMN identity is well formed.
func (p PLMNID) Valid() bool {
	_, err := NewPLMNID(p.MCC, p.MNC)
	return err == nil
}

func (p PLMNID) String() string {
	return p.MCC + "-" + p.MNC
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// RoamingRule expresses whether a home slice may be served through a visited
// PLMN S-NSSAI. A home S-NSSAI without an allowed rule for a visited PLMN is
// home-only: presenting it while roaming is rejected, never silently mapped.
type RoamingRule struct {
	VisitedPLMN PLMNID
	// MappedSlice is the visited-PLMN S-NSSAI that maps to the home slice,
	// carried as the mapped S-NSSAI in roaming signalling.
	MappedSlice SliceID
	Allowed     bool
}

// SliceSubscription is the provisioned authorization record for one home
// S-NSSAI: its allowed DNNs, the QoS policy generation bound to the slice and
// the per-visited-PLMN roaming rules.
type SliceSubscription struct {
	Slice               SliceID
	AllowedDNNs         []string
	QoSPolicyGeneration uint64
	Roaming             []RoamingRule
}

// GrantedSlice is an authorization decision: a serving S-NSSAI that has been
// verified against the subscription, together with the DNNs and QoS policy
// generation derived from the subscription (never from a peer node context).
type GrantedSlice struct {
	Slice               SliceID
	HomeSlice           SliceID
	AllowedDNNs         []string
	QoSPolicyGeneration uint64
}

// Roaming reports whether the grant uses a visited-PLMN mapped S-NSSAI.
func (g GrantedSlice) Roaming() bool {
	return g.Slice != g.HomeSlice
}

// Registration carries the S-NSSAI sets involved in UE registration.
// RequestedNSSAI is input; AllowedNSSAI is output and must be nil/empty on
// input — the authorizer refuses pre-populated allowed lists so that an old
// AMF context can never be inherited wholesale.
type Registration struct {
	AMFID          string
	ServingPLMN    PLMNID
	RequestedNSSAI []SliceID
	AllowedNSSAI   []GrantedSlice
}

// PDUSession is the slice-relevant part of a PDU session context held by an
// SMF. Every field is re-verified on mobility procedures; nothing here is
// trusted as proof of prior authorization.
type PDUSession struct {
	ID                  uint32
	ServingSlice        SliceID
	SMFID               string
	DNN                 string
	QoSPolicyGeneration uint64
}

// RejectReason is the component-internal reason for a denial. Each reason maps
// to a standard 5GMM or 5GSM cause code depending on the procedure layer.
type RejectReason string

const (
	ReasonSliceNotSubscribed       RejectReason = "slice_not_subscribed"
	ReasonSDMismatch               RejectReason = "sd_mismatch"
	ReasonRoamingRestricted        RejectReason = "roaming_restricted"
	ReasonSliceNotInAllowedNSSAI   RejectReason = "slice_not_in_allowed_nssai"
	ReasonSliceUnsupportedByTarget RejectReason = "slice_unsupported_by_target"
	ReasonDNNNotSubscribed         RejectReason = "dnn_not_subscribed"
	ReasonPolicyGenerationMismatch RejectReason = "qos_policy_generation_mismatch"
	ReasonNoSlicesAvailable        RejectReason = "no_network_slices_available"
)

// Rejection binds an internal reason to the standard cause code returned to
// the peer and a human-readable detail string. A zero Cause.Code means allow.
type Rejection struct {
	Reason RejectReason
	Cause  Cause
	Detail string
}

// Denied reports whether the rejection carries a mapped cause code.
func (r Rejection) Denied() bool {
	return r.Cause.Code != 0
}

func (r Rejection) String() string {
	return fmt.Sprintf("%s %s -> cause %d (%s): %s", r.Cause.Layer, r.Reason, r.Cause.Code, r.Cause.Name, r.Detail)
}

// RejectedSlice is a denied standalone S-NSSAI (registration, AMF reselection,
// newly requested handover slices).
type RejectedSlice struct {
	Slice SliceID
	Rejection
}

// RejectedSession is a PDU session that must not be moved to the target node.
type RejectedSession struct {
	PDUSessionID uint32
	Rejection
}

// AcceptedSession is a PDU session admitted to the target node with the grant
// freshly derived from the subscription.
type AcceptedSession struct {
	PDUSessionID uint32
	Grant        GrantedSlice
}

// AMFCapability describes the NSSAI a candidate target AMF supports. The
// information may only remove slices during AMF reselection, never add them.
type AMFCapability struct {
	AMFID          string
	SupportedNSSAI []SliceID
}

// HandoverRequest carries the S-NSSAI sets involved in a handover. Deliberately
// there is no field for the source allowed NSSAI: handover authorization is an
// intersection of subscription and target RAN NSSAI, not a copy of the old list.
type HandoverRequest struct {
	TargetPLMN            PLMNID
	TargetAMFID           string
	TargetRANAllowedNSSAI []SliceID
	Sessions              []PDUSession
	NewSlices             []SliceID
}

// HandoverDecision is the per-session and per-new-slice handover result.
type HandoverDecision struct {
	AllowedSessions   []AcceptedSession
	RejectedSessions  []RejectedSession
	GrantedNewSlices  []GrantedSlice
	RejectedNewSlices []RejectedSlice
	OverallRejection  *Rejection
}

// AllowedSlices returns every distinct S-NSSAI admitted at the target node.
func (d HandoverDecision) AllowedSlices() []SliceID {
	seen := make(map[SliceID]bool)
	for _, accepted := range d.AllowedSessions {
		seen[accepted.Grant.Slice] = true
	}
	for _, granted := range d.GrantedNewSlices {
		seen[granted.Slice] = true
	}
	out := make([]SliceID, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sortSlices(out)
	return out
}

func sortSlices(slices []SliceID) {
	sort.Slice(slices, func(i, j int) bool {
		if slices[i].SST != slices[j].SST {
			return slices[i].SST < slices[j].SST
		}
		return slices[i].SD < slices[j].SD
	})
}

func normalizeDNN(dnn string) string {
	return strings.ToLower(strings.TrimSpace(dnn))
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
