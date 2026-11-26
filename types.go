package delegation

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type Effect string

const (
	EffectDeny  Effect = "deny"
	EffectAllow Effect = "allow"
)

type TagPredicate struct {
	RequireAll []string `json:"require_all,omitempty"`
	ForbidAll  []string `json:"forbid_all,omitempty"`
}

type Rule struct {
	Pattern   string       `json:"pattern"`
	Predicate TagPredicate `json:"predicate"`
	Effect    Effect       `json:"effect"`
	Start     int64        `json:"start"`
	End       int64        `json:"end"`
}

type RuleRef struct {
	DelegationID string `json:"delegation_id"`
	RuleIndex    int    `json:"rule_index"`
	Effect       Effect `json:"effect"`
}

type ParentRef struct {
	DelegationID string `json:"delegation_id"`
}

type Delegation struct {
	ID        string      `json:"id"`
	Tenant    string      `json:"tenant"`
	Action    string      `json:"action"`
	Rules     []Rule      `json:"rules"`
	Parents   []ParentRef `json:"parents,omitempty"`
	QuorumK   int         `json:"quorum_k"`
	SignerKey string      `json:"signer_key"`
}

type SignedDelegation struct {
	Delegation Delegation `json:"delegation"`
	Signature  []byte     `json:"signature"`
}

type LabelConstraints struct {
	MutexPairs   [][2]string   `json:"mutex_pairs,omitempty"`
	Required     []string      `json:"required,omitempty"`
	Implications []Implication `json:"implications,omitempty"`
}

type Implication struct {
	IfLabel   string `json:"if_label"`
	ThenLabel string `json:"then_label"`
}

type Universe struct {
	Tenant           string                       `json:"tenant"`
	Action           string                       `json:"action"`
	PathAlphabet     []string                     `json:"path_alphabet"`
	Labels           []string                     `json:"labels"`
	LabelRules       LabelConstraints             `json:"label_rules"`
	Delegations      []SignedDelegation           `json:"delegations"`
	KeyRing          map[string]ed25519.PublicKey `json:"-"`
	RootDelegationID string                       `json:"root_delegation_id"`
}

type Tuple struct {
	Path      []string `json:"path"`
	Tags      []string `json:"tags"`
	Timestamp int64    `json:"timestamp"`
}

type FailedParentAttribution struct {
	DelegationID string `json:"delegation_id"`
	ParentID     string `json:"parent_id"`
	Reason       string `json:"reason"`
}

type FailedQuorumAttribution struct {
	DelegationID    string   `json:"delegation_id"`
	Required        int      `json:"required"`
	Achieved        int      `json:"achieved"`
	AllowingParents []string `json:"allowing_parents"`
}

type Counterexample struct {
	Path         []string                 `json:"path"`
	Tags         []string                 `json:"tags"`
	Timestamp    int64                    `json:"timestamp"`
	FailedParent *FailedParentAttribution `json:"failed_parent,omitempty"`
	FailedQuorum *FailedQuorumAttribution `json:"failed_quorum,omitempty"`
}

type RuleEvaluation struct {
	DelegationID string `json:"delegation_id"`
	RuleIndex    int    `json:"rule_index"`
	Matched      bool   `json:"matched"`
	Effect       Effect `json:"effect,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type Explanation struct {
	Tuple       Tuple             `json:"tuple"`
	Decision    Effect            `json:"decision"`
	Evaluations []RuleEvaluation  `json:"evaluations"`
	ParentVotes map[string]Effect `json:"parent_votes,omitempty"`
	QuorumK     int               `json:"quorum_k,omitempty"`
}

func CanonicalDelegationJSON(d Delegation) ([]byte, error) {
	copy := d
	copy.Rules = append([]Rule(nil), d.Rules...)
	for i := range copy.Rules {
		copy.Rules[i].Predicate.RequireAll = sortedCopy(d.Rules[i].Predicate.RequireAll)
		copy.Rules[i].Predicate.ForbidAll = sortedCopy(d.Rules[i].Predicate.ForbidAll)
	}
	copy.Parents = append([]ParentRef(nil), d.Parents...)
	slices.SortFunc(copy.Parents, func(a, b ParentRef) int {
		return strings.Compare(a.DelegationID, b.DelegationID)
	})
	return json.Marshal(copy)
}

func SignDelegation(d Delegation, private ed25519.PrivateKey) (SignedDelegation, error) {
	if len(private) != ed25519.PrivateKeySize {
		return SignedDelegation{}, fmt.Errorf("delegation %q: invalid ed25519 private key", d.ID)
	}
	encoded, err := CanonicalDelegationJSON(d)
	if err != nil {
		return SignedDelegation{}, err
	}
	return SignedDelegation{
		Delegation: d,
		Signature:  ed25519.Sign(private, encoded),
	}, nil
}

func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	slices.Sort(out)
	return slices.Compact(out)
}

func patternTokens(pattern string) ([]string, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty path pattern")
	}
	tokens := strings.Split(pattern, "/")
	for _, token := range tokens {
		if token == "" {
			return nil, fmt.Errorf("pattern %q contains an empty segment", pattern)
		}
	}
	return tokens, nil
}
