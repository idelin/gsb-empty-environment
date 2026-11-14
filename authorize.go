package netsec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type actionKind int

const (
	actionKindObject actionKind = iota
	actionKindBucket
)

// Request is one authorization decision request.
//
// List requests use ActionListBucket and set Prefix/Delimiter/MaxKeys. Object
// requests use ActionGetObject and set Key. Prefix and Delimiter are also
// carried into per-object checks made by List so a statement can bind a read
// grant to a listing scope.
type Request struct {
	Action            string
	Bucket            string
	Key               string
	Prefix            string
	Delimiter         string
	MaxKeys           int
	ContinuationToken string
	Identity          Identity
	Conditions        ConditionValues
}

// Decision is the result of a policy evaluation.
type Decision struct {
	Allowed   bool
	Effect    Effect
	Sid       string
	Reason    string
	Statement *Statement
	Err       error
}

// Evaluate is an alias for Authorize.
func (p *Policy) Evaluate(req Request) Decision {
	return p.Authorize(req)
}

// Allowed is a boolean convenience wrapper around Authorize.
func (p *Policy) Allowed(req Request) bool {
	return p.Authorize(req).Allowed
}

// Authorize evaluates the policy against req. Explicit Deny statements win
// over Allow statements regardless of order. If no statement matches, access is
// implicitly denied.
func (p *Policy) Authorize(req Request) Decision {
	cp, err := p.snapshot()
	if err != nil {
		return Decision{
			Allowed: false,
			Effect:  EffectDeny,
			Reason:  "policy is not compiled",
			Err:     err,
		}
	}
	return cp.authorize(req, nowFunc())
}

func (cp *compiledPolicy) authorize(req Request, now time.Time) Decision {
	if cp == nil {
		return Decision{Effect: EffectDeny, Reason: "nil compiled policy", Err: ErrInvalidPolicy}
	}
	if strings.TrimSpace(req.Action) == "" {
		return Decision{Effect: EffectDeny, Reason: "missing action", Err: fmt.Errorf("%w: missing action", ErrInvalidRequest)}
	}
	if strings.TrimSpace(req.Bucket) == "" {
		return Decision{Effect: EffectDeny, Reason: "missing bucket", Err: fmt.Errorf("%w: missing bucket", ErrInvalidRequest)}
	}
	if req.Identity.expired(now) {
		return Decision{
			Effect: EffectDeny,
			Reason: "temporary credential is expired",
			Err:    fmt.Errorf("%w: temporary credential is expired", ErrAccessDenied),
		}
	}

	action := normalizeAction(req.Action)
	kind := classifyAction(action)
	if kind == actionKindObject && req.Key == "" {
		return Decision{
			Effect: EffectDeny,
			Reason: "missing object key",
			Err:    fmt.Errorf("%w: missing object key", ErrInvalidRequest),
		}
	}
	if kind == actionKindObject && unsafeObjectKey(req.Key) {
		return Decision{
			Effect: EffectDeny,
			Reason: "ambiguous object key path",
			Err:    ErrAccessDenied,
		}
	}
	values := req.conditionValues()

	var allowed *compiledStatement
	for index := range cp.statements {
		statement := &cp.statements[index]
		if !statement.appliesToAction(action) {
			continue
		}
		if !statement.principals.matches(req.Identity) {
			continue
		}
		if !statement.resourceMatches(kind, req.Bucket, req.Key) {
			continue
		}
		ok, err := evaluateConditions(statement.conditions, values)
		if err != nil {
			return Decision{
				Effect:    EffectDeny,
				Sid:       statement.sid,
				Reason:    "condition evaluation failed",
				Statement: statement.source,
				Err:       fmt.Errorf("%w: statement %q: %v", ErrInvalidPolicy, statement.sid, err),
			}
		}
		if !ok {
			continue
		}
		if statement.effect == EffectDeny {
			return Decision{
				Effect:    EffectDeny,
				Sid:       statement.sid,
				Reason:    "explicit deny",
				Statement: statement.source,
				Err:       ErrAccessDenied,
			}
		}
		if allowed == nil {
			allowed = statement
		}
	}
	if allowed != nil {
		return Decision{
			Allowed:   true,
			Effect:    EffectAllow,
			Sid:       allowed.sid,
			Reason:    "allowed",
			Statement: allowed.source,
		}
	}
	return Decision{
		Effect: EffectDeny,
		Reason: "no matching allow statement",
		Err:    ErrAccessDenied,
	}
}

func (cs *compiledStatement) appliesToAction(action string) bool {
	for _, pattern := range cs.notActions {
		if actionPatternMatches(pattern, action) {
			return false
		}
	}
	if len(cs.actions) == 0 {
		return len(cs.notActions) > 0
	}
	for _, pattern := range cs.actions {
		if actionPatternMatches(pattern, action) {
			return true
		}
	}
	return false
}

func (cs *compiledStatement) resourceMatches(kind actionKind, bucket, key string) bool {
	if len(cs.resources) == 0 {
		if len(cs.notResources) == 0 {
			return false
		}
	} else {
		matched := false
		for _, pattern := range cs.resources {
			if pattern.matches(kind, bucket, key) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pattern := range cs.notResources {
		if pattern.matches(kind, bucket, key) {
			return false
		}
	}
	return true
}

func normalizeAction(action string) string {
	value := strings.TrimSpace(action)
	lower := strings.ToLower(value)
	switch lower {
	case "list", "listbucket", "listobjects", "s3:list", "s3:listbucket", "s3:listobjects":
		return ActionListBucket
	case "get", "read", "getobject", "s3:get", "s3:getobject":
		return ActionGetObject
	case "put", "write", "putobject", "s3:put", "s3:putobject":
		return "s3:PutObject"
	case "delete", "deleteobject", "s3:delete", "s3:deleteobject":
		return "s3:DeleteObject"
	}
	if strings.HasPrefix(lower, "s3:") {
		return "s3:" + value[len("s3:"):]
	}
	return value
}

func normalizeActionPattern(pattern string) string {
	value := strings.TrimSpace(pattern)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch lower {
	case "*", "s3:*":
		return "s3:*"
	}
	if !strings.Contains(value, ":") {
		return "s3:" + value
	}
	return normalizeAction(value)
}

func actionPatternMatches(pattern, action string) bool {
	return globMatch(
		strings.ToLower(normalizeActionPattern(pattern)),
		strings.ToLower(normalizeAction(action)),
	)
}

func classifyAction(action string) actionKind {
	switch strings.ToLower(normalizeAction(action)) {
	case "s3:listbucket", "s3:listbucketversions", "s3:listbucketmultipartuploads",
		"s3:listmultipartuploadparts", "s3:getbucketlocation", "s3:headbucket",
		"s3:getbucketpolicy", "s3:putbucketpolicy", "s3:deletebucketpolicy",
		"s3:getbucketacl", "s3:putbucketacl", "s3:getbucketcors", "s3:putbucketcors",
		"s3:deletebucketcors", "s3:getbucketwebsite", "s3:putbucketwebsite",
		"s3:deletebucketwebsite", "s3:getbucketlogging", "s3:putbucketlogging",
		"s3:getbucketnotification", "s3:putbucketnotification",
		"s3:getbucketversioning", "s3:putbucketversioning",
		"s3:getbuckettagging", "s3:putbuckettagging", "s3:deletebuckettagging",
		"s3:getbucketobjectlockconfiguration", "s3:putbucketobjectlockconfiguration",
		"s3:getbucketencription", "s3:putbucketencription",
		"s3:deletebucket", "s3:createbucket":
		return actionKindBucket
	default:
		return actionKindObject
	}
}

func (req Request) conditionValues() ConditionValues {
	out := req.Conditions.Clone()
	if out == nil {
		out = make(ConditionValues)
	}
	identity := req.Identity
	identityType := identity.normalizedType()
	for key, values := range identity.Attributes {
		out.Set(key, values...)
	}

	action := normalizeAction(req.Action)
	out.Set("s3:action", action)
	out.Set("s3:bucket", req.Bucket)
	out.Set("s3:key", req.Key)
	out.Set("s3:prefix", req.Prefix)
	out.Set("s3:delimiter", req.Delimiter)
	out.Set("s3:max-keys", strconv.Itoa(req.MaxKeys))
	out.Set("s3:continuation-token", req.ContinuationToken)
	out.Set("aws:PrincipalType", string(identityType))
	out.Set("aws:PrincipalIsTemporary", strconv.FormatBool(identityType == IdentityTemporary))
	out.Set("aws:PrincipalTenant", identity.Tenant)
	out.Set("aws:PrincipalSession", identity.SessionID)
	account := identity.ID
	if account == "" {
		account = identity.Tenant
	}
	out.Set("aws:PrincipalAccount", account)
	switch identityType {
	case IdentityAnonymous:
		out.Set("s3:authType", "Anonymous")
	case IdentityTemporary:
		out.Set("s3:authType", "Temporary")
	default:
		out.Set("s3:authType", "Authenticated")
	}
	return out
}

// AccessDeniedError describes a denied request.
type AccessDeniedError struct {
	Action string
	Bucket string
	Key    string
	Sid    string
	Reason string
	Cause  error
}

func (e *AccessDeniedError) Error() string {
	name := e.Action
	if name == "" {
		name = "request"
	}
	if e.Bucket != "" {
		name += " on " + e.Bucket
	}
	if e.Key != "" {
		name += "/" + e.Key
	}
	reason := e.Reason
	if reason == "" {
		reason = "access denied"
	}
	if e.Sid != "" {
		return fmt.Sprintf("%s: %s (statement %s)", name, reason, e.Sid)
	}
	return fmt.Sprintf("%s: %s", name, reason)
}

// Unwrap exposes ErrAccessDenied for errors.Is.
func (e *AccessDeniedError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrAccessDenied
}

// Check returns nil when req is allowed, otherwise an *AccessDeniedError.
func (p *Policy) Check(req Request) error {
	decision := p.Authorize(req)
	if decision.Allowed {
		return nil
	}
	cause := decision.Err
	if cause == nil {
		cause = ErrAccessDenied
	}
	return &AccessDeniedError{
		Action: req.Action,
		Bucket: req.Bucket,
		Key:    req.Key,
		Sid:    decision.Sid,
		Reason: decision.Reason,
		Cause:  cause,
	}
}

// AllowedKeys filters keys through the object-read policy using the same
// identity and condition context as req. Input order is preserved.
func (p *Policy) AllowedKeys(req Request, keys []string) []string {
	cp, err := p.snapshot()
	if err != nil {
		return nil
	}
	return cp.allowedKeys(req, keys)
}

// FilterObjects filters objects through the object-read policy. Input order is
// preserved.
func (p *Policy) FilterObjects(req Request, objects []Object) []Object {
	cp, err := p.snapshot()
	if err != nil {
		return nil
	}
	return cp.filterObjects(req, objects)
}

func (cp *compiledPolicy) allowedKeys(req Request, keys []string) []string {
	base := req
	base.Action = ActionGetObject
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		candidate := base
		candidate.Key = key
		if cp.authorize(candidate, nowFunc()).Allowed {
			out = append(out, key)
		}
	}
	return out
}

func (cp *compiledPolicy) filterObjects(req Request, objects []Object) []Object {
	base := req
	base.Action = ActionGetObject
	out := make([]Object, 0, len(objects))
	for _, object := range objects {
		candidate := base
		candidate.Key = object.Key
		if cp.authorize(candidate, nowFunc()).Allowed {
			out = append(out, object)
		}
	}
	return out
}
