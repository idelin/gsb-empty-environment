package changeaccept

import (
	"encoding/json"
	"errors"
	"testing"
)

func allow(labels ...Label) Policy {
	return Policy{{Match: Labels(labels...), Action: ActionAllow}}
}

func deny(labels ...Label) Policy {
	return Policy{{Match: Labels(labels...), Action: ActionDeny}}
}

func allowAll() Policy {
	return Policy{{Match: AnyLabel(), Action: ActionAllow}}
}

func denyAll() Policy {
	return Policy{}
}

func mustAuth(t *testing.T, tenants []TenantID, pairs map[TenantPair]PairPolicy) *Authorizer {
	t.Helper()
	auth, err := NewAuthorizer(tenants, pairs)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	return auth
}

func pair(source, destination string) TenantPair {
	return TenantPair{Source: TenantID(source), Destination: TenantID(destination)}
}

func TestOrderedPolicyFirstMatchAndDefaultDeny(t *testing.T) {
	ordered := Policy{
		{Match: Labels("gold"), Action: ActionAllow},
		{Match: AnyLabel(), Action: ActionDeny},
	}
	if got := ordered.Decide("gold"); !got.allowed() || got.Rule != 0 {
		t.Fatalf("gold should match allow rule 0, got %+v", got)
	}
	if got := ordered.Decide("silver"); got.allowed() || got.Rule != 1 {
		t.Fatalf("silver should match deny rule 1, got %+v", got)
	}

	shadowed := Policy{
		{Match: Labels("gold"), Action: ActionDeny},
		{Match: AnyLabel(), Action: ActionAllow},
	}
	if got := shadowed.Decide("gold"); got.allowed() {
		t.Fatalf("later allow must not override the first matching deny: %+v", got)
	}
	if got := (Policy(nil)).Decide("gold"); got.allowed() || got.Rule != -1 {
		t.Fatalf("empty policy must default deny, got %+v", got)
	}
}

func TestCrossPlaneAndLabelChangeMustBothPass(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: allow("gold"), Changes: Passthrough()},
			{ID: "b", Tenant: "tx", Ingress: allow("gold"), Egress: allow("gold"), Changes: ExactGraph(map[Label][]Label{"gold": {"silver"}})},
			{ID: "d", Tenant: "t2", Ingress: allow("silver"), Changes: Passthrough()},
		},
		Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "d"}},
	}
	auth := mustAuth(t, []TenantID{"t1", "tx", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: Labels("gold"), Destination: Labels("silver"), Action: ActionAllow}},
	})
	input := Input{
		Topology: topology,
		Auth:     auth,
		Pairs:    []TenantPair{pair("t1", "t2")},
		Probes: []Probe{{
			ID: "a-to-d", Pair: pair("t1", "t2"), Source: "a", Destination: "d",
		}},
	}
	report, err := input.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Outcome != OutcomeCompliant {
		t.Fatalf("expected compliant, got %s: %+v", report.Outcome, report.Prefixes[0].Violations)
	}
	if len(report.Prefixes[0].Probes) != 1 || report.Prefixes[0].Probes[0].Outcome != OutcomeCompliant {
		t.Fatalf("probe result = %+v", report.Prefixes[0].Probes)
	}
	if got := report.Prefixes[0].Probes[0].Path.Labels; len(got) != 3 || got[0] != "gold" || got[1] != "gold" || got[2] != "silver" {
		t.Fatalf("unexpected witness labels: %v", got)
	}

	topology.Endpoints[2].Ingress = allow("gold")
	input.Topology = topology
	report, err = input.Check()
	if err != nil {
		t.Fatalf("Check after ingress deny: %v", err)
	}
	if report.Outcome != OutcomeNonCompliant {
		t.Fatalf("liveness must fail when destination ingress denies the rewritten label, got %s", report.Outcome)
	}
}

func TestExactLabelGraphHasNoImplicitIdentity(t *testing.T) {
	graph := ExactGraph(map[Label][]Label{"gold": {"silver"}})
	if got := graph.Next("gold"); len(got) != 1 || got[0] != "silver" {
		t.Fatalf("gold successors = %v", got)
	}
	if got := graph.Next("silver"); len(got) != 0 {
		t.Fatalf("exact graph must not invent an identity edge, got %v", got)
	}
	if got := (LabelGraph{}).Next("silver"); len(got) != 1 || got[0] != "silver" {
		t.Fatalf("nil graph should pass through, got %v", got)
	}
}

func TestCycleIsFiniteAndUnauthorizedDestinationStateIsRejected(t *testing.T) {
	base := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: allowAll(), Changes: Passthrough()},
			{ID: "b", Tenant: "tx", Ingress: allowAll(), Egress: allowAll(), Changes: ExactGraph(map[Label][]Label{
				"gold":   {"silver"},
				"silver": {"gold"},
			})},
			{ID: "d", Tenant: "t2", Ingress: allowAll(), Changes: Passthrough()},
		},
		Edges: []Edge{
			{From: "a", To: "b"},
			{From: "b", To: "a"},
			{From: "b", To: "d"},
		},
	}
	authDenyGoldDestination := mustAuth(t, []TenantID{"t1", "tx", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: Labels("gold"), Destination: Labels("silver"), Action: ActionAllow}},
	})
	input := Input{Topology: base, Auth: authDenyGoldDestination, Pairs: []TenantPair{pair("t1", "t2")}}
	report, err := input.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Outcome != OutcomeNonCompliant {
		t.Fatalf("cycle exposed an unauthorized destination label; got %s", report.Outcome)
	}
	if len(report.Prefixes[0].Violations) == 0 {
		t.Fatal("expected an end-to-end violation")
	}
	if report.Prefixes[0].States > 32 {
		t.Fatalf("state exploration did not converge: %d states", report.Prefixes[0].States)
	}

	authAllowBoth := mustAuth(t, []TenantID{"t1", "tx", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionAllow}},
	})
	input.Auth = authAllowBoth
	report, err = input.Check()
	if err != nil {
		t.Fatalf("Check allowed cycle: %v", err)
	}
	if report.Outcome != OutcomeCompliant {
		t.Fatalf("allowed cycle was falsely rejected: %+v", report.Prefixes[0].Violations)
	}
}

func TestSharedIntermediateNodeKeepsSourceTenantStateSeparate(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "s1", Tenant: "t1", Egress: allowAll()},
			{ID: "s2", Tenant: "t2", Egress: allowAll()},
			{ID: "x", Tenant: "tx", Ingress: allowAll(), Egress: allowAll()},
			{ID: "d", Tenant: "t3", Ingress: allowAll()},
		},
		Edges: []Edge{
			{From: "s1", To: "x"},
			{From: "s2", To: "x"},
			{From: "x", To: "d"},
		},
	}
	auth := mustAuth(t, []TenantID{"t1", "t2", "tx", "t3"}, map[TenantPair]PairPolicy{
		pair("t1", "t3"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionAllow}},
		pair("t2", "t3"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionDeny}},
	})
	input := Input{
		Topology: topology,
		Auth:     auth,
		Pairs:    []TenantPair{pair("t1", "t3"), pair("t2", "t3")},
		Labels:   []Label{"gold"},
	}
	report, err := input.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Outcome != OutcomeNonCompliant {
		t.Fatalf("expected t2 path to fail, got %s", report.Outcome)
	}
	if len(report.Prefixes[0].Violations) != 1 {
		t.Fatalf("expected exactly one tenant-scoped violation, got %+v", report.Prefixes[0].Violations)
	}
	if got := report.Prefixes[0].Violations[0].Pair; got != pair("t2", "t3") {
		t.Fatalf("wrong tenant pair on violation: %+v", got)
	}

	certT1, outcome, err := BuildCoverageCertificate(
		Snapshot{Generation: 0, Topology: topology, Auth: auth, Labels: []Label{"gold"}},
		pair("t1", "t3"),
		Budget{},
	)
	if err != nil || outcome != OutcomeCompliant {
		t.Fatalf("t1 certificate: outcome=%s err=%v", outcome, err)
	}
	certT2, outcome, err := BuildCoverageCertificate(
		Snapshot{Generation: 0, Topology: topology, Auth: auth, Labels: []Label{"gold"}},
		pair("t2", "t3"),
		Budget{},
	)
	if err != nil || outcome != OutcomeNonCompliant {
		t.Fatalf("t2 certificate: outcome=%s err=%v", outcome, err)
	}
	if certT1.SnapshotDigest != certT2.SnapshotDigest {
		t.Fatal("the same snapshot should have the same fingerprint")
	}
	if certT1.Pair == certT2.Pair {
		t.Fatal("certificate pair must be part of the independently checked scope")
	}
}

func TestCoverageCertificateIsIndependentAndTamperEvident(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: allow("gold")},
			{ID: "b", Tenant: "t2", Ingress: allow("gold")},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	auth := mustAuth(t, []TenantID{"t1", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionAllow}},
	})
	snapshot := Snapshot{Generation: 7, Topology: topology, Auth: auth}
	certificate, outcome, err := BuildCoverageCertificate(snapshot, pair("t1", "t2"), Budget{})
	if err != nil {
		t.Fatalf("BuildCoverageCertificate: %v", err)
	}
	if outcome != OutcomeCompliant {
		t.Fatalf("certificate outcome = %s", outcome)
	}
	if checked, err := CheckCoverageCertificate(snapshot, *certificate, Budget{}); err != nil || checked != OutcomeCompliant {
		t.Fatalf("CheckCoverageCertificate = %s, %v", checked, err)
	}
	encoded, err := json.Marshal(certificate)
	if err != nil {
		t.Fatalf("marshal certificate: %v", err)
	}
	var decoded CoverageCertificate
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal certificate: %v", err)
	}
	if checked, err := CheckCoverageCertificate(snapshot, decoded, Budget{}); err != nil || checked != OutcomeCompliant {
		t.Fatalf("JSON round-trip certificate check = %s, %v", checked, err)
	}

	tampered := *certificate
	tampered.States = append([]CertificateState(nil), certificate.States...)
	if len(tampered.States) == 0 {
		t.Fatal("certificate has no states")
	}
	tampered.States = tampered.States[1:]
	tampered.Digest = certificateDigest(tampered)
	if _, err := CheckCoverageCertificate(snapshot, tampered, Budget{}); err == nil {
		t.Fatal("checker accepted an incomplete reachable-state cover")
	}

	denyAuth := mustAuth(t, []TenantID{"t1", "t2"}, map[TenantPair]PairPolicy{pair("t1", "t2"): {{Action: ActionDeny}}})
	denySnapshot := Snapshot{Generation: 8, Topology: topology, Auth: denyAuth}
	denied, outcome, err := BuildCoverageCertificate(denySnapshot, pair("t1", "t2"), Budget{})
	if err != nil {
		t.Fatalf("denied BuildCoverageCertificate: %v", err)
	}
	if outcome != OutcomeNonCompliant {
		t.Fatalf("denied certificate outcome = %s", outcome)
	}
	if checked, err := CheckCoverageCertificate(denySnapshot, *denied, Budget{}); err != nil || checked != OutcomeNonCompliant {
		t.Fatalf("denied certificate check = %s, %v", checked, err)
	}
}

func TestBudgetExhaustionReturnsUndetermined(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: allowAll()},
			{ID: "b", Tenant: "t2", Ingress: allowAll()},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	auth := mustAuth(t, []TenantID{"t1", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionAllow}},
	})
	input := Input{
		Topology: topology,
		Auth:     auth,
		Pairs:    []TenantPair{pair("t1", "t2")},
		Labels:   []Label{"gold"},
		Budget:   Budget{MaxStates: 1},
	}
	report, err := input.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Outcome != OutcomeUndetermined {
		t.Fatalf("exhausted budget must be undetermined, got %s", report.Outcome)
	}

	snapshot := Snapshot{Generation: 1, Topology: topology, Auth: auth, Labels: []Label{"gold"}}
	_, outcome, err := BuildCoverageCertificate(snapshot, pair("t1", "t2"), Budget{MaxStates: 1})
	if outcome != OutcomeUndetermined || !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("BuildCoverageCertificate = %s, %v; want undetermined/budget", outcome, err)
	}
}

func TestActiveConnectionContinuesFromVerifiedPrefix(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: denyAll()},
			{ID: "b", Tenant: "tx", Ingress: allowAll(), Egress: allowAll()},
			{ID: "d", Tenant: "t2", Ingress: allowAll()},
		},
		Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "d"}},
	}
	auth := mustAuth(t, []TenantID{"t1", "tx", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionAllow}},
	})
	connection := Connection{
		ID:     "live-1",
		Pair:   pair("t1", "t2"),
		Path:   []EndpointID{"a", "b", "d"},
		Labels: []Label{"gold", "gold", "gold"},
	}
	snapshot := Snapshot{Generation: 2, Topology: topology, Auth: auth}
	verified := ActiveConnection{
		Connection:      connection,
		VerifiedThrough: 1,
		VerifiedHistory: HistoryDigest(connection.Path[:2], connection.Labels[:2]),
	}
	if violations := verifyConnection(verified.Connection, snapshot, verified.VerifiedThrough); len(violations) != 0 {
		t.Fatalf("verified old prefix should be retained: %+v", violations)
	}
	if violations := verifyConnection(connection, snapshot, 0); len(violations) == 0 {
		t.Fatal("unverified new connection must be denied by the current egress rule")
	}
}

func TestDefaultPairsCoverEveryOtherManagedTenant(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Ingress: allowAll(), Egress: allowAll()},
			{ID: "b", Tenant: "t2", Ingress: allowAll(), Egress: allowAll()},
		},
		Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "a"}},
	}
	auth := mustAuth(t, []TenantID{"t1", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionAllow}},
	})
	input := Input{
		Topology: topology,
		Auth:     auth,
		Labels:   []Label{"gold"},
	}
	report, err := input.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Outcome != OutcomeNonCompliant {
		t.Fatalf("default pair coverage missed reverse reachable flow: %s", report.Outcome)
	}

	input.Pairs = []TenantPair{pair("t1", "t2")}
	report, err = input.Check()
	if err != nil {
		t.Fatalf("Check narrowed pair: %v", err)
	}
	if report.Outcome != OutcomeCompliant {
		t.Fatalf("narrowed pair should be compliant, got %s: %+v", report.Outcome, report.Prefixes[0].Violations)
	}
}

func TestExplicitLabelDomainIsExact(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: allow("gold", "silver")},
			{ID: "b", Tenant: "t2", Ingress: allow("gold", "silver")},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	auth := mustAuth(t, []TenantID{"t1", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: Labels("gold"), Destination: Labels("gold"), Action: ActionAllow}},
	})
	input := Input{
		Topology: topology,
		Auth:     auth,
		Labels:   []Label{"gold"},
	}
	report, err := input.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Outcome != OutcomeCompliant {
		t.Fatalf("silver was explicitly excluded from the initial domain: %+v", report.Prefixes[0].Violations)
	}
	input.Labels = []Label{"gold", "silver"}
	report, err = input.Check()
	if err != nil {
		t.Fatalf("Check with silver: %v", err)
	}
	if report.Outcome != OutcomeNonCompliant {
		t.Fatalf("silver initial connection must be denied, got %s", report.Outcome)
	}
}

func TestSnapshotFingerprintDistinguishesStrictEmptyLabelGraph(t *testing.T) {
	auth := mustAuth(t, []TenantID{"t1", "t2"}, nil)
	passthrough := Snapshot{
		Generation: 1,
		Auth:       auth,
		Topology: Topology{Endpoints: []Endpoint{{
			ID: "a", Tenant: "t1", Changes: Passthrough(),
		}}},
	}
	strictEmpty := Snapshot{
		Generation: 1,
		Auth:       auth,
		Topology: Topology{Endpoints: []Endpoint{{
			ID: "a", Tenant: "t1", Changes: ExactGraph(nil),
		}}},
	}
	if SnapshotFingerprint(passthrough) == SnapshotFingerprint(strictEmpty) {
		t.Fatal("passthrough and strict-empty label graphs have different semantics and must not share a fingerprint")
	}
}

func TestActiveVerifiedHistoryMustMatchItsPrefix(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: allowAll()},
			{ID: "b", Tenant: "t2", Ingress: allowAll()},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	auth := mustAuth(t, []TenantID{"t1", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {{Source: AnyLabel(), Destination: AnyLabel(), Action: ActionAllow}},
	})
	connection := Connection{
		ID:     "c",
		Pair:   pair("t1", "t2"),
		Path:   []EndpointID{"a", "b"},
		Labels: []Label{"gold", "gold"},
	}
	input := Input{
		Topology: topology,
		Auth:     auth,
		Labels:   []Label{"gold"},
		Active: []ActiveConnection{{
			Connection:      connection,
			VerifiedThrough: 1,
			VerifiedHistory: HistoryDigest([]EndpointID{"other", "b"}, connection.Labels),
		}},
	}
	if err := input.Validate(); err == nil {
		t.Fatal("active connection with a foreign verified history must be rejected")
	}
}
