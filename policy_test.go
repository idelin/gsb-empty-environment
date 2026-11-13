package netsec

import (
	"errors"
	"testing"
)

func TestAuthorizeSubscription(t *testing.T) {
	authorizer, err := NewAuthorizer([]string{"a/**"}, []string{"a/secret/*"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		expression string
		want       bool
	}{
		{"a/public/**", true},
		{"a/public/*", true},
		{"a/$*b/*", true}, // $* does not cross chunks and the literal b excludes "secret".
		{"a/$*/*", false},
		{"a/*/**", false},
		{"a/**", false},
		{"a/**/secret/**", false},
		{"a/secret/*", false},
		{"a/secret", true}, // a/secret/* matches children, not the parent itself.
		{"a", true},
		{"b/**", false},
		{"", false},
		{"a/b*c", false},
	}

	for _, test := range tests {
		t.Run(test.expression, func(t *testing.T) {
			if got := authorizer.AuthorizeSubscription(test.expression); got != test.want {
				t.Fatalf("AuthorizeSubscription(%q) = %v, want %v", test.expression, got, test.want)
			}
		})
	}
}

func TestAuthorizePublish(t *testing.T) {
	authorizer, err := NewAuthorizer([]string{"a/**"}, []string{"a/secret/*"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		key  string
		want bool
	}{
		{"a/public/x", true},
		{"a/secret/x", false},
		{"a/secret/deep/x", true},
		{"a/secret", true},
		{"a", true},
		{"b/x", false},
		{"a/**", false},
		{"a/$*b/x", false},
		{"", false},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			if got := authorizer.AuthorizePublish(test.key); got != test.want {
				t.Fatalf("AuthorizePublish(%q) = %v, want %v", test.key, got, test.want)
			}
		})
	}
}

func TestUnicodeKeys(t *testing.T) {
	authorizer, err := NewAuthorizer([]string{"a/**"}, []string{"a/秘密/*"})
	if err != nil {
		t.Fatal(err)
	}
	if !authorizer.AuthorizePublish("a/公开/温度") {
		t.Fatal("unicode publish key was denied")
	}
	if authorizer.AuthorizePublish("a/秘密/温度") {
		t.Fatal("unicode deny rule was not applied")
	}
	if authorizer.AuthorizeSubscription("a/秘密/**") {
		t.Fatal("unicode subscription intersecting a deny was authorized")
	}
}

func TestDefaultDeny(t *testing.T) {
	authorizer, err := NewAuthorizer(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.AuthorizeSubscription("**") {
		t.Fatal("empty policy authorized a subscription")
	}
	if authorizer.AuthorizePublish("a/b") {
		t.Fatal("empty policy authorized a publish")
	}

	authorizer, err = NewAuthorizer(nil, []string{"**"})
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.AuthorizeSubscription("a/b") {
		t.Fatal("deny-only policy authorized a subscription")
	}
}

func TestDenyWins(t *testing.T) {
	authorizer, err := NewAuthorizer(
		[]string{"a/**"},
		[]string{"a/secret/**", "a/public/private/*"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if authorizer.AuthorizePublish("a/secret") {
		t.Fatal("a/secret matched by deny failed to be denied")
	}
	if authorizer.AuthorizeSubscription("a/public/private/**") {
		t.Fatal("subscription intersecting a deny rule was authorized")
	}
	if !authorizer.AuthorizeSubscription("a/public/*") {
		t.Fatal("a/public/* does not reach the denied a/public/private/* subtree")
	}
}

func TestSubscriptionMustFitOneAllowRule(t *testing.T) {
	authorizer, err := NewAuthorizer([]string{"a/*", "a/*/*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.AuthorizeSubscription("a/**") {
		t.Fatal("a/** is not wholly contained by any single allow rule")
	}
	if !authorizer.AuthorizeSubscription("a/*") {
		t.Fatal("a/* is contained by the first allow rule")
	}
}

func TestExpressionRelations(t *testing.T) {
	tests := []struct {
		name      string
		container string
		candidate string
		contains  bool
	}{
		{"double star contains descendants", "a/**", "a/public/**", true},
		{"double star contains parent", "a/**", "a", true},
		{"double star contains zero chunks", "a/**/c", "a/c", true},
		{"double star contains one chunk", "a/**/c", "a/b/c", true},
		{"consecutive double stars", "a/**/**/b", "a/b", true},
		{"double stars at both ends", "a/**/b/**/c", "a/b/c", true},
		{"single star does not contain double star", "a/*/c", "a/**/c", false},
		{"literal is contained by single star", "a/*/c", "a/b/c", true},
		{"single star is not a literal", "a/b/c", "a/*/c", false},
		{"subchunk wildcard contains literal", "a/$*/c", "a/hello/c", true},
		{"subchunk wildcard contains another subchunk", "a/$*/c", "a/$*b/c", true},
		{"subchunk suffix contains empty suffix", "a/b$*", "a/b", true},
		{"literal does not contain wildcard suffix", "a/b", "a/b$*", false},
		{"pure subchunk equals single star", "a/*/c", "a/$*/c", true},
		{"double star equals itself", "a/**/b", "a/**/b", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ExpressionContains(test.container, test.candidate); got != test.contains {
				t.Fatalf("ExpressionContains(%q, %q) = %v, want %v", test.container, test.candidate, got, test.contains)
			}
		})
	}
}

func TestExpressionIntersections(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{"double star overlaps denied subtree", "a/**", "a/secret/*", true},
		{"subchunk suffix avoids secret", "a/$*b/*", "a/secret/*", false},
		{"subchunk can match secret", "a/$*/*", "a/secret/*", true},
		{"star and double star overlap", "a/*/c", "a/**/c", true},
		{"different literals do not overlap", "a/*/c", "a/**/d", false},
		{"double star overlaps parent", "a/**", "a", true},
		{"single star does not overlap parent", "a/*", "a", false},
		{"universal double star", "**", "x/y", true},
		{"suffix wildcard overlap", "a/b$*", "a/bc$*", true},
		{"subchunk prefixes overlap", "a/$*b", "a/b$*", true},
		{"different subchunk suffixes do not overlap", "a/$*b", "a/$*c", false},
		{"prefix and suffix overlap", "a/b$*", "a/$*c", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ExpressionsIntersect(test.left, test.right); got != test.want {
				t.Fatalf("ExpressionsIntersect(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
			}
			if got := ExpressionsIntersect(test.right, test.left); got != test.want {
				t.Fatalf("ExpressionsIntersect(%q, %q) = %v, want %v", test.right, test.left, got, test.want)
			}
		})
	}
}

func TestRulesAndErrors(t *testing.T) {
	authorizer, err := NewFromRules([]Rule{
		{Effect: EffectAllow, Expression: "a/**"},
		{Effect: EffectDeny, Expression: "a/secret/*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = authorizer.CheckSubscription("a/**")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("CheckSubscription error = %v, want ErrUnauthorized", err)
	}
	var authorizationError *AuthorizationError
	if !errors.As(err, &authorizationError) || authorizationError.RuleIndex != 0 {
		t.Fatalf("CheckSubscription error does not identify the deny rule: %v", err)
	}

	err = authorizer.CheckPublish("a/**")
	if !errors.Is(err, ErrNotConcreteKey) {
		t.Fatalf("CheckPublish error = %v, want ErrNotConcreteKey", err)
	}

	if _, err := NewAuthorizer([]string{"a/*b"}, nil); !errors.Is(err, ErrMalformedExpression) {
		t.Fatalf("invalid rule error = %v, want ErrMalformedExpression", err)
	}
	if _, err := NewFromRules([]Rule{{Effect: "permit", Expression: "a/b"}}); err == nil {
		t.Fatal("invalid rule effect was accepted")
	}
}

func TestMalformedExpressions(t *testing.T) {
	expressions := []string{
		"",
		"/a",
		"a/",
		"a//b",
		"a/*b",
		"a/b**",
		"a/**b",
	}
	for _, expression := range expressions {
		if _, err := parseExpression(expression); err == nil {
			t.Errorf("parseExpression(%q) succeeded, want an error", expression)
		}
		if ExpressionContains("**", expression) {
			t.Errorf("ExpressionContains accepted malformed expression %q", expression)
		}
	}
}
