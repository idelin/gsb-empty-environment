package netsec

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	// ActionListBucket is the bucket-level action used by the listing engine.
	ActionListBucket = "s3:ListBucket"
	// ActionGetObject is the object-level action used to authorize keys.
	ActionGetObject = "s3:GetObject"

	defaultMaxKeys = 1000
	maxMaxKeys     = 1000
)

var (
	// ErrAccessDenied is returned when a policy check or secure listing is
	// denied.
	ErrAccessDenied = errors.New("netsec: access denied")
	// ErrInvalidPolicy is returned when a policy document cannot be compiled.
	ErrInvalidPolicy = errors.New("netsec: invalid policy")
	// ErrInvalidRequest is returned for malformed authorization or listing
	// requests.
	ErrInvalidRequest = errors.New("netsec: invalid request")
	// ErrInvalidContinuationToken is returned when a continuation token is
	// malformed, was minted for another scope, or belongs to an older policy.
	ErrInvalidContinuationToken = errors.New("netsec: invalid continuation token")
	// ErrSourceContract is returned when an ObjectSource violates the ordering
	// contract of ListObjects.
	ErrSourceContract = errors.New("netsec: object source contract violation")
)

// Effect is the effect of a policy statement.
type Effect string

const (
	EffectAllow Effect = "Allow"
	EffectDeny  Effect = "Deny"
)

// StringList accepts a JSON string or an array of strings. Policy documents in
// the wild use both forms for Action and Resource.
type StringList []string

// UnmarshalJSON implements json.Unmarshaler.
func (s *StringList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*s = nil
		return nil
	}
	if data[0] == '"' {
		var one string
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		*s = StringList{one}
		return nil
	}
	if data[0] == 't' || data[0] == 'f' || data[0] == 'n' ||
		(data[0] >= '0' && data[0] <= '9') || data[0] == '-' {
		var scalar any
		if err := json.Unmarshal(data, &scalar); err != nil {
			return err
		}
		switch value := scalar.(type) {
		case bool:
			*s = StringList{strconv.FormatBool(value)}
			return nil
		case float64:
			*s = StringList{strconv.FormatFloat(value, 'f', -1, 64)}
			return nil
		case nil:
			*s = nil
			return nil
		default:
			return fmt.Errorf("string list scalar has unsupported type %T", scalar)
		}
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		var scalars []any
		if scalarErr := json.Unmarshal(data, &scalars); scalarErr != nil {
			return fmt.Errorf("string list must be a string or array of strings: %w", err)
		}
		many = make([]string, 0, len(scalars))
		for _, scalar := range scalars {
			switch value := scalar.(type) {
			case string:
				many = append(many, value)
			case bool:
				many = append(many, strconv.FormatBool(value))
			case float64:
				many = append(many, strconv.FormatFloat(value, 'f', -1, 64))
			case nil:
				continue
			default:
				return fmt.Errorf("string list scalar has unsupported type %T", scalar)
			}
		}
	}
	*s = StringList(many)
	return nil
}

// MarshalJSON implements json.Marshaler. It emits an array for consistency.
func (s StringList) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string(s))
}

// Principal identifies one class and set of identities that a statement
// applies to.
//
// Supported Type values are "AWS" (authenticated account, the S3-compatible
// default), "User" (alias for an authenticated account), "Service",
// "Temporary" (explicitly temporary/session credentials), "Anonymous", "Any"
// (everyone, including anonymous), and "*" (authenticated wildcard). A Type of
// "Anonymous" must be declared explicitly; anonymous callers never match an
// authenticated wildcard. Temporary callers match explicit "Temporary"
// principals, or an authenticated principal with an explicit (non-wildcard)
// account ID. Wildcard authenticated grants do not silently flow to temporary
// credentials.
type Principal struct {
	Type string   `json:"type"`
	IDs  []string `json:"ids,omitempty"`
}

// PrincipalSet is an OR-set of principals.
type PrincipalSet []Principal

// UnmarshalJSON accepts both the S3 map form and the native array form.
//
// Examples:
//
//	{"AWS": ["tenant-a"]}
//	{"Anonymous": "*"}
//	{"Temporary": ["session-1"]}
//	["tenant-a", "tenant-b"]
//	"*"
//
// The map form is interpreted as a principal domain and one or more IDs. The
// native form is a JSON array of Principal objects.
func (ps *PrincipalSet) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*ps = nil
		return nil
	}

	switch data[0] {
	case '"':
		var one string
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		domain := principalDomainFromString(one)
		ids := []string{one}
		switch strings.ToLower(strings.TrimSpace(one)) {
		case "anonymous", "public", "any", "everyone",
			"temporary", "temp", "session", "service", "system":
			// A bare domain name means every identity in that domain.
			ids = []string{"*"}
		case "*":
			// Authenticated wildcard keeps its explicit ID.
		default:
			// A bare string is an authenticated account/user ID.
			domain = "AWS"
		}
		*ps = PrincipalSet{{Type: domain, IDs: ids}}
		return nil
	case '[':
		// A plain array is a convenience alias for authenticated IDs.
		var ids []string
		if err := json.Unmarshal(data, &ids); err == nil {
			if len(ids) == 0 {
				*ps = nil
				return nil
			}
			*ps = PrincipalSet{{Type: "AWS", IDs: ids}}
			return nil
		}
		var native []Principal
		if err := json.Unmarshal(data, &native); err != nil {
			return err
		}
		*ps = PrincipalSet(native)
		return nil
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
		rawType, hasType := object["Type"]
		if !hasType {
			rawType, hasType = object["type"]
		}
		if hasType {
			var typ string
			if err := json.Unmarshal(rawType, &typ); err != nil {
				return err
			}
			var ids []string
			if rawIDs, ok := object["IDs"]; ok {
				if err := json.Unmarshal(rawIDs, &ids); err != nil {
					return fmt.Errorf("principal IDs: %w", err)
				}
			} else if rawIDs, ok := object["ids"]; ok {
				if err := json.Unmarshal(rawIDs, &ids); err != nil {
					return fmt.Errorf("principal IDs: %w", err)
				}
			} else if rawID, ok := object["ID"]; ok {
				var one string
				if err := json.Unmarshal(rawID, &one); err != nil {
					return err
				}
				ids = []string{one}
			} else if rawID, ok := object["id"]; ok {
				var one string
				if err := json.Unmarshal(rawID, &one); err != nil {
					return err
				}
				ids = []string{one}
			}
			*ps = PrincipalSet{{Type: typ, IDs: ids}}
			return nil
		}

		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		*ps = nil
		for _, key := range keys {
			var ids StringList
			if err := ids.UnmarshalJSON(object[key]); err != nil {
				return fmt.Errorf("principal %q: %w", key, err)
			}
			domain := principalDomainFromString(key)
			if domain == "" {
				return fmt.Errorf("unknown principal domain %q", key)
			}
			*ps = append(*ps, Principal{Type: domain, IDs: []string(ids)})
		}
		return nil
	default:
		return fmt.Errorf("principal must be a string, array, or object")
	}
}

func principalDomainFromString(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "*":
		return "*"
	case "any", "everyone":
		return "Any"
	case "anonymous", "public":
		return "Anonymous"
	case "temporary", "temp", "session":
		return "Temporary"
	case "service", "system":
		return "Service"
	case "user", "canonicaluser", "aws", "federated":
		return "AWS"
	default:
		return strings.TrimSpace(s)
	}
}

// ConditionBlock is the standard AWS-style condition block:
//
//	{"StringEquals": {"s3:prefix": ["tenant-a/"]}}
type ConditionBlock map[string]map[string]StringList

// UnmarshalJSON implements json.Unmarshaler and accepts either a string or an
// array value for each condition key.
func (c *ConditionBlock) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*c = nil
		return nil
	}
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(ConditionBlock, len(raw))
	for operator, entries := range raw {
		if entries == nil {
			out[operator] = nil
			continue
		}
		set := make(map[string]StringList, len(entries))
		for key, value := range entries {
			var values StringList
			if err := values.UnmarshalJSON(value); err != nil {
				return fmt.Errorf("condition %q key %q: %w", operator, key, err)
			}
			set[key] = values
		}
		out[operator] = set
	}
	*c = out
	return nil
}

// ConditionValues contains request/context values used by conditions.
type ConditionValues map[string][]string

// Clone returns an independent copy.
func (v ConditionValues) Clone() ConditionValues {
	if v == nil {
		return nil
	}
	out := make(ConditionValues, len(v))
	for key, values := range v {
		out[key] = append([]string(nil), values...)
	}
	return out
}

// Set replaces a condition key.
func (v ConditionValues) Set(key string, values ...string) {
	v[key] = append([]string(nil), values...)
}

// Add appends condition values.
func (v ConditionValues) Add(key string, values ...string) {
	v[key] = append(v[key], values...)
}

// Statement is one rule in a bucket policy.
//
// Action/Actions, Resource/Resources, and Condition/Conditions aliases are
// accepted for integration convenience. At least one action form, one resource
// form, and one principal must be supplied.
type Statement struct {
	Sid         string         `json:"Sid,omitempty"`
	Effect      Effect         `json:"Effect"`
	Action      StringList     `json:"Action,omitempty"`
	Actions     StringList     `json:"Actions,omitempty"`
	NotAction   StringList     `json:"NotAction,omitempty"`
	Resource    StringList     `json:"Resource,omitempty"`
	Resources   StringList     `json:"Resources,omitempty"`
	NotResource StringList     `json:"NotResource,omitempty"`
	Principal   PrincipalSet   `json:"Principal,omitempty"`
	Principals  PrincipalSet   `json:"Principals,omitempty"`
	Condition   ConditionBlock `json:"Condition,omitempty"`
	Conditions  ConditionBlock `json:"Conditions,omitempty"`
}

// Policy is a compiled bucket policy. Use NewPolicy or (*Policy).Prepare
// before evaluating it. The exported fields are safe for JSON decoding and
// inspection; evaluation uses an immutable compiled snapshot.
type Policy struct {
	Version    string      `json:"Version,omitempty"`
	ID         string      `json:"Id,omitempty"`
	Statements []Statement `json:"Statement"`

	mu       sync.Mutex
	compiled *compiledPolicy
}

// UnmarshalJSON accepts both the S3 form where Statement is an array and the
// common single-statement form where it is one object.
func (p *Policy) UnmarshalJSON(data []byte) error {
	var raw struct {
		Version   string          `json:"Version"`
		ID        string          `json:"Id"`
		Statement json.RawMessage `json:"Statement"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Version = raw.Version
	p.ID = raw.ID
	p.Statements = nil
	p.mu.Lock()
	p.compiled = nil
	p.mu.Unlock()
	statement := bytes.TrimSpace(raw.Statement)
	if len(statement) == 0 || bytes.Equal(statement, []byte("null")) {
		return nil
	}
	switch statement[0] {
	case '[':
		if err := json.Unmarshal(statement, &p.Statements); err != nil {
			return err
		}
	case '{':
		var one Statement
		if err := json.Unmarshal(statement, &one); err != nil {
			return err
		}
		p.Statements = []Statement{one}
	default:
		return fmt.Errorf("Statement must be an object or array")
	}
	return nil
}

type compiledPolicy struct {
	digest     string
	tokenKey   [32]byte
	statements []compiledStatement
}

type compiledStatement struct {
	source       *Statement
	sid          string
	effect       Effect
	actions      []string
	notActions   []string
	resources    []resourcePattern
	notResources []resourcePattern
	principals   PrincipalSet
	conditions   ConditionBlock
}

// NewPolicy decodes and compiles a JSON policy document.
func NewPolicy(document []byte) (*Policy, error) {
	var policy Policy
	decoder := json.NewDecoder(bytes.NewReader(document))
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, fmt.Errorf("%w: trailing JSON document", ErrInvalidPolicy)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data: %v", ErrInvalidPolicy, err)
	}
	if err := policy.Prepare(); err != nil {
		return nil, err
	}
	return &policy, nil
}

// ParsePolicy is an alias for NewPolicy.
func ParsePolicy(document []byte) (*Policy, error) {
	return NewPolicy(document)
}

// CompilePolicy compiles an already-decoded policy in place.
func CompilePolicy(policy *Policy) error {
	if policy == nil {
		return fmt.Errorf("%w: nil policy", ErrInvalidPolicy)
	}
	return policy.Prepare()
}

// Prepare compiles the policy and publishes the immutable evaluation snapshot.
// It is safe to call more than once; re-preparing replaces the snapshot.
func (p *Policy) Prepare() error {
	if p == nil {
		return fmt.Errorf("%w: nil policy", ErrInvalidPolicy)
	}
	cp, err := compilePolicy(p)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.compiled = cp
	p.mu.Unlock()
	return nil
}

// Fingerprint returns the content fingerprint of the compiled policy.
func (p *Policy) Fingerprint() string {
	cp, err := p.snapshot()
	if err != nil || cp == nil {
		return ""
	}
	return cp.digest
}

// VersionID is an alias for Fingerprint.
func (p *Policy) VersionID() string {
	return p.Fingerprint()
}

func (p *Policy) snapshot() (*compiledPolicy, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil policy", ErrInvalidPolicy)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.compiled != nil {
		return p.compiled, nil
	}
	cp, err := compilePolicy(p)
	if err != nil {
		return nil, err
	}
	p.compiled = cp
	return cp, nil
}

func compilePolicy(p *Policy) (*compiledPolicy, error) {
	canonical, err := policyCanonicalJSON(p)
	if err != nil {
		return nil, err
	}
	digestBytes := sha256.Sum256(canonical)
	cp := &compiledPolicy{
		digest: hex.EncodeToString(digestBytes[:]),
	}
	if _, err := rand.Read(cp.tokenKey[:]); err != nil {
		return nil, fmt.Errorf("%w: generate token key: %v", ErrInvalidPolicy, err)
	}

	for index := range p.Statements {
		statement := &p.Statements[index]
		cs, err := compileStatement(statement, index)
		if err != nil {
			return nil, err
		}
		cp.statements = append(cp.statements, cs)
	}
	return cp, nil
}

func policyCanonicalJSON(p *Policy) ([]byte, error) {
	type canonicalPolicy struct {
		Version    string      `json:"Version,omitempty"`
		ID         string      `json:"Id,omitempty"`
		Statements []Statement `json:"Statement"`
	}
	return json.Marshal(canonicalPolicy{
		Version:    p.Version,
		ID:         p.ID,
		Statements: p.Statements,
	})
}

func compileStatement(statement *Statement, index int) (compiledStatement, error) {
	var out compiledStatement
	out.source = statement
	out.sid = statement.Sid
	if out.sid == "" {
		out.sid = fmt.Sprintf("statement-%d", index)
	}

	effect, err := normalizeEffect(statement.Effect)
	if err != nil {
		return out, fmt.Errorf("%w: statement %q: %v", ErrInvalidPolicy, out.sid, err)
	}
	out.effect = effect

	allowActions := append(append([]string(nil), statement.Action...), statement.Actions...)
	if len(allowActions) == 0 && len(statement.NotAction) == 0 {
		return out, fmt.Errorf("%w: statement %q has no Action or NotAction", ErrInvalidPolicy, out.sid)
	}
	for _, action := range allowActions {
		action = strings.TrimSpace(action)
		if action == "" {
			return out, fmt.Errorf("%w: statement %q has an empty action", ErrInvalidPolicy, out.sid)
		}
		out.actions = append(out.actions, action)
	}
	for _, action := range statement.NotAction {
		action = strings.TrimSpace(action)
		if action == "" {
			return out, fmt.Errorf("%w: statement %q has an empty NotAction", ErrInvalidPolicy, out.sid)
		}
		out.notActions = append(out.notActions, action)
	}

	resources := append(append([]string(nil), statement.Resource...), statement.Resources...)
	if len(resources) == 0 && len(statement.NotResource) == 0 {
		return out, fmt.Errorf("%w: statement %q has no Resource or NotResource", ErrInvalidPolicy, out.sid)
	}
	for _, raw := range resources {
		rp, err := parseResourcePattern(raw)
		if err != nil {
			return out, fmt.Errorf("%w: statement %q: %v", ErrInvalidPolicy, out.sid, err)
		}
		out.resources = append(out.resources, rp)
	}
	for _, raw := range statement.NotResource {
		rp, err := parseResourcePattern(raw)
		if err != nil {
			return out, fmt.Errorf("%w: statement %q: %v", ErrInvalidPolicy, out.sid, err)
		}
		out.notResources = append(out.notResources, rp)
	}

	principals := append(append(PrincipalSet(nil), statement.Principal...), statement.Principals...)
	if len(principals) == 0 {
		return out, fmt.Errorf("%w: statement %q has no Principal", ErrInvalidPolicy, out.sid)
	}
	for index := range principals {
		principal := &principals[index]
		principal.IDs = append([]string(nil), principal.IDs...)
		switch principalDomainFromString(principal.Type) {
		case "AWS", "User", "*", "Any", "Anonymous", "Temporary", "Service":
			// Supported principal classes.
		default:
			return out, fmt.Errorf(
				"%w: statement %q has unsupported principal type %q",
				ErrInvalidPolicy, out.sid, principal.Type,
			)
		}
		if len(principal.IDs) == 0 {
			return out, fmt.Errorf(
				"%w: statement %q principal %q has no IDs (use \"*\" consistently)",
				ErrInvalidPolicy, out.sid, principal.Type,
			)
		}
	}
	out.principals = principals

	conditions := mergeConditions(statement.Condition, statement.Conditions)
	if err := validateConditions(conditions); err != nil {
		return out, fmt.Errorf("%w: statement %q: %v", ErrInvalidPolicy, out.sid, err)
	}
	out.conditions = conditions
	return out, nil
}

func normalizeEffect(effect Effect) (Effect, error) {
	switch strings.ToLower(strings.TrimSpace(string(effect))) {
	case "allow":
		return EffectAllow, nil
	case "deny":
		return EffectDeny, nil
	default:
		return "", fmt.Errorf("effect must be Allow or Deny")
	}
}

func mergeConditions(a, b ConditionBlock) ConditionBlock {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(ConditionBlock, len(a)+len(b))
	for operator, entries := range a {
		if out[operator] == nil {
			out[operator] = make(map[string]StringList, len(entries))
		}
		for key, values := range entries {
			out[operator][key] = append(StringList(nil), values...)
		}
	}
	for operator, entries := range b {
		if out[operator] == nil {
			out[operator] = make(map[string]StringList, len(entries))
		}
		for key, values := range entries {
			out[operator][key] = append(StringList(nil), values...)
		}
	}
	return out
}

func validateConditions(block ConditionBlock) error {
	for rawOperator, entries := range block {
		operator, quantifier, ifExists, err := parseConditionOperator(rawOperator)
		if err != nil {
			return err
		}
		_ = quantifier
		_ = ifExists
		if len(entries) == 0 {
			return fmt.Errorf("condition operator %q has no keys", rawOperator)
		}
		for key, values := range entries {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("condition operator %q has an empty key", rawOperator)
			}
			if operator == "Null" {
				for _, value := range values {
					if _, err := strconv.ParseBool(strings.TrimSpace(value)); err != nil {
						return fmt.Errorf("Null condition %q value %q is not a bool", key, value)
					}
				}
				continue
			}
			if len(values) == 0 {
				return fmt.Errorf("condition operator %q key %q has no values", rawOperator, key)
			}
			for _, value := range values {
				if _, err := normalizeConditionValue(operator, value); err != nil {
					return fmt.Errorf("condition operator %q key %q: %w", rawOperator, key, err)
				}
			}
		}
	}
	return nil
}

func normalizeConditionValue(operator string, value string) (string, error) {
	switch operator {
	case "NumericEquals", "NumericNotEquals", "NumericLessThan",
		"NumericLessThanEquals", "NumericGreaterThan", "NumericGreaterThanEquals":
		v := strings.TrimSpace(value)
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return "", fmt.Errorf("%q is not numeric: %w", value, err)
		}
		return v, nil
	case "Bool":
		v := strings.TrimSpace(value)
		if _, err := strconv.ParseBool(v); err != nil {
			return "", fmt.Errorf("%q is not a bool: %w", value, err)
		}
		return v, nil
	case "IpAddress", "NotIpAddress":
		if _, _, err := parseCIDROrIP(strings.TrimSpace(value)); err != nil {
			return "", err
		}
		return strings.TrimSpace(value), nil
	default:
		return value, nil
	}
}
