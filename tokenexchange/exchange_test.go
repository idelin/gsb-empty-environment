package tokenexchange

import (
	"context"
	"errors"
	"testing"
	"time"
)

const fixedNow = "2026-11-13T00:00:00Z"

type fakeInspector struct {
	tokens map[string]TokenInfo
}

func (f fakeInspector) InspectToken(_ context.Context, token string) (TokenInfo, error) {
	info, ok := f.tokens[token]
	if !ok {
		return TokenInfo{}, nil
	}
	return info, nil
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func newTestEngine(t *testing.T, inspector Inspector, policy AudiencePolicy) *Engine {
	t.Helper()
	now := parseTime(t, fixedNow)
	return NewEngine(inspector, policy).WithClock(func() time.Time { return now })
}

func baseSubject(now time.Time) TokenInfo {
	return TokenInfo{
		Active:   true,
		Type:     TypeAccessToken,
		Subject:  "user-1",
		Scopes:   []string{"read", "write"},
		Audience: []string{"api-internal"},
		Expiry:   now.Add(time.Hour),
		MayAct:   &Actor{ClientID: "gateway"},
	}
}

func newPolicy() AudienceTable {
	return NewAudienceTable(map[string][]string{
		"gateway":      {"api-external", "billing"},
		"other":        {"billing"},
		"svc-identity": {"billing"},
	})
}

func TestExchangeAudienceAllowed(t *testing.T) {
	now := parseTime(t, fixedNow)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": baseSubject(now)}},
		newPolicy())

	decision, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		Audience:         "api-external",
	})
	if err != nil {
		t.Fatalf("expected approval, got %v", err)
	}
	if decision.Subject != "user-1" || decision.Audience != "api-external" {
		t.Fatalf("unexpected decision claims: %+v", decision)
	}
	if decision.Delegated || decision.Act != nil {
		t.Fatalf("non-delegation exchange must not carry an act claim: %+v", decision)
	}
	if got := len(decision.Scopes); got != 2 {
		t.Fatalf("expected subject scopes to be carried over, got %v", decision.Scopes)
	}
}

func TestExchangeAudienceNotInTable(t *testing.T) {
	now := parseTime(t, fixedNow)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": baseSubject(now)}},
		newPolicy())

	_, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		Audience:         "secret-service",
	})
	assertErrorCode(t, err, ErrInvalidTarget)
}

func TestExchangeScopeEscalationDenied(t *testing.T) {
	now := parseTime(t, fixedNow)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": baseSubject(now)}},
		newPolicy())

	_, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		Audience:         "api-external",
		Scopes:           []string{"read", "admin"},
	})
	assertErrorCode(t, err, ErrInvalidScope)
}

func TestExchangeRequestedScopeSubset(t *testing.T) {
	now := parseTime(t, fixedNow)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": baseSubject(now)}},
		newPolicy())

	decision, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		Audience:         "api-external",
		Scopes:           []string{"read"},
	})
	if err != nil {
		t.Fatalf("expected approval, got %v", err)
	}
	if len(decision.Scopes) != 1 || decision.Scopes[0] != "read" {
		t.Fatalf("expected narrowed scopes [read], got %v", decision.Scopes)
	}
}

func TestExchangeMayActDenied(t *testing.T) {
	now := parseTime(t, fixedNow)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": baseSubject(now)}},
		newPolicy())

	_, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "attacker"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		Audience:         "api-external",
	})
	assertErrorCode(t, err, ErrAccessDenied)
}

func TestExchangeMayActMatchesBySubject(t *testing.T) {
	now := parseTime(t, fixedNow)
	subject := baseSubject(now)
	subject.MayAct = &Actor{Subject: "svc-identity"}
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": subject}},
		newPolicy())

	_, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{Subject: "svc-identity"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		Audience:         "billing",
	})
	if err != nil {
		t.Fatalf("subject-based may_act match should pass, got %v", err)
	}
}

func TestExchangeSubjectTokenTypeMismatch(t *testing.T) {
	now := parseTime(t, fixedNow)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": baseSubject(now)}},
		newPolicy())

	_, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeJWT,
		Audience:         "api-external",
	})
	assertErrorCode(t, err, ErrInvalidRequest)
}

func TestExchangeExpiredOrRevokedOrInactive(t *testing.T) {
	now := parseTime(t, fixedNow)

	cases := map[string]func(TokenInfo) TokenInfo{
		"expired": func(info TokenInfo) TokenInfo {
			info.Expiry = now.Add(-time.Minute)
			return info
		},
		"revoked": func(info TokenInfo) TokenInfo {
			info.Revoked = true
			return info
		},
		"inactive": func(info TokenInfo) TokenInfo {
			info.Active = false
			return info
		},
		"unknown": func(_ TokenInfo) TokenInfo {
			return TokenInfo{}
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			engine := newTestEngine(t,
				fakeInspector{tokens: map[string]TokenInfo{"subj": mutate(baseSubject(now))}},
				newPolicy())

			_, err := engine.Decide(context.Background(), Request{
				Requester:        Requester{ClientID: "gateway"},
				SubjectToken:     "subj",
				SubjectTokenType: TypeAccessToken,
				Audience:         "api-external",
			})
			assertErrorCode(t, err, ErrInvalidToken)
		})
	}
}

func TestDelegationNestsPreviousAct(t *testing.T) {
	now := parseTime(t, fixedNow)
	subject := baseSubject(now)
	subject.Act = &Actor{Subject: "inner-actor", ClientID: "inner-client"}

	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{
			"subj": subject,
			"actr": {
				Active:   true,
				Type:     TypeAccessToken,
				Subject:  "outer-actor",
				ClientID: "outer-client",
				Expiry:   now.Add(time.Hour),
			},
		}},
		newPolicy())

	decision, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		ActorToken:       "actr",
		ActorTokenType:   TypeAccessToken,
		Audience:         "api-external",
	})
	if err != nil {
		t.Fatalf("expected delegation approval, got %v", err)
	}
	if !decision.Delegated || decision.Act == nil {
		t.Fatalf("delegation decision must carry an act claim: %+v", decision)
	}
	if decision.Act.Subject != "outer-actor" || decision.Act.ClientID != "outer-client" {
		t.Fatalf("outer act entry mismatch: %+v", decision.Act)
	}
	if decision.Act.Act == nil || decision.Act.Act.Subject != "inner-actor" {
		t.Fatalf("previous act chain must be nested inside the new act: %+v", decision.Act)
	}
}

func TestDelegationDepthLimit(t *testing.T) {
	now := parseTime(t, fixedNow)

	for existingDepth := 0; existingDepth <= 3; existingDepth++ {
		subject := baseSubject(now)
		subject.Act = buildChain(existingDepth)

		engine := newTestEngine(t,
			fakeInspector{tokens: map[string]TokenInfo{
				"subj": subject,
				"actr": {
					Active:  true,
					Type:    TypeAccessToken,
					Subject: "new-actor",
					Expiry:  now.Add(time.Hour),
				},
			}},
			newPolicy())

		decision, err := engine.Decide(context.Background(), Request{
			Requester:        Requester{ClientID: "gateway"},
			SubjectToken:     "subj",
			SubjectTokenType: TypeAccessToken,
			ActorToken:       "actr",
			ActorTokenType:   TypeAccessToken,
			Audience:         "api-external",
		})

		resultingDepth := existingDepth + 1
		if resultingDepth <= MaxDelegationDepth {
			if err != nil {
				t.Fatalf("depth %d should be allowed, got %v", resultingDepth, err)
			}
			if actDepth(decision.Act) != resultingDepth {
				t.Fatalf("expected chain depth %d, got %d", resultingDepth, actDepth(decision.Act))
			}
			continue
		}
		assertErrorCode(t, err, ErrAccessDenied)
	}
}

func TestActorTokenTypeMismatch(t *testing.T) {
	now := parseTime(t, fixedNow)
	subject := baseSubject(now)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{
			"subj": subject,
			"actr": {Active: true, Type: TypeIDToken, Subject: "actor", Expiry: now.Add(time.Hour)},
		}},
		newPolicy())

	_, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
		ActorToken:       "actr",
		ActorTokenType:   TypeAccessToken,
		Audience:         "api-external",
	})
	assertErrorCode(t, err, ErrInvalidRequest)
}

func TestMissingParameters(t *testing.T) {
	now := parseTime(t, fixedNow)
	engine := newTestEngine(t,
		fakeInspector{tokens: map[string]TokenInfo{"subj": baseSubject(now)}},
		newPolicy())

	_, err := engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectTokenType: TypeAccessToken,
		Audience:         "api-external",
	})
	assertErrorCode(t, err, ErrInvalidRequest)

	_, err = engine.Decide(context.Background(), Request{
		Requester:        Requester{ClientID: "gateway"},
		SubjectToken:     "subj",
		SubjectTokenType: TypeAccessToken,
	})
	assertErrorCode(t, err, ErrInvalidTarget)
}

func TestErrorCodeOf(t *testing.T) {
	if code, ok := ErrorCodeOf(decisionError(ErrInvalidScope, "x")); !ok || code != ErrInvalidScope {
		t.Fatalf("expected ErrInvalidScope, got %q ok=%v", code, ok)
	}
	if _, ok := ErrorCodeOf(errors.New("other")); ok {
		t.Fatal("plain error must not report a token endpoint code")
	}
}

func buildChain(depth int) *Actor {
	var chain *Actor
	for i := 0; i < depth; i++ {
		chain = &Actor{Subject: "actor", Act: chain}
	}
	return chain
}

func assertErrorCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", want)
	}
	code, ok := ErrorCodeOf(err)
	if !ok || code != want {
		t.Fatalf("expected error code %q, got %v", want, err)
	}
}
