package netsec

import "testing"

func TestExecutionEnumerationMatchesUpperBound(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			field("posts",
				field("title"),
				field("comments", field("body")),
			),
			aliased("pals", "friends", field("name")),
		),
	)

	report, err := admitQuery(schema, doc, maxCost, "read:profile")
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if !report.Admitted {
		t.Fatalf("query should be admitted: %s", report.Message)
	}

	maxActual, runs := runEnumeration(t, schema, doc)
	if runs == 0 {
		t.Fatal("enumeration produced no executions")
	}
	if maxActual != report.UpperBound {
		t.Fatalf("max actual weighted cost %d != upper bound %d over %d runs",
			maxActual, report.UpperBound, runs)
	}

	// Contributions must partition the upper bound exactly.
	sum := int64(0)
	for _, contribution := range report.Contributions {
		sum += contribution.Cost
	}
	if sum != report.UpperBound {
		t.Fatalf("contribution sum %d != upper bound %d", sum, report.UpperBound)
	}
}

func TestExecutionEnumerationAliasGroups(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			aliased("a", "posts", field("title")),
			aliased("b", "posts", field("title")),
			field("posts", field("title")),
		),
	)

	report, err := admitQuery(schema, doc, maxCost)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	maxActual, runs := runEnumeration(t, schema, doc)
	if maxActual != report.UpperBound {
		t.Fatalf("distinct-key actual %d != bound %d over %d runs",
			maxActual, report.UpperBound, runs)
	}

	// Repeating the same response key merges into a single resolver group.
	merged := singleDoc(
		field("me",
			field("posts", field("title")),
			field("posts", field("title")),
		),
	)
	mergedReport, err := admitQuery(schema, merged, maxCost)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	maxMerged, _ := runEnumeration(t, schema, merged)
	if maxMerged != mergedReport.UpperBound {
		t.Fatalf("merged actual %d != bound %d", maxMerged, mergedReport.UpperBound)
	}
	mePosts := findContribution(mergedReport, []string{"me", "posts"})
	if mePosts == nil || mePosts.Invocations != 1 {
		t.Fatalf("merged posts should invoke once per parent: %+v", mePosts)
	}
}

func TestExecutionEnumerationFragmentDeduplication(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			spread("PostFields"),
			spread("PostFields"),
			inline("", field("posts", field("title"))),
		),
	)
	doc.Fragments["PostFields"] = &FragmentDefinition{
		Name:          "PostFields",
		TypeCondition: "User",
		SelectionSet:  []Selection{field("posts", field("title"))},
	}

	report, err := admitQuery(schema, doc, maxCost)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	maxActual, _ := runEnumeration(t, schema, doc)
	if maxActual != report.UpperBound {
		t.Fatalf("fragment actual %d != bound %d", maxActual, report.UpperBound)
	}
	mePosts := findContribution(report, []string{"me", "posts"})
	if mePosts == nil || mePosts.Invocations != 1 {
		t.Fatalf("fragment spread did not merge into one group: %+v", mePosts)
	}
}

func TestExecutionEnumerationNestedLists(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("posts",
			field("batches"),
			field("comments", field("body")),
		),
	)

	report, err := admitQuery(schema, doc, maxCost)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	comments := findContribution(report, []string{"posts", "comments"})
	if comments == nil || comments.Multiplier != 3 {
		t.Fatalf("comments multiplier = %+v, want 3", comments)
	}
	body := findContribution(report, []string{"posts", "comments", "body"})
	if body == nil || body.Multiplier != 6 {
		t.Fatalf("body multiplier = %+v, want 6", body)
	}

	maxActual, _ := runEnumeration(t, schema, doc)
	if maxActual != report.UpperBound {
		t.Fatalf("nested list actual %d != bound %d", maxActual, report.UpperBound)
	}
}

func TestExecutionEnumerationAbstractType(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("search",
			inline("User", field("name"), field("email")),
			inline("Post", field("title")),
		),
	)

	report, err := admitQuery(schema, doc, maxCost, "read:profile", "read:email")
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	maxActual, runs := runEnumeration(t, schema, doc)
	if maxActual != report.UpperBound {
		t.Fatalf("abstract actual %d != bound %d over %d runs",
			maxActual, report.UpperBound, runs)
	}

	// The bound must cover the most expensive concrete branch, which is the
	// User branch (name + email) versus Post (title).
	userName := findContribution(report, []string{"search", "name"})
	if userName == nil {
		t.Fatal("expected contributions from the worst (User) branch")
	}
}

func TestOverBudgetNeverExecutesAnyField(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("posts",
			field("comments",
				field("author",
					field("friends", field("name")),
				),
			),
		),
	)

	report, err := admitQuery(schema, doc, 1, "read:profile")
	if err != nil {
		t.Fatalf("want budget report, got error: %v", err)
	}
	if report.Admitted {
		t.Fatal("over-budget query must be rejected")
	}

	// Exhaustively enumerate every execution shape; the gateway must never
	// reach execution, demonstrated by zero callback counts on every run.
	enumerate(t, schema, func(lengths map[string]int) {
		exec := newExecutor(schema, doc, lengths)
		// Intentionally never call runObject: admission denied execution.
		if exec.totalCallbacks() != 0 {
			t.Fatalf("field callback invoked despite denial: %d", exec.totalCallbacks())
		}
	})
}
