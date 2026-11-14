package netsec

import (
	"strings"
	"testing"
)

func TestCoverageAgainstExhaustiveLanguage(t *testing.T) {
	t.Parallel()

	patterns := patternStrings(3)
	routes := routeStrings(4)

	for _, allowedString := range patterns {
		allowed, err := ParsePattern(allowedString)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", allowedString, err)
		}
		for _, requestedString := range patterns {
			requested, err := ParsePattern(requestedString)
			if err != nil {
				t.Fatalf("ParsePattern(%q): %v", requestedString, err)
			}

			want := true
			for _, route := range routes {
				if requested.Matches(route) && !allowed.Matches(route) {
					want = false
					break
				}
			}
			if got := allowed.Covers(requested); got != want {
				t.Fatalf("Covers(%q, %q) = %v, want %v", allowedString, requestedString, got, want)
			}
		}
	}
}

func patternStrings(maxLength int) []string {
	values := []string{"a", "b", "*", "#"}
	patterns := []string{""}
	current := []string{""}
	for length := 1; length <= maxLength; length++ {
		next := make([]string, 0, len(current)*len(values))
		for _, prefix := range current {
			for _, value := range values {
				pattern := value
				if prefix != "" {
					pattern = prefix + "." + value
				}
				next = append(next, pattern)
				patterns = append(patterns, pattern)
			}
		}
		current = next
	}
	return patterns
}

func routeStrings(maxLength int) []string {
	values := []string{"a", "b", "c"}
	routes := []string{""}
	current := []string{""}
	for length := 1; length <= maxLength; length++ {
		next := make([]string, 0, len(current)*len(values))
		for _, prefix := range current {
			for _, value := range values {
				route := value
				if prefix != "" {
					route = prefix + "." + value
				}
				next = append(next, route)
				routes = append(routes, route)
			}
		}
		current = next
	}
	return routes
}

func TestPatternStringsAreWellFormed(t *testing.T) {
	for _, pattern := range patternStrings(2) {
		if strings.Count(pattern, ".") > 1 {
			t.Fatalf("unexpected pattern %q", pattern)
		}
		if _, err := ParsePattern(pattern); err != nil {
			t.Fatalf("generated invalid pattern %q: %v", pattern, err)
		}
	}
}
