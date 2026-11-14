package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrUnauthorized is returned when any active alias at a response path is
	// denied. The whole response path, not only the failing alias, is rejected.
	ErrUnauthorized = errors.New("netsec: unauthorized")
	// ErrResponseConflict indicates that aliases sharing a response key cannot
	// be merged safely (different fields, arguments, identity, or grants).
	ErrResponseConflict = errors.New("netsec: response path conflict")
	// ErrInvalidQuery is returned for malformed or unsupported documents.
	ErrInvalidQuery = errors.New("netsec: invalid query")
	// ErrInvalidVariables is returned when variable values cannot be coerced or
	// would make authorization ambiguous.
	ErrInvalidVariables = errors.New("netsec: invalid variables")
	// ErrPolicy is returned when a policy fails.
	ErrPolicy = errors.New("netsec: policy error")
)

// Path is an exact GraphQL response path. Each element is a response key (an
// alias when present). It is never treated as a JavaScript object property,
// which is important for aliases such as __proto__ and constructor.
type Path []string

// Clone returns a detached path.
func (p Path) Clone() Path {
	return append(Path(nil), p...)
}

// Key returns an unambiguous map key. Length prefixes prevent alias text from
// colliding with separators or with another nested path.
func (p Path) Key() string {
	var builder strings.Builder
	for _, segment := range p {
		fmt.Fprintf(&builder, "%d:%s", len(segment), segment)
	}
	return builder.String()
}

// String returns a readable JSONPath-like representation.
func (p Path) String() string {
	if len(p) == 0 {
		return "$"
	}
	var builder strings.Builder
	builder.WriteByte('$')
	for _, segment := range p {
		if isSimplePathSegment(segment) {
			builder.WriteByte('.')
			builder.WriteString(segment)
			continue
		}
		builder.WriteString("[")
		builder.WriteString(fmt.Sprintf("%q", segment))
		builder.WriteString("]")
	}
	return builder.String()
}

func isSimplePathSegment(segment string) bool {
	if segment == "" {
		return false
	}
	switch segment {
	case "__proto__", "prototype", "constructor", "toString", "__defineGetter__":
		return false
	}
	for i, r := range segment {
		if i == 0 {
			if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
				return false
			}
			continue
		}
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func joinPath(parent Path, key string) Path {
	result := make(Path, len(parent)+1)
	copy(result, parent)
	result[len(parent)] = key
	return result
}

// Identity identifies the caller whose field access is being authorized.
type Identity struct {
	TenantID   string            `json:"tenant_id,omitempty"`
	SubjectID  string            `json:"subject_id,omitempty"`
	Roles      []string          `json:"roles,omitempty"`
	Scopes     []string          `json:"scopes,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Key returns a stable identity fingerprint. It is used when deciding whether
// two aliases at the same response path can be merged.
func (i Identity) Key() string {
	return canonicalJSON(i)
}

// Decision is the result of field-level authorization. Scope, Grant,
// PolicyVersion, Subject, MaxPageSize, and Fingerprint are part of the
// authorization result and must match before duplicate aliases are merged.
// Reason is diagnostic only.
type Decision struct {
	Allowed       bool
	Scope         string
	Grant         string
	PolicyVersion string
	Subject       string
	Fingerprint   string
	MaxPageSize   int64
	Reason        string
}

// Allow returns an allowed decision with an optional scope.
func Allow(scope ...string) Decision {
	decision := Decision{Allowed: true}
	if len(scope) > 0 {
		decision.Scope = scope[0]
	}
	return decision
}

// Deny returns a denied decision with an optional reason.
func Deny(reason ...string) Decision {
	decision := Decision{Allowed: false}
	if len(reason) > 0 {
		decision.Reason = reason[0]
	}
	return decision
}

// Equal compares the identity-bearing parts of an authorization result. It
// deliberately ignores Reason.
func (d Decision) Equal(other Decision) bool {
	return d.Allowed == other.Allowed &&
		d.Scope == other.Scope &&
		d.Grant == other.Grant &&
		d.PolicyVersion == other.PolicyVersion &&
		d.Subject == other.Subject &&
		d.Fingerprint == other.Fingerprint &&
		d.MaxPageSize == other.MaxPageSize
}

func (d Decision) key() string {
	type decisionKey struct {
		Allowed       bool
		Scope         string
		Grant         string
		PolicyVersion string
		Subject       string
		Fingerprint   string
		MaxPageSize   int64
	}
	return canonicalJSON(decisionKey{
		Allowed:       d.Allowed,
		Scope:         d.Scope,
		Grant:         d.Grant,
		PolicyVersion: d.PolicyVersion,
		Subject:       d.Subject,
		Fingerprint:   d.Fingerprint,
		MaxPageSize:   d.MaxPageSize,
	})
}

// DirectiveState is the evaluated state of one directive occurrence.
type DirectiveState struct {
	Name       string
	Origin     string
	Position   Position
	Evaluated  bool
	Recognized bool
	Value      any
	Active     bool
}

// Pagination contains commonly used GraphQL pagination arguments. Values are
// kept separately from the raw argument map so a policy can cap page sizes
// without reparsing variables.
type Pagination struct {
	First       int64
	HasFirst    bool
	Last        int64
	HasLast     bool
	Offset      int64
	HasOffset   bool
	Limit       int64
	HasLimit    bool
	Page        int64
	HasPage     bool
	PageSize    int64
	HasPageSize bool
	After       string
	HasAfter    bool
	Before      string
	HasBefore   bool
}

// RequestedSize returns the largest explicit item-count bound requested by
// pagination arguments. Offset and page are not item counts and do not widen
// the number of returned fields.
func (p Pagination) RequestedSize() (int64, bool) {
	var size int64
	var found bool
	for _, candidate := range []struct {
		value int64
		set   bool
	}{
		{p.First, p.HasFirst},
		{p.Last, p.HasLast},
		{p.Limit, p.HasLimit},
		{p.PageSize, p.HasPageSize},
	} {
		if candidate.set && (!found || candidate.value > size) {
			size = candidate.value
			found = true
		}
	}
	return size, found
}

// FieldRequest is supplied to the policy for every active selection occurrence.
type FieldRequest struct {
	Identity    Identity
	IdentityKey string

	OperationName string
	OperationType OperationType
	ParentType    string
	FieldName     string
	Alias         string
	ResponseKey   string

	ResponsePath Path
	Path         Path

	Arguments  map[string]any
	Variables  map[string]any
	Pagination Pagination
	NodeIDs    []string

	Directives []DirectiveState

	AuthorizationNodeID string
	Occurrence          int
	Depth               int
	Position            Position
}

// Policy makes the field-level authorization decision. It must inspect
// Arguments (already resolved from variables/defaults), Identity, and
// ResponsePath. Returning an error fails closed.
type Policy interface {
	AuthorizeField(ctx Context, request FieldRequest) (Decision, error)
}

// Context is the standard library context type.
type Context = context.Context

// PolicyFunc adapts a function to the Policy interface.
type PolicyFunc func(ctx Context, request FieldRequest) (Decision, error)

// AuthorizeField implements Policy.
func (f PolicyFunc) AuthorizeField(ctx Context, request FieldRequest) (Decision, error) {
	return f(ctx, request)
}

// AllowAllPolicy authorizes every active field. Tenant isolation still runs
// before this policy when an engine is created with NewEngine.
type AllowAllPolicy struct{}

// AuthorizeField implements Policy.
func (AllowAllPolicy) AuthorizeField(Context, FieldRequest) (Decision, error) {
	return Allow(), nil
}

// NodeTemplate is one syntactic field occurrence captured by Compile. Fragment
// spreads and inline fragments are expanded here; their occurrence chains and
// conditional directives are retained independently.
type NodeTemplate struct {
	ID          string
	ParentID    string
	Order       int
	ParentType  string
	FieldName   string
	Alias       string
	ResponseKey string
	Path        Path
	Arguments   []*Argument
	Directives  []*Directive
	Conditions  []PlannedDirective
	Depth       int
	Position    Position
	Origin      string
}

// PlannedDirective binds an inherited directive to the selection occurrence
// that supplied it. A condition on a fragment spread therefore gates every
// field occurrence created by that spread, independently of other uses of the
// same fragment.
type PlannedDirective struct {
	Name      string
	Origin    string
	Arguments []*Argument
	Position  Position
}

// StaticPlan is the query-shape plan. It contains every selection occurrence,
// including those that may be skipped at runtime. Digest depends only on the
// normalized executable document and operation name, never on batch metadata,
// persisted-query IDs, identity, or runtime variable values.
type StaticPlan struct {
	Document      *Document
	Operation     *Operation
	OperationName string
	Digest        string
	Nodes         []*NodeTemplate
}

// AuthNode is the runtime authorization record for one field occurrence.
type AuthNode struct {
	ID          string
	ParentID    string
	TemplateID  string
	Order       int
	ParentType  string
	FieldName   string
	Alias       string
	ResponseKey string
	Path        Path

	Arguments         map[string]any
	ArgumentSignature string
	Pagination        Pagination
	NodeIDs           []string
	Directives        []DirectiveState

	Active      bool
	IdentityKey string
	Decision    Decision
	Authorized  bool
	Error       string
	Depth       int
	Position    Position
}

// ResponseField is a safely merged response path. When duplicate aliases share
// a response key, Nodes contains every occurrence and both identity and the
// authorization result must match exactly before Merged is true.
type ResponseField struct {
	Path        Path
	FieldName   string
	ResponseKey string
	ParentType  string
	IdentityKey string
	Decision    Decision
	Nodes       []*AuthNode
	Merged      bool
	Occurrences int
}

// AuthFailure records one failed authorization node.
type AuthFailure struct {
	Path      Path
	FieldName string
	Alias     string
	NodeID    string
	Reason    string
	Decision  Decision
}

// AuthorizationError rejects a whole response path when any active occurrence
// on that path is denied or cannot be merged safely.
type AuthorizationError struct {
	Message  string
	Failures []AuthFailure
	Cause    error
}

func (e *AuthorizationError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "netsec: authorization failed"
}

// Unwrap exposes the sentinel class (ErrUnauthorized or
// ErrResponseConflict) to errors.Is.
func (e *AuthorizationError) Unwrap() error {
	return e.Cause
}

// Plan is the result of parsing, resolving variables, evaluating conditional
// directives, re-authorizing every active occurrence, and merging only safe
// response paths.
type Plan struct {
	Static              *StaticPlan
	Digest              string
	AuthorizationDigest string
	OperationName       string
	Identity            Identity
	IdentityKey         string
	Variables           map[string]any
	Nodes               []*AuthNode
	Fields              []*ResponseField
	ResponseFields      []*ResponseField
	DeniedPaths         []Path
	BlockedPaths        []Path
	Failures            []AuthFailure
	Rejected            bool
}

// Request describes one operation to authorize. BatchID and PersistedQueryID
// are transport metadata and never contribute to Plan.Digest.
type Request struct {
	Query            string
	OperationName    string
	Variables        map[string]any
	Identity         Identity
	PersistedQueryID string
	BatchID          string
}

// BatchResult groups independently authorized operations from one batch.
type BatchResult struct {
	Plans  []*Plan
	Errors []error
}

// FieldsByPath returns the response fields grouped by exact path key.
func (p *Plan) FieldsByPath() map[string]*ResponseField {
	result := make(map[string]*ResponseField, len(p.Fields))
	for _, field := range p.Fields {
		result[field.Path.Key()] = field
	}
	return result
}

// FieldAt returns the merged response field at an exact response path.
func (p *Plan) FieldAt(path ...string) (*ResponseField, bool) {
	key := Path(path).Key()
	for _, field := range p.Fields {
		if field.Path.Key() == key {
			return field, true
		}
	}
	return nil, false
}

// ActiveNodes returns only the occurrences that survived runtime @skip and
// @include evaluation.
func (p *Plan) ActiveNodes() []*AuthNode {
	result := make([]*AuthNode, 0, len(p.Nodes))
	for _, node := range p.Nodes {
		if node.Active {
			result = append(result, node)
		}
	}
	return result
}

// Allowed reports whether no response path was denied or left in conflict.
func (p *Plan) Allowed() bool {
	return p != nil && !p.Rejected
}

// RejectedPaths returns directly denied paths followed by paths that are
// unreachable because an ancestor was denied.
func (p *Plan) RejectedPaths() []Path {
	if p == nil {
		return nil
	}
	result := make([]Path, 0, len(p.DeniedPaths)+len(p.BlockedPaths))
	for _, path := range p.DeniedPaths {
		result = append(result, path.Clone())
	}
	for _, path := range p.BlockedPaths {
		result = append(result, path.Clone())
	}
	return result
}

// SortedFields returns response fields in execution order.
func (p *Plan) SortedFields() []*ResponseField {
	fields := append([]*ResponseField(nil), p.Fields...)
	sort.SliceStable(fields, func(i, j int) bool {
		return fields[i].Nodes[0].Order < fields[j].Nodes[0].Order
	})
	return fields
}
