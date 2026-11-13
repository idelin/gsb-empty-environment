package netsec

import (
	"strings"
	"testing"
)

func TestNFAMatchesReference(t *testing.T) {
	expressions := []string{
		"a",
		"a/**",
		"a/*",
		"a/**/b",
		"a/*/b",
		"**/b",
		"**",
		"a/$*b/*",
		"a/$*/*",
		"a/b$*",
		"a/$*b$*",
		"a/**/secret/**",
		"**/secret/*",
		"a/**/**/b",
	}
	keys := enumerateKeys([]string{"a", "b", "c", "ab", "bc", "secret"}, 3)

	for _, raw := range expressions {
		ex, err := parseExpression(raw)
		if err != nil {
			t.Fatalf("parseExpression(%q): %v", raw, err)
		}
		for _, key := range keys {
			want := referenceMatches(raw, key)
			if got := nfaMatches(ex.nfa, key); got != want {
				t.Fatalf("%q against %q: NFA = %v, reference = %v", raw, key, got, want)
			}
		}
	}
}

func TestRelationsOnFiniteUniverse(t *testing.T) {
	expressions := []string{
		"a/**",
		"a/*",
		"a/**/b",
		"a/*/b",
		"a/$*b/*",
		"a/$*/*",
		"a/**/secret/*",
		"**/secret/*",
		"**",
	}
	keys := enumerateKeys([]string{"a", "b", "c", "ab", "secret"}, 3)

	for _, container := range expressions {
		for _, candidate := range expressions {
			contains := ExpressionContains(container, candidate)
			overlap := ExpressionsIntersect(container, candidate)
			for _, key := range keys {
				inContainer := referenceMatches(container, key)
				inCandidate := referenceMatches(candidate, key)
				if inCandidate && !inContainer && contains {
					t.Fatalf("ExpressionContains(%q, %q) missed finite counterexample %q", container, candidate, key)
				}
				if inContainer && inCandidate && !overlap {
					t.Fatalf("ExpressionsIntersect(%q, %q) missed finite witness %q", container, candidate, key)
				}
			}
		}
	}
}

func enumerateKeys(chunks []string, maxLength int) []string {
	current := make([]string, 0)
	var result []string
	var visit func(depth int)
	visit = func(depth int) {
		if depth > 0 {
			result = append(result, strings.Join(current, "/"))
		}
		if depth == maxLength {
			return
		}
		for _, chunk := range chunks {
			current = append(current, chunk)
			visit(depth + 1)
			current = current[:len(current)-1]
		}
	}
	visit(0)
	return result
}

func nfaMatches(n *nfa, key string) bool {
	current := epsilonClosure(n, n.start)
	for i := 0; i < len(key); i++ {
		var next []int
		for _, state := range current {
			for _, out := range n.edges[state] {
				switch out.kind {
				case edgeLiteral:
					if out.ch == key[i] {
						next = append(next, out.to)
					}
				case edgeNonSlash:
					if key[i] != '/' {
						next = append(next, out.to)
					}
				}
			}
		}
		current = epsilonClosure(n, next...)
		if len(current) == 0 {
			return false
		}
	}
	for _, state := range current {
		if state == n.accept {
			return true
		}
	}
	return false
}

type referenceSegment struct {
	kind  segmentKind
	parts []chunkPart
}

func referenceMatches(expression, key string) bool {
	segments := referenceSegments(expression)
	chunks := strings.Split(key, "/")
	return referenceSegmentsMatch(segments, chunks)
}

func referenceSegments(expression string) []referenceSegment {
	chunks := strings.Split(expression, "/")
	result := make([]referenceSegment, 0, len(chunks))
	for _, chunk := range chunks {
		switch chunk {
		case "*":
			result = append(result, referenceSegment{kind: segmentStar})
		case "**":
			result = append(result, referenceSegment{kind: segmentDoubleStar})
		default:
			var parts []chunkPart
			var text []byte
			for i := 0; i < len(chunk); i++ {
				if chunk[i] == '$' && i+1 < len(chunk) && chunk[i+1] == '*' {
					if len(text) > 0 {
						parts = append(parts, chunkPart{text: text})
						text = nil
					}
					parts = append(parts, chunkPart{wildcard: true})
					i++
					continue
				}
				text = append(text, chunk[i])
			}
			if len(text) > 0 {
				parts = append(parts, chunkPart{text: text})
			}
			result = append(result, referenceSegment{kind: segmentPattern, parts: parts})
		}
	}
	return result
}

func referenceSegmentsMatch(segments []referenceSegment, chunks []string) bool {
	if len(segments) == 0 {
		return len(chunks) == 0
	}
	segment := segments[0]
	if segment.kind == segmentDoubleStar {
		if referenceSegmentsMatch(segments[1:], chunks) {
			return true
		}
		return len(chunks) > 0 && referenceSegmentsMatch(segments, chunks[1:])
	}
	if len(chunks) == 0 || !referenceSegmentMatches(segment, chunks[0]) {
		return false
	}
	return referenceSegmentsMatch(segments[1:], chunks[1:])
}

func referenceSegmentMatches(segment referenceSegment, chunk string) bool {
	if segment.kind == segmentStar {
		return chunk != ""
	}
	if segment.kind == segmentDoubleStar {
		return true
	}
	if len(segment.parts) == 0 {
		return chunk != ""
	}
	return referencePartsMatch(segment.parts, chunk)
}

func referencePartsMatch(parts []chunkPart, chunk string) bool {
	if len(parts) == 0 {
		return len(chunk) == 0
	}
	part := parts[0]
	if part.wildcard {
		for end := 0; end <= len(chunk); end++ {
			if referencePartsMatch(parts[1:], chunk[end:]) {
				return true
			}
		}
		return false
	}
	if !strings.HasPrefix(chunk, string(part.text)) {
		return false
	}
	return referencePartsMatch(parts[1:], chunk[len(part.text):])
}
