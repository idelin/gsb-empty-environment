package netsec

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
)

const (
	srcInstance SourceID = "instance"
	srcTask     SourceID = "task"
	srcEnv      SourceID = "env"
	srcPeer     SourceID = "peer"
)

func testGraph(t *testing.T) *TrustGraph {
	t.Helper()
	graph, err := NewTrustGraph(
		[]SourceID{srcInstance, srcTask, srcEnv, srcPeer},
		[]TrustEdge{
			{Higher: srcEnv, Lower: srcTask},
			{Higher: srcTask, Lower: srcInstance},
		},
	)
	if err != nil {
		t.Fatalf("NewTrustGraph: %v", err)
	}
	return graph
}

func testRequest(now time.Time) Request {
	return Request{Tenant: "tenant-a", Audience: "svc/resource", Now: now}
}

func cred(source SourceID, identity string) Credential {
	return Credential{
		Source:   source,
		Identity: identity,
		Tenant:   "tenant-a",
		Audience: "svc/resource",
	}
}

func TestRefreshSelectsUniqueMaximum(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	instance := NewMutableSource(srcInstance)
	task := NewMutableSource(srcTask)
	env := NewMutableSource(srcEnv)
	instance.Set(cred(srcInstance, "instance-id"))
	task.Set(cred(srcTask, "task-id"))
	env.Set(cred(srcEnv, "env-id"))

	selector, err := NewSelector(testGraph(t), []Source{task, instance, env})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}

	selection, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if selection.Credential.Identity != "env-id" {
		t.Fatalf("selected identity = %q, want env-id", selection.Credential.Identity)
	}
	if selection.Generation != 1 {
		t.Fatalf("generation = %d, want 1", selection.Generation)
	}
	if selection.Evidence.Chosen == nil || selection.Evidence.Chosen.Source != srcEnv {
		t.Fatalf("evidence chosen = %+v, want source %q", selection.Evidence.Chosen, srcEnv)
	}
	wantProof := map[SourceID][]SourceID{
		srcInstance: {srcEnv, srcTask, srcInstance},
		srcTask:     {srcEnv, srcTask},
	}
	if got := selection.Evidence.Chosen.Dominance; !reflect.DeepEqual(got, wantProof) {
		t.Fatalf("dominance proof = %v, want %v", got, wantProof)
	}
}

func TestRefreshArrivalOrderIndependent(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	base := map[SourceID]Credential{
		srcInstance: cred(srcInstance, "instance-id"),
		srcTask:     cred(srcTask, "task-id"),
		srcEnv:      cred(srcEnv, "env-id"),
	}
	orders := [][]SourceID{
		{srcInstance, srcTask, srcEnv},
		{srcEnv, srcTask, srcInstance},
		{srcTask, srcEnv, srcInstance},
	}
	for _, order := range orders {
		sources := make([]Source, 0, len(order))
		for _, id := range order {
			source := NewMutableSource(id)
			source.Set(base[id])
			sources = append(sources, source)
		}
		selector, err := NewSelector(testGraph(t), sources)
		if err != nil {
			t.Fatalf("NewSelector: %v", err)
		}
		selection, err := selector.Refresh(context.Background(), testRequest(now))
		if err != nil {
			t.Fatalf("Refresh for order %v: %v", order, err)
		}
		if selection.Credential.Source != srcEnv {
			t.Fatalf("order %v selected %q, want %q", order, selection.Credential.Source, srcEnv)
		}
	}
}

func TestRefreshConflictOnIncomparableCandidates(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	env := NewMutableSource(srcEnv)
	peer := NewMutableSource(srcPeer)
	env.Set(cred(srcEnv, "env-id"))
	peer.Set(cred(srcPeer, "peer-id"))

	selector, err := NewSelector(testGraph(t), []Source{env, peer})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	selection, err := selector.Refresh(context.Background(), testRequest(now))
	if selection != nil {
		t.Fatalf("selection = %+v, want nil", selection)
	}
	conflict, ok := AsConflict(err)
	if !ok {
		t.Fatalf("err = %v, want ConflictError", err)
	}
	gotSources := make([]SourceID, 0, len(conflict.Candidates))
	for _, candidate := range conflict.Candidates {
		gotSources = append(gotSources, candidate.Source)
	}
	if !slices.Equal(gotSources, []SourceID{srcEnv, srcPeer}) {
		t.Fatalf("conflict sources = %v", gotSources)
	}
	if selector.Current() != nil {
		t.Fatal("conflicting refresh must not publish a selection")
	}
}

func TestConflictDoesNotOverridePublishedIdentity(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	env := NewMutableSource(srcEnv)
	peer := NewMutableSource(srcPeer)
	env.Set(cred(srcEnv, "env-id"))

	selector, err := NewSelector(testGraph(t), []Source{env, peer})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	first, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	peer.Set(cred(srcPeer, "peer-id"))
	retained, err := selector.Refresh(context.Background(), testRequest(now))
	if err == nil {
		t.Fatal("incomparable candidates must conflict")
	}
	if retained != first {
		t.Fatal("conflict must retain the previously published selection pointer")
	}
	if selector.Current() != first {
		t.Fatal("Current identity drifted after a conflicting refresh")
	}
}

func TestLowTrustNeverOverridesHighTrust(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	instance := NewMutableSource(srcInstance)
	task := NewMutableSource(srcTask)
	env := NewMutableSource(srcEnv)
	instance.Set(cred(srcInstance, "instance-id"))
	task.Set(cred(srcTask, "task-id"))
	env.Set(cred(srcEnv, "env-id"))

	selector, err := NewSelector(testGraph(t), []Source{instance, task, env})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	first, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	for round := 0; round < 3; round++ {
		task.Set(cred(srcTask, "task-id-rotated"))
		instance.Set(cred(srcInstance, "instance-id-rotated"))
		selection, err := selector.Refresh(context.Background(), testRequest(now))
		if err != nil {
			t.Fatalf("Refresh round %d: %v", round, err)
		}
		if selection.Credential.Source != srcEnv || selection.Credential.Identity != first.Credential.Identity {
			t.Fatalf("round %d selected %+v, high-trust identity drifted", round, selection.Credential)
		}
	}
}
