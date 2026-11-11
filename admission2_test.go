package netsec

import (
	"errors"
	"testing"
)

func TestFragmentExpansionAndMerging(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			spread("UserCore"),
			inline("", field("name")),
		),
	)
	doc.Fragments["UserCore"] = &FragmentDefinition{
		Name:          "UserCore",
		TypeCondition: "User",
		SelectionSet: []Selection{
			field("id"),
			field("name"),
		},
	}

	report, err := admitQuery(schema, doc, maxCost, "read:profile")
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	// me(1) + id(1) + name(1); name repeats but merges once.
	if report.UpperBound != 3 {
		t.Fatalf("upper bound = %d, want 3", report.UpperBound)
	}
	name := findContribution(report, []string{"me", "name"})
	if name == nil || name.Invocations != 1 {
		t.Fatalf("name should merge to one invocation: %+v", name)
	}
}

func TestFragmentTypeConditionFilters(t *testing.T) {
	schema := fixtureSchema()
	// On User, an Admin-only fragment condition must not apply.
	doc := singleDoc(
		field("me",
			inline("User", field("id")),
			inline("Post", field("title")),
		),
	)
	report, err := admitQuery(schema, doc, maxCost)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	// me(1) + id(1); the Post fragment never applies to a User.
	if report.UpperBound != 2 {
		t.Fatalf("upper bound = %d, want 2", report.UpperBound)
	}
}

func TestFragmentCycleRejected(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(field("me", spread("A")))
	doc.Fragments["A"] = &FragmentDefinition{
		Name:          "A",
		TypeCondition: "User",
		SelectionSet:  []Selection{spread("B")},
	}
	doc.Fragments["B"] = &FragmentDefinition{
		Name:          "B",
		TypeCondition: "User",
		SelectionSet:  []Selection{spread("A")},
	}

	if _, err := admitQuery(schema, doc, maxCost); !isValidation(err) {
		t.Fatalf("want validation error for cycle, got %v", err)
	}
}

func TestUndefinedFragmentRejected(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(field("me", spread("Ghost")))
	if _, err := admitQuery(schema, doc, maxCost); !isValidation(err) {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestResponseKeyConflictRejected(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			aliased("x", "name"),
			aliased("x", "email"),
		),
	)
	if _, err := admitQuery(schema, doc, maxCost, "read:profile", "read:email"); !isValidation(err) {
		t.Fatalf("want conflict validation error, got %v", err)
	}
}

func TestArgumentConflictAndDistinctKeys(t *testing.T) {
	schema := fixtureSchema()

	// Distinct response keys are allowed and charged independently.
	distinct := singleDoc(
		field("me",
			aliased("p1", "posts", field("title")),
			aliased("p2", "posts", field("title")),
		),
	)
	report, err := admitQuery(schema, distinct, maxCost)
	if err != nil || !report.Admitted {
		t.Fatalf("distinct keys must be allowed: %v %v", report, err)
	}

	// Same response key with different arguments conflicts.
	conflicting := singleDoc(
		field("me",
			&Field{
				Name:         "user",
				Alias:        "u",
				Arguments:    []*Argument{arg("id", &StringValue{Value: "1"})},
				SelectionSet: []Selection{field("id")},
			},
			&Field{
				Name:         "user",
				Alias:        "u",
				Arguments:    []*Argument{arg("id", &StringValue{Value: "2"})},
				SelectionSet: []Selection{field("id")},
			},
		),
	)
	if _, err := admitQuery(schema, conflicting, maxCost); !isValidation(err) {
		t.Fatalf("want argument conflict error, got %v", err)
	}
}

func TestConditionalDirectives(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			&Field{
				Name:       "name",
				Directives: []*Directive{skipDirective(&BooleanValue{Value: true})},
			},
			&Field{
				Name:       "email",
				Directives: []*Directive{includeDirective(&Variable{Name: "wantEmail"})},
			},
			field("id"),
		),
	)

	report, err := Admit(Options{
		Schema:        schema,
		Document:      doc,
		Variables:     map[string]any{"wantEmail": false},
		GrantedScopes: map[string]bool{"read:email": true},
		Budget:        maxCost,
	})
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	// me(1) + id(1); email excluded, name skipped.
	if report.UpperBound != 2 {
		t.Fatalf("upper bound = %d, want 2", report.UpperBound)
	}

	bad := singleDoc(&Field{
		Name:       "me",
		Directives: []*Directive{skipDirective(&StringValue{Value: "yes"})},
	})
	if _, err := admitQuery(schema, bad, maxCost); !isValidation(err) {
		t.Fatalf("want directive validation error, got %v", err)
	}
}

func TestUnknownFieldAndLeafSelectionRejected(t *testing.T) {
	schema := fixtureSchema()

	if _, err := admitQuery(schema, singleDoc(field("nope")), maxCost); !isValidation(err) {
		t.Fatal("want unknown field error")
	}

	leafWithSelection := singleDoc(field("me", field("name", field("id"))))
	if _, err := admitQuery(schema, leafWithSelection, maxCost, "read:profile"); !isValidation(err) {
		t.Fatal("want leaf selection error")
	}

	objectWithoutSelection := singleDoc(field("me"))
	if _, err := admitQuery(schema, objectWithoutSelection, maxCost); !isValidation(err) {
		t.Fatal("want missing selection set error")
	}
}

func TestBudgetDenialIsInclusive(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("posts",
			field("comments", field("body")),
		),
	)

	report, err := admitQuery(schema, doc, 10)
	if err != nil {
		t.Fatalf("budget denial must return a report, not error: %v", err)
	}
	if report.Admitted || report.Reason != ReasonBudget {
		t.Fatalf("want budget denial, got %+v", report)
	}
	if report.UpperBound != 23 || report.Budget != 10 {
		t.Fatalf("report numbers wrong: %+v", report)
	}

	boundary, err := admitQuery(schema, doc, 23)
	if err != nil || !boundary.Admitted {
		t.Fatalf("inclusive budget should admit: %v %v", boundary, err)
	}
}

func TestOverBudgetRejectsBeforeAnyCallback(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("posts",
			field("comments",
				field("author",
					field("friends",
						field("name"),
					),
				),
			),
		),
	)

	report, err := admitQuery(schema, doc, 1, "read:profile")
	if err != nil {
		t.Fatalf("want report, got error: %v", err)
	}
	if report.Admitted {
		t.Fatal("over-budget query must be rejected")
	}

	// Admission runs before execution; an unrun executor must show zero
	// field resolver callbacks.
	exec := newExecutor(schema, doc, map[string]int{})
	if exec.totalCallbacks() != 0 {
		t.Fatalf("callbacks ran before admission: %d", exec.totalCallbacks())
	}
}

func TestMutationPermissionAndCost(t *testing.T) {
	schema := fixtureSchema()
	doc := &Document{
		Operations: []*Operation{{
			Type:         OpMutation,
			SelectionSet: []Selection{field("createPost", field("title"))},
		}},
		Fragments: map[string]*FragmentDefinition{},
	}

	if _, err := admitQuery(schema, doc, maxCost); !isPermission(err) {
		t.Fatalf("want permission error, got %v", err)
	}

	report, err := admitQuery(schema, doc, maxCost, "write:post")
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	// createPost(10) + title(2) = 12
	if report.UpperBound != 12 {
		t.Fatalf("mutation upper bound = %d, want 12", report.UpperBound)
	}
}

func isPermission(err error) bool {
	var admissionErr *AdmissionError
	return errors.As(err, &admissionErr) && admissionErr.Code == ReasonPermission
}
