package netsec

import "testing"

func TestMarkerDistinguishesSameRequestIDAcrossTenantsAndEmptyInput(t *testing.T) {
	a := Request{TenantID: "a", RequestID: "same", ModelGeneration: 1, MaxOutputTokens: 6, DecodeStrategy: "greedy"}
	b := Request{TenantID: "b", RequestID: "same", ModelGeneration: 1, MaxOutputTokens: 6, DecodeStrategy: "greedy"}
	if MarkerToken(a) == MarkerToken(b) {
		t.Fatal("marker must include tenant identity")
	}
	output := MarkerOutput(a)
	if len(output) == 0 || output[0] != MarkerToken(a) {
		t.Fatalf("empty input marker output = %v", output)
	}
}

func TestSearchSafeMappingFindsNoLeak(t *testing.T) {
	cfg := LeakSearchConfig{
		Requests: []Request{
			{TenantID: "tenant-a", RequestID: "same", ModelGeneration: 1, InputTokens: []uint32{1}, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
			{TenantID: "tenant-b", RequestID: "same", ModelGeneration: 1, InputTokens: nil, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
		},
		Mapping:   MappingByRowIndex,
		MaxDepth:  3,
		MaxStates: 5000,
	}
	result, err := SearchShortestLeak(cfg)
	if err != nil {
		t.Fatalf("SearchShortestLeak: %v", err)
	}
	if result.Found {
		t.Fatalf("safe mapping produced a leak trace: %+v", result.Trace)
	}
	if result.Explored == 0 {
		t.Fatal("search did not explore any state")
	}
}

func TestSearchFindsShortestCompletionOrderLeak(t *testing.T) {
	cfg := LeakSearchConfig{
		Requests: []Request{
			{TenantID: "tenant-a", RequestID: "same", ModelGeneration: 1, InputTokens: []uint32{9}, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
			{TenantID: "tenant-b", RequestID: "same", ModelGeneration: 1, InputTokens: nil, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
		},
		Mapping:   MappingByCompletionOrder,
		MaxDepth:  4,
		MaxStates: 5000,
	}
	result, err := SearchShortestLeak(cfg)
	if err != nil {
		t.Fatalf("SearchShortestLeak: %v", err)
	}
	if !result.Found {
		t.Fatalf("completion-order mapping did not produce a leak: %s", result.Reason)
	}
	if len(result.Trace.Actions) != 1 {
		t.Fatalf("shortest leak has %d actions, want 1: %+v", len(result.Trace.Actions), result.Trace.Actions)
	}
	action := result.Trace.Actions[0]
	if action.Kind != ActionCompleteRow {
		t.Fatalf("unexpected shortest action: %+v", action)
	}
	witness := result.Trace.Witness
	if witness.ToTenantID != "tenant-a" || witness.FromTenantID != "tenant-b" {
		t.Fatalf("witness = %+v", witness)
	}
	if witness.ToRequestID != "same" || witness.FromRequestID != "same" {
		t.Fatalf("tenant-local IDs were collapsed: %+v", witness)
	}
}

func TestSearchDoesNotLeakAfterModelSwitch(t *testing.T) {
	cfg := LeakSearchConfig{
		Requests: []Request{
			{TenantID: "tenant-a", RequestID: "a", ModelGeneration: 1, InputTokens: []uint32{1}, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
			{TenantID: "tenant-b", RequestID: "b", ModelGeneration: 1, InputTokens: []uint32{2}, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
		},
		Mapping:   MappingByRowIndex,
		MaxDepth:  2,
		MaxStates: 2000,
	}
	result, err := SearchShortestLeak(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Found {
		t.Fatalf("unexpected leak: %+v", result.Trace)
	}
}
