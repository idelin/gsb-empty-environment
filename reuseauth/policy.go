package reuseauth

import (
	"fmt"
)

// PolicyAction identifies a reuse or late-callback operation.
type PolicyAction string

const (
	ActionAny      PolicyAction = "*"
	ActionRead     PolicyAction = "read"
	ActionComplete PolicyAction = "complete"
	ActionError    PolicyAction = "error"
	ActionLength   PolicyAction = "length"
)

// Visibility selects whether a policy decision applies to public or private
// response objects.
type Visibility uint8

const (
	VisibilityAny Visibility = iota
	VisibilityPublic
	VisibilityPrivate
)

func (v Visibility) matches(public bool) bool {
	switch v {
	case VisibilityAny:
		return true
	case VisibilityPublic:
		return public
	case VisibilityPrivate:
		return !public
	default:
		return false
	}
}

// PolicyDecision is one append-only source-policy history record. Sequence is
// navigation scoped and gapless. Epoch/context fields are filled by
// Navigation.AppendDecision when omitted.
type PolicyDecision struct {
	Sequence       uint64         `json:"sequence"`
	Epoch          Epoch          `json:"epoch"`
	At             int64          `json:"at"`
	TopLevelSite   string         `json:"top_level_site"`
	CredentialMode CredentialMode `json:"credential_mode"`
	Source         Origin         `json:"source"`
	Target         Origin         `json:"target"`
	Action         PolicyAction   `json:"action"`
	Visibility     Visibility     `json:"visibility"`
	Allow          bool           `json:"allow"`
}

// PolicyQuery is evaluated against the current navigation generation.
type PolicyQuery struct {
	Action PolicyAction
	Source Origin
	Target Origin
	Public bool
}

// Navigation is the current navigation generation plus its gapless policy
// history. Mutations are expected to be synchronized by Broker or the caller.
type Navigation struct {
	ID             NavID            `json:"id"`
	Epoch          Epoch            `json:"epoch"`
	Recipient      Origin           `json:"recipient"`
	TopLevelSite   string           `json:"top_level_site"`
	CredentialMode CredentialMode   `json:"credential_mode"`
	Revoked        bool             `json:"revoked"`
	History        []PolicyDecision `json:"history,omitempty"`
}

// NewNavigation creates epoch 1 for a navigation.
func NewNavigation(id NavID, recipient Origin, topLevelSite string, mode CredentialMode) (*Navigation, error) {
	if id == "" || recipient.IsZero() || topLevelSite == "" || !mode.Valid() {
		return nil, fmt.Errorf("%w: id, recipient, site, and valid credential mode are required", ErrInvalidArgument)
	}
	return &Navigation{
		ID:             id,
		Epoch:          1,
		Recipient:      recipient,
		TopLevelSite:   topLevelSite,
		CredentialMode: mode,
	}, nil
}

// AppendDecision appends a gapless decision in the current generation.
func (n *Navigation) AppendDecision(d PolicyDecision) error {
	if n == nil {
		return fmt.Errorf("%w: nil navigation", ErrInvalidArgument)
	}
	expected := uint64(len(n.History) + 1)
	if d.Sequence == 0 {
		d.Sequence = expected
	} else if d.Sequence != expected {
		return fmt.Errorf("%w: policy sequence %d, expected %d", ErrInvalidArgument, d.Sequence, expected)
	}
	if d.Epoch == 0 {
		d.Epoch = n.Epoch
	} else if d.Epoch != n.Epoch {
		return fmt.Errorf("%w: policy epoch %d is not current epoch %d", ErrEpoch, d.Epoch, n.Epoch)
	}
	if d.TopLevelSite == "" {
		d.TopLevelSite = n.TopLevelSite
	} else if d.TopLevelSite != n.TopLevelSite {
		return fmt.Errorf("%w: policy site %q does not match %q", ErrPolicyDenied, d.TopLevelSite, n.TopLevelSite)
	}
	if d.CredentialMode == "" {
		d.CredentialMode = n.CredentialMode
	} else if d.CredentialMode != n.CredentialMode {
		return fmt.Errorf("%w: policy credential mode %q does not match %q", ErrPolicyDenied, d.CredentialMode, n.CredentialMode)
	}
	if d.Action == "" {
		d.Action = ActionAny
	}
	if d.At == 0 {
		d.At = n.lastAt() + 1
	}
	n.History = append(n.History, d)
	return nil
}

// Allow is a concise AppendDecision with Allow=true.
func (n *Navigation) Allow(d PolicyDecision) error {
	d.Allow = true
	return n.AppendDecision(d)
}

// Deny is a concise AppendDecision with Allow=false.
func (n *Navigation) Deny(d PolicyDecision) error {
	d.Allow = false
	return n.AppendDecision(d)
}

func (n *Navigation) lastAt() int64 {
	if len(n.History) == 0 {
		return 0
	}
	return n.History[len(n.History)-1].At
}

// Revoke fences the current generation and advances the epoch.
func (n *Navigation) Revoke(at int64) error {
	if n == nil {
		return fmt.Errorf("%w: nil navigation", ErrInvalidArgument)
	}
	if n.Revoked {
		return nil
	}
	n.Revoked = true
	n.Epoch++
	return n.AppendDecision(PolicyDecision{
		At:             at,
		Action:         ActionAny,
		Visibility:     VisibilityAny,
		Allow:          false,
		Source:         AnyOrigin,
		Target:         AnyOrigin,
		TopLevelSite:   n.TopLevelSite,
		CredentialMode: n.CredentialMode,
		Epoch:          n.Epoch,
	})
}

// SwitchTopLevel changes the top-level site and starts a fresh fenced epoch.
func (n *Navigation) SwitchTopLevel(site string, at int64) error {
	if n == nil {
		return fmt.Errorf("%w: nil navigation", ErrInvalidArgument)
	}
	if site == "" {
		return fmt.Errorf("%w: empty top-level site", ErrInvalidArgument)
	}
	if n.Revoked {
		return fmt.Errorf("%w: cannot switch revoked navigation %s", ErrRevoked, n.ID)
	}
	n.Epoch++
	n.TopLevelSite = site
	// This generation-fence record is intentionally a default-deny decision.
	// A newer explicit decision is required before new-epoch reuse is allowed.
	return n.AppendDecision(PolicyDecision{
		At:             at,
		Action:         ActionAny,
		Visibility:     VisibilityAny,
		Allow:          false,
		Source:         AnyOrigin,
		Target:         AnyOrigin,
		TopLevelSite:   site,
		CredentialMode: n.CredentialMode,
		Epoch:          n.Epoch,
	})
}

// Check evaluates the latest matching decision in the current epoch. Absence
// of an explicit matching allow is denial.
func (n Navigation) Check(q PolicyQuery) error {
	if n.Revoked {
		return fmt.Errorf("%w: navigation %s", ErrRevoked, n.ID)
	}
	if n.Epoch == 0 {
		return fmt.Errorf("%w: navigation epoch missing", ErrEpoch)
	}

	var latest *PolicyDecision
	for i := range n.History {
		d := n.History[i]
		if d.Epoch != n.Epoch || d.TopLevelSite != n.TopLevelSite || d.CredentialMode != n.CredentialMode {
			continue
		}
		if d.Action != ActionAny && d.Action != q.Action {
			continue
		}
		if !OriginMatches(d.Source, q.Source) || !OriginMatches(d.Target, q.Target) {
			continue
		}
		if !d.Visibility.matches(q.Public) {
			continue
		}
		if latest == nil || d.Sequence > latest.Sequence {
			latest = &d
		}
	}
	if latest == nil {
		return fmt.Errorf("%w: no %s policy for %s -> %s", ErrPolicyDenied, q.Action, q.Source, q.Target)
	}
	if !latest.Allow {
		return fmt.Errorf("%w: latest decision %d denies %s", ErrPolicyDenied, latest.Sequence, q.Action)
	}
	return nil
}

func hashPolicyHistory(history []PolicyDecision) Hash {
	return hashString("reuseauth.policy-history.v1", string(mustJSON(history)))
}

func policyPrefixHash(history []PolicyDecision, sequence uint64) (Hash, error) {
	if sequence > uint64(len(history)) {
		return Hash{}, fmt.Errorf("%w: proof policy sequence %d exceeds history length %d", ErrEpoch, sequence, len(history))
	}
	return hashPolicyHistory(history[:sequence]), nil
}

// Clone returns a deep-ish copy safe for use outside Broker's lock.
func (n Navigation) Clone() Navigation {
	if n.History != nil {
		n.History = append([]PolicyDecision(nil), n.History...)
	}
	return n
}
