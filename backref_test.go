package netsec

import (
	"errors"
	"testing"
)

func TestValidateRejectsBackreferenceToOpenGroup(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{name: "self numeric", pattern: `(a\1)`},
		{name: "self numeric g form", pattern: `(a\g<1>)`},
		{name: "self numeric g quote form", pattern: `(a\g'1')`},
		{name: "self numeric g bare form", pattern: `(a\g1)`},
		{name: "self named angle", pattern: `(?<word>a\k<word>)`},
		{name: "self named brace", pattern: `(?<word>a\k{word})`},
		{name: "self named g quote form", pattern: `(?<word>a\g'word')`},
		{name: "self named python", pattern: `(?P<word>a(?P=word))`},
		{name: "outer numeric", pattern: `(a(b)\1)`},
		{name: "outer named", pattern: `(?<word>a(?<inner>b\k<word>))`},
		{name: "nested self", pattern: `((a\2))`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.pattern)
			if err == nil {
				t.Fatalf("Validate(%q) accepted a reference to an open group", test.pattern)
			}
			if !errors.Is(err, ErrBackreferenceToOpenGroup) {
				t.Fatalf("Validate(%q) error = %v, want ErrBackreferenceToOpenGroup", test.pattern, err)
			}
		})
	}
}

func TestValidateAllowsBackreferenceToClosedGroup(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{name: "numeric", pattern: `(a)\1`},
		{name: "numeric g bare form", pattern: `(a)\g1`},
		{name: "numeric second group", pattern: `(a)(b)\2`},
		{name: "named angle", pattern: `(?<word>a)\k<word>`},
		{name: "named brace", pattern: `(?<word>a)\k{word}`},
		{name: "named g form", pattern: `(?<word>a)\g<word>`},
		{name: "named g quote form", pattern: `(?<word>a)\g'word'`},
		{name: "named python", pattern: `(?P<word>a)(?P=word)`},
		{name: "closed sibling while another group is open", pattern: `(?<word>a)(?<other>b\k<word>)`},
		{name: "closed inner group inside open outer group", pattern: `(a(b)\2)`},
		{name: "inside non-capturing group", pattern: `(a)(?:\1)`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.pattern); err != nil {
				t.Fatalf("Validate(%q) = %v, want nil", test.pattern, err)
			}
		})
	}
}

func TestValidateIgnoresNonBackreferenceEscapes(t *testing.T) {
	tests := []string{
		`(a)\Q\1\E`,
		`(a)[\1]`,
		`\(\1\)`,
	}
	for _, pattern := range tests {
		if err := Validate(pattern); err != nil {
			t.Fatalf("Validate(%q) = %v, want nil", pattern, err)
		}
	}
}

func TestValidateRejectsForwardReference(t *testing.T) {
	tests := []string{
		`\1(a)`,
		`\k<word>(?<word>a)`,
	}
	for _, pattern := range tests {
		err := Validate(pattern)
		if !errors.Is(err, ErrBackreferenceToFutureGroup) {
			t.Fatalf("Validate(%q) error = %v, want ErrBackreferenceToFutureGroup", pattern, err)
		}
	}
}

func TestValidateRejectsUnknownNamedGroup(t *testing.T) {
	for _, pattern := range []string{`\k<missing>`, `(?P=missing)`} {
		if err := Validate(pattern); !errors.Is(err, ErrUnknownGroup) {
			t.Fatalf("Validate(%q) error = %v, want ErrUnknownGroup", pattern, err)
		}
	}
}

func TestValidateRejectsUnbalancedGroups(t *testing.T) {
	for _, pattern := range []string{`(a`, `a)`, `(?#comment`} {
		if err := Validate(pattern); !errors.Is(err, ErrUnbalancedGroups) {
			t.Fatalf("Validate(%q) error = %v, want ErrUnbalancedGroups", pattern, err)
		}
	}
}

func TestAnalyzeReportsGroupLifetimes(t *testing.T) {
	pattern := `(?<word>a)(b\1)`
	report, err := Analyze(pattern)
	if err != nil {
		t.Fatalf("Analyze(%q) = %v, want nil", pattern, err)
	}
	if !report.Safe {
		t.Fatalf("Analyze(%q).Safe = false, want true", pattern)
	}
	if len(report.Groups) != 2 {
		t.Fatalf("len(Groups) = %d, want 2", len(report.Groups))
	}
	if len(report.Backreferences) != 1 {
		t.Fatalf("len(Backreferences) = %d, want 1", len(report.Backreferences))
	}
	reference := report.Backreferences[0]
	if reference.Target != 1 || reference.EnclosingGroup != 2 || !reference.TargetClosed {
		t.Fatalf("unexpected reference: %+v", reference)
	}
}

func TestConditionalGroupDoesNotShiftNumbering(t *testing.T) {
	pattern := `(a)(?(1)b|c)\1`
	report, err := Analyze(pattern)
	if err != nil {
		t.Fatalf("Analyze(%q) = %v, want nil", pattern, err)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("len(Groups) = %d, want 1", len(report.Groups))
	}
}

func TestAnalyzeReturnsReportForRejectedReference(t *testing.T) {
	pattern := `(a\1)`
	report, err := Analyze(pattern)
	if !errors.Is(err, ErrBackreferenceToOpenGroup) {
		t.Fatalf("Analyze(%q) error = %v, want ErrBackreferenceToOpenGroup", pattern, err)
	}
	if report == nil {
		t.Fatal("Analyze returned a nil report for a rejected reference")
	}
	if report.Safe {
		t.Fatal("report.Safe = true, want false")
	}
	if len(report.Backreferences) != 1 {
		t.Fatalf("len(Backreferences) = %d, want 1", len(report.Backreferences))
	}
	if report.Backreferences[0].TargetClosed {
		t.Fatal("reference target reported as closed")
	}
}
