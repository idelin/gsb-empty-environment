package netsec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// Decision is the result of evaluating a permissions document.
//
// The zero value is Deny so an uninitialized decision fails closed.
type Decision int

const (
	Deny Decision = iota
	Allow
)

func (d Decision) String() string {
	if d == Allow {
		return "ALLOW"
	}
	return "DENY"
}

// Allowed reports whether the decision is Allow.
func (d Decision) Allowed() bool {
	return d == Allow
}

// Operation is the DDS operation being authorized.
type Operation int

const (
	Publish Operation = iota
	Subscribe
)

const (
	OperationPublish   = Publish
	OperationSubscribe = Subscribe
)

func (o Operation) String() string {
	if o == Subscribe {
		return "SUBSCRIBE"
	}
	return "PUBLISH"
}

// Request describes the access control check requested by the DDS plugin.
//
// Partitions is optional. When it is empty, Partition is used as the single
// partition name. An empty partition name denotes DDS's default partition.
type Request struct {
	DomainID   int
	Topic      string
	Partition  string
	Partitions []string
	Operation  Operation
}

func (r Request) partitionNames() []string {
	if len(r.Partitions) != 0 {
		return r.Partitions
	}
	return []string{r.Partition}
}

// Document is a validated, signature-verified permissions document.
//
// Signature verification is deliberately outside this package: callers must
// verify the document signature before passing its bytes to ParsePermissions.
// This package still validates the binding to the identity certificate and the
// grant's validity interval because an authentic document can be stale or
// issued for a different participant.
type Document struct {
	SubjectName string
	NotBefore   time.Time
	NotAfter    time.Time

	rules []rule
}

// Authorize applies the document's rules to req. Rules are evaluated in
// document order. The first matching rule supplies the decision. If nothing
// matches, the result is Deny.
func (d *Document) Authorize(req Request) Decision {
	if d == nil {
		return Deny
	}
	for i := range d.rules {
		if d.rules[i].matches(req) {
			return d.rules[i].effect
		}
	}
	return Deny
}

// Decide is an alias for Authorize.
func (d *Document) Decide(req Request) Decision {
	return d.Authorize(req)
}

// Allows reports whether Authorize returns Allow.
func (d *Document) Allows(req Request) bool {
	return d.Authorize(req) == Allow
}

// ValidAt reports whether the document's validity interval contains t.
// Both endpoints are inclusive.
func (d *Document) ValidAt(t time.Time) bool {
	if d == nil {
		return false
	}
	return !t.Before(d.NotBefore) && !t.After(d.NotAfter)
}

// RuleCount returns the number of ordered allow/deny rules in the document.
func (d *Document) RuleCount() int {
	if d == nil {
		return 0
	}
	return len(d.rules)
}

var (
	// ErrInvalidDocument identifies a malformed or otherwise unusable
	// permissions document.
	ErrInvalidDocument = errors.New("invalid permissions document")
	// ErrSubjectMismatch identifies a document with no grant for the
	// participant's identity certificate subject.
	ErrSubjectMismatch = errors.New("permissions document subject does not match certificate subject")
	// ErrNotYetValid identifies a document whose not_before is in the future.
	ErrNotYetValid = errors.New("permissions document is not yet valid")
	// ErrExpired identifies a document whose not_after is in the past.
	ErrExpired = errors.New("permissions document is expired")
)

// ParsePermissions parses and validates a signature-verified DDS permissions
// document for certificateSubject at time now.
//
// A document is accepted only when it contains a grant whose subject_name is
// equal to certificateSubject (after light RFC 2253 whitespace/type
// normalization), and now is within that grant's inclusive validity interval.
// If now is the zero time, the current UTC time is used.
func ParsePermissions(data []byte, certificateSubject string, now time.Time) (*Document, error) {
	grants, err := decodeGrants(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	if len(grants) == 0 {
		return nil, fmt.Errorf("%w: no grant found", ErrInvalidDocument)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var selected *rawGrant
	for i := range grants {
		if subjectsEqual(grants[i].SubjectName, certificateSubject) {
			selected = &grants[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDocument, ErrSubjectMismatch)
	}

	notBefore, err := parseDDSDateTime(selected.Validity.NotBefore)
	if err != nil {
		return nil, fmt.Errorf("%w: not_before: %w", ErrInvalidDocument, err)
	}
	notAfter, err := parseDDSDateTime(selected.Validity.NotAfter)
	if err != nil {
		return nil, fmt.Errorf("%w: not_after: %w", ErrInvalidDocument, err)
	}
	if notAfter.Before(notBefore) {
		return nil, fmt.Errorf("%w: not_after is before not_before", ErrInvalidDocument)
	}
	if now.Before(notBefore) {
		return nil, fmt.Errorf("%w: %w (not_before %s)", ErrInvalidDocument, ErrNotYetValid, notBefore.Format(time.RFC3339))
	}
	if now.After(notAfter) {
		return nil, fmt.Errorf("%w: %w (not_after %s)", ErrInvalidDocument, ErrExpired, notAfter.Format(time.RFC3339))
	}

	doc := &Document{
		SubjectName: strings.TrimSpace(selected.SubjectName),
		NotBefore:   notBefore,
		NotAfter:    notAfter,
	}
	for _, entry := range selected.Rules {
		compiled, err := compileRule(entry)
		if err != nil {
			return nil, fmt.Errorf("%w: rule %d: %w", ErrInvalidDocument, len(doc.rules)+1, err)
		}
		doc.rules = append(doc.rules, compiled)
	}
	return doc, nil
}

// Parse is shorthand for ParsePermissions.
func Parse(data []byte, certificateSubject string, now time.Time) (*Document, error) {
	return ParsePermissions(data, certificateSubject, now)
}

// ParsePermissionsReader is the streaming form of ParsePermissions.
func ParsePermissionsReader(r io.Reader, certificateSubject string, now time.Time) (*Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	return ParsePermissions(data, certificateSubject, now)
}

// CheckAccess parses the document and returns its decision for req. An invalid
// document always yields Deny together with the validation error.
func CheckAccess(data []byte, certificateSubject string, now time.Time, req Request) (Decision, error) {
	doc, err := ParsePermissions(data, certificateSubject, now)
	if err != nil {
		return Deny, err
	}
	return doc.Authorize(req), nil
}

type rule struct {
	effect Decision

	domains    []domainRange
	hasDomains bool

	publishTopics    []string
	subscribeTopics  []string
	hasPublish       bool
	hasSubscribe     bool
	hasPartitions    bool
	partitionPattern []string
}

type domainRange struct {
	min int
	max int
}

func (r *rule) matches(req Request) bool {
	if !r.hasDomains {
		return false
	}
	domainMatched := false
	for _, d := range r.domains {
		if req.DomainID >= d.min && req.DomainID <= d.max {
			domainMatched = true
			break
		}
	}
	if !domainMatched {
		return false
	}

	switch req.Operation {
	case Publish:
		if !r.hasPublish || !matchAnyTopic(r.publishTopics, req.Topic) {
			return false
		}
	case Subscribe:
		if !r.hasSubscribe || !matchAnyTopic(r.subscribeTopics, req.Topic) {
			return false
		}
	default:
		return false
	}

	return r.matchPartitions(req.partitionNames())
}

func (r *rule) matchPartitions(requested []string) bool {
	if !r.hasPartitions || len(r.partitionPattern) == 0 {
		if r.effect == Deny {
			// A deny rule without a partition condition is as broad as
			// possible and applies to every partition.
			return true
		}
		// An allow rule without a partition condition only grants the
		// default, empty-named partition.
		for _, p := range requested {
			if p == "" {
				return true
			}
		}
		return false
	}

	for _, requestedPartition := range requested {
		for _, pattern := range r.partitionPattern {
			if Fnmatch(pattern, requestedPartition) {
				return true
			}
		}
	}
	return false
}

func matchAnyTopic(patterns []string, topic string) bool {
	for _, pattern := range patterns {
		if Fnmatch(pattern, topic) {
			return true
		}
	}
	return false
}

func compileRule(entry rawRuleEntry) (rule, error) {
	out := rule{effect: entry.effect}
	raw := entry.rule

	if !raw.HasDomains {
		return rule{}, errors.New("rule has no domains condition")
	}
	out.hasDomains = true
	for _, value := range raw.Domains.IDs {
		id, err := parseDomainID(value)
		if err != nil {
			return rule{}, err
		}
		out.domains = append(out.domains, domainRange{min: id, max: id})
	}
	for _, value := range raw.Domains.Ranges {
		min, err := parseDomainID(value.Min)
		if err != nil {
			return rule{}, fmt.Errorf("id_range min: %w", err)
		}
		max := math.MaxInt
		if strings.TrimSpace(value.Max) != "" {
			max, err = parseDomainID(value.Max)
			if err != nil {
				return rule{}, fmt.Errorf("id_range max: %w", err)
			}
		}
		if max < min {
			return rule{}, fmt.Errorf("id_range max %d is less than min %d", max, min)
		}
		out.domains = append(out.domains, domainRange{min: min, max: max})
	}
	// Older/simplified documents use <domain> instead of <id>.
	for _, value := range raw.Domains.Legacy {
		id, err := parseDomainID(value)
		if err != nil {
			return rule{}, err
		}
		out.domains = append(out.domains, domainRange{min: id, max: id})
	}
	if len(out.domains) == 0 {
		return rule{}, errors.New("domains condition is empty")
	}

	if raw.HasPublish {
		out.hasPublish = true
		out.publishTopics = append(out.publishTopics, raw.Publish.allTopics()...)
	}
	if raw.HasSubscribe {
		out.hasSubscribe = true
		out.subscribeTopics = append(out.subscribeTopics, raw.Subscribe.allTopics()...)
	}
	if raw.HasTopics {
		topics := raw.Topics.allTopics()
		// A top-level <topics> block is accepted as a shorthand that applies
		// to both operations. Standard DDS documents use <publish> and
		// <subscribe> explicitly.
		if !out.hasPublish {
			out.hasPublish = true
			out.publishTopics = append(out.publishTopics, topics...)
		}
		if !out.hasSubscribe {
			out.hasSubscribe = true
			out.subscribeTopics = append(out.subscribeTopics, topics...)
		}
	}

	if raw.HasPartitions {
		out.hasPartitions = true
		out.partitionPattern = append(out.partitionPattern, raw.Partitions.Names...)
	}

	for _, pattern := range out.publishTopics {
		if !validPattern(pattern) {
			return rule{}, fmt.Errorf("invalid publish topic pattern %q", pattern)
		}
	}
	for _, pattern := range out.subscribeTopics {
		if !validPattern(pattern) {
			return rule{}, fmt.Errorf("invalid subscribe topic pattern %q", pattern)
		}
	}
	for _, pattern := range out.partitionPattern {
		if !validPattern(pattern) {
			return rule{}, fmt.Errorf("invalid partition pattern %q", pattern)
		}
	}
	return out, nil
}

func parseDomainID(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("empty domain id")
	}
	id, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid domain id %q", value)
	}
	if id < 0 {
		return 0, fmt.Errorf("negative domain id %d", id)
	}
	return id, nil
}

func parseDDSDateTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty date-time")
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC(), nil
	}
	// DDS specifies the timestamp in GMT when no zone is present.
	layouts := []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date-time %q", value)
}

func subjectsEqual(a, b string) bool {
	a = normalizeSubject(a)
	b = normalizeSubject(b)
	return a != "" && a == b
}

func normalizeSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}

	fields := splitDN(subject)
	for i, field := range fields {
		field = strings.TrimSpace(field)
		if eq := strings.IndexByte(field, '='); eq > 0 {
			kind := strings.ToUpper(strings.TrimSpace(field[:eq]))
			value := strings.TrimSpace(field[eq+1:])
			field = kind + "=" + value
		}
		fields[i] = field
	}
	return strings.Join(fields, ",")
}

// splitDN splits an RFC 2253-style distinguished name on commas that are not
// escaped. It keeps escape sequences intact for comparison.
func splitDN(s string) []string {
	var parts []string
	var current strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			current.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			current.WriteByte(c)
			escaped = true
			continue
		}
		if c == ',' {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(c)
	}
	parts = append(parts, current.String())
	return parts
}

type rawGrant struct {
	SubjectName string
	Validity    rawValidity
	Rules       []rawRuleEntry
}

type rawRuleEntry struct {
	effect Decision
	rule   rawRule
}

func (g *rawGrant) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := g.decodeChild(dec, t); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

func (g *rawGrant) decodeChild(dec *xml.Decoder, start xml.StartElement) error {
	switch start.Name.Local {
	case "subject_name":
		return dec.DecodeElement(&g.SubjectName, &start)
	case "validity":
		return dec.DecodeElement(&g.Validity, &start)
	case "allow_rule", "deny_rule":
		var r rawRule
		if err := dec.DecodeElement(&r, &start); err != nil {
			return err
		}
		effect := Allow
		if start.Name.Local == "deny_rule" {
			effect = Deny
		}
		g.Rules = append(g.Rules, rawRuleEntry{effect: effect, rule: r})
		return nil
	case "default":
		// Accepted for document compatibility. The access-control decision
		// still fails closed: no matching rule always means Deny.
		var ignored string
		return dec.DecodeElement(&ignored, &start)
	default:
		return dec.Skip()
	}
}

type rawValidity struct {
	NotBefore string `xml:"not_before"`
	NotAfter  string `xml:"not_after"`
}

type rawRule struct {
	HasDomains    bool
	Domains       rawDomains
	HasPublish    bool
	Publish       rawTopicSection
	HasSubscribe  bool
	Subscribe     rawTopicSection
	HasTopics     bool
	Topics        rawTopicSection
	HasPartitions bool
	Partitions    rawPartitions
}

func (r *rawRule) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "domains":
				r.HasDomains = true
				if err := dec.DecodeElement(&r.Domains, &t); err != nil {
					return err
				}
			case "publish":
				r.HasPublish = true
				if err := dec.DecodeElement(&r.Publish, &t); err != nil {
					return err
				}
			case "subscribe":
				r.HasSubscribe = true
				if err := dec.DecodeElement(&r.Subscribe, &t); err != nil {
					return err
				}
			case "topics":
				r.HasTopics = true
				if err := dec.DecodeElement(&r.Topics, &t); err != nil {
					return err
				}
			case "partitions":
				r.HasPartitions = true
				if err := dec.DecodeElement(&r.Partitions, &t); err != nil {
					return err
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			return nil
		}
	}
}

type rawDomains struct {
	IDs    []string     `xml:"id"`
	Ranges []rawIDRange `xml:"id_range"`
	Legacy []string     `xml:"domain"`
}

type rawIDRange struct {
	Min string `xml:"min"`
	Max string `xml:"max"`
}

type rawPartitions struct {
	Names []string `xml:"partition"`
}

type rawTopicSection struct {
	Nested struct {
		Items []string `xml:"topic"`
	} `xml:"topics"`
	Direct []string `xml:"topic"`
}

func (s rawTopicSection) allTopics() []string {
	out := make([]string, 0, len(s.Nested.Items)+len(s.Direct))
	out = append(out, s.Nested.Items...)
	out = append(out, s.Direct...)
	return out
}

func decodeGrants(data []byte) ([]rawGrant, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty XML document")
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "grant":
			var grant rawGrant
			if err := dec.DecodeElement(&grant, &start); err != nil {
				return nil, err
			}
			return []rawGrant{grant}, nil
		case "permissions", "dds":
			return decodePermissionContainer(dec, start)
		default:
			return nil, fmt.Errorf("unexpected root element <%s>", start.Name.Local)
		}
	}
}

// decodePermissionContainer accepts both the standard <dds><permissions>
// wrapper and the simplified <permissions> root. It also tolerates rule fields
// placed directly under <permissions> without an enclosing <grant>.
func decodePermissionContainer(dec *xml.Decoder, start xml.StartElement) ([]rawGrant, error) {
	var grants []rawGrant
	var direct rawGrant
	directSeen := false

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "grant":
				var grant rawGrant
				if err := dec.DecodeElement(&grant, &t); err != nil {
					return nil, err
				}
				grants = append(grants, grant)
			case "permissions":
				nested, err := decodePermissionContainer(dec, t)
				if err != nil {
					return nil, err
				}
				grants = append(grants, nested...)
			case "subject_name", "validity", "allow_rule", "deny_rule", "default":
				directSeen = true
				if err := direct.decodeChild(dec, t); err != nil {
					return nil, err
				}
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if t.Name == start.Name {
				if directSeen {
					grants = append(grants, direct)
				}
				return grants, nil
			}
		}
	}
}
