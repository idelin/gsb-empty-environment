package changeaccept

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// CertificateState is one reachable product state (endpoint, label, original
// label). Started distinguishes an actual path from the zero-hop root.
type CertificateState struct {
	Endpoint      EndpointID            `json:"endpoint"`
	Label         Label                 `json:"label"`
	InitialLabel  Label                 `json:"initial_label"`
	Started       bool                  `json:"started"`
	Destination   bool                  `json:"destination"`
	Authorization *AuthorizationWitness `json:"authorization,omitempty"`
}

// AuthorizationWitness records the exact first matching end-to-end rule, or -1
// for default deny.
type AuthorizationWitness struct {
	Allowed bool `json:"allowed"`
	Rule    int  `json:"rule"`
}

// CertificateTransition is one policy-enabled hop. Both decisions and the
// concrete label change are included for independent re-evaluation.
type CertificateTransition struct {
	FromEndpoint EndpointID `json:"from_endpoint"`
	FromLabel    Label      `json:"from_label"`
	InitialLabel Label      `json:"initial_label"`
	FromStarted  bool       `json:"from_started"`
	ToEndpoint   EndpointID `json:"to_endpoint"`
	ToLabel      Label      `json:"to_label"`
	EgressRule   int        `json:"egress_rule"`
	IngressRule  int        `json:"ingress_rule"`
}

// CoverageCertificate proves that the listed finite state graph is the complete
// reachable graph for pair and that every destination state is authorized. A
// checker recomputes the graph from the snapshot; it does not trust the builder.
type CoverageCertificate struct {
	Version        int                     `json:"version"`
	Generation     Generation              `json:"generation"`
	SnapshotDigest string                  `json:"snapshot_digest"`
	Pair           TenantPair              `json:"pair"`
	States         []CertificateState      `json:"states"`
	Transitions    []CertificateTransition `json:"transitions"`
	Digest         string                  `json:"digest"`
}

// BuildCoverageCertificate explores one tenant pair exhaustively. A non-empty
// certificate may carry denied destination witnesses; its Outcome is then
// NonCompliant. Budget exhaustion returns Undetermined and no certificate.
func BuildCoverageCertificate(snapshot Snapshot, pair TenantPair, budget Budget) (*CoverageCertificate, Outcome, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, OutcomeUndetermined, err
	}
	if !snapshot.Auth.IsManaged(pair.Source) || !snapshot.Auth.IsManaged(pair.Destination) {
		return nil, OutcomeUndetermined, fmt.Errorf("pair %q -> %q is not fully managed", pair.Source, pair.Destination)
	}
	m := newMeter(budget)
	exploration, err := explorePair(snapshot, pair, snapshotLabels(snapshot, nil), m)
	if err != nil {
		if err == ErrBudgetExhausted {
			return nil, OutcomeUndetermined, err
		}
		return nil, OutcomeUndetermined, err
	}
	certificate := certificateFromExploration(snapshot, exploration)
	outcome := OutcomeCompliant
	for _, state := range certificate.States {
		if state.Authorization != nil && !state.Authorization.Allowed {
			outcome = OutcomeNonCompliant
			break
		}
	}
	return certificate, outcome, nil
}

// CheckCoverageCertificate independently re-explores the snapshot, compares the
// complete state/transition graph, checks every witness, and verifies both
// digests. A returned error means the certificate is malformed or tampered
// with; OutcomeNonCompliant means the graph was covered but contains a denied
// destination.
func CheckCoverageCertificate(snapshot Snapshot, certificate CoverageCertificate, budget Budget) (Outcome, error) {
	if err := snapshot.Validate(); err != nil {
		return OutcomeUndetermined, err
	}
	if certificate.Version != 1 {
		return OutcomeUndetermined, fmt.Errorf("unsupported certificate version %d", certificate.Version)
	}
	if certificate.Generation != snapshot.Generation {
		return OutcomeUndetermined, fmt.Errorf("certificate generation %d does not match snapshot generation %d", certificate.Generation, snapshot.Generation)
	}
	wantSnapshot := SnapshotFingerprint(snapshot)
	if certificate.SnapshotDigest != wantSnapshot {
		return OutcomeUndetermined, fmt.Errorf("snapshot digest mismatch")
	}
	if certificate.Digest != certificateDigest(certificate) {
		return OutcomeUndetermined, fmt.Errorf("certificate digest mismatch")
	}
	if !snapshot.Auth.IsManaged(certificate.Pair.Source) || !snapshot.Auth.IsManaged(certificate.Pair.Destination) {
		return OutcomeUndetermined, fmt.Errorf("certificate pair is not fully managed")
	}

	m := newMeter(budget)
	exploration, err := explorePair(snapshot, certificate.Pair, snapshotLabels(snapshot, nil), m)
	if err != nil {
		if err == ErrBudgetExhausted {
			return OutcomeUndetermined, err
		}
		return OutcomeUndetermined, err
	}
	expected := certificateFromExploration(snapshot, exploration)
	if !reflect.DeepEqual(certificate.States, expected.States) {
		return OutcomeUndetermined, fmt.Errorf("certificate states do not cover the reachable state graph")
	}
	if !reflect.DeepEqual(certificate.Transitions, expected.Transitions) {
		return OutcomeUndetermined, fmt.Errorf("certificate transitions do not cover the reachable transition graph")
	}
	for _, state := range certificate.States {
		if state.Authorization != nil && !state.Authorization.Allowed {
			return OutcomeNonCompliant, nil
		}
	}
	return OutcomeCompliant, nil
}

func certificateFromExploration(snapshot Snapshot, exploration pairExploration) *CoverageCertificate {
	certificate := &CoverageCertificate{
		Version:        1,
		Generation:     snapshot.Generation,
		SnapshotDigest: SnapshotFingerprint(snapshot),
		Pair:           exploration.Pair,
		States:         make([]CertificateState, 0, len(exploration.States)),
		Transitions:    make([]CertificateTransition, 0, len(exploration.Transitions)),
	}
	targets := make(map[EndpointID]struct{})
	for _, endpoint := range snapshot.Topology.Endpoints {
		if endpoint.Tenant == exploration.Pair.Destination {
			targets[endpoint.ID] = struct{}{}
		}
	}
	for _, state := range exploration.States {
		_, destination := targets[state.Key.Endpoint]
		item := CertificateState{
			Endpoint:     state.Key.Endpoint,
			Label:        state.Key.Label,
			InitialLabel: state.Key.Initial,
			Started:      state.Key.Started,
			Destination:  destination,
		}
		if state.Key.Started && destination {
			decision := snapshot.Auth.Decide(exploration.Pair, state.Key.Initial, state.Key.Label)
			item.Authorization = &AuthorizationWitness{
				Allowed: decision.allowed(),
				Rule:    decision.Rule,
			}
		}
		certificate.States = append(certificate.States, item)
	}
	for _, transition := range exploration.Transitions {
		certificate.Transitions = append(certificate.Transitions, CertificateTransition{
			FromEndpoint: transition.From.Endpoint,
			FromLabel:    transition.From.Label,
			InitialLabel: transition.From.Initial,
			FromStarted:  transition.From.Started,
			ToEndpoint:   transition.To.Endpoint,
			ToLabel:      transition.To.Label,
			EgressRule:   transition.EgressRule,
			IngressRule:  transition.IngressRule,
		})
	}
	certificate.Digest = certificateDigest(*certificate)
	return certificate
}

func certificateDigest(certificate CoverageCertificate) string {
	certificate.Digest = ""
	encoded, err := json.Marshal(certificate)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type fingerprintRule struct {
	Labels []Label `json:"labels,omitempty"`
	Action Action  `json:"action"`
}

type fingerprintEndpoint struct {
	ID           EndpointID        `json:"id"`
	Tenant       TenantID          `json:"tenant"`
	Ingress      []fingerprintRule `json:"ingress"`
	Egress       []fingerprintRule `json:"egress"`
	ChangeStrict bool              `json:"change_strict"`
	ChangeFrom   []Label           `json:"change_from,omitempty"`
	ChangeTo     [][]Label         `json:"change_to,omitempty"`
}

type fingerprintPair struct {
	Source      TenantID              `json:"source"`
	Destination TenantID              `json:"destination"`
	Rules       []fingerprintPairRule `json:"rules"`
}

type fingerprintPairRule struct {
	Source      []Label `json:"source,omitempty"`
	Destination []Label `json:"destination,omitempty"`
	Action      Action  `json:"action"`
}

type snapshotFingerprintBody struct {
	Generation Generation            `json:"generation"`
	Labels     []Label               `json:"labels,omitempty"`
	Endpoints  []fingerprintEndpoint `json:"endpoints"`
	Edges      []Edge                `json:"edges"`
	Managed    []TenantID            `json:"managed"`
	Pairs      []fingerprintPair     `json:"pairs"`
}

// SnapshotFingerprint is a deterministic SHA-256 binding of the complete
// topology, label graphs, policies, authorizer, and generation.
func SnapshotFingerprint(snapshot Snapshot) string {
	body := snapshotFingerprintBody{
		Generation: snapshot.Generation,
		Labels:     normalizeLabels(snapshot.Labels),
	}
	endpoints := append([]Endpoint(nil), snapshot.Topology.Endpoints...)
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].ID < endpoints[j].ID })
	for _, endpoint := range endpoints {
		item := fingerprintEndpoint{
			ID:           endpoint.ID,
			Tenant:       endpoint.Tenant,
			Ingress:      fingerprintPolicy(endpoint.Ingress),
			Egress:       fingerprintPolicy(endpoint.Egress),
			ChangeStrict: endpoint.Changes.Edges != nil,
		}
		if endpoint.Changes.Edges != nil {
			froms := make([]Label, 0, len(endpoint.Changes.Edges))
			for from := range endpoint.Changes.Edges {
				froms = append(froms, from)
			}
			sort.Slice(froms, func(i, j int) bool { return froms[i] < froms[j] })
			for _, from := range froms {
				item.ChangeFrom = append(item.ChangeFrom, from)
				item.ChangeTo = append(item.ChangeTo, normalizeLabels(endpoint.Changes.Edges[from]))
			}
		}
		body.Endpoints = append(body.Endpoints, item)
	}
	body.Edges = append([]Edge(nil), snapshot.Topology.Edges...)
	sort.Slice(body.Edges, func(i, j int) bool {
		if body.Edges[i].From != body.Edges[j].From {
			return body.Edges[i].From < body.Edges[j].From
		}
		return body.Edges[i].To < body.Edges[j].To
	})
	if snapshot.Auth != nil {
		body.Managed = snapshot.Auth.managedTenants()
		for _, pair := range snapshot.Auth.Pairs() {
			item := fingerprintPair{Source: pair.Source, Destination: pair.Destination}
			for _, rule := range snapshot.Auth.PairPolicy(pair) {
				item.Rules = append(item.Rules, fingerprintPairRule{
					Source:      normalizeLabels(rule.Source.Labels),
					Destination: normalizeLabels(rule.Destination.Labels),
					Action:      rule.Action,
				})
			}
			body.Pairs = append(body.Pairs, item)
		}
	}
	encoded, _ := json.Marshal(body)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func fingerprintPolicy(policy Policy) []fingerprintRule {
	result := make([]fingerprintRule, 0, len(policy))
	for _, rule := range policy {
		result = append(result, fingerprintRule{
			Labels: normalizeLabels(rule.Match.Labels),
			Action: rule.Action,
		})
	}
	return result
}
