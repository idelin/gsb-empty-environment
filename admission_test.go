package netsec

import (
	"errors"
	"testing"
)

func isValidation(err error) bool {
	var admissionErr *AdmissionError
	return errors.As(err, &admissionErr) && admissionErr.Code == ReasonValidation
}

func TestBasicCostAndContributionPaths(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("me",
			field("name"),
			field("posts",
				field("title"),
			),
		),
	)

	report, err := admitQuery(schema, doc, maxCost, "read:profile")
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}

	// me(1) + name(1) + posts(5) + 3 * title(2) = 13
	if report.UpperBound != 13 {
		t.Fatalf("upper bound = %d, want 13", report.UpperBound)
	}

	posts := findContribution(report, []string{"me", "posts"})
	if posts == nil {
		t.Fatal("missing me.posts contribution")
	}
	if posts.Multiplier != 1 || posts.Invocations != 1 || posts.Cost != 5 {
		t.Fatalf("posts contribution wrong: %+v", posts)
	}
	title := findContribution(report, []string{"me", "posts", "title"})
	if title == nil || title.Multiplier != 3 || title.Invocations != 3 || title.Cost != 6 {
		t.Fatalf("title contribution wrong: %+v", title)
	}
	if !equalStrings(title.SchemaPath, []string{"me", "posts", "title"}) {
		t.Fatalf("schema path = %v", title.SchemaPath)
	}
}

func TestAliasResponsePathVsSchemaPath(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		aliased("myself", "me",
			aliased("display", "name"),
			aliased("articles", "posts",
				aliased("headline", "title"),
			),
		),
	)

	report, err := admitQuery(schema, doc, maxCost, "read:profile")
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if report.UpperBound != 13 {
		t.Fatalf("upper bound = %d, want 13 (aliases must not change cost)", report.UpperBound)
	}

	headline := findContribution(report, []string{"myself", "articles", "headline"})
	if headline == nil {
		t.Fatal("missing aliased contribution")
	}
	if !equalStrings(headline.SchemaPath, []string{"me", "posts", "title"}) {
		t.Fatalf("schema path leaks alias: %v", headline.SchemaPath)
	}
	if headline.Field != "title" || headline.ObjectType != "Post" {
		t.Fatalf("contribution metadata wrong: %+v", headline)
	}
}

func TestAliasCannotBypassPermission(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		aliased("publicProfile", "me",
			aliased("contact", "email"),
		),
	)

	_, err := admitQuery(schema, doc, maxCost)
	var admissionErr *AdmissionError
	if !errors.As(err, &admissionErr) || admissionErr.Code != ReasonPermission {
		t.Fatalf("want permission error, got %v", err)
	}
	if !equalStrings(admissionErr.SchemaPath, []string{"me", "email"}) {
		t.Fatalf("permission path = %v, want me.email", admissionErr.SchemaPath)
	}

	report, err := admitQuery(schema, doc, maxCost, "read:email")
	if err != nil || !report.Admitted {
		t.Fatalf("granted scope should admit query: report=%v err=%v", report, err)
	}
}

func TestNestedListMultipliers(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("posts",
			field("comments",
				field("body"),
			),
		),
	)

	report, err := admitQuery(schema, doc, maxCost)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}

	// posts(5) + 3 * comments(4) + 3*2 * body(1) = 5 + 12 + 6 = 23
	if report.UpperBound != 23 {
		t.Fatalf("upper bound = %d, want 23", report.UpperBound)
	}
	comments := findContribution(report, []string{"posts", "comments"})
	if comments.Multiplier != 3 || comments.Invocations != 3 || comments.Cost != 12 {
		t.Fatalf("comments = %+v", comments)
	}
	body := findContribution(report, []string{"posts", "comments", "body"})
	if body.Multiplier != 6 || body.Invocations != 6 || body.Cost != 6 {
		t.Fatalf("body = %+v", body)
	}
}

func TestTwoDimensionalList(t *testing.T) {
	schema := fixtureSchema()
	doc := singleDoc(
		field("posts",
			field("batches"),
		),
	)

	report, err := admitQuery(schema, doc, maxCost)
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	// posts(5) + 3 * batches(1); batches is a leaf scalar list.
	if report.UpperBound != 8 {
		t.Fatalf("upper bound = %d, want 8", report.UpperBound)
	}
	batches := findContribution(report, []string{"posts", "batches"})
	if batches.Invocations != 3 {
		t.Fatalf("batches invocations = %d, want 3", batches.Invocations)
	}
}
