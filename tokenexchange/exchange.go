package tokenexchange

import (
	"context"
	"time"
)

// Engine applies the RFC 8693 token exchange rules to parsed requests. It is
// safe for concurrent use as long as the configured AudiencePolicy is.
type Engine struct {
	inspector Inspector
	policy    AudiencePolicy
	now       func() time.Time
	maxDepth  int
}

// NewEngine builds an Engine. The inspector performs token validation and
// revocation checks; the policy constrains target audiences per requester.
func NewEngine(inspector Inspector, policy AudiencePolicy) *Engine {
	return &Engine{
		inspector: inspector,
		policy:    policy,
		now:       time.Now,
		maxDepth:  MaxDelegationDepth,
	}
}

// WithClock overrides the clock used for expiry checks, mainly for tests.
func (e *Engine) WithClock(now func() time.Time) *Engine {
	e.now = now
	return e
}

// Decide validates an RFC 8693 exchange request and returns the claims of the
// token that may be issued, or a *Error describing why the request is denied.
func (e *Engine) Decide(ctx context.Context, req Request) (*Decision, error) {
	if req.SubjectToken == "" || req.SubjectTokenType == "" {
		return nil, decisionError(ErrInvalidRequest, "subject_token and subject_token_type are required")
	}
	if req.Audience == "" {
		return nil, decisionError(ErrInvalidTarget, "a target audience is required")
	}

	subject, err := e.inspector.InspectToken(ctx, req.SubjectToken)
	if err != nil {
		return nil, decisionError(ErrInvalidToken, "subject token introspection failed: %v", err)
	}
	if err := e.checkUsable(subject, "subject token"); err != nil {
		return nil, err
	}
	if subject.Type != req.SubjectTokenType {
		return nil, decisionError(ErrInvalidRequest,
			"subject_token_type %q does not match the actual token type %q",
			req.SubjectTokenType, subject.Type)
	}

	if !mayActAuthorizes(subject.MayAct, req.Requester) {
		return nil, decisionError(ErrAccessDenied,
			"requester %q is not listed in the subject token may_act claim",
			requesterKey(req.Requester))
	}

	if !e.policy.AllowedAudience(req.Requester, req.Audience) {
		return nil, decisionError(ErrInvalidTarget,
			"requester %q is not authorized to obtain a token for audience %q",
			requesterKey(req.Requester), req.Audience)
	}

	granted := make(map[string]struct{}, len(subject.Scopes))
	for _, scope := range subject.Scopes {
		granted[scope] = struct{}{}
	}
	for _, scope := range req.Scopes {
		if _, ok := granted[scope]; !ok {
			return nil, decisionError(ErrInvalidScope,
				"requested scope %q is not granted by the subject token", scope)
		}
	}

	approvedScopes := req.Scopes
	if len(approvedScopes) == 0 {
		approvedScopes = append([]string(nil), subject.Scopes...)
	}

	decision := &Decision{
		Subject:  subject.Subject,
		Audience: req.Audience,
		Scopes:   approvedScopes,
	}

	if req.ActorToken != "" {
		act, err := e.buildAct(ctx, req, subject)
		if err != nil {
			return nil, err
		}
		decision.Act = act
		decision.Delegated = true
	}

	return decision, nil
}

func (e *Engine) checkUsable(info TokenInfo, label string) error {
	if !info.Active {
		return decisionError(ErrInvalidToken, "%s is not active", label)
	}
	if info.Revoked {
		return decisionError(ErrInvalidToken, "%s has been revoked", label)
	}
	if !info.Expiry.IsZero() && !info.Expiry.After(e.now()) {
		return decisionError(ErrInvalidToken, "%s has expired", label)
	}
	return nil
}

func (e *Engine) buildAct(ctx context.Context, req Request, subject TokenInfo) (*Actor, error) {
	if req.ActorTokenType == "" {
		return nil, decisionError(ErrInvalidRequest,
			"actor_token_type is required when actor_token is present")
	}

	actor, err := e.inspector.InspectToken(ctx, req.ActorToken)
	if err != nil {
		return nil, decisionError(ErrInvalidToken, "actor token introspection failed: %v", err)
	}
	if err := e.checkUsable(actor, "actor token"); err != nil {
		return nil, err
	}
	if actor.Type != req.ActorTokenType {
		return nil, decisionError(ErrInvalidRequest,
			"actor_token_type %q does not match the actual token type %q",
			req.ActorTokenType, actor.Type)
	}

	existingDepth := actDepth(subject.Act)
	if existingDepth+1 > e.maxDepth {
		return nil, decisionError(ErrAccessDenied,
			"delegation chain depth %d exceeds the maximum depth of %d",
			existingDepth+1, e.maxDepth)
	}

	return &Actor{
		Subject:  actor.Subject,
		ClientID: actor.ClientID,
		Act:      subject.Act,
	}, nil
}

// mayActAuthorizes reports whether the may_act claim names the requester.
// RFC 8693 section 4.4 defines may_act as a single actor object identified by
// sub and/or client_id.
func mayActAuthorizes(mayAct *Actor, requester Requester) bool {
	if mayAct == nil {
		return false
	}
	if requester.Subject != "" && mayAct.Subject == requester.Subject {
		return true
	}
	if requester.ClientID != "" && mayAct.ClientID == requester.ClientID {
		return true
	}
	return false
}

// actDepth counts the actors in an act chain, outermost actor first.
func actDepth(act *Actor) int {
	depth := 0
	for act != nil {
		depth++
		act = act.Act
	}
	return depth
}

func requesterKey(requester Requester) string {
	if requester.ClientID != "" {
		return requester.ClientID
	}
	return requester.Subject
}
