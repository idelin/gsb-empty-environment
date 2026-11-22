package proofaccept

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func state(page PageID, frame FrameID, object ObjectID, gen Generation, digest Digest, executable bool, signer SignerID) PageState {
	return PageState{
		Page:       page,
		Frame:      frame,
		Object:     object,
		Generation: gen,
		Digest:     digest,
		Executable: executable,
		Signer:     signer,
	}
}

func mapping(id EventID, page PageID, object ObjectID, gen Generation, digest Digest, trusted bool, deps ...EventID) Event {
	return Event{
		ID:         id,
		Op:         OpMap,
		Page:       page,
		Object:     object,
		Generation: gen,
		Digest:     digest,
		Signer:     "",
		Trusted:    trusted,
		Deps:       deps,
	}
}

func TestSmallGraphAgainstAllLinearExtensionsAndPrefixes(t *testing.T) {
	events := []Event{
		mapping("a", "p1", "o1", 1, "d1", true),
		mapping("b", "p1", "o1", 2, "d2", true, "a"),
		mapping("c", "p2", "o2", 1, "d3", true),
		{ID: "d", Op: OpRevoke, Page: "p3"},
		mapping("e", "p3", "o3", 1, "d4", true, "d"),
	}

	tests := []struct {
		name    string
		samples []Sample
	}{
		{
			name: "all pages share a cut",
			samples: []Sample{
				{ID: "s1", States: []PageState{state("p1", "p1", "o1", 2, "d2", false, "")}},
				{ID: "s2", States: []PageState{state("p2", "p2", "o2", 1, "d3", false, "")}},
			},
		},
		{
			name: "two generations of one page cannot share a cut",
			samples: []Sample{
				{ID: "s1", States: []PageState{state("p1", "p1", "o1", 1, "d1", false, "")}},
				{ID: "s2", States: []PageState{state("p1", "p1", "o1", 2, "d2", false, "")}},
			},
		},
		{
			name: "sample interval excludes generation one",
			samples: []Sample{
				{ID: "s1", States: []PageState{state("p1", "p1", "o1", 1, "d1", false, "")}, After: []EventID{"b"}},
			},
		},
		{
			name: "sample interval ends before p2 is mapped",
			samples: []Sample{
				{ID: "s1", States: []PageState{state("p2", "p2", "o2", 1, "d3", false, "")}, Before: []EventID{"c"}},
			},
		},
		{
			name: "independent pages fit between two page events",
			samples: []Sample{
				{ID: "s1", States: []PageState{state("p2", "p2", "o2", 1, "d3", false, "")}, After: []EventID{"a"}, Before: []EventID{"b"}},
			},
		},
		{
			name: "revoke then remap is visible",
			samples: []Sample{
				{ID: "s1", States: []PageState{state("p3", "p3", "o3", 1, "d4", false, "")}, After: []EventID{"e"}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := Input{Events: events, Samples: test.samples}
			got := Check(in)
			want := bruteCommonPrefix(t, in)
			if got.CommonPrefix != want {
				t.Fatalf("CommonPrefix=%v, brute force=%v, result=%+v", got.CommonPrefix, want, got)
			}
			if !want && got.Status != StatusIncompatible {
				t.Fatalf("status=%s, want incompatible", got.Status)
			}
		})
	}
}

func bruteCommonPrefix(t *testing.T, in Input) bool {
	t.Helper()
	c, err := compile(in)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	initial := make(map[FrameID]PageState)
	for _, s := range in.Initial {
		frame, err := c.frameOf(s.Page, s.Frame)
		if err != nil {
			t.Fatal(err)
		}
		s.Frame = frame
		initial[frame] = s
	}

	var walk func(done map[EventID]bool, states map[FrameID]PageState) bool
	walk = func(done map[EventID]bool, states map[FrameID]PageState) bool {
		if satisfiesSamples(c, in.Samples, done, states) {
			return true
		}
		for _, id := range c.order {
			if done[id] {
				continue
			}
			ready := true
			for _, dep := range c.events[id].Deps {
				if !done[dep] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			nextDone := cloneBoolMap(done)
			nextStates := clonePageMap(states)
			applyBrute(c, c.events[id], nextStates)
			nextDone[id] = true
			if walk(nextDone, nextStates) {
				return true
			}
		}
		return false
	}

	return walk(make(map[EventID]bool), initial)
}

func satisfiesSamples(c *compiled, samples []Sample, done map[EventID]bool, states map[FrameID]PageState) bool {
	for _, sample := range samples {
		for _, id := range sample.After {
			if !done[id] {
				return false
			}
		}
		for _, id := range sample.Before {
			if done[id] {
				return false
			}
		}
		for _, want := range sample.States {
			frame, err := c.frameOf(want.Page, want.Frame)
			if err != nil || !sameMapping(states[frame], want) {
				return false
			}
			if _, ok := states[frame]; !ok {
				return false
			}
		}
	}
	return true
}

func applyBrute(c *compiled, event Event, states map[FrameID]PageState) {
	if mappingEvent(event) {
		frame, _ := c.frameOf(event.Page, event.Frame)
		states[frame] = c.stateOfEvent(event)
		return
	}
	if event.Page == "" && event.Frame == "" {
		return
	}
	frame, _ := c.frameOf(event.Page, event.Frame)
	current, ok := states[frame]
	if !ok {
		return
	}
	if event.Generation == 0 || current.Generation == event.Generation {
		delete(states, frame)
	}
}

func cloneBoolMap(src map[EventID]bool) map[EventID]bool {
	out := make(map[EventID]bool, len(src))
	for id, value := range src {
		out[id] = value
	}
	return out
}

func clonePageMap(src map[FrameID]PageState) map[FrameID]PageState {
	out := make(map[FrameID]PageState, len(src))
	for id, value := range src {
		out[id] = value
	}
	return out
}

func TestThirtyTwoUnrelatedEventsUseOneFrontier(t *testing.T) {
	const n = 32
	var events []Event
	var observations []PageState
	var samples []Sample
	for i := 0; i < n; i++ {
		page := PageID(fmt.Sprintf("p%02d", i))
		event := mapping(EventID(fmt.Sprintf("e%02d", i)), page, ObjectID(fmt.Sprintf("o%02d", i)), 1, Digest(fmt.Sprintf("d%02d", i)), false)
		events = append(events, event)
		observations = append(observations, state(page, FrameID(page), event.Object, 1, event.Digest, false, ""))
		samples = append(samples, Sample{ID: SampleID(fmt.Sprintf("s%02d", i)), States: []PageState{observations[i]}})
	}

	oneReport := Input{Events: events, Samples: []Sample{{ID: "all", States: observations}}}
	result := Check(oneReport)
	if result.Status != StatusCompatible {
		t.Fatalf("one report: status=%s reason=%s", result.Status, result.Reason)
	}
	if len(result.Frontier) != n {
		t.Fatalf("frontier length=%d, want %d", len(result.Frontier), n)
	}
	accepted := AcceptFrontier(oneReport, result.Frontier)
	if !accepted.CertificateAccepted {
		t.Fatalf("frontier rejected: %+v", accepted)
	}

	manyReports := Input{Events: events, Samples: samples}
	result = Check(manyReports)
	if result.Status != StatusCompatible {
		t.Fatalf("many reports: status=%s reason=%s", result.Status, result.Reason)
	}
	if len(result.Frontier) != n {
		t.Fatalf("many-report frontier length=%d, want %d", len(result.Frontier), n)
	}
}

func TestSameDigestDoesNotMergeMappingGenerations(t *testing.T) {
	events := []Event{
		mapping("e1", "p", "obj", 1, "same-digest", false),
		mapping("e2", "p", "obj", 2, "same-digest", false, "e1"),
	}
	in := Input{
		Events: events,
		Samples: []Sample{
			{ID: "left", States: []PageState{state("p", "p", "obj", 1, "same-digest", false, "")}},
			{ID: "right", States: []PageState{state("p", "p", "obj", 2, "same-digest", false, "")}},
		},
	}
	result := Check(in)
	if result.Status != StatusIncompatible {
		t.Fatalf("status=%s, want incompatible", result.Status)
	}
	if !reflect.DeepEqual(result.Conflict, []SampleID{"left", "right"}) {
		t.Fatalf("conflict=%v", result.Conflict)
	}
	if !AcceptConflict(in, result.Conflict).CertificateAccepted {
		t.Fatal("conflict certificate was not accepted")
	}
	if AcceptConflict(in, []SampleID{"left"}).CertificateAccepted {
		t.Fatal("incomplete conflict certificate was accepted")
	}

	// Repeating the same generation is still compatible.
	in.Samples[1].States[0].Generation = 1
	if result := Check(in); result.Status != StatusCompatible {
		t.Fatalf("same generation status=%s", result.Status)
	}
}

func TestPagePairOnlyHoldsAtDifferentInstants(t *testing.T) {
	events := []Event{
		mapping("map-a", "a", "a", 1, "da", false),
		{ID: "drop-a", Op: OpRevoke, Page: "a", Deps: []EventID{"map-a"}},
		mapping("map-b", "b", "b", 1, "db", false, "drop-a"),
	}
	in := Input{
		Events: events,
		Samples: []Sample{
			{ID: "before", States: []PageState{state("a", "a", "a", 1, "da", false, "")}},
			{ID: "after", States: []PageState{state("b", "b", "b", 1, "db", false, "")}},
		},
	}
	result := Check(in)
	if result.Status != StatusIncompatible {
		t.Fatalf("status=%s, want incompatible", result.Status)
	}
	if !reflect.DeepEqual(result.Conflict, []SampleID{"after", "before"}) {
		t.Fatalf("conflict=%v", result.Conflict)
	}
}

func TestVirtualAliasesShareOneBackingPage(t *testing.T) {
	event := mapping("e1", "alias-a", "obj", 1, "digest", false)
	event.Frame = "frame"
	observation := state("alias-b", "frame", "obj", 1, "digest", false, "")
	in := Input{
		Events:  []Event{event},
		Aliases: map[PageID]FrameID{"alias-a": "frame", "alias-b": "frame"},
		Samples: []Sample{{
			ID: "s",
			States: []PageState{
				state("alias-a", "frame", "obj", 1, "digest", false, ""),
				observation,
			},
		}},
	}
	if result := Check(in); result.Status != StatusCompatible {
		t.Fatalf("alias status=%s reason=%s", result.Status, result.Reason)
	}

	in.Samples[0].States = []PageState{
		state("alias-a", "frame", "obj", 1, "digest", false, ""),
		state("alias-b", "frame", "obj", 2, "digest", false, ""),
	}
	result := Check(in)
	if result.Status != StatusIncompatible {
		t.Fatalf("alias mismatch status=%s, want incompatible", result.Status)
	}
	if !reflect.DeepEqual(result.Conflict, []SampleID{"s"}) {
		t.Fatalf("alias conflict=%v", result.Conflict)
	}
}

func TestRevokeBeforeConfirmationIsUnsafe(t *testing.T) {
	mapEvent := mapping("map", "p", "obj", 1, "digest", true)
	mapEvent.Executable = true
	mapEvent.Signer = "signer"
	events := []Event{
		mapEvent,
		{ID: "revoke", Op: OpRevoke, Object: "obj", Deps: []EventID{"map"}},
	}
	observation := state("p", "p", "obj", 1, "digest", true, "signer")

	afterRevoke := Input{
		Events: events,
		Samples: []Sample{{
			ID:     "confirm-after-revoke",
			States: []PageState{observation},
			After:  []EventID{"revoke"},
		}},
	}
	result := Check(afterRevoke)
	if !result.CommonPrefix || result.Status != StatusUnsafe {
		t.Fatalf("after revoke: status=%s prefix=%v result=%+v", result.Status, result.CommonPrefix, result)
	}
	if result.Safe {
		t.Fatal("revoked executable page marked safe")
	}

	unbounded := Input{
		Events:  events,
		Samples: []Sample{{ID: "either", States: []PageState{observation}}},
	}
	result = Check(unbounded)
	if result.Status != StatusPossiblyUnsafe {
		t.Fatalf("status=%s, want possibly unsafe", result.Status)
	}
}

func TestIncompleteChangeListIsEvidenceInsufficient(t *testing.T) {
	events := []Event{
		mapping("e1", "p", "obj", 1, "d1", false),
		mapping("e2", "p", "obj", 2, "d2", false, "e1"),
	}
	in := Input{
		Events: events,
		Samples: []Sample{
			{ID: "s1", States: []PageState{state("p", "p", "obj", 1, "d1", false, "")}},
			{
				ID:              "s2",
				States:          []PageState{state("p", "p", "obj", 2, "d2", false, "")},
				IncompletePages: []PageID{"p"},
			},
		},
	}
	result := Check(in)
	if result.Status != StatusEvidenceInsufficient {
		t.Fatalf("status=%s, want evidence_insufficient (result=%+v)", result.Status, result)
	}
	if result.Conflict != nil {
		t.Fatalf("insufficient evidence produced conflict %v", result.Conflict)
	}
	if !reflect.DeepEqual(result.Incomplete, []PageID{"p"}) {
		t.Fatalf("incomplete=%v", result.Incomplete)
	}
}

func TestConflictUsesLexicographicTieBreak(t *testing.T) {
	events := []Event{
		mapping("e1", "p", "obj", 1, "d1", false),
		mapping("e2", "p", "obj", 2, "d2", false, "e1"),
	}
	in := Input{
		Events: events,
		Samples: []Sample{
			{ID: "a", States: []PageState{state("p", "p", "obj", 1, "d1", false, "")}},
			{ID: "b", States: []PageState{state("p", "p", "obj", 1, "d1", false, "")}},
			{ID: "c", States: []PageState{state("p", "p", "obj", 2, "d2", false, "")}},
		},
	}
	result := Check(in)
	if result.Status != StatusIncompatible {
		t.Fatalf("status=%s", result.Status)
	}
	if !reflect.DeepEqual(result.Conflict, []SampleID{"a", "c"}) {
		t.Fatalf("conflict=%v, want [a c]", result.Conflict)
	}
}

func TestCertificateRejectionAndContext(t *testing.T) {
	in := Input{
		Events: []Event{
			mapping("e1", "p", "obj", 1, "d1", false),
			mapping("e2", "p", "obj", 2, "d2", false, "e1"),
		},
		Samples: []Sample{
			{ID: "s1", States: []PageState{state("p", "p", "obj", 2, "d2", false, "")}},
		},
	}
	good := Check(in)
	if good.Status != StatusCompatible {
		t.Fatalf("status=%s", good.Status)
	}
	if !AcceptFrontier(in, good.Frontier).CertificateAccepted {
		t.Fatal("good frontier rejected")
	}
	if AcceptFrontier(in, nil).CertificateAccepted {
		t.Fatal("empty frontier accepted for a non-empty cut")
	}
	noncompact := []EventID{"e1", "e2"}
	if result := AcceptFrontier(in, noncompact); result.CertificateAccepted || result.Status != StatusInvalidInput {
		t.Fatalf("non-compact frontier result=%+v", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := CheckContext(ctx, in); result.Status != StatusCanceled {
		t.Fatalf("canceled context status=%s", result.Status)
	}
}

func TestStatesAtCutUseOnlyCompleteEventPrefixes(t *testing.T) {
	events := []Event{
		mapping("a", "p", "o", 1, "d1", false),
		mapping("b", "p", "o", 2, "d2", false, "a"),
		mapping("c", "q", "q", 1, "d3", false),
	}
	in := Input{
		Events: events,
		Samples: []Sample{
			{ID: "s", States: []PageState{state("p", "p", "o", 1, "d1", false, ""), state("q", "q", "q", 1, "d3", false, "")}},
		},
	}
	result := Check(in)
	if result.Status != StatusCompatible {
		t.Fatalf("status=%s reason=%s", result.Status, result.Reason)
	}
	// The frontier must be an antichain: "a" is implied by "b" only at the
	// later state, but here the cut is {a,c}.
	want := []EventID{"a", "c"}
	sorted := append([]EventID(nil), result.Frontier...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if !reflect.DeepEqual(sorted, want) {
		t.Fatalf("frontier=%v, want %v", sorted, want)
	}
}
