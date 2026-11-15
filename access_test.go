package netsec

import (
	"errors"
	"testing"
	"time"
)

var (
	t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = t0.Add(24 * time.Hour)
	t2 = t1.Add(24 * time.Hour)
)

func fixedClock() func() time.Time {
	return func() time.Time { return t1 }
}

func doc(subject string, notBefore, notAfter time.Time, rules ...Rule) PermissionDocument {
	return PermissionDocument{
		SubjectName: subject,
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		Rules:       rules,
	}
}

func rule(effect Decision, action Action, topics []string, domains []int, partitions []string) Rule {
	return Rule{
		Effect:     effect,
		Action:     action,
		Topics:     topics,
		DomainIDs:  domains,
		Partitions: partitions,
	}
}

func request(subject string, action Action, topic string, domain int, partition string) AccessRequest {
	return AccessRequest{
		CertificateSubject: subject,
		Action:             action,
		Topic:              topic,
		DomainID:           domain,
		Partition:          partition,
	}
}

// The classic attacker scenario: a broad allow-all rule exists, but an
// earlier deny rule for sensitive topics must win regardless of wildcard use.
func TestAuthorize_DenyBeforeAllowStopsWildcardBypass(t *testing.T) {
	engine, err := NewEngine([]PermissionDocument{
		doc("cn=pub", t0, t2,
			rule(Deny, Publish, []string{"secret/*"}, []int{0}, nil),
			rule(Allow, Publish, []string{"*"}, []int{0}, nil),
		),
	}, WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cases := []struct {
		name string
		req  AccessRequest
		want Decision
	}{
		{"wildcard cannot bypass deny", request("cn=pub", Publish, "secret/keys", 0, "p1"), Deny},
		{"nested wildcard topic denied", request("cn=pub", Publish, "secret/region/keys", 0, "p1"), Deny},
		{"ordinary topic allowed", request("cn=pub", Publish, "telemetry/temp", 0, "p1"), Allow},
		{"case-sensitive deny pattern", request("cn=pub", Publish, "SECRET/keys", 0, "p1"), Allow},
	}
	for _, tc := range cases {
		got, explanation := engine.Authorize(tc.req)
		if got != tc.want {
			t.Errorf("%s: decision = %s, want %s (explanation %+v)", tc.name, got, tc.want, explanation)
		}
	}
}

// First-match-wins applies regardless of effect: when the allow precedes the
// deny, the allow is the verdict.
func TestAuthorize_FirstMatchingRuleWins(t *testing.T) {
	engine, err := NewEngine([]PermissionDocument{
		doc("cn=pub", t0, t2,
			rule(Allow, Publish, []string{"events/*"}, []int{0}, nil),
			rule(Deny, Publish, []string{"events/*"}, []int{0}, nil),
		),
	}, WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	decision, explanation := engine.Authorize(request("cn=pub", Publish, "events/1", 0, "p"))
	if decision != Allow {
		t.Fatalf("decision = %s, want allow", decision)
	}
	if explanation.RuleIndex != 0 {
		t.Fatalf("RuleIndex = %d, want 0", explanation.RuleIndex)
	}
}

func TestAuthorize_ExpiredOrFutureDocumentIsInvalid(t *testing.T) {
	allowAll := rule(Allow, ActionAny, []string{"*"}, []int{0}, nil)

	expired := NewEngineOrFail(t, []PermissionDocument{
		doc("cn=pub", t0, t1, allowAll), // half-open: at t1 it is expired
	})
	if decision, _ := expired.Authorize(request("cn=pub", Publish, "x", 0, "p")); decision != Deny {
		t.Errorf("expired document: decision = %s, want deny", decision)
	}

	future := NewEngineOrFail(t, []PermissionDocument{
		doc("cn=pub", t2, t2.Add(time.Hour), allowAll),
	})
	if decision, _ := future.Authorize(request("cn=pub", Publish, "x", 0, "p")); decision != Deny {
		t.Errorf("not-yet-valid document: decision = %s, want deny", decision)
	}

	// Boundary: t0 is inclusive.
	atStart := NewEngineOrFailWithClock(t, []PermissionDocument{
		doc("cn=pub", t1, t2, allowAll),
	}, t1)
	if decision, _ := atStart.Authorize(request("cn=pub", Publish, "x", 0, "p")); decision != Allow {
		t.Errorf("document at NotBefore: decision = %s, want allow", decision)
	}
}

func TestAuthorize_SubjectMismatchInvalidatesDocument(t *testing.T) {
	engine := NewEngineOrFail(t, []PermissionDocument{
		doc("cn=legit", t0, t2,
			rule(Allow, Publish, []string{"*"}, []int{0}, nil),
		),
	})

	decision, explanation := engine.Authorize(request("cn=attacker", Publish, "anything", 0, "p"))
	if decision != Deny {
		t.Fatalf("decision = %s, want deny", decision)
	}
	if len(explanation.SkippedDocuments) != 1 || explanation.SkippedDocuments[0] != 0 {
		t.Fatalf("SkippedDocuments = %v, want [0]", explanation.SkippedDocuments)
	}
}

func TestAuthorize_DomainAndPartitionMustMatch(t *testing.T) {
	engine := NewEngineOrFail(t, []PermissionDocument{
		doc("cn=pub", t0, t2,
			rule(Allow, Publish,
				[]string{"robot/*"},
				[]int{7},
				[]string{"factory-a/*"}),
		),
	})

	cases := []struct {
		name string
		req  AccessRequest
		want Decision
	}{
		{"all match", request("cn=pub", Publish, "robot/arm", 7, "factory-a/line1"), Allow},
		{"wrong domain", request("cn=pub", Publish, "robot/arm", 8, "factory-a/line1"), Deny},
		{"wrong partition", request("cn=pub", Publish, "robot/arm", 7, "factory-b/line1"), Deny},
		{"wrong action", request("cn=pub", Subscribe, "robot/arm", 7, "factory-a/line1"), Deny},
		{"topic does not match", request("cn=pub", Publish, "robot2/arm", 7, "factory-a/line1"), Deny},
	}
	for _, tc := range cases {
		got, _ := engine.Authorize(tc.req)
		if got != tc.want {
			t.Errorf("%s: decision = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestAuthorize_EmptyPartitionListMatchesAll(t *testing.T) {
	engine := NewEngineOrFail(t, []PermissionDocument{
		doc("cn=pub", t0, t2,
			rule(Allow, Subscribe, []string{"t"}, []int{0}, nil),
		),
	})
	if decision, _ := engine.Authorize(request("cn=pub", Subscribe, "t", 0, "any/partition")); decision != Allow {
		t.Fatalf("decision = %s, want allow", decision)
	}
}

func TestAuthorize_NoMatchDefaultsToDeny(t *testing.T) {
	engine := NewEngineOrFail(t, []PermissionDocument{
		doc("cn=pub", t0, t2,
			rule(Allow, Publish, []string{"allowed/*"}, []int{0}, nil),
		),
	})
	decision, explanation := engine.Authorize(request("cn=pub", Publish, "unlisted/x", 0, "p"))
	if decision != Deny {
		t.Fatalf("decision = %s, want deny", decision)
	}
	if explanation.RuleIndex != -1 || explanation.DocumentIndex != -1 {
		t.Fatalf("expected default-deny explanation, got %+v", explanation)
	}
}

// A valid document for another subject cannot "rescue" a request that the
// caller's own document denies.
func TestAuthorize_ValidOtherSubjectDoesNotGrant(t *testing.T) {
	engine := NewEngineOrFail(t, []PermissionDocument{
		doc("cn=attacker", t0, t2,
			rule(Deny, Publish, []string{"*"}, []int{0}, nil),
		),
		doc("cn=victim", t0, t2,
			rule(Allow, Publish, []string{"*"}, []int{0}, nil),
		),
	})
	if decision, _ := engine.Authorize(request("cn=attacker", Publish, "x", 0, "p")); decision != Deny {
		t.Fatalf("decision = %s, want deny", decision)
	}
}

func TestNewEngine_RejectsMalformedDocuments(t *testing.T) {
	cases := []struct {
		name string
		docs []PermissionDocument
		want error
	}{
		{"no documents", nil, ErrNoDocuments},
		{"empty subject", []PermissionDocument{doc("", t0, t2)}, ErrEmptySubject},
		{"invalid window", []PermissionDocument{
			doc("cn=x", t2, t0),
		}, ErrInvalidWindow},
		{"rule without topics", []PermissionDocument{
			doc("cn=x", t0, t2, rule(Allow, Publish, nil, []int{0}, nil)),
		}, ErrEmptyRule},
		{"rule without domains", []PermissionDocument{
			doc("cn=x", t0, t2, rule(Allow, Publish, []string{"*"}, nil, nil)),
		}, ErrEmptyRule},
		{"malformed topic pattern", []PermissionDocument{
			doc("cn=x", t0, t2, rule(Allow, Publish, []string{"bad["}, []int{0}, nil)),
		}, ErrBadPattern},
	}
	for _, tc := range cases {
		if _, err := NewEngine(tc.docs); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func NewEngineOrFail(t *testing.T, docs []PermissionDocument) *Engine {
	t.Helper()
	return NewEngineOrFailWithClock(t, docs, t1)
}

func NewEngineOrFailWithClock(t *testing.T, docs []PermissionDocument, at time.Time) *Engine {
	t.Helper()
	engine, err := NewEngine(docs, WithClock(func() time.Time { return at }))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}
