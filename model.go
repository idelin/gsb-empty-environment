package netsec

import (
	"strings"
	"unicode"
)

// RoamingRestriction is a compact boolean form of a roaming restriction. It is
// ORed with SlicePolicy.RoamingAllowed so both the compact and explicit forms
// are accepted.
type RoamingRestriction bool

// SlicePolicy is the authoritative subscription policy for one S-NSSAI. A
// subscription policy binds the slice identity to roaming permission, a DNN,
// and a QoS policy generation.
type SlicePolicy struct {
	SNSSAI SNSSAI

	// Slice is accepted as an alternative spelling of SNSSAI. At most one of
	// SNSSAI and Slice should be populated; conflicting values are rejected.
	Slice SNSSAI

	RoamingAllowed bool
	Roaming        RoamingRestriction

	// AllowedPLMNs, when non-empty, is an allow-list for the serving PLMN while
	// roaming. An explicit list takes precedence over RoamingAllowed.
	AllowedPLMNs []string

	DNN           string
	QoSGeneration int

	// QoSVersion and PolicyGeneration are accepted as alternative spellings
	// of QoSGeneration. If more than one is set, they must agree.
	QoSVersion       int
	PolicyGeneration int
}

// Subscription is the authoritative set of slices and their policies.
type Subscription struct {
	Slices []SlicePolicy

	// AllowedSlices is accepted as an alternative spelling of Slices.
	AllowedSlices []SlicePolicy
}

// Registration carries the S-NSSAIs currently allowed for the registration.
type Registration struct {
	AllowedSlices []SNSSAI

	// Slices is accepted as an alternative spelling of AllowedSlices.
	Slices []SNSSAI
}

// PDUSession carries the slices requested or used by a PDU session and the
// DNN/QoS policy generation that must match the subscription binding.
type PDUSession struct {
	Slices        []SNSSAI
	DNN           string
	QoSGeneration int
}

// ServingNetwork describes the home and currently serving PLMNs. Empty PLMN
// values disable the roaming check because the serving relationship is
// unknown; when ServingPLMN is set and HomePLMN is not, the network fails
// closed for slices that do not permit roaming.
type ServingNetwork struct {
	HomePLMN    string
	ServingPLMN string

	// Home and Serving are accepted as shorter alternative spellings.
	Home    string
	Serving string
}

// SMFContext is an old SMF context. Its slices are untrusted input: they may be
// used as candidates during a handover, but they are never copied into the
// result without full re-authorization.
type SMFContext struct {
	AllowedSlices []SNSSAI

	// Slices is accepted as an alternative spelling of AllowedSlices.
	Slices []SNSSAI
}

func (p SlicePolicy) identity() (SNSSAI, error) {
	primary, primarySet := p.SNSSAI, p.SNSSAI != (SNSSAI{})
	secondary, secondarySet := p.Slice, p.Slice != (SNSSAI{})

	switch {
	case primarySet && secondarySet:
		left, err := primary.Normalize()
		if err != nil {
			return SNSSAI{}, err
		}
		right, err := secondary.Normalize()
		if err != nil {
			return SNSSAI{}, err
		}
		if left != right {
			return SNSSAI{}, errConflictingSlicePolicy
		}
		return left, nil
	case primarySet:
		return primary.Normalize()
	case secondarySet:
		return secondary.Normalize()
	default:
		return SNSSAI{}, errMissingSlicePolicy
	}
}

func (p SlicePolicy) qosGeneration() (int, error) {
	values := []int{p.QoSGeneration, p.QoSVersion, p.PolicyGeneration}
	generation := 0
	for _, value := range values {
		if value == 0 {
			continue
		}
		if generation != 0 && generation != value {
			return 0, errConflictingQoSGeneration
		}
		generation = value
	}
	return generation, nil
}

func (p SlicePolicy) allowsRoaming(home, serving string) bool {
	serving = canonicalPLMN(serving)
	if serving == "" {
		return true
	}

	home = canonicalPLMN(home)
	if home == serving {
		return true
	}

	if len(p.AllowedPLMNs) > 0 {
		for _, plmn := range p.AllowedPLMNs {
			if canonicalPLMN(plmn) == serving {
				return true
			}
		}
		return false
	}

	// If the home PLMN is unknown, the serving network cannot prove that this
	// is a non-roaming case, so only a slice with explicit roaming permission
	// is accepted.
	return p.RoamingAllowed || bool(p.Roaming)
}

func canonicalPLMN(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

func (sn ServingNetwork) home() string {
	if sn.HomePLMN != "" {
		return sn.HomePLMN
	}
	return sn.Home
}

func (sn ServingNetwork) serving() string {
	if sn.ServingPLMN != "" {
		return sn.ServingPLMN
	}
	return sn.Serving
}
