package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDecodePreservesSharingCyclesAndDeduplicatesLeases(t *testing.T) {
	registry := NewRegistry()
	token := registerTestToken(t, registry, "tenant-a", 7, "write", "read")

	graph := Graph{
		Root: 0,
		Nodes: []Node{
			{Kind: KindDict, Dict: map[string]NodeID{
				"left":  1,
				"right": 1,
				"alias": 4,
				"self":  0,
				"cycle": 6,
				"cap":   3,
				"cap2":  5,
				"ref":   7,
			}},
			{Kind: KindString, String: "shared"},
			{Kind: KindString, String: "unreachable"},
			{Kind: KindCapabilityRef, Token: token},
			{Kind: KindObjectRef, Target: 1},
			{Kind: KindCapabilityRef, Token: token},
			{Kind: KindArray, Array: []NodeID{0}},
			{Kind: KindObjectRef, Target: 0},
		},
	}

	value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 7}, registry)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	defer leases.Release()

	if value == nil || value.Kind != KindDict {
		t.Fatalf("Decode() root = %#v, want dict", value)
	}
	if value.Dict["left"] != value.Dict["right"] {
		t.Fatal("shared string identity was not preserved")
	}
	if value.Dict["alias"] != value.Dict["left"] {
		t.Fatal("object reference did not resolve to the shared object")
	}
	if value.Dict["self"] != value {
		t.Fatal("direct graph cycle was not preserved")
	}
	if cycle := value.Dict["cycle"]; cycle == nil || len(cycle.Array) != 1 || cycle.Array[0] != value {
		t.Fatal("cycle through an array was not preserved")
	}
	if value.Dict["ref"] != value {
		t.Fatal("object reference cycle was not preserved")
	}

	firstCapability := value.Dict["cap"]
	secondCapability := value.Dict["cap2"]
	if firstCapability == nil || firstCapability.Kind != KindCapabilityRef {
		t.Fatalf("capability value = %#v", firstCapability)
	}
	if firstCapability != secondCapability {
		t.Fatal("duplicate capability references did not share identity")
	}
	if firstCapability.Capability != secondCapability.Capability {
		t.Fatal("duplicate capability references did not share a lease")
	}
	if !firstCapability.Capability.Allows("read") || !firstCapability.Capability.Allows("write") {
		t.Fatalf("operations = %v, want read and write", firstCapability.Capability.Operations)
	}
	if firstCapability.Capability.Allows("delete") {
		t.Fatal("capability unexpectedly allows delete")
	}
	if leases.Len() != 1 {
		t.Fatalf("LeaseSet.Len() = %d, want 1", leases.Len())
	}
	if got := registry.ActiveLeaseCount(token); got != 1 {
		t.Fatalf("ActiveLeaseCount() = %d, want 1", got)
	}
	for _, formatted := range []string{fmt.Sprint(leases), fmt.Sprintf("%#v", leases)} {
		if strings.Contains(formatted, token) {
			t.Fatalf("lease diagnostic leaked bearer token: %s", formatted)
		}
	}

	releaseConcurrently(leases, 32)
	if leases.Active() {
		t.Fatal("lease set remained active after release")
	}
	if got := registry.ActiveLeaseCount(token); got != 0 {
		t.Fatalf("ActiveLeaseCount() = %d after release, want 0", got)
	}
}

func TestDecodeDeniesCrossTenantAtomically(t *testing.T) {
	registry := NewRegistry()
	tokenA := registerTestToken(t, registry, "tenant-a", 1, "read")
	tokenB := registerTestToken(t, registry, "tenant-b", 1, "read")

	graph := Graph{
		Root: 0,
		Nodes: []Node{
			{Kind: KindArray, Array: []NodeID{1, 2}},
			{Kind: KindCapabilityRef, Token: tokenA},
			{Kind: KindCapabilityRef, Token: tokenB},
		},
	}

	value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 1}, registry)
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Decode() error = %v, want ErrCapabilityDenied", err)
	}
	if value != nil || leases != nil {
		t.Fatal("failed decode returned a partial value or lease set")
	}
	if got := registry.ActiveLeaseCount(tokenA); got != 0 {
		t.Fatalf("active leases for token A = %d, want 0", got)
	}
	if got := registry.ActiveLeaseCount(tokenB); got != 0 {
		t.Fatalf("active leases for token B = %d, want 0", got)
	}
}

func TestDecodeRejectsStaleGeneration(t *testing.T) {
	registry := NewRegistry()
	token := registerTestToken(t, registry, "tenant-a", 2, "read")
	graph := Graph{
		Root:  0,
		Nodes: []Node{{Kind: KindCapabilityRef, Token: token}},
	}

	value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 1}, registry)
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Decode() error = %v, want ErrCapabilityDenied", err)
	}
	if value != nil || leases != nil {
		t.Fatal("stale generation returned a partial result")
	}
	if got := registry.ActiveLeaseCount(token); got != 0 {
		t.Fatalf("ActiveLeaseCount() = %d, want 0", got)
	}

	value, leases, err = Decode(graph, Principal{Tenant: "tenant-a", Generation: 2}, registry)
	if err != nil {
		t.Fatalf("Decode() at current generation error = %v", err)
	}
	if value == nil || leases == nil {
		t.Fatal("current generation returned no result")
	}
	leases.Release()
}

func TestDecodeValidatesWholeGraphBeforeLeasing(t *testing.T) {
	registry := NewRegistry()
	token := registerTestToken(t, registry, "tenant-a", 1, "read")
	graph := Graph{
		Root: 0,
		Nodes: []Node{
			{Kind: KindCapabilityRef, Token: token},
			{Kind: KindArray, Array: []NodeID{99}},
		},
	}

	value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 1}, registry)
	if !errors.Is(err, ErrInvalidNodeReference) {
		t.Fatalf("Decode() error = %v, want ErrInvalidNodeReference", err)
	}
	if value != nil || leases != nil {
		t.Fatal("invalid graph returned a partial result")
	}
	if got := registry.ActiveLeaseCount(token); got != 0 {
		t.Fatalf("invalid graph acquired %d leases, want 0", got)
	}
}

func TestDecodeRejectsInvalidGraphs(t *testing.T) {
	oversized := make([]Node, MaxGraphNodes+1)
	for i := range oversized {
		oversized[i] = Node{Kind: KindString, String: "node"}
	}

	tests := []struct {
		name  string
		graph Graph
		want  error
	}{
		{name: "empty", graph: Graph{}, want: ErrEmptyGraph},
		{name: "oversized", graph: Graph{Root: 0, Nodes: oversized}, want: ErrGraphTooLarge},
		{
			name:  "dangling root",
			graph: Graph{Root: 1, Nodes: []Node{{Kind: KindString}}},
			want:  ErrInvalidNodeReference,
		},
		{
			name: "dangling array edge",
			graph: Graph{
				Root:  0,
				Nodes: []Node{{Kind: KindArray, Array: []NodeID{2}}},
			},
			want: ErrInvalidNodeReference,
		},
		{
			name: "object reference cycle",
			graph: Graph{
				Root: 0,
				Nodes: []Node{
					{Kind: KindObjectRef, Target: 1},
					{Kind: KindObjectRef, Target: 0},
				},
			},
			want: ErrInvalidObjectReference,
		},
		{
			name: "object reference to capability",
			graph: Graph{
				Root: 0,
				Nodes: []Node{
					{Kind: KindObjectRef, Target: 1},
					{Kind: KindCapabilityRef, Token: "token"},
				},
			},
			want: ErrInvalidObjectReference,
		},
		{
			name: "empty capability token",
			graph: Graph{
				Root:  0,
				Nodes: []Node{{Kind: KindCapabilityRef}},
			},
			want: ErrMissingCapabilityToken,
		},
		{
			name: "invalid kind",
			graph: Graph{
				Root:  0,
				Nodes: []Node{{Kind: 0}},
			},
			want: ErrInvalidNodeKind,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, leases, err := Decode(test.graph, Principal{Tenant: "tenant-a"}, nil)
			if !errors.Is(err, test.want) {
				t.Fatalf("Decode() error = %v, want %v", err, test.want)
			}
			if value != nil || leases != nil {
				t.Fatal("invalid graph returned a partial result")
			}
		})
	}
}

func TestDecodeDeepCycleAtNodeLimit(t *testing.T) {
	nodes := make([]Node, MaxGraphNodes)
	for i := range nodes {
		next := NodeID((i + 1) % len(nodes))
		nodes[i] = Node{Kind: KindArray, Array: []NodeID{next}}
	}
	graph := Graph{Root: 0, Nodes: nodes}

	value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 1}, nil)
	if err != nil {
		t.Fatalf("Decode() deep cycle error = %v", err)
	}
	defer leases.Release()

	current := value
	for i := 0; i < MaxGraphNodes; i++ {
		if current == nil || current.Kind != KindArray || len(current.Array) != 1 {
			t.Fatalf("cycle node %d = %#v", i, current)
		}
		current = current.Array[0]
	}
	if current != value {
		t.Fatal("deep cycle did not return to the root object")
	}
}

func TestDecodeRequiresIssuerForCapabilities(t *testing.T) {
	graph := Graph{
		Root:  0,
		Nodes: []Node{{Kind: KindCapabilityRef, Token: "opaque-token"}},
	}
	value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 1}, nil)
	if !errors.Is(err, ErrMissingLeaseIssuer) {
		t.Fatalf("Decode() error = %v, want ErrMissingLeaseIssuer", err)
	}
	if value != nil || leases != nil {
		t.Fatal("missing issuer returned a partial result")
	}
}

func TestRevocationBlocksNewLeasesButKeepsExistingLeases(t *testing.T) {
	registry := NewRegistry()
	tokenA := registerTestToken(t, registry, "tenant-a", 3, "read")
	tokenB := registerTestToken(t, registry, "tenant-a", 3, "write")
	graph := Graph{
		Root: 0,
		Nodes: []Node{
			{Kind: KindArray, Array: []NodeID{1, 2}},
			{Kind: KindCapabilityRef, Token: tokenA},
			{Kind: KindCapabilityRef, Token: tokenB},
		},
	}
	principal := Principal{Tenant: "tenant-a", Generation: 3}

	value, existing, err := Decode(graph, principal, registry)
	if err != nil {
		t.Fatalf("first Decode() error = %v", err)
	}
	if value == nil || existing.Len() != 2 {
		t.Fatalf("first decode returned %#v and %d leases", value, existing.Len())
	}
	if !registry.Revoke(tokenA) || !registry.Revoke(tokenB) {
		t.Fatal("Revoke() failed for a registered token")
	}
	if !existing.Active() {
		t.Fatal("revocation invalidated an existing lease")
	}

	value, next, err := Decode(graph, principal, registry)
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("decode after revocation error = %v, want ErrCapabilityDenied", err)
	}
	if value != nil || next != nil {
		t.Fatal("decode after revocation returned a partial result")
	}
	if got := registry.ActiveLeaseCount(tokenA); got != 1 {
		t.Fatalf("active leases for revoked token A = %d, want 1", got)
	}
	if got := registry.ActiveLeaseCount(tokenB); got != 1 {
		t.Fatalf("active leases for revoked token B = %d, want 1", got)
	}

	existing.Release()
	existing.Release()
	if got := registry.ActiveLeaseCount(tokenA); got != 0 {
		t.Fatalf("active leases for token A after release = %d, want 0", got)
	}
}

func TestDecodeRevokeRaceScheduling(t *testing.T) {
	t.Run("revoke before batch acquisition", func(t *testing.T) {
		registry := NewRegistry()
		token := registerTestToken(t, registry, "tenant-a", 4, "read")
		issuer := &gatedIssuer{
			inner:   registry,
			entered: make(chan struct{}),
			proceed: make(chan struct{}),
			ready:   make(chan struct{}),
		}
		graph := Graph{Root: 0, Nodes: []Node{{Kind: KindCapabilityRef, Token: token}}}
		result := make(chan decodeResult, 1)
		go func() {
			value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 4}, issuer)
			result <- decodeResult{value: value, leases: leases, err: err}
		}()

		waitSignal(t, issuer.entered, "decoder did not reach lease acquisition")
		if !registry.Revoke(token) {
			t.Fatal("Revoke() failed")
		}
		close(issuer.proceed)
		waitSignal(t, issuer.ready, "issuer did not finish acquisition attempt")

		got := waitDecode(t, result)
		if !errors.Is(got.err, ErrCapabilityDenied) {
			t.Fatalf("Decode() error = %v, want ErrCapabilityDenied", got.err)
		}
		if got.value != nil || got.leases != nil {
			t.Fatal("denied decode returned a partial result")
		}
		if count := registry.ActiveLeaseCount(token); count != 0 {
			t.Fatalf("revoke-before-acquire left %d active leases", count)
		}
	})

	t.Run("revoke after batch acquisition", func(t *testing.T) {
		registry := NewRegistry()
		token := registerTestToken(t, registry, "tenant-a", 5, "read")
		issuer := &gatedIssuer{
			inner:   registry,
			entered: make(chan struct{}),
			proceed: make(chan struct{}),
			ready:   make(chan struct{}),
			finish:  make(chan struct{}),
		}
		graph := Graph{Root: 0, Nodes: []Node{{Kind: KindCapabilityRef, Token: token}}}
		result := make(chan decodeResult, 1)
		go func() {
			value, leases, err := Decode(graph, Principal{Tenant: "tenant-a", Generation: 5}, issuer)
			result <- decodeResult{value: value, leases: leases, err: err}
		}()

		waitSignal(t, issuer.entered, "decoder did not reach lease acquisition")
		close(issuer.proceed)
		waitSignal(t, issuer.ready, "issuer did not acquire the batch")
		if !registry.Revoke(token) {
			t.Fatal("Revoke() failed")
		}
		if count := registry.ActiveLeaseCount(token); count != 1 {
			t.Fatalf("active leases after revocation = %d, want 1", count)
		}
		close(issuer.finish)

		got := waitDecode(t, result)
		if got.err != nil {
			t.Fatalf("Decode() error = %v", got.err)
		}
		if got.value == nil || got.leases == nil || got.leases.Len() != 1 {
			t.Fatalf("successful decode returned %#v with %d leases", got.value, got.leases.Len())
		}
		if !got.leases.Active() {
			t.Fatal("acquired lease was invalidated by a later revocation")
		}
		got.leases.Release()
		if count := registry.ActiveLeaseCount(token); count != 0 {
			t.Fatalf("active leases after release = %d, want 0", count)
		}
	})
}

func registerTestToken(t *testing.T, registry *Registry, tenant string, generation uint64, operations ...string) string {
	t.Helper()
	token, err := NewCapabilityToken()
	if err != nil {
		t.Fatalf("NewCapabilityToken() error = %v", err)
	}
	if err := registry.Register(token, tenant, generation, operations...); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return token
}

func releaseConcurrently(leases *LeaseSet, count int) {
	var wait sync.WaitGroup
	wait.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wait.Done()
			leases.Release()
		}()
	}
	wait.Wait()
}

type gatedIssuer struct {
	inner   *Registry
	entered chan struct{}
	proceed chan struct{}
	ready   chan struct{}
	finish  chan struct{}
}

func (g *gatedIssuer) AcquireAll(principal Principal, tokens []string) (*LeaseSet, error) {
	close(g.entered)
	<-g.proceed
	leases, err := g.inner.AcquireAll(principal, tokens)
	close(g.ready)
	if g.finish != nil {
		<-g.finish
	}
	return leases, err
}

type decodeResult struct {
	value  *Value
	leases *LeaseSet
	err    error
}

func waitSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func waitDecode(t *testing.T, result <-chan decodeResult) decodeResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Decode")
		return decodeResult{}
	}
}
