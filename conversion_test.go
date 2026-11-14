package netsec_test

import (
	"testing"

	netsec "example.org/netsec/q0487"
)

// Scenario 5: explicit cross-version conversion and re-validation.
func TestVersionConversion(t *testing.T) {
	registry := testRegistry(t)
	v1, _ := registry.Schema("v1")
	v2, _ := registry.Schema("v2")

	src, err := netsec.NewSanitizer(v1, netsec.PolicyDiscard).Sanitize(netsec.RawMessage{
		{Number: 1, Value: int64(77)},
		{Number: 2, Value: "operator"},
		{Number: 3, Value: "x"},
	})
	if err != nil {
		t.Fatalf("sanitize v1: %v", err)
	}

	t.Run("only explicitly mapped fields cross the version boundary", func(t *testing.T) {
		plan, err := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{
			{From: 1, To: 1},
			{From: 3, To: 3},
		})
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}

		rawV2, result, err := plan.Convert(src)
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if result.Mapped != 2 || result.Unmapped != 1 {
			t.Fatalf("result = %+v, want Mapped=2 Unmapped=1", result)
		}

		dst, err := netsec.NewSanitizer(v2, netsec.PolicyReject).Sanitize(rawV2)
		if err != nil {
			t.Fatalf("sanitize converted v2: %v", err)
		}
		got := snapshot(t, dst)
		if _, present := got[2]; present {
			t.Fatal("v1 role must not silently become v2 route_hint")
		}
		if len(got) != 2 {
			t.Fatalf("converted message = %v, want fields 1 and 3 only", got)
		}
	})

	t.Run("explicit mapping can repurpose the reused number safely", func(t *testing.T) {
		// The operator explicitly maps v1 role(2) onto v2 route_hint(2);
		// this is an acknowledged, audited decision, not implicit reuse.
		plan, err := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{
			{From: 2, To: 2},
		})
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}
		rawV2, _, err := plan.Convert(src)
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		dst, err := netsec.NewSanitizer(v2, netsec.PolicyReject).Sanitize(rawV2)
		if err != nil {
			t.Fatalf("sanitize v2: %v", err)
		}
		values, ok := dst.GetByName("route_hint")
		if !ok || values[0] != "operator" {
			t.Fatalf("route_hint = %v, %v", values, ok)
		}
	})

	t.Run("type change requires an explicit transform", func(t *testing.T) {
		// Map v1 role(string,2) onto v2 privilege_level(int64,4).
		plan, err := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{
			{
				From: 2,
				To:   4,
				Transform: func(v any) (any, error) {
					if v == "operator" {
						return int64(9), nil
					}
					return int64(1), nil
				},
			},
		})
		if err != nil {
			t.Fatalf("build plan with transform: %v", err)
		}
		rawV2, _, err := plan.Convert(src)
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		dst, err := netsec.NewSanitizer(v2, netsec.PolicyReject).Sanitize(rawV2)
		if err != nil {
			t.Fatalf("sanitize v2: %v", err)
		}
		values, _ := dst.GetByName("privilege_level")
		if values[0] != int64(9) {
			t.Fatalf("privilege_level = %v, want 9", values)
		}
	})

	t.Run("plan without transform rejects a type change", func(t *testing.T) {
		_, err := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{
			{From: 2, To: 4}, // string -> int64, no Transform
		})
		if err == nil {
			t.Fatal("implicit type change must be rejected at plan construction")
		}
	})

	t.Run("plan construction rejects reserved and unknown endpoints", func(t *testing.T) {
		if _, err := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{
			{From: 100, To: 1},
		}); err == nil {
			t.Fatal("reserved source number must fail")
		}
		if _, err := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{
			{From: 1, To: 999},
		}); err == nil {
			t.Fatal("unknown target number must fail")
		}
		if _, err := registry.NewConversionPlan("v1", "v9", nil); err == nil {
			t.Fatal("unregistered version must fail")
		}
	})

	t.Run("conversion refuses a message from the wrong source version", func(t *testing.T) {
		other, err := netsec.NewSanitizer(v2, netsec.PolicyDiscard).Sanitize(netsec.RawMessage{
			{Number: 1, Value: int64(1)},
		})
		if err != nil {
			t.Fatalf("sanitize v2: %v", err)
		}
		plan, err := registry.NewConversionPlan("v1", "v2", []netsec.FieldMapping{{From: 1, To: 1}})
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}
		if _, _, err := plan.Convert(other); err == nil {
			t.Fatal("version mismatch must be rejected")
		}
	})
}

// Downstream contract: code holding only KnownFieldReader can never reach
// unknown, quarantined or reserved fields, and cannot mutate the view.
func TestDownstreamOnlyReadsKnownFields(t *testing.T) {
	registry := testRegistry(t)
	schema, _ := registry.Schema("v1")

	sanitized, err := netsec.NewSanitizer(schema, netsec.PolicyQuarantine).Sanitize(netsec.RawMessage{
		{Number: 1, Value: int64(8)},
		{Number: 2, Value: "guest"},
		{Number: 900, Value: "injected"},
	})
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}

	var reader netsec.KnownFieldReader = sanitized
	for _, n := range reader.Numbers() {
		if n != 1 && n != 2 {
			t.Fatalf("downstream reached non-known field number %d", n)
		}
	}
	if reader.Get(900) != nil {
		t.Fatal("injected field reachable through the read interface")
	}

	// Mutating the slice returned by Get must not change the stored view.
	values := reader.Get(1)
	values[0] = int64(999)
	if again := reader.Get(1); again[0] != int64(8) {
		t.Fatalf("stored view mutated through returned slice: %v", again)
	}
}
