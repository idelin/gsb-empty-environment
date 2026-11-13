package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const testLocal = ASN(100)

func scenarioDataset(t *testing.T, version string) *Dataset {
	t.Helper()
	dataset, err := NewDataset(version, Records{
		1: {2},
		2: {4},
		3: {4},
		4: {7},
		5: {2, 6},
		6: {7},
		7: {},
		8: {1},
	})
	if err != nil {
		t.Fatalf("NewDataset: %v", err)
	}
	return dataset
}

func TestASNValidation(t *testing.T) {
	for _, asn := range []ASN{1, 64512, 4200000000} {
		if err := ValidateASN(asn); err != nil {
			t.Errorf("ValidateASN(%d) = %v, want nil", asn, err)
		}
	}
	for _, asn := range []ASN{0, ASNTrans, 64496, 64511, 65535, 65536, 65551, ASNMax} {
		if err := ValidateASN(asn); !errors.Is(err, ErrInvalidASN) {
			t.Errorf("ValidateASN(%d) = %v, want ErrInvalidASN", asn, err)
		}
	}
}

func TestDatasetIsValidatedAndImmutable(t *testing.T) {
	records := Records{
		1: {2, 2, 3},
		4: {},
	}
	dataset, err := NewDataset("one", records)
	if err != nil {
		t.Fatalf("NewDataset: %v", err)
	}
	providers, ok := dataset.Providers(1)
	if !ok || fmt.Sprint(providers) != "[2 3]" {
		t.Fatalf("Providers(1) = %v, %v", providers, ok)
	}
	providers[0] = 99
	again, _ := dataset.Providers(1)
	if fmt.Sprint(again) != "[2 3]" {
		t.Fatalf("dataset mutated through returned provider slice: %v", again)
	}
	if !dataset.HasRecord(4) {
		t.Fatal("empty ASPA record must be treated as complete")
	}
	if dataset.Authorizes(4, 1) {
		t.Fatal("empty ASPA record authorized a provider")
	}

	bad := []struct {
		name    string
		version string
		records Records
	}{
		{"empty version", "", Records{}},
		{"zero customer", "v", Records{0: {1}}},
		{"reserved provider", "v", Records{1: {ASNMax}}},
		{"self provider", "v", Records{1: {1}}},
	}
	for _, test := range bad {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewDataset(test.version, test.records); !errors.Is(err, ErrInvalidDataset) {
				t.Fatalf("NewDataset error = %v, want ErrInvalidDataset", err)
			}
		})
	}
}

func TestPathNormalizationCompressesPrependAndRejectsMalformedPath(t *testing.T) {
	path := Path{Segments: []Segment{
		{Type: SegmentASSequence, ASNs: []ASN{1, 1, 1}},
		{Type: SegmentASSequence, ASNs: []ASN{1, 2, 2, 3}},
	}}
	normalized, err := NormalizePath(path, testLocal, 2)
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if fmt.Sprint(normalized.ASNs) != "[1 2 3]" {
		t.Fatalf("normalized ASNs = %v", normalized.ASNs)
	}
	if normalized.Runs[0] != (PrependRun{ASN: 1, Count: 2, Truncated: true}) {
		t.Fatalf("run 0 = %+v", normalized.Runs[0])
	}
	if normalized.Runs[1] != (PrependRun{ASN: 2, Count: 2}) {
		t.Fatalf("run 1 = %+v", normalized.Runs[1])
	}

	if _, err := NormalizePath(SequencePath(1, 2, 1), testLocal, 9); !errors.Is(err, ErrPathLoop) {
		t.Fatalf("loop error = %v, want ErrPathLoop", err)
	}
	if _, err := NormalizePath(Path{Segments: []Segment{{
		Type: SegmentASSet, ASNs: []ASN{1, 2},
	}}}, testLocal, 9); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("AS_SET error = %v, want ErrInvalidPath", err)
	}
	if _, err := NormalizePath(SequencePath(1, testLocal), testLocal, 9); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("local AS error = %v, want ErrInvalidPath", err)
	}
	if _, err := NormalizePath(SequencePath(0, 1), testLocal, 9); !errors.Is(err, ErrInvalidASN) {
		t.Fatalf("zero ASN error = %v, want ErrInvalidASN", err)
	}
}

func TestProviderPeerProviderLeakAndCustomerExport(t *testing.T) {
	dataset := scenarioDataset(t, "scenario-v1")
	tests := []struct {
		name       string
		asns       []ASN
		relation   Relation
		policy     Policy
		want       Verdict
		wantAccept bool
		wantReason ReasonCode
		wantIndex  int
	}{
		{
			name:       "valid provider-to-customer path",
			asns:       []ASN{1, 2, 4, 7},
			relation:   Provider,
			policy:     Strict,
			want:       VerdictValid,
			wantAccept: true,
			wantIndex:  -1,
		},
		{
			name:       "provider-peer-provider leak",
			asns:       []ASN{1, 2, 4, 5, 6, 7},
			relation:   Provider,
			policy:     Strict,
			want:       VerdictInvalid,
			wantAccept: false,
			wantReason: ReasonUnauthorizedCustomer,
			wantIndex:  2,
		},
		{
			name:       "down then up valley",
			asns:       []ASN{1, 2, 5, 6, 7},
			relation:   Provider,
			policy:     Strict,
			want:       VerdictInvalid,
			wantAccept: false,
			wantReason: ReasonValleyViolation,
			wantIndex:  1,
		},
		{
			name:       "customer route may leave in any direction",
			asns:       []ASN{1, 2, 4, 5, 3},
			relation:   Customer,
			policy:     Strict,
			want:       VerdictValid,
			wantAccept: true,
			wantIndex:  -1,
		},
		{
			name:       "missing customer record is unknown and rejected",
			asns:       []ASN{9, 6, 7},
			relation:   Provider,
			policy:     Strict,
			want:       VerdictUnknown,
			wantAccept: false,
			wantReason: ReasonMissingASPA,
			wantIndex:  0,
		},
		{
			name:       "missing customer record is only a warning in monitor mode",
			asns:       []ASN{9, 6, 7},
			relation:   Provider,
			policy:     Monitor,
			want:       VerdictUnknown,
			wantAccept: true,
			wantReason: ReasonMissingASPA,
			wantIndex:  0,
		},
		{
			name:       "complete record denies provider",
			asns:       []ASN{8, 6, 7},
			relation:   Provider,
			policy:     Strict,
			want:       VerdictInvalid,
			wantAccept: false,
			wantReason: ReasonUnauthorizedCustomer,
			wantIndex:  0,
		},
		{
			name:       "peer session at the apex",
			asns:       []ASN{1, 2, 4, 7},
			relation:   Peer,
			policy:     Strict,
			want:       VerdictValid,
			wantAccept: true,
			wantIndex:  -1,
		},
		{
			name:       "peer route may not contain a downhill prefix",
			asns:       []ASN{1, 2, 5},
			relation:   Peer,
			policy:     Strict,
			want:       VerdictInvalid,
			wantAccept: false,
			wantReason: ReasonValleyViolation,
			wantIndex:  1,
		},
		{
			name:       "unattested internal peer remains a data gap",
			asns:       []ASN{3, 2, 5},
			relation:   Provider,
			policy:     Monitor,
			want:       VerdictUnknown,
			wantAccept: true,
			wantReason: ReasonUnattestedPeer,
			wantIndex:  0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := Session{
				LocalASN:    testLocal,
				NeighborASN: test.asns[len(test.asns)-1],
				Relation:    test.relation,
			}
			evaluation := EvaluatePath(dataset, test.policy, session, SequencePath(test.asns...), 8)
			if evaluation.Verdict != test.want {
				t.Fatalf("Verdict = %s, want %s; issue=%+v hops=%+v",
					evaluation.Verdict, test.want, evaluation.FirstIssue, evaluation.Hops)
			}
			if evaluation.Accepted != test.wantAccept {
				t.Fatalf("Accepted = %v, want %v", evaluation.Accepted, test.wantAccept)
			}
			if test.want == VerdictUnknown && evaluation.Policy == Monitor {
				if !evaluation.Warning || evaluation.IsValid() {
					t.Fatalf("monitor unknown must warn and must not be valid: %+v", evaluation)
				}
			}
			if test.wantReason != ReasonNone && issueReason(evaluation.FirstIssue) != test.wantReason {
				t.Fatalf("first reason = %s, want %s; issue=%+v",
					issueReason(evaluation.FirstIssue), test.wantReason, evaluation.FirstIssue)
			}
			if test.wantIndex >= 0 {
				if evaluation.FirstIssue == nil || evaluation.FirstIssue.HopIndex != test.wantIndex {
					t.Fatalf("first issue index = %+v, want %d", evaluation.FirstIssue, test.wantIndex)
				}
			}
			if test.want == VerdictInvalid {
				if evaluation.FirstInvalid == nil || evaluation.FirstInvalid.Class != VerdictInvalid {
					t.Fatalf("invalid verdict without FirstInvalid: %+v", evaluation)
				}
			}
			if test.want == VerdictUnknown {
				if evaluation.FirstDataGap == nil || evaluation.FirstDataGap.Class != VerdictUnknown {
					t.Fatalf("unknown verdict without FirstDataGap: %+v", evaluation)
				}
			}
		})
	}
}

func TestMalformedHostInputIsRejected(t *testing.T) {
	dataset := scenarioDataset(t, "v")
	validator, err := NewValidator(dataset, Options{})
	if err != nil {
		t.Fatal(err)
	}
	session := Session{LocalASN: testLocal, NeighborASN: 7, Relation: Provider}

	tests := []struct {
		name   string
		path   Path
		reason ReasonCode
	}{
		{"empty", Path{}, ReasonEmptyPath},
		{"AS_SET", Path{Segments: []Segment{{Type: SegmentASSet, ASNs: []ASN{1}}}}, ReasonNonSequenceSegment},
		{"zero ASN", SequencePath(0, 7), ReasonInvalidASN},
		{"reserved ASN", SequencePath(ASNTrans, 7), ReasonInvalidASN},
		{"local in path", SequencePath(1, testLocal), ReasonLocalASInPath},
		{"loop", SequencePath(1, 7, 1), ReasonPathLoop},
		{"wrong neighbor", SequencePath(1, 2), ReasonNeighborMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := validator.Validate(test.path, session)
			if evaluation.Verdict != VerdictInvalid || evaluation.Accepted {
				t.Fatalf("malformed path was accepted: %+v", evaluation)
			}
			if issueReason(evaluation.FirstIssue) != test.reason {
				t.Fatalf("reason = %s, want %s; issue=%+v",
					issueReason(evaluation.FirstIssue), test.reason, evaluation.FirstIssue)
			}
		})
	}

	badSession := Session{LocalASN: testLocal, NeighborASN: 0, Relation: Provider}
	if evaluation := validator.Validate(SequencePath(1, 7), badSession); issueReason(evaluation.FirstIssue) != ReasonInvalidSession {
		t.Fatalf("invalid session reason = %s", issueReason(evaluation.FirstIssue))
	}
}

func TestPrependEvidenceRetainedInEvaluation(t *testing.T) {
	dataset := scenarioDataset(t, "v")
	evaluation := EvaluatePath(dataset, Strict, Session{
		LocalASN: testLocal, NeighborASN: 7, Relation: Provider,
	}, SequencePath(1, 1, 1, 2, 2, 4, 7), 2)
	if evaluation.Verdict != VerdictValid {
		t.Fatalf("Verdict = %s, issue=%+v", evaluation.Verdict, evaluation.FirstIssue)
	}
	if fmt.Sprint(evaluation.Path.ASNs) != "[1 2 4 7]" {
		t.Fatalf("normalized path = %v", evaluation.Path.ASNs)
	}
	if evaluation.Hops[0].PrependCount != 2 || !evaluation.Hops[0].PrependTruncated {
		t.Fatalf("hop 0 prepend evidence = %+v", evaluation.Hops[0])
	}
	if evaluation.Hops[1].PrependCount != 2 || evaluation.Hops[1].PrependTruncated {
		t.Fatalf("hop 1 prepend evidence = %+v", evaluation.Hops[1])
	}
}

func TestFirstIssueIsEarliestGapOrViolation(t *testing.T) {
	dataset := scenarioDataset(t, "v")
	evaluation := EvaluatePath(dataset, Strict, Session{
		LocalASN: testLocal, NeighborASN: 7, Relation: Provider,
	}, SequencePath(9, 8, 6, 7), 8)
	if evaluation.Verdict != VerdictInvalid {
		t.Fatalf("Verdict = %s, want invalid; issue=%+v", evaluation.Verdict, evaluation.FirstIssue)
	}
	if evaluation.FirstIssue == nil || evaluation.FirstIssue.HopIndex != 0 ||
		evaluation.FirstIssue.Class != VerdictUnknown {
		t.Fatalf("FirstIssue = %+v, want earliest data gap at hop 0", evaluation.FirstIssue)
	}
	if evaluation.FirstInvalid == nil || evaluation.FirstInvalid.HopIndex != 1 {
		t.Fatalf("FirstInvalid = %+v, want hop 1", evaluation.FirstInvalid)
	}
}

func TestAtomicDatasetSwitchRecomputesOnlyAffectedRoutes(t *testing.T) {
	v1 := scenarioDataset(t, "v1")
	v2, err := NewDataset("v2", Records{
		1: {},
		2: {4},
		3: {4},
		4: {7},
		5: {2, 6},
		6: {7},
		7: {},
		8: {1},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRouteStore(v1, Options{Policy: Strict})
	if err != nil {
		t.Fatal(err)
	}
	affected := Route{
		ID:      "affected",
		Path:    SequencePath(1, 2, 4, 7),
		Session: Session{LocalASN: testLocal, NeighborASN: 7, Relation: Provider},
	}
	unaffected := Route{
		ID:      "unaffected",
		Path:    SequencePath(6, 7),
		Session: Session{LocalASN: testLocal, NeighborASN: 7, Relation: Provider},
	}
	if evaluation, err := store.Upsert(affected); err != nil || evaluation.Verdict != VerdictValid {
		t.Fatalf("Upsert affected = %+v, %v", evaluation, err)
	}
	if evaluation, err := store.Upsert(unaffected); err != nil || evaluation.Verdict != VerdictValid {
		t.Fatalf("Upsert unaffected = %+v, %v", evaluation, err)
	}

	report, err := store.SwapDataset(v2)
	if err != nil {
		t.Fatalf("SwapDataset: %v", err)
	}
	if fmt.Sprint(report.Revalidated) != "[affected]" {
		t.Fatalf("Revalidated = %v", report.Revalidated)
	}
	if len(report.Changes) != 1 || report.Changes[0].ID != "affected" ||
		report.Changes[0].Before != VerdictValid || report.Changes[0].After != VerdictInvalid {
		t.Fatalf("Changes = %+v", report.Changes)
	}

	snapshot := store.Snapshot()
	if snapshot.DatasetVersion != "v2" {
		t.Fatalf("snapshot version = %s", snapshot.DatasetVersion)
	}
	if got := snapshot.Routes["affected"].Evaluation.Verdict; got != VerdictInvalid {
		t.Fatalf("affected verdict = %s, want invalid", got)
	}
	if got := snapshot.Routes["unaffected"].Evaluation.Verdict; got != VerdictValid {
		t.Fatalf("unaffected verdict = %s, want valid", got)
	}
	for id, route := range snapshot.Routes {
		if route.Evaluation.DatasetVersion != "v2" {
			t.Fatalf("route %s has version %s", id, route.Evaluation.DatasetVersion)
		}
	}
	if v1.Version() != "v1" || !v1.Authorizes(1, 2) {
		t.Fatal("old dataset was mutated")
	}
}

func TestAtomicSnapshotMovesAsOneUnit(t *testing.T) {
	v1 := scenarioDataset(t, "v1")
	v2, err := NewDataset("v2", Records{
		1: {},
		2: {4},
		3: {4},
		4: {7},
		5: {2, 6},
		6: {7},
		7: {},
		8: {1},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRouteStore(v1, Options{Policy: Monitor})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		_, err := store.Upsert(Route{
			ID:      fmt.Sprintf("r%d", i),
			Path:    SequencePath(1, 2, 4, 7),
			Session: Session{LocalASN: testLocal, NeighborASN: 7, Relation: Provider},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	const readers = 4
	const iterations = 200
	start := make(chan struct{})
	errorsFound := make(chan error, readers)
	var wait sync.WaitGroup
	for i := 0; i < readers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for j := 0; j < iterations; j++ {
				snapshot := store.Snapshot()
				if len(snapshot.Routes) != 8 {
					errorsFound <- fmt.Errorf("route count = %d", len(snapshot.Routes))
					return
				}
				for id, installed := range snapshot.Routes {
					if installed.Evaluation.DatasetVersion != snapshot.DatasetVersion {
						errorsFound <- fmt.Errorf("%s has version %s, snapshot has %s",
							id, installed.Evaluation.DatasetVersion, snapshot.DatasetVersion)
						return
					}
				}
			}
		}()
	}
	close(start)
	for i := 0; i < 30; i++ {
		next := v1
		if i%2 == 0 {
			next = v2
		}
		if _, err := store.SwapDataset(next); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

func TestExhaustiveSmallGraphAgainstReference(t *testing.T) {
	dataset := scenarioDataset(t, "enum-v1")
	records := Records{
		1: {2},
		2: {4},
		3: {4},
		4: {7},
		5: {2, 6},
		6: {7},
		7: {},
		8: {1},
	}
	edges := [][2]ASN{
		{1, 2}, {2, 4}, {3, 4}, {4, 7}, {5, 2}, {5, 6}, {6, 7},
		{8, 1}, {8, 6}, {9, 6}, {2, 3}, {4, 5},
	}
	graph := make(map[ASN][]ASN)
	for _, edge := range edges {
		graph[edge[0]] = append(graph[edge[0]], edge[1])
		graph[edge[1]] = append(graph[edge[1]], edge[0])
	}

	var paths [][]ASN
	var walk func(path []ASN, used map[ASN]bool)
	walk = func(path []ASN, used map[ASN]bool) {
		if len(path) > 0 {
			paths = append(paths, append([]ASN(nil), path...))
		}
		if len(path) == 9 {
			return
		}
		for _, next := range graph[path[len(path)-1]] {
			if used[next] {
				continue
			}
			used[next] = true
			walk(append(path, next), used)
			delete(used, next)
		}
	}
	for asn := ASN(1); asn <= 9; asn++ {
		walk([]ASN{asn}, map[ASN]bool{asn: true})
	}

	for _, relation := range []Relation{Customer, Provider, Peer} {
		for _, path := range paths {
			session := Session{LocalASN: testLocal, NeighborASN: path[len(path)-1], Relation: relation}
			for _, policy := range []Policy{Strict, Monitor} {
				got := EvaluatePath(dataset, policy, session, SequencePath(path...), 8)
				want := referenceVerdict(records, session, path)
				if got.Verdict != want {
					t.Fatalf("relation=%s policy=%s path=%v: verdict=%s, reference=%s, issue=%+v",
						relation, policy, path, got.Verdict, want, got.FirstIssue)
				}
				wantAccepted := want == VerdictValid || (want == VerdictUnknown && policy == Monitor)
				if got.Accepted != wantAccepted {
					t.Fatalf("relation=%s policy=%s path=%v: accepted=%v, want %v",
						relation, policy, path, got.Accepted, wantAccepted)
				}
			}
		}
	}
}

type referencePhase uint8

const (
	referenceUp referencePhase = iota
	referencePeer
	referenceDown
)

func referenceVerdict(records Records, session Session, path []ASN) Verdict {
	if session.Relation == Customer {
		return VerdictValid
	}
	vertices := append(append([]ASN(nil), path...), session.LocalASN)
	hasRecord := func(asn ASN) bool {
		_, ok := records[asn]
		return ok
	}
	authorizes := func(customer, provider ASN) bool {
		for _, candidate := range records[customer] {
			if candidate == provider {
				return true
			}
		}
		return false
	}

	type referenceCandidate struct{ invalid, unknown int }
	best := referenceCandidate{invalid: len(vertices), unknown: len(vertices)}
	found := false

	var assign func(index int, currentPhase referencePhase, invalid, unknown int)
	assign = func(index int, currentPhase referencePhase, invalid, unknown int) {
		if index == len(vertices)-1 {
			if !found || invalid < best.invalid ||
				(invalid == best.invalid && unknown < best.unknown) {
				best = referenceCandidate{invalid: invalid, unknown: unknown}
				found = true
			}
			return
		}
		terminal := index == len(vertices)-2
		from := vertices[index]
		to := vertices[index+1]
		directions := []Direction{DirectionUp, DirectionPeer, DirectionDown}
		if index == 0 {
			currentPhase = referenceUp
		} else {
			switch currentPhase {
			case referencePeer, referenceDown:
				directions = []Direction{DirectionDown}
			}
		}
		for _, direction := range directions {
			if index > 0 && currentPhase == referencePeer && direction != DirectionDown {
				continue
			}
			class := VerdictValid
			if terminal {
				expected := referenceRelationDirection(session.Relation)
				if direction != expected {
					class = VerdictInvalid
				}
			} else {
				up := authorizes(from, to)
				down := authorizes(to, from)
				switch direction {
				case DirectionUp:
					if !up {
						if down || hasRecord(from) {
							class = VerdictInvalid
						} else {
							class = VerdictUnknown
						}
					}
				case DirectionDown:
					if !down {
						if up || hasRecord(to) {
							class = VerdictInvalid
						} else {
							class = VerdictUnknown
						}
					}
				case DirectionPeer:
					if up || down {
						class = VerdictInvalid
					} else {
						class = VerdictUnknown
					}
				}
			}
			nextInvalid, nextUnknown := invalid, unknown
			if class == VerdictInvalid {
				nextInvalid++
			} else if class == VerdictUnknown {
				nextUnknown++
			}
			nextPhase := currentPhase
			switch direction {
			case DirectionUp:
				nextPhase = referenceUp
			case DirectionPeer:
				nextPhase = referencePeer
			case DirectionDown:
				nextPhase = referenceDown
			}
			assign(index+1, nextPhase, nextInvalid, nextUnknown)
		}
	}
	assign(0, referenceUp, 0, 0)
	switch {
	case best.invalid > 0:
		return VerdictInvalid
	case best.unknown > 0:
		return VerdictUnknown
	default:
		return VerdictValid
	}
}

func referenceRelationDirection(relation Relation) Direction {
	switch relation {
	case Customer:
		return DirectionUp
	case Provider:
		return DirectionDown
	case Peer:
		return DirectionPeer
	default:
		return DirectionUnknown
	}
}
