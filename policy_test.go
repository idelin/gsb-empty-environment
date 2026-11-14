package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0487"
)

// testRegistry builds two related versions.
//
// v1: 1 user_id(int64,singular), 2 role(string,singular),
//
//	3 tags(string,repeated,max 3), reserved 100..109.
//
// v2: 1 user_id(int64,singular), 2 route_hint(string,singular) -- number 2
//
//	was reused with new meaning, 3 tags(string,repeated,max 3),
//	4 privilege_level(int64,singular), reserved 100..109.
func testRegistry(t *testing.T) *netsec.Registry {
	t.Helper()
	registry := netsec.NewRegistry()

	v1, err := netsec.NewVersionSchema(
		"v1",
		[]netsec.FieldSpec{
			{Number: 1, Name: "user_id", Type: netsec.TypeInt64, Cardinality: netsec.CardinalitySingular},
			{Number: 2, Name: "role", Type: netsec.TypeString, Cardinality: netsec.CardinalitySingular},
			{Number: 3, Name: "tags", Type: netsec.TypeString, Cardinality: netsec.CardinalityRepeated, MaxCount: 3},
		},
		[]netsec.NumberRange{{From: 100, To: 109}},
	)
	if err != nil {
		t.Fatalf("build v1: %v", err)
	}

	v2, err := netsec.NewVersionSchema(
		"v2",
		[]netsec.FieldSpec{
			{Number: 1, Name: "user_id", Type: netsec.TypeInt64, Cardinality: netsec.CardinalitySingular},
			{Number: 2, Name: "route_hint", Type: netsec.TypeString, Cardinality: netsec.CardinalitySingular},
			{Number: 3, Name: "tags", Type: netsec.TypeString, Cardinality: netsec.CardinalityRepeated, MaxCount: 3},
			{Number: 4, Name: "privilege_level", Type: netsec.TypeInt64, Cardinality: netsec.CardinalitySingular},
		},
		[]netsec.NumberRange{{From: 100, To: 109}},
	)
	if err != nil {
		t.Fatalf("build v2: %v", err)
	}

	if err := registry.Register(v1); err != nil {
		t.Fatalf("register v1: %v", err)
	}
	if err := registry.Register(v2); err != nil {
		t.Fatalf("register v2: %v", err)
	}
	return registry
}

// snapshot reads everything a downstream component can reach through the
// KnownFieldReader interface.
func snapshot(t *testing.T, r netsec.KnownFieldReader) map[int][]any {
	t.Helper()
	seen := make(map[int][]any)
	for _, n := range r.Numbers() {
		seen[n] = r.Get(n)
	}
	return seen
}

func assertViolation(t *testing.T, err error, want netsec.ViolationKind) {
	t.Helper()
	ve, ok := netsec.AsValidationError(err)
	if !ok {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	for _, v := range ve.Violations {
		if v.Kind == want {
			return
		}
	}
	t.Fatalf("violation %s not found in %#v", want, ve.Violations)
}

// Scenario 1: unknown-field injection. An attacker smuggles privilege and
// routing fields using numbers the schema does not know about.
func TestUnknownFieldInjection(t *testing.T) {
	registry := testRegistry(t)
	schema, _ := registry.Schema("v1")

	attack := netsec.RawMessage{
		{Number: 1, Value: int64(42)},
		{Number: 2, Value: "member"},
		{Number: 900, Value: "is_admin=true"},          // injected privilege marker
		{Number: 901, Value: "route://internal/admin"}, // injected routing
	}

	t.Run("reject policy blocks the message", func(t *testing.T) {
		sanitizer := netsec.NewSanitizer(schema, netsec.PolicyReject)
		out, err := sanitizer.Sanitize(attack)
		if err == nil {
			t.Fatal("injected unknown fields must be rejected")
		}
		if out != nil {
			t.Fatal("rejected message must not produce a readable view")
		}
		assertViolation(t, err, netsec.ViolationUnknownField)
	})

	t.Run("discard policy strips injected fields", func(t *testing.T) {
		sanitizer := netsec.NewSanitizer(schema, netsec.PolicyDiscard)
		out, err := sanitizer.Sanitize(attack)
		if err != nil {
			t.Fatalf("discard policy should accept message: %v", err)
		}
		got := snapshot(t, out)
		if len(got) != 2 {
			t.Fatalf("downstream sees %v, want only fields 1 and 2", got)
		}
		if _, reachable := got[900]; reachable {
			t.Fatal("injected field 900 must not be readable downstream")
		}
		if out.Stats().UnknownDropped != 2 {
			t.Fatalf("stats: got dropped=%d, want 2", out.Stats().UnknownDropped)
		}
		if len(out.Quarantine()) != 0 {
			t.Fatal("discard policy must not retain quarantined data")
		}
	})

	t.Run("quarantine policy isolates injected fields", func(t *testing.T) {
		sanitizer := netsec.NewSanitizer(schema, netsec.PolicyQuarantine)
		out, err := sanitizer.Sanitize(attack)
		if err != nil {
			t.Fatalf("quarantine policy should accept message: %v", err)
		}
		if _, reachable := snapshot(t, out)[901]; reachable {
			t.Fatal("quarantined field 901 must not be readable downstream")
		}
		q := out.Quarantine()
		if len(q) != 2 || q[0].Number != 900 || q[1].Number != 901 {
			t.Fatalf("quarantine = %#v, want fields 900 and 901", q)
		}
		if out.Stats().UnknownQuarantined != 2 {
			t.Fatalf("stats: got quarantined=%d, want 2", out.Stats().UnknownQuarantined)
		}
	})
}

// Scenario 2: number reuse. Reserved numbers must never carry data.
func TestReservedNumberReuse(t *testing.T) {
	registry := testRegistry(t)
	schema, _ := registry.Schema("v1")

	raw := netsec.RawMessage{
		{Number: 1, Value: int64(7)},
		{Number: 100, Value: "squatted reserved number"},
	}
	for _, policy := range []netsec.UnknownPolicy{
		netsec.PolicyReject,
		netsec.PolicyDiscard,
		netsec.PolicyQuarantine,
	} {
		out, err := netsec.NewSanitizer(schema, policy).Sanitize(raw)
		if err == nil {
			t.Fatalf("policy %d: reserved number must reject", policy)
		}
		if out != nil {
			t.Fatalf("policy %d: reserved rejection yields no view", policy)
		}
		assertViolation(t, err, netsec.ViolationReservedNumber)
	}

	if _, err := netsec.NewVersionSchema(
		"bad",
		[]netsec.FieldSpec{
			{Number: 100, Name: "squat", Type: netsec.TypeString, Cardinality: netsec.CardinalitySingular},
		},
		[]netsec.NumberRange{{From: 100, To: 109}},
	); err == nil {
		t.Fatal("declaring a known field on a reserved number must fail")
	}
}

// Scenario 3: type conflicts and repetition violations are hard failures.
func TestTypeAndCardinalityConflicts(t *testing.T) {
	registry := testRegistry(t)
	schema, _ := registry.Schema("v1")
	sanitizer := netsec.NewSanitizer(schema, netsec.PolicyDiscard)

	t.Run("wrong dynamic type is rejected", func(t *testing.T) {
		_, err := sanitizer.Sanitize(netsec.RawMessage{
			{Number: 1, Value: "not-an-int"},
		})
		assertViolation(t, err, netsec.ViolationTypeMismatch)
	})

	t.Run("singular field repeated is rejected", func(t *testing.T) {
		_, err := sanitizer.Sanitize(netsec.RawMessage{
			{Number: 1, Value: int64(1)},
			{Number: 1, Value: int64(2)},
		})
		assertViolation(t, err, netsec.ViolationSingularRepeated)
	})

	t.Run("repeated field over MaxCount is rejected", func(t *testing.T) {
		_, err := sanitizer.Sanitize(netsec.RawMessage{
			{Number: 3, Value: "a"},
			{Number: 3, Value: "b"},
			{Number: 3, Value: "c"},
			{Number: 3, Value: "d"},
		})
		assertViolation(t, err, netsec.ViolationMaxCountExceeded)
	})

	t.Run("unknown policy never downgrades hard violations", func(t *testing.T) {
		quarantine := netsec.NewSanitizer(schema, netsec.PolicyQuarantine)
		_, err := quarantine.Sanitize(netsec.RawMessage{
			{Number: 1, Value: true}, // known number, wrong type
		})
		var ve *netsec.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("type conflict must reject under quarantine policy, got %v", err)
		}
	})
}

// Scenario 4: a fully legal message round-trips and exposes only known data.
func TestLegalMessage(t *testing.T) {
	registry := testRegistry(t)
	schema, _ := registry.Schema("v2")
	raw := netsec.RawMessage{
		{Number: 1, Value: int64(555)},
		{Number: 3, Value: "alpha"},
		{Number: 3, Value: "beta"},
		{Number: 4, Value: int64(3)},
	}

	out, err := netsec.NewSanitizer(schema, netsec.PolicyReject).Sanitize(raw)
	if err != nil {
		t.Fatalf("legal message rejected: %v", err)
	}
	if out.Version() != "v2" {
		t.Fatalf("version = %q, want v2", out.Version())
	}
	got := snapshot(t, out)
	if len(got) != 3 {
		t.Fatalf("legal message exposes %v, want 3 populated fields", got)
	}
	if ids := out.Get(1); len(ids) != 1 || ids[0] != int64(555) {
		t.Fatalf("user_id = %v", ids)
	}
	tags := out.Get(3)
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("tags = %v", tags)
	}
	if values, ok := out.GetByName("privilege_level"); !ok || values[0] != int64(3) {
		t.Fatalf("privilege_level lookup = %v, %v", values, ok)
	}
	if _, ok := out.GetByName("nonexistent"); ok {
		t.Fatal("unknown name must not resolve")
	}
	if out.Stats().KnownAccepted != 4 {
		t.Fatalf("accepted count = %d, want 4", out.Stats().KnownAccepted)
	}
	if len(out.Quarantine()) != 0 {
		t.Fatal("legal message must have empty quarantine")
	}
}
