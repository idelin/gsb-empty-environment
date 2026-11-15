package netsec

import (
	"errors"
	"time"
)

// Decision is the verdict produced by the engine.
type Decision int

const (
	// Deny is the default, fail-closed verdict.
	Deny Decision = iota
	// Allow permits the requested operation.
	Allow
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	default:
		return "deny"
	}
}

// Action identifies the DDS operation a rule governs.
type Action string

const (
	// Publish covers writing data to a topic (DataWriter creation/use).
	Publish Action = "publish"
	// Subscribe covers reading data from a topic (DataReader creation/use).
	Subscribe Action = "subscribe"
	// ActionAny matches every action. An empty Rule.Action is normalized
	// to ActionAny.
	ActionAny Action = ""
)

// Rule is a single ordered entry inside a permission document.
type Rule struct {
	// Effect is the verdict applied when the rule matches. It must be Allow
	// or Deny.
	Effect Decision
	// Action restricts the rule to one operation. ActionAny matches both.
	Action Action
	// Topics are case-sensitive fnmatch patterns; a rule matches when any
	// pattern matches the requested topic. At least one entry is required.
	Topics []string
	// DomainIDs lists the DDS domain IDs the rule applies to. At least one
	// entry is required.
	DomainIDs []int
	// Partitions are case-sensitive fnmatch patterns. An empty list matches
	// every partition; otherwise a rule matches when any pattern matches the
	// requested partition.
	Partitions []string
}

// PermissionDocument is an already signature-verified permissions document.
// Signature verification happens outside this package; callers only feed in
// documents whose cryptographic signature has already been accepted.
type PermissionDocument struct {
	// SubjectName is the identity asserted by the document. It must equal the
	// certificate subject presented by the participant, otherwise the whole
	// document is treated as invalid.
	SubjectName string
	// NotBefore / NotAfter bound the validity window. The interval is
	// half-open: [NotBefore, NotAfter). A zero NotAfter means "no upper bound".
	NotBefore time.Time
	NotAfter  time.Time
	// Rules are evaluated in order for every request; the first matching rule
	// wins.
	Rules []Rule
}

// AccessRequest is the operation being authorized.
type AccessRequest struct {
	// CertificateSubject is the subject name from the participant's valid
	// identity certificate.
	CertificateSubject string
	Action             Action
	Topic              string
	DomainID           int
	Partition          string
}

var (
	// ErrNoDocuments is returned by NewEngine when no document is supplied.
	ErrNoDocuments = errors.New("netsec: no permission documents provided")
	// ErrEmptySubject is returned when a document has no subject name.
	ErrEmptySubject = errors.New("netsec: permission document missing subject name")
	// ErrInvalidWindow is returned when NotAfter is not zero and precedes
	// NotBefore.
	ErrInvalidWindow = errors.New("netsec: permission document has invalid validity window")
	// ErrEmptyRule is returned when a document contains a rule without topics
	// or domain IDs, or with an invalid effect.
	ErrEmptyRule = errors.New("netsec: rule must declare an effect, at least one topic and one domain id")
)

type compiledRule struct {
	rule Rule
}

type compiledDocument struct {
	doc   PermissionDocument
	rules []compiledRule
}

// Engine evaluates access requests against the compiled permission
// documents. It is safe for concurrent use once constructed.
type Engine struct {
	documents []compiledDocument
	now       func() time.Time
}

// Option customizes Engine construction.
type Option func(*Engine)

// WithClock overrides the time source, mainly useful for testing validity
// windows. The default is time.Now.
func WithClock(clock func() time.Time) Option {
	return func(e *Engine) {
		if clock != nil {
			e.now = clock
		}
	}
}

// NewEngine validates and compiles the supplied permission documents.
// Documents whose static content is malformed cause an error; documents that
// are merely expired or do not match the current caller are filtered at
// decision time in Authorize.
func NewEngine(documents []PermissionDocument, opts ...Option) (*Engine, error) {
	if len(documents) == 0 {
		return nil, ErrNoDocuments
	}
	engine := &Engine{now: time.Now}
	for _, opt := range opts {
		opt(engine)
	}
	for _, doc := range documents {
		compiled, err := compileDocument(doc)
		if err != nil {
			return nil, err
		}
		engine.documents = append(engine.documents, compiled)
	}
	return engine, nil
}

func compileDocument(doc PermissionDocument) (compiledDocument, error) {
	if doc.SubjectName == "" {
		return compiledDocument{}, ErrEmptySubject
	}
	if !doc.NotAfter.IsZero() && doc.NotAfter.Before(doc.NotBefore) {
		return compiledDocument{}, ErrInvalidWindow
	}
	compiled := compiledDocument{doc: doc}
	for _, rule := range doc.Rules {
		if rule.Effect != Allow && rule.Effect != Deny {
			return compiledDocument{}, ErrEmptyRule
		}
		if len(rule.Topics) == 0 || len(rule.DomainIDs) == 0 {
			return compiledDocument{}, ErrEmptyRule
		}
		for _, pattern := range rule.Topics {
			if err := validatePattern(pattern); err != nil {
				return compiledDocument{}, err
			}
		}
		for _, pattern := range rule.Partitions {
			if err := validatePattern(pattern); err != nil {
				return compiledDocument{}, err
			}
		}
		compiled.rules = append(compiled.rules, compiledRule{rule: rule})
	}
	return compiled, nil
}

// Explanation records why a verdict was reached. RuleIndex is -1 and
// DocumentIndex is -1 when no rule matched and the fail-closed default was
// applied.
type Explanation struct {
	Decision      Decision
	DocumentIndex int
	RuleIndex     int
	// SkippedDocuments indexes documents ignored because they were expired or
	// belonged to a different subject.
	SkippedDocuments []int
}

// Authorize evaluates a request. It is fail-closed: any document that is
// expired or whose subject does not match the certificate subject is ignored
// in full, and a request with no matching rule is denied.
func (e *Engine) Authorize(req AccessRequest) (Decision, Explanation) {
	now := e.now()
	explanation := Explanation{Decision: Deny, DocumentIndex: -1, RuleIndex: -1}

	for docIndex, compiled := range e.documents {
		if compiled.doc.SubjectName != req.CertificateSubject {
			explanation.SkippedDocuments = append(explanation.SkippedDocuments, docIndex)
			continue
		}
		if now.Before(compiled.doc.NotBefore) {
			explanation.SkippedDocuments = append(explanation.SkippedDocuments, docIndex)
			continue
		}
		if !compiled.doc.NotAfter.IsZero() && !now.Before(compiled.doc.NotAfter) {
			explanation.SkippedDocuments = append(explanation.SkippedDocuments, docIndex)
			continue
		}
		for ruleIndex, candidate := range compiled.rules {
			if !ruleMatches(candidate.rule, req) {
				continue
			}
			explanation.Decision = candidate.rule.Effect
			explanation.DocumentIndex = docIndex
			explanation.RuleIndex = ruleIndex
			return explanation.Decision, explanation
		}
	}

	return Deny, explanation
}

// Allowed is a convenience wrapper returning only the boolean verdict.
func (e *Engine) Allowed(req AccessRequest) bool {
	decision, _ := e.Authorize(req)
	return decision == Allow
}

func ruleMatches(rule Rule, req AccessRequest) bool {
	if rule.Action != ActionAny && rule.Action != req.Action {
		return false
	}
	if !containsInt(rule.DomainIDs, req.DomainID) {
		return false
	}
	if !anyPattern(rule.Topics, req.Topic) {
		return false
	}
	if len(rule.Partitions) > 0 && !anyPattern(rule.Partitions, req.Partition) {
		return false
	}
	return true
}

func anyPattern(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matchPattern(pattern, value) {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
