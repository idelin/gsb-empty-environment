package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

const (
	roleDefiner  PrincipalID = "role_definer"
	roleCaller   PrincipalID = "role_caller"
	roleAttacker PrincipalID = "role_attacker"
	roleAdmin    PrincipalID = "role_admin"
	roleSystem   PrincipalID = "role_system"
)

type fixture struct {
	c *Catalog
	e *Engine
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	c := NewCatalog()
	mustSchema(t, c, Schema{ID: "sys", Name: "pg_catalog", Owner: roleSystem, System: true, ReadOnly: true})
	mustSchema(t, c, Schema{ID: "legacy", Name: "legacy_readonly", Owner: roleAdmin, ReadOnly: true})
	mustSchema(t, c, Schema{ID: "shadow", Name: "shadow_readonly", Owner: roleAttacker, ReadOnly: true})
	mustSchema(t, c, Schema{ID: "app", Name: "app", Owner: roleDefiner})
	mustSchema(t, c, Schema{ID: "public", Name: "public_writable", Owner: roleAdmin, PublicWritable: true})
	mustSchema(t, c, Schema{ID: "foreign", Name: "foreign", Owner: roleAttacker})
	mustSchema(t, c, Schema{ID: "temp", Name: "pg_temp", Owner: roleAttacker, Temporary: true})

	mustObject(t, c, Object{ID: "accounts", SchemaID: "app", Kind: KindTable, Name: "accounts", Owner: roleDefiner, Version: 1})
	mustObject(t, c, Object{ID: "lookup_legacy", SchemaID: "legacy", Kind: KindFunction, Name: "lookup", Signature: "(integer)", Owner: roleAdmin, Version: 1})
	mustObject(t, c, Object{ID: "lookup_app", SchemaID: "app", Kind: KindFunction, Name: "lookup", Signature: "(integer)", Owner: roleDefiner, Version: 2})
	mustObject(t, c, Object{ID: "operator_int", SchemaID: "app", Kind: KindOperator, Name: "+", Signature: "(integer,integer)", Owner: roleDefiner, Version: 3})
	mustObject(t, c, Object{ID: "operator_text", SchemaID: "app", Kind: KindOperator, Name: "+", Signature: "(text,text)", Owner: roleDefiner, Version: 4})
	mustObject(t, c, Object{ID: "temp_accounts", SchemaID: "temp", Kind: KindTable, Name: "accounts", Owner: roleAttacker, Version: 1})
	return fixture{c: c}
}

func mustSchema(t *testing.T, c *Catalog, schema Schema) {
	t.Helper()
	if err := c.AddSchema(schema); err != nil {
		t.Fatalf("AddSchema(%s): %v", schema.ID, err)
	}
}

func mustObject(t *testing.T, c *Catalog, object Object) {
	t.Helper()
	if err := c.AddObject(object); err != nil {
		t.Fatalf("AddObject(%s): %v", object.ID, err)
	}
}

func basicDefinition(body AST) FunctionDefinition {
	return FunctionDefinition{
		ID:             "fn_secure",
		Name:           "secure_fn",
		SchemaID:       "app",
		DefinerID:      roleDefiner,
		SearchPath:     []SchemaID{"sys", "app"},
		AST:            body,
		AllowedCallers: map[PrincipalID]bool{roleCaller: true},
	}
}

func TestPublishRejectsUnsafeSearchPaths(t *testing.T) {
	tests := []struct {
		name string
		path []SchemaID
		want error
	}{
		{"mutable names instead of IDs", []SchemaID{"pg_catalog", "app"}, ErrInvalidSearchPath},
		{"first schema is not system", []SchemaID{"app"}, ErrUnsafeSystemSchema},
		{"caller path marker", []SchemaID{"sys", CallerSearchPath}, ErrTemporarySchema},
		{"temp schema", []SchemaID{"sys", "temp"}, ErrTemporarySchema},
		{"public writable schema", []SchemaID{"sys", "public"}, ErrPublicWritableSchema},
		{"named public writable schema", []SchemaID{"sys", "foreign"}, ErrWritableSchema},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			def := basicDefinition(AST{TableRef("accounts")})
			def.SearchPath = tc.path
			_, err := f.c.Publish(def)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Publish() error = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("duplicate schema IDs", func(t *testing.T) {
		f := newFixture(t)
		def := basicDefinition(AST{TableRef("accounts")})
		def.SearchPath = []SchemaID{"sys", "app", "app"}
		_, err := f.c.Publish(def)
		if !errors.Is(err, ErrInvalidSearchPath) {
			t.Fatalf("Publish() error = %v, want duplicate-path rejection", err)
		}
	})

	t.Run("system schema writable", func(t *testing.T) {
		f := newFixture(t)
		sys := f.c.Schemas["sys"]
		sys.ReadOnly = false
		f.c.Schemas["sys"] = sys
		def := basicDefinition(AST{TableRef("accounts")})
		_, err := f.c.Publish(def)
		if !errors.Is(err, ErrUnsafeSystemSchema) {
			t.Fatalf("Publish() error = %v, want writable system rejection", err)
		}
	})
}

func TestPublishFreezesTableAndRejectsTempObject(t *testing.T) {
	f := newFixture(t)
	def := basicDefinition(AST{TableRef("accounts")})
	pub, err := f.c.Publish(def)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	m := pub.Manifest()
	if len(m.Dependencies) != 1 {
		t.Fatalf("manifest dependencies = %d, want 1", len(m.Dependencies))
	}
	dep := m.Dependencies[0]
	if dep.Ref.ObjectID != "accounts" || dep.Ref.Version != 1 {
		t.Fatalf("frozen ref = %+v, want accounts version 1", dep.Ref)
	}
	if dep.Ref.Kind != KindTable || dep.Ref.SearchIndex != 1 {
		t.Fatalf("frozen ref metadata = %+v", dep.Ref)
	}

	t.Run("qualified temp table", func(t *testing.T) {
		def := basicDefinition(AST{QualifiedTableRef("temp", "accounts")})
		_, err := f.c.Publish(def)
		if !errors.Is(err, ErrTemporarySchema) {
			t.Fatalf("Publish(temp ref) error = %v, want ErrTemporarySchema", err)
		}
	})

	t.Run("qualified ref outside path", func(t *testing.T) {
		def := basicDefinition(AST{QualifiedTableRef("legacy", "accounts")})
		_, err := f.c.Publish(def)
		if !errors.Is(err, ErrOutsideSearchPath) {
			t.Fatalf("Publish(outside ref) error = %v, want ErrOutsideSearchPath", err)
		}
	})
}

func TestFunctionShadowingAndOperatorOverload(t *testing.T) {
	f := newFixture(t)
	def := basicDefinition(AST{
		FunctionRef("lookup", "(integer)"),
		OperatorRef("+(integer,integer)"),
		OperatorRef("+(text,text)"),
	})
	def.SearchPath = []SchemaID{"sys", "legacy", "app"}
	pub, err := f.c.Publish(def)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	m := pub.Manifest()
	if got := m.Dependencies[0].Ref.ObjectID; got != "lookup_legacy" {
		t.Fatalf("function resolved to %q, want shadowing object lookup_legacy", got)
	}
	if got := m.Dependencies[1].Ref.ObjectID; got != "operator_int" {
		t.Fatalf("integer operator resolved to %q, want operator_int", got)
	}
	if got := m.Dependencies[2].Ref.ObjectID; got != "operator_text" {
		t.Fatalf("text operator resolved to %q, want operator_text", got)
	}

	var calls []ObjectID
	f.e = NewEngine(f.c, func(call FrozenCall) (any, error) {
		if call.Principal != roleDefiner {
			t.Fatalf("callback principal = %q, want definer", call.Principal)
		}
		calls = append(calls, call.ObjectID)
		return string(call.ObjectID), nil
	})
	if err := f.e.Register(pub); err != nil {
		t.Fatalf("Register: %v", err)
	}
	result, err := f.e.Invoke(pub, Invocation{CallerID: roleCaller})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	want := []ObjectID{"lookup_legacy", "operator_int", "operator_text"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("callback object IDs = %v, want %v", calls, want)
	}
	if len(result.Trace) != len(want) {
		t.Fatalf("trace length = %d, want %d", len(result.Trace), len(want))
	}
}

func TestExecutionContextAndParameterSideEffect(t *testing.T) {
	f := newFixture(t)
	pub, err := f.c.Publish(basicDefinition(AST{Sequence(TableRef("accounts"), Param{Index: 0})}))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var (
		order       []string
		callbackIDs []ObjectID
	)
	f.e = NewEngine(f.c, func(call FrozenCall) (any, error) {
		order = append(order, "callback")
		callbackIDs = append(callbackIDs, call.ObjectID)
		if call.Principal != roleDefiner {
			t.Fatalf("callback principal = %q, want definer", call.Principal)
		}
		return "row", nil
	})
	result, err := f.e.Invoke(pub, Invocation{
		CallerID: roleCaller,
		EvaluateParameters: func(ctx ParameterContext) ([]any, error) {
			order = append(order, "parameters")
			if ctx.CallerID != roleCaller || ctx.DefinerID != roleDefiner {
				t.Fatalf("parameter context = %+v", ctx)
			}
			return []any{"side-effect-value"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"parameters", "callback"}) {
		t.Fatalf("execution order = %v, want parameter evaluation before definer callback", order)
	}
	if !reflect.DeepEqual(callbackIDs, []ObjectID{"accounts"}) {
		t.Fatalf("callback IDs = %v", callbackIDs)
	}
	if result.Value != "side-effect-value" {
		t.Fatalf("result = %#v, want caller-evaluated parameter", result.Value)
	}
}

func TestAllowedCallersAndRevocation(t *testing.T) {
	f := newFixture(t)
	pub, err := f.c.Publish(basicDefinition(AST{TableRef("accounts")}))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	f.e = NewEngine(f.c, func(call FrozenCall) (any, error) { return nil, nil })
	if err := f.e.Register(pub); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := f.e.Invoke(pub, Invocation{CallerID: "stranger"}); !errors.Is(err, ErrCallerDenied) {
		t.Fatalf("Invoke(stranger) error = %v, want ErrCallerDenied", err)
	}
	f.e.Revoke(pub.ID)
	if _, err := f.e.Invoke(pub, Invocation{CallerID: roleCaller}); !errors.Is(err, ErrFunctionRevoked) {
		t.Fatalf("Invoke(revoked) error = %v, want ErrFunctionRevoked", err)
	}
}

func TestVersionSchemaAndDropRecreationValidation(t *testing.T) {
	f := newFixture(t)
	pub, err := f.c.Publish(basicDefinition(AST{TableRef("accounts")}))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	f.e = NewEngine(f.c, func(call FrozenCall) (any, error) { return nil, nil })
	if err := f.e.Register(pub); err != nil {
		t.Fatalf("Register: %v", err)
	}

	inv := Invocation{CallerID: roleCaller}
	obj := f.c.Objects["accounts"]
	obj.Version = 2
	f.c.Objects["accounts"] = obj
	if _, err := f.e.Invoke(pub, inv); !errors.Is(err, ErrStaleObject) {
		t.Fatalf("Invoke(version changed) error = %v, want ErrStaleObject", err)
	}
	obj.Version = 1
	f.c.Objects["accounts"] = obj

	schema := f.c.Schemas["app"]
	schema.Writers = map[PrincipalID]bool{roleAttacker: true}
	f.c.Schemas["app"] = schema
	if _, err := f.e.Invoke(pub, inv); !errors.Is(err, ErrUnsafeSchemaAtExecution) {
		t.Fatalf("Invoke(schema writable) error = %v, want ErrUnsafeSchemaAtExecution", err)
	}
	schema.Writers = nil
	f.c.Schemas["app"] = schema

	f.c.DropObject("accounts")
	mustObject(t, f.c, Object{ID: "accounts_recreated", SchemaID: "app", Kind: KindTable, Name: "accounts", Owner: roleDefiner, Version: 1})
	if _, err := f.e.Invoke(pub, inv); !errors.Is(err, ErrStaleObject) {
		t.Fatalf("Invoke(after drop/recreate) error = %v, want ErrStaleObject", err)
	}
}

func TestParameterSideEffectIsRecheckedBeforeDispatch(t *testing.T) {
	f := newFixture(t)
	pub, err := f.c.Publish(basicDefinition(AST{TableRef("accounts")}))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	callbacks := 0
	f.e = NewEngine(f.c, func(call FrozenCall) (any, error) {
		callbacks++
		return nil, nil
	})
	if err := f.e.Register(pub); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = f.e.Invoke(pub, Invocation{
		CallerID: roleCaller,
		EvaluateParameters: func(ctx ParameterContext) ([]any, error) {
			obj := f.c.Objects["accounts"]
			obj.Version = 99
			f.c.Objects["accounts"] = obj
			return nil, nil
		},
	})
	if !errors.Is(err, ErrStaleObject) {
		t.Fatalf("Invoke(parameter side effect) error = %v, want post-parameter stale check", err)
	}
	if callbacks != 0 {
		t.Fatalf("callback ran %d times before stale check", callbacks)
	}
}

func TestTemporaryTableDoesNotShadowFrozenPath(t *testing.T) {
	f := newFixture(t)
	pub, err := f.c.Publish(basicDefinition(AST{TableRef("accounts")}))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var got []ObjectID
	f.e = NewEngine(f.c, func(call FrozenCall) (any, error) {
		got = append(got, call.ObjectID)
		return nil, nil
	})
	if err := f.e.Register(pub); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := f.e.Invoke(pub, Invocation{CallerID: roleCaller}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !reflect.DeepEqual(got, []ObjectID{"accounts"}) {
		t.Fatalf("resolved object IDs = %v, temp table must not participate", got)
	}
}

func TestFunctionObjectDropRevokesDefinition(t *testing.T) {
	f := newFixture(t)
	mustObject(t, f.c, Object{ID: "function_secure", SchemaID: "app", Kind: KindFunction, Name: "secure_fn", Signature: "()", Owner: roleDefiner, Version: 1})
	def := basicDefinition(AST{TableRef("accounts")})
	def.ObjectID = "function_secure"
	engine := NewEngine(f.c, func(call FrozenCall) (any, error) { return nil, nil })
	pub, err := engine.Publish(def)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	f.c.DropObject("function_secure")
	if _, err := engine.Invoke(pub, Invocation{CallerID: roleCaller}); !errors.Is(err, ErrFunctionRevoked) {
		t.Fatalf("Invoke(dropped function object) error = %v, want ErrFunctionRevoked", err)
	}
}

func TestDynamicSQLDefaultDenyAndTrustedASTResolution(t *testing.T) {
	f := newFixture(t)
	pub, err := f.c.Publish(basicDefinition(AST{DynamicSQL{SQL: "SELECT * FROM accounts", Key: "q1"}}))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var calls []ObjectID
	f.e = NewEngine(f.c, func(call FrozenCall) (any, error) {
		calls = append(calls, call.ObjectID)
		return "dynamic-row", nil
	})
	if err := f.e.Register(pub); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := f.e.Invoke(pub, Invocation{CallerID: roleCaller}); !errors.Is(err, ErrDynamicSQLDenied) {
		t.Fatalf("Invoke(untrusted dynamic) error = %v, want ErrDynamicSQLDenied", err)
	}
	result, err := f.e.Invoke(pub, Invocation{
		CallerID: roleCaller,
		TrustedDynamicAST: map[string]AST{
			"q1": {TableRef("accounts")},
		},
	})
	if err != nil {
		t.Fatalf("Invoke(trusted dynamic): %v", err)
	}
	if !reflect.DeepEqual(calls, []ObjectID{"accounts"}) {
		t.Fatalf("dynamic callback IDs = %v, want frozen accounts ID", calls)
	}
	if result.Value != "dynamic-row" {
		t.Fatalf("dynamic result = %#v", result.Value)
	}
	if len(result.Trace) != 1 || !result.Trace[0].Dynamic {
		t.Fatalf("dynamic trace = %+v, want one dynamic call", result.Trace)
	}

	if _, err := f.e.Invoke(pub, Invocation{
		CallerID: roleCaller,
		TrustedDynamicAST: map[string]AST{
			"q1": {QualifiedTableRef("temp", "accounts")},
		},
	}); !errors.Is(err, ErrTemporarySchema) {
		t.Fatalf("Invoke(temp dynamic AST) error = %v, want ErrTemporarySchema", err)
	}
}

func TestNestedDefinerFunctionsAndShortestHijack(t *testing.T) {
	f := newFixture(t)
	mustObject(t, f.c, Object{ID: "nested_fn_object", SchemaID: "app", Kind: KindFunction, Name: "nested_fn", Signature: "()", Owner: roleDefiner, Version: 1})
	engine := NewEngine(f.c, func(call FrozenCall) (any, error) { return nil, nil })

	nestedDef := FunctionDefinition{
		ID: "nested_fn", ObjectID: "nested_fn_object", Name: "nested_fn", SchemaID: "app",
		DefinerID: roleDefiner, SearchPath: []SchemaID{"sys", "shadow", "app"},
		AST:            AST{TableRef("accounts")},
		AllowedCallers: map[PrincipalID]bool{roleCaller: true},
	}
	nested, err := engine.Publish(nestedDef)
	if err != nil {
		t.Fatalf("Publish(nested): %v", err)
	}

	mustObject(t, f.c, Object{ID: "outer_fn_object", SchemaID: "app", Kind: KindFunction, Name: "outer_fn", Signature: "()", Owner: roleDefiner, Version: 1})
	outerDef := FunctionDefinition{
		ID: "outer_fn", ObjectID: "outer_fn_object", Name: "outer_fn", SchemaID: "app",
		DefinerID: roleDefiner, SearchPath: []SchemaID{"sys", "shadow", "app"},
		// Qualification keeps the outer function call unshadowable; the
		// nested function's own unqualified table reference remains the
		// exploitable edge.
		AST:            AST{QualifiedFunctionRef("app", "nested_fn", "()")},
		AllowedCallers: map[PrincipalID]bool{roleCaller: true},
	}
	outer, err := engine.Publish(outerDef)
	if err != nil {
		t.Fatalf("Publish(outer): %v", err)
	}

	calls := 0
	engine.Execute = func(call FrozenCall) (any, error) {
		calls++
		return string(call.ObjectID), nil
	}
	if _, err := engine.Invoke(outer, Invocation{CallerID: roleCaller}); err != nil {
		t.Fatalf("Invoke(outer): %v", err)
	}
	if calls != 1 {
		t.Fatalf("frozen callback count = %d, want nested table callback only", calls)
	}

	// A policy regression after publication makes the earlier shadow schema
	// attacker-writable. The outer reference is qualified, so the shortest
	// proof must traverse nested_fn and flag its table reference.
	shadow := f.c.Schemas["shadow"]
	shadow.ReadOnly = false
	f.c.Schemas["shadow"] = shadow
	ev, ok := engine.ShortestHijackEvidenceFrom([]*PublishedFunction{outer}, roleAttacker)
	if !ok {
		t.Fatal("ShortestHijackEvidenceFrom found no evidence")
	}
	if !reflect.DeepEqual(ev.Hops, []FunctionID{"outer_fn", "nested_fn"}) {
		t.Fatalf("hijack hops = %v, want outer -> nested", ev.Hops)
	}
	if len(ev.Steps) != 1 || ev.Steps[0].Kind != KindTable || ev.Steps[0].Name != "accounts" {
		t.Fatalf("hijack step = %+v", ev.Steps)
	}
	if ev.Steps[0].CandidateSchemaID != "shadow" {
		t.Fatalf("candidate schema = %q, want shadow", ev.Steps[0].CandidateSchemaID)
	}

	// A direct call path should beat the nested path.
	if ev, ok := engine.ShortestHijackEvidence(roleAttacker); !ok || len(ev.Hops) != 1 || ev.Hops[0] != "nested_fn" {
		t.Fatalf("global shortest evidence = %+v, ok=%v; want direct nested_fn proof", ev, ok)
	}

	shadow.ReadOnly = true
	f.c.Schemas["shadow"] = shadow
	engine.Revoke(nested.ID)
	if _, err := engine.Invoke(outer, Invocation{CallerID: roleCaller}); !errors.Is(err, ErrFunctionRevoked) {
		t.Fatalf("Invoke(nested revoked) error = %v, want ErrFunctionRevoked", err)
	}
}

func TestHijackEvidenceForFunctionAndOperatorOverloads(t *testing.T) {
	f := newFixture(t)
	pub, err := f.c.Publish(FunctionDefinition{
		ID: "fn_refs", Name: "fn_refs", SchemaID: "app", DefinerID: roleDefiner,
		SearchPath:     []SchemaID{"sys", "shadow", "app"},
		AST:            AST{FunctionRef("lookup", "(integer)"), OperatorRef("+(integer,integer)")},
		AllowedCallers: map[PrincipalID]bool{roleCaller: true},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	shadow := f.c.Schemas["shadow"]
	shadow.ReadOnly = false
	f.c.Schemas["shadow"] = shadow

	steps := pub.DirectHijackCandidates(f.c, roleAttacker)
	if len(steps) != 2 {
		t.Fatalf("candidates = %d, want function and operator candidates", len(steps))
	}
	seen := map[ObjectKind]bool{}
	for _, step := range steps {
		seen[step.Kind] = true
		if step.CandidateSchemaID != "shadow" || step.CandidateSearchIdx != 1 {
			t.Fatalf("step candidate = %+v", step)
		}
	}
	if !seen[KindFunction] || !seen[KindOperator] {
		t.Fatalf("candidate kinds = %v, want function and operator", seen)
	}
	ev, ok := pub.ShortestHijackEvidence(f.c, roleAttacker)
	if !ok || len(ev.Steps) != 1 || ev.Steps[0].CandidateSearchIdx != 1 {
		t.Fatalf("shortest evidence = %+v, ok=%v", ev, ok)
	}
}

func TestFindShortestHijackAuditsUnsafeCatalog(t *testing.T) {
	f := newFixture(t)
	base := FunctionDefinition{
		ID: "audit_fn", Name: "audit_fn", SchemaID: "app", DefinerID: roleDefiner,
		SearchPath: []SchemaID{"sys", "foreign", "app"},
	}

	fnDef := base
	fnDef.AST = AST{FunctionRef("lookup", "(integer)")}
	if _, err := f.c.Publish(fnDef); !errors.Is(err, ErrWritableSchema) {
		t.Fatalf("secure Publish must reject attacker-writable path, got %v", err)
	}
	ev, ok, err := FindShortestHijack(f.c, fnDef, roleAttacker)
	if err != nil {
		t.Fatalf("FindShortestHijack(function): %v", err)
	}
	if !ok || len(ev.Steps) != 1 {
		t.Fatalf("function evidence = %+v, ok=%v", ev, ok)
	}
	if ev.Steps[0].Kind != KindFunction || ev.Steps[0].Name != "lookup" ||
		ev.Steps[0].Signature != "(integer)" || ev.Steps[0].CandidateSchemaID != "foreign" {
		t.Fatalf("function hijack step = %+v", ev.Steps[0])
	}

	opDef := base
	opDef.AST = AST{OperatorRef("+(integer,integer)")}
	ev, ok, err = FindShortestHijack(f.c, opDef, roleAttacker)
	if err != nil {
		t.Fatalf("FindShortestHijack(operator): %v", err)
	}
	if !ok || ev.Steps[0].Kind != KindOperator || ev.Steps[0].Signature != "(integer,integer)" {
		t.Fatalf("operator evidence = %+v, ok=%v", ev, ok)
	}
}

func TestCallbackCannotReceiveMutableNames(t *testing.T) {
	// FrozenCall deliberately has no Name or SchemaID field. This compile-
	// time-oriented regression test keeps the callback contract explicit.
	var call FrozenCall
	typ := reflect.TypeOf(call)
	for _, forbidden := range []string{"Name", "SchemaID", "Schema"} {
		if _, ok := typ.FieldByName(forbidden); ok {
			t.Fatalf("FrozenCall exposes forbidden mutable identity field %q", forbidden)
		}
	}
	if _, ok := typ.FieldByName("ObjectID"); !ok {
		t.Fatal("FrozenCall must expose ObjectID")
	}
}

func ExampleCatalog_Publish() {
	c := NewCatalog()
	_ = c.AddSchema(Schema{ID: "sys", Name: "pg_catalog", Owner: "system", System: true, ReadOnly: true})
	_ = c.AddSchema(Schema{ID: "app", Name: "app", Owner: "owner"})
	_ = c.AddObject(Object{ID: "accounts_v1", SchemaID: "app", Kind: KindTable, Name: "accounts", Version: 1})

	pub, err := c.Publish(FunctionDefinition{
		ID: "secure_read", DefinerID: "owner",
		SearchPath: []SchemaID{"sys", "app"},
		AST:        AST{TableRef("accounts")},
		Public:     true,
	})
	if err != nil {
		panic(err)
	}
	dep := pub.Manifest().Dependencies[0].Ref
	fmt.Printf("%s %d\n", dep.ObjectID, dep.Version)
	// Output:
	// accounts_v1 1
}
