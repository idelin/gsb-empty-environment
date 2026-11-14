package netsec

import (
	"context"
	"time"
)

// Request is a directory user search performed by the gateway.
type Request struct {
	// Server is the initial LDAP URL.
	Server string
	// BindDN and Password are the gateway's credentials. They are sent only
	// when policy permits credential forwarding.
	BindDN   string
	Password string

	// BaseDN, Scope, Filter, and Attributes are inherited by referrals that
	// omit the corresponding LDAP URL fields.
	BaseDN     string
	Scope      Scope
	Filter     string
	Attributes []string
	SizeLimit  int
	TimeLimit  time.Duration

	// Source identifies the original requesting client. It is copied into
	// every audit event.
	Source string
}

// Hop describes one referral target that was contacted.
type Hop struct {
	Target              Target
	BoundDN             string
	AuthMechanism       AuthMechanism
	CredentialForwarded bool
	Entries             int
	References          int
	Depth               int
}

// UnfollowedReferral describes a referral that was not contacted.
type UnfollowedReferral struct {
	Raw      string
	Decision Decision
	Err      error
}

// FollowResult is the union of entries and referrals encountered along a
// referral chain. Policies that refuse a referral leave it in Unfollowed
// rather than failing the entire search.
type FollowResult struct {
	Entries    []Entry
	Hops       []Hop
	Unfollowed []UnfollowedReferral
}

// Follow binds to the initial server, performs the search, and follows
// permitted referrals up to the configured depth. Passwords are never sent to
// a target whose Decision is FollowAnonymous.
func (f *Follower) Follow(ctx context.Context, req Request) (FollowResult, error) {
	var result FollowResult
	initial, err := ParseReferral(req.Server)
	if err != nil {
		return result, &FollowError{Op: "parse initial server", Referral: redactReferralForLog(req.Server), Err: err}
	}
	state := EvalState{Depth: 0}
	decision := f.Evaluate(initial, state)
	f.record(ctx, eventForDecision(req.Source, "", 0, decision))
	if !decision.Followable() {
		return result, &FollowError{
			Op:       "evaluate initial server",
			Referral: req.Server,
			Decision: decision,
			Err:      decision.Err,
		}
	}

	initialSearch := SearchRequest{
		BaseDN:     req.BaseDN,
		Scope:      req.Scope,
		Filter:     req.Filter,
		Attributes: req.Attributes,
		SizeLimit:  req.SizeLimit,
		TimeLimit:  req.TimeLimit,
	}
	if initialSearch.BaseDN == "" {
		initialSearch.BaseDN = initial.DN
	}
	if initial.HasFilter {
		initialSearch.Filter = initial.Filter
	}
	if initial.HasScope {
		initialSearch.Scope = initial.Scope
	}
	if len(initial.Attributes) > 0 {
		initialSearch.Attributes = initial.Attributes
	}

	return f.follow(ctx, req, decision, initialSearch, 0, map[string]struct{}{}, &result)
}

func (f *Follower) follow(
	ctx context.Context,
	req Request,
	decision Decision,
	search SearchRequest,
	depth int,
	path map[string]struct{},
	result *FollowResult,
) (FollowResult, error) {
	conn, err := f.dialer.Dial(ctx, decision.Target)
	if err != nil {
		return *result, &FollowError{
			Op:       "dial referral",
			Referral: decision.Target.String(),
			Decision: decision,
			Err:      err,
		}
	}
	defer conn.Close()

	boundDN := req.BindDN
	mechanism := AuthSimple
	forwarded := decision.Action == FollowWithCredentials && !(req.BindDN == "" && req.Password == "")
	if !forwarded {
		// Anonymous simple bind: send an empty DN and an empty password. The
		// caller's password never leaves this process on this path.
		boundDN = ""
		mechanism = AuthAnonymousSimple
	}
	password := ""
	if forwarded {
		password = req.Password
	}

	_, bindErr := conn.Bind(ctx, boundDN, password)
	bindEvent := AuditEvent{
		Time:                time.Now(),
		Kind:                "bind",
		Source:              req.Source,
		Target:              decision.Target.String(),
		Host:                decision.Target.Host,
		Port:                decision.Target.Port,
		Scheme:              decision.Target.Scheme,
		Transport:           decision.Target.Mode,
		DN:                  decision.Target.DN,
		BoundDN:             boundDN,
		AuthMechanism:       mechanism,
		CredentialForwarded: forwarded,
		Followable:          true,
		Depth:               depth,
		Forest:              decision.TargetForest,
		CrossForest:         decision.CrossForest,
	}
	if bindErr != nil {
		bindEvent.Error = bindErr.Error()
	}
	f.record(ctx, bindEvent)
	if bindErr != nil {
		return *result, &FollowError{
			Op:       "bind referral",
			Referral: decision.Target.String(),
			Decision: decision,
			Err:      bindErr,
		}
	}

	found, err := conn.Search(ctx, search)
	if err != nil {
		return *result, &FollowError{
			Op:       "search referral",
			Referral: decision.Target.String(),
			Decision: decision,
			Err:      err,
		}
	}

	result.Entries = append(result.Entries, found.Entries...)
	result.Hops = append(result.Hops, Hop{
		Target:              decision.Target,
		BoundDN:             boundDN,
		AuthMechanism:       mechanism,
		CredentialForwarded: forwarded,
		Entries:             len(found.Entries),
		References:          len(found.References),
		Depth:               depth,
	})

	childPath := make(map[string]struct{}, len(path)+1)
	for key := range path {
		childPath[key] = struct{}{}
	}
	childPath[decision.Target.Key()] = struct{}{}

	for _, raw := range found.References {
		ref, parseErr := ParseReferral(raw)
		if parseErr != nil {
			f.record(ctx, AuditEvent{
				Time:       time.Now(),
				Kind:       "referral-decision",
				Source:     req.Source,
				Target:     redactReferralForLog(raw),
				Followable: false,
				Reason:     parseErr.Error(),
				Depth:      depth + 1,
			})
			result.Unfollowed = append(result.Unfollowed, UnfollowedReferral{
				Raw: raw,
				Err: parseErr,
			})
			continue
		}

		childDecision := f.Evaluate(ref, EvalState{
			From:    &decision.Target,
			Depth:   depth + 1,
			Visited: childPath,
		})
		f.record(ctx, eventForDecision(req.Source, decision.Target.String(), depth+1, childDecision))
		if !childDecision.Followable() {
			result.Unfollowed = append(result.Unfollowed, UnfollowedReferral{
				Raw:      raw,
				Decision: childDecision,
				Err:      childDecision.Err,
			})
			continue
		}

		childSearch := inheritedSearch(search, ref)
		_, err := f.follow(ctx, req, childDecision, childSearch, depth+1, childPath, result)
		if err != nil {
			return *result, err
		}
	}
	return *result, nil
}

func inheritedSearch(parent SearchRequest, ref Referral) SearchRequest {
	child := parent
	if ref.DN != "" {
		child.BaseDN = ref.DN
	}
	if ref.HasScope {
		child.Scope = ref.Scope
	}
	if ref.HasFilter {
		child.Filter = ref.Filter
	}
	if len(ref.Attributes) > 0 {
		child.Attributes = ref.Attributes
	}
	return child
}

// EvaluateReferral parses raw and applies policy without contacting it.
func (f *Follower) EvaluateReferral(raw string, state EvalState) (Decision, error) {
	ref, err := ParseReferral(raw)
	if err != nil {
		return Decision{Action: DoNotFollow, Err: err}, err
	}
	return f.Evaluate(ref, state), nil
}
