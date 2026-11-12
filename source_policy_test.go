package netsec

import (
	"fmt"
	"strings"
	"testing"
)

const (
	testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testCommit = "0123456789abcdef0123456789abcdef01234567"
)

func allowAll() []Rule {
	return []Rule{{ID: "allow-all", MatchType: MatchWildcard, Pattern: "*", Action: ActionAllow}}
}

func evaluateOne(t *testing.T, vertex Vertex, rules []Rule) (*Result, *Decision) {
	t.Helper()
	result, err := Evaluate(&Graph{Vertices: []Vertex{vertex}}, rules)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("got %d decisions, want 1", len(result.Decisions))
	}
	return result, &result.Decisions[0]
}

func TestImageNormalization(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "default registry and library prefix",
			source: "ubuntu:latest",
			want:   "docker.io/library/ubuntu:latest",
		},
		{
			name:   "bare official image",
			source: "alpine",
			want:   "docker.io/library/alpine",
		},
		{
			name:   "docker image scheme",
			source: "docker-image://docker.io/library/alpine:3.20",
			want:   "docker.io/library/alpine:3.20",
		},
		{
			name:   "index alias",
			source: "index.docker.io/ubuntu:24.04",
			want:   "docker.io/library/ubuntu:24.04",
		},
		{
			name:   "non official docker hub repository",
			source: "docker.io/acme/tool:v1",
			want:   "docker.io/acme/tool:v1",
		},
		{
			name:   "custom registry",
			source: "registry.example.com:5000/team/tool:v1",
			want:   "registry.example.com:5000/team/tool:v1",
		},
		{
			name:   "localhost registry",
			source: "localhost:5000/team/tool:v1",
			want:   "localhost:5000/team/tool:v1",
		},
		{
			name:   "digest only",
			source: "alpine@" + testDigest,
			want:   "docker.io/library/alpine@" + testDigest,
		},
		{
			name:   "digest wins over tag",
			source: "docker.io/library/alpine:latest@" + testDigest,
			want:   "docker.io/library/alpine@" + testDigest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, decision := evaluateOne(t, Vertex{ID: "v0", Source: tt.source}, allowAll())
			if !decision.Allowed {
				t.Fatalf("source %q was denied: %s", tt.source, decision.Reason)
			}
			if decision.Canonical != tt.want {
				t.Fatalf("Canonical = %q, want %q", decision.Canonical, tt.want)
			}
			if decision.Kind != KindImage {
				t.Fatalf("Kind = %q, want %q", decision.Kind, KindImage)
			}
		})
	}
}

func TestImageAtSpoofingIsRejected(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "multiple at separators",
			source: "docker.io/library/alpine@" + testDigest + "@" + testDigest,
		},
		{
			name:   "short digest payload",
			source: "docker.io/library/alpine@sha256:deadbeef",
		},
		{
			name:   "forged identity after at",
			source: "docker.io/library/alpine:latest@evil.example.com/repo",
		},
		{
			name:   "empty digest",
			source: "docker.io/library/alpine@",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, decision := evaluateOne(t, Vertex{ID: "v0", Source: tt.source}, []Rule{{
				ID:        "allow-trusted-looking-prefix",
				MatchType: MatchExact,
				Pattern:   "docker.io/library/alpine:latest",
				Action:    ActionAllow,
			}})
			if result.Allowed || decision.Allowed {
				t.Fatalf("forged image reference %q was allowed", tt.source)
			}
			if decision.Reason == "" {
				t.Fatal("expected a denial reason")
			}
		})
	}
}

func TestGitRequiresImmutableCommitAndNoUserInfo(t *testing.T) {
	pinned := "git://github.com/acme/repo.git#" + testCommit
	_, decision := evaluateOne(t, Vertex{ID: "v0", Source: pinned}, allowAll())
	if !decision.Allowed {
		t.Fatalf("pinned git source denied: %s", decision.Reason)
	}
	if decision.Canonical != pinned {
		t.Fatalf("Canonical = %q, want %q", decision.Canonical, pinned)
	}

	tests := []struct {
		name   string
		vertex Vertex
	}{
		{
			name:   "branch fragment",
			vertex: Vertex{ID: "branch", Source: "git://github.com/acme/repo.git#main"},
		},
		{
			name:   "tag fragment",
			vertex: Vertex{ID: "tag", Source: "git://github.com/acme/repo.git#v1.2.3"},
		},
		{
			name:   "missing ref",
			vertex: Vertex{ID: "missing", Source: "git://github.com/acme/repo.git"},
		},
		{
			name:   "mutable ref attribute",
			vertex: Vertex{ID: "attr-branch", Source: "git://github.com/acme/repo.git", Attrs: map[string]string{"git.ref": "main"}},
		},
		{
			name:   "url userinfo",
			vertex: Vertex{ID: "userinfo", Source: "ssh://git@github.com/acme/repo.git#" + testCommit},
		},
		{
			name: "conflicting refs",
			vertex: Vertex{
				ID:     "conflict",
				Source: "git://github.com/acme/repo.git#" + testCommit,
				Attrs:  map[string]string{"git.ref": "main"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, decision := evaluateOne(t, tt.vertex, allowAll())
			if result.Allowed || decision.Allowed {
				t.Fatalf("mutable or credential-bearing git source was allowed: %+v", tt.vertex)
			}
		})
	}

	_, decision = evaluateOne(t, Vertex{
		ID:     "attr-pinned",
		Source: "git://github.com/acme/repo.git",
		Attrs:  map[string]string{"git.ref": testCommit},
	}, allowAll())
	if !decision.Allowed {
		t.Fatalf("pinned git.ref attribute denied: %s", decision.Reason)
	}
	if !strings.Contains(decision.Canonical, "#"+testCommit) {
		t.Fatalf("Canonical = %q, want pinned commit fragment", decision.Canonical)
	}
}

func TestHTTPSourceRequiresChecksum(t *testing.T) {
	const artifact = "https://example.com/releases/tool.tar.gz"
	_, decision := evaluateOne(t, Vertex{
		ID:     "v0",
		Source: artifact,
		Attrs:  map[string]string{"http.checksum": testDigest},
	}, allowAll())
	if !decision.Allowed {
		t.Fatalf("checksummed HTTP source denied: %s", decision.Reason)
	}
	if decision.Canonical != artifact {
		t.Fatalf("Canonical = %q, want %q", decision.Canonical, artifact)
	}

	for _, vertex := range []Vertex{
		{ID: "missing", Source: artifact},
		{
			ID:     "short",
			Source: artifact,
			Attrs:  map[string]string{"http.checksum": "sha256:deadbeef"},
		},
		{
			ID:     "userinfo",
			Source: "https://user:pass@example.com/releases/tool.tar.gz",
			Attrs:  map[string]string{"http.checksum": testDigest},
		},
	} {
		result, decision := evaluateOne(t, vertex, allowAll())
		if result.Allowed || decision.Allowed {
			t.Fatalf("HTTP source %+v was allowed", vertex)
		}
	}
}

func TestConvertedSourceMustPassLaterDeny(t *testing.T) {
	rules := []Rule{
		{
			ID:        "pin-tag",
			MatchType: MatchWildcard,
			Pattern:   "docker.io/library/alpine:*",
			Action:    ActionConvert,
			ConvertTo: "docker.io/library/alpine@" + testDigest,
		},
		{
			ID:        "block-digest",
			MatchType: MatchExact,
			Pattern:   "docker.io/library/alpine@" + testDigest,
			Action:    ActionDeny,
		},
	}
	result, decision := evaluateOne(t, Vertex{
		ID:     "v0",
		Source: "docker.io/library/alpine:latest",
	}, rules)
	if result.Allowed || decision.Allowed {
		t.Fatal("converted source bypassed later DENY rule")
	}
	if decision.RuleID != "block-digest" {
		t.Fatalf("RuleID = %q, want block-digest", decision.RuleID)
	}
	if decision.Canonical != "docker.io/library/alpine@"+testDigest {
		t.Fatalf("Canonical = %q, want pinned digest", decision.Canonical)
	}
}

func TestConvertedSourceScansPastLaterAllowForDeny(t *testing.T) {
	rules := []Rule{
		{
			ID:        "pin-tag",
			MatchType: MatchWildcard,
			Pattern:   "docker.io/library/alpine:*",
			Action:    ActionConvert,
			ConvertTo: "docker.io/library/alpine@" + testDigest,
		},
		{
			ID:        "allow-pinned",
			MatchType: MatchExact,
			Pattern:   "docker.io/library/alpine@" + testDigest,
			Action:    ActionAllow,
		},
		{
			ID:        "deny-pinned",
			MatchType: MatchExact,
			Pattern:   "docker.io/library/alpine@" + testDigest,
			Action:    ActionDeny,
		},
	}
	result, decision := evaluateOne(t, Vertex{
		ID:     "v0",
		Source: "docker.io/library/alpine:latest",
	}, rules)
	if result.Allowed || decision.Allowed {
		t.Fatal("later ALLOW hid a DENY from the converted source")
	}
	if decision.RuleID != "deny-pinned" {
		t.Fatalf("RuleID = %q, want deny-pinned", decision.RuleID)
	}
}

func TestFirstMatchWinsForNonConvert(t *testing.T) {
	rules := []Rule{
		{
			ID:        "allow-first",
			MatchType: MatchExact,
			Pattern:   "docker.io/library/alpine:latest",
			Action:    ActionAllow,
		},
		{
			ID:        "deny-second",
			MatchType: MatchWildcard,
			Pattern:   "docker.io/library/*",
			Action:    ActionDeny,
		},
	}
	result, decision := evaluateOne(t, Vertex{
		ID:     "v0",
		Source: "alpine:latest",
	}, rules)
	if !result.Allowed || !decision.Allowed {
		t.Fatalf("first matching ALLOW did not win: %+v", decision)
	}
	if decision.RuleID != "allow-first" {
		t.Fatalf("RuleID = %q, want allow-first", decision.RuleID)
	}
}

func TestExactImagePatternUsesNormalizedIdentity(t *testing.T) {
	rules := []Rule{{
		ID:        "allow-short-form",
		MatchType: MatchExact,
		Pattern:   "ubuntu:latest",
		Action:    ActionAllow,
	}}
	result, decision := evaluateOne(t, Vertex{
		ID:     "v0",
		Source: "docker.io/library/ubuntu:latest",
	}, rules)
	if !result.Allowed || !decision.Allowed {
		t.Fatalf("normalized image did not match exact shorthand rule: %+v", decision)
	}
}

func TestLocalContextReference(t *testing.T) {
	_, decision := evaluateOne(t, Vertex{
		ID:     "v0",
		Source: "local://build-context",
	}, allowAll())
	if !decision.Allowed {
		t.Fatalf("local source denied: %s", decision.Reason)
	}
	if decision.Kind != KindLocal || decision.Canonical != "local://build-context" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestSharedSubgraphEvaluatedOnce(t *testing.T) {
	graph := &Graph{
		Vertices: []Vertex{
			{ID: "root-a", Source: "alpine:3.20", Deps: []string{"shared", "shared"}},
			{ID: "root-b", Source: "busybox:1.36", Dependencies: []string{"shared"}},
			{ID: "shared", Source: "ubuntu:24.04"},
		},
	}
	result, err := Evaluate(graph, allowAll())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Allowed {
		t.Fatalf("graph was denied: %+v", result.FirstDenial())
	}
	if len(result.Decisions) != len(graph.Vertices) {
		t.Fatalf("got %d decisions, want %d (one per distinct vertex)", len(result.Decisions), len(graph.Vertices))
	}
	seen := make(map[string]bool)
	for _, decision := range result.Decisions {
		if seen[decision.VertexID] {
			t.Fatalf("vertex %q was evaluated more than once", decision.VertexID)
		}
		seen[decision.VertexID] = true
	}
}

func TestIdenticalDuplicateVertexCollapses(t *testing.T) {
	shared := Vertex{ID: "shared", Source: "ubuntu:24.04"}
	graph := &Graph{Vertices: []Vertex{
		{ID: "root", Source: "alpine:3.20", Deps: []string{"shared"}},
		shared,
		shared,
	}}
	result, err := Evaluate(graph, allowAll())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Decisions) != 2 {
		t.Fatalf("got %d decisions, want 2", len(result.Decisions))
	}
}

func TestRootsLimitEvaluationToReachableSubgraph(t *testing.T) {
	graph := &Graph{
		Roots: []string{"root-a"},
		Vertices: []Vertex{
			{ID: "root-a", Source: "alpine:3.20", Deps: []string{"shared"}},
			{ID: "root-b", Source: "ubuntu:24.04"},
			{ID: "shared", Source: "busybox:1.36"},
		},
	}
	result, err := Evaluate(graph, allowAll())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(result.Decisions) != 2 {
		t.Fatalf("got %d decisions, want 2 reachable vertices", len(result.Decisions))
	}
	for _, decision := range result.Decisions {
		if decision.VertexID == "root-b" {
			t.Fatal("unreachable vertex was evaluated")
		}
	}
}

func TestMalformedGraphRejected(t *testing.T) {
	graph := &Graph{Vertices: []Vertex{
		{ID: "dup", Source: "alpine"},
		{ID: "dup", Source: "busybox"},
	}}
	if _, err := Evaluate(graph, allowAll()); err == nil {
		t.Fatal("expected duplicate vertex ID error")
	}

	graph = &Graph{Vertices: []Vertex{
		{ID: "root", Source: "alpine", Deps: []string{"missing"}},
	}}
	if _, err := Evaluate(graph, allowAll()); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestDefaultActionIsDeny(t *testing.T) {
	result, err := Evaluate(&Graph{Vertices: []Vertex{{ID: "v0", Source: "alpine"}}}, nil)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Allowed {
		t.Fatal("unmatched source was allowed by the zero-value policy")
	}
	if got := result.FirstDenial().Reason; got != "no matching source policy rule" {
		t.Fatalf("Reason = %q", got)
	}
}

func TestInvalidRuleConfiguration(t *testing.T) {
	for _, rules := range [][]Rule{
		{{MatchType: "BOGUS", Pattern: "*", Action: ActionAllow}},
		{{MatchType: MatchRegex, Pattern: "(", Action: ActionAllow}},
		{{MatchType: MatchWildcard, Pattern: "*", Action: ActionConvert}},
	} {
		if _, err := Evaluate(&Graph{}, rules); err == nil {
			t.Fatalf("expected error for rules %+v", rules)
		}
	}
}

func ExamplePolicy_Authorize() {
	policy := Policy{Rules: []Rule{{
		ID:        "pin-alpine",
		MatchType: MatchWildcard,
		Pattern:   "docker.io/library/alpine:*",
		Action:    ActionConvert,
		ConvertTo: "docker.io/library/alpine@" + testDigest,
	}}}
	err := policy.Authorize(&Graph{Vertices: []Vertex{{
		ID:     "source",
		Source: "alpine:latest",
	}}})
	fmt.Println(err == nil)
	// Output: true
}
