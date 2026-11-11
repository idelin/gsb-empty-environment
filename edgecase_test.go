package netsec

import (
	"errors"
	"testing"
)

func TestSaturatingArithmetic(t *testing.T) {
	if got := satMul(maxCost, 2); got != maxCost {
		t.Fatalf("satMul overflow = %d, want %d", got, maxCost)
	}
	if got := satMul(0, maxCost); got != 0 {
		t.Fatalf("satMul zero = %d, want 0", got)
	}
	if got := satAdd(maxCost, 1); got != maxCost {
		t.Fatalf("satAdd overflow = %d, want %d", got, maxCost)
	}
}

func TestInterfaceReturnTypeEnum(t *testing.T) {
	schema := fixtureSchema()
	// Query.node returns the Node interface; each concrete branch must be
	// considered and the most expensive one charged.
	doc := singleDoc(
		field("node",
			field("id"),
			inline("User", field("email")),
			inline("Post", field("title")),
		),
	)

	report, err := admitQuery(schema, doc, maxCost, "read:email")
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	maxActual, _ := runEnumeration(t, schema, doc)
	if maxActual != report.UpperBound {
		t.Fatalf("interface actual %d != bound %d", maxActual, report.UpperBound)
	}
}

func TestOperationSelection(t *testing.T) {
	schema := fixtureSchema()
	doc := &Document{
		Operations: []*Operation{
			{Name: "Cheap", Type: OpQuery, SelectionSet: []Selection{field("me", field("id"))}},
			{Name: "Expensive", Type: OpQuery, SelectionSet: []Selection{
				field("posts", field("comments", field("body"))),
			}},
		},
		Fragments: map[string]*FragmentDefinition{},
	}

	if _, err := admitQuery(schema, doc, maxCost); !isValidation(err) {
		t.Fatalf("anonymous selection among many operations must fail, got %v", err)
	}

	cheap, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		OperationName: "Cheap",
		Budget:        maxCost,
	})
	if err != nil || cheap.UpperBound != 2 {
		t.Fatalf("Cheap selection wrong: %+v err=%v", cheap, err)
	}

	missing, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		OperationName: "Ghost",
		Budget:        maxCost,
	})
	if err == nil || missing != nil {
		t.Fatalf("unknown operation name must fail")
	}
}

func TestVariableDrivenArgumentConflict(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		&Field{
			Name:         "user",
			Alias:        "u",
			Arguments:    []*Argument{arg("id", &Variable{Name: "id"})},
			SelectionSet: []Selection{field("id")},
		},
		&Field{
			Name:         "user",
			Alias:        "u",
			Arguments:    []*Argument{arg("id", &StringValue{Value: "fixed"})},
			SelectionSet: []Selection{field("id")},
		},
	)

	// Variable resolves to a different value: conflict.
	_, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		Variables:     map[string]any{"id": "other"},
		GrantedScopes: map[string]bool{},
		Budget:        maxCost,
	})
	if !isValidation(err) {
		t.Fatalf("want variable argument conflict, got %v", err)
	}

	// Variable resolves to the same literal: fields merge cleanly.
	report, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		Variables:     map[string]any{"id": "fixed"},
		GrantedScopes: map[string]bool{},
		Budget:        maxCost,
	})
	if err != nil || !report.Admitted {
		t.Fatalf("equal resolved arguments must merge: %v %v", report, err)
	}
}

func TestUndefinedVariableRejected(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(&Field{
		Name:       "me",
		Directives: []*Directive{includeDirective(&Variable{Name: "flag"})},
	})
	_, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		Variables:     map[string]any{},
		GrantedScopes: map[string]bool{},
		Budget:        maxCost,
	})
	if !isValidation(err) {
		t.Fatalf("want undefined variable error, got %v", err)
	}
}

func TestNegativeBudgetTreatedAsZero(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(field("me", field("id")))
	report, err := admitQuery(schema, doc, -5)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if report.Budget != 0 || report.Admitted {
		t.Fatalf("negative budget should clamp to zero and deny: %+v", report)
	}
}

func TestFragmentSpreadDirectiveGating(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			&FragmentSpread{
				Name:       "Extra",
				Directives: []*Directive{includeDirective(&Variable{Name: "withExtra"})},
			},
			field("id"),
		),
	)
	doc.Fragments["Extra"] = &FragmentDefinition{
		Name:          "Extra",
		TypeCondition: "User",
		SelectionSet:  []Selection{field("name")},
	}

	excluded, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		Variables:     map[string]any{"withExtra": false},
		GrantedScopes: map[string]bool{},
		Budget:        maxCost,
	})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	// me(1) + id(1); the gated fragment is excluded, so no scope is needed.
	if excluded.UpperBound != 2 {
		t.Fatalf("excluded fragment bound = %d, want 2", excluded.UpperBound)
	}

	// Included again: name requires the profile scope and is charged.
	included, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		Variables:     map[string]any{"withExtra": true},
		GrantedScopes: map[string]bool{"read:profile": true},
		Budget:        maxCost,
	})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if included.UpperBound != 3 {
		t.Fatalf("included fragment bound = %d, want 3", included.UpperBound)
	}
}

func TestInterfaceTypeConditionApplies(t *testing.T) {
	schema := fixtureSchema()
	// A fragment conditioned on the Node interface applies to every object
	// implementing Node, including User.
	doc := singleDoc(
		field("me",
			inline("Node", field("id")),
		),
	)
	report, err := admitQuery(schema, doc, maxCost)
	if err != nil {
		t.Fatalf("interface condition should apply to User: %v", err)
	}
	if report.UpperBound != 2 {
		t.Fatalf("bound = %d, want 2 (me + id)", report.UpperBound)
	}
}

func TestErrorClassification(t *testing.T) {
	schema := fixtureSchema()

	permissionDoc := singleDoc(field("me", field("email")))
	_, permissionErr := admitQuery(schema, permissionDoc, maxCost)
	if !errors.Is(permissionErr, &AdmissionError{Code: ReasonPermission}) {
		t.Fatalf("errors.Is should classify permission denial: %v", permissionErr)
	}

	validationDoc := singleDoc(field("me", spread("Missing")))
	_, validationErr := admitQuery(schema, validationDoc, maxCost)
	if !errors.Is(validationErr, &AdmissionError{Code: ReasonValidation}) {
		t.Fatalf("errors.Is should classify validation failure: %v", validationErr)
	}
	if errors.Is(validationErr, &AdmissionError{Code: ReasonPermission}) {
		t.Fatal("validation error must not match permission sentinel")
	}
}

func TestObjectAndListValueArguments(t *testing.T) {
	schema := fixtureSchema()
	build := func(second Value) *Document {
		return singleDoc(
			&Field{
				Name:  "user",
				Alias: "u",
				Arguments: []*Argument{arg("id", &ObjectValue{Fields: []*ObjectField{
					{Name: "nested", Value: &ListValue{Values: []Value{
						&IntValue{Value: 1},
						&Variable{Name: "tail"},
					}}},
				}})},
				SelectionSet: []Selection{field("id")},
			},
			&Field{
				Name:  "user",
				Alias: "u",
				Arguments: []*Argument{arg("id", &ObjectValue{Fields: []*ObjectField{
					{Name: "nested", Value: &ListValue{Values: []Value{
						&IntValue{Value: 1},
						second,
					}}},
				}})},
				SelectionSet: []Selection{field("id")},
			},
		)
	}

	equalDoc := build(&IntValue{Value: 2})
	equalReport, err := Admit(Options{
		Schema:        schema,
		Document:      equalDoc,
		Variables:     map[string]any{"tail": int64(2)},
		GrantedScopes: map[string]bool{},
		Budget:        maxCost,
	})
	if err != nil || !equalReport.Admitted {
		t.Fatalf("deep-equal object/list args must merge: %v %v", equalReport, err)
	}

	differentDoc := build(&IntValue{Value: 3})
	if _, err := Admit(Options{
		Schema:        schema,
		Document:      differentDoc,
		Variables:     map[string]any{"tail": int64(2)},
		GrantedScopes: map[string]bool{},
		Budget:        maxCost,
	}); !isValidation(err) {
		t.Fatalf("deep-unequal args must conflict, got %v", err)
	}
}

func TestSubscriptionRejected(t *testing.T) {
	schema := fixtureSchema()
	doc := &Document{
		Operations: []*Operation{{
			Type:         OpSubscription,
			SelectionSet: []Selection{field("me", field("id"))},
		}},
		Fragments: map[string]*FragmentDefinition{},
	}
	if _, err := admitQuery(schema, doc, maxCost); !isValidation(err) {
		t.Fatalf("subscription must be rejected, got %v", err)
	}
}

func TestDuplicateDirectiveRejected(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(&Field{
		Name: "me",
		Directives: []*Directive{
			skipDirective(&BooleanValue{Value: false}),
			skipDirective(&BooleanValue{Value: false}),
		},
	})
	if _, err := admitQuery(schema, doc, maxCost); !isValidation(err) {
		t.Fatalf("duplicate @skip must be rejected, got %v", err)
	}
}
