package netsec

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// Build contains the build properties that affect network-security-config
// evaluation.
type Build struct {
	// TargetSDK is the app's targetSdkVersion and must be positive. A value
	// below 24 trusts user-installed CAs by default, and a value below 28
	// permits cleartext traffic by default.
	TargetSDK int
	// Debug reports whether the evaluated build is debuggable.
	Debug bool
}

// TrustAnchors is the set of certificate stores trusted for a domain.
type TrustAnchors struct {
	System bool
	User   bool
}

// Pin is a certificate public-key pin.
type Pin struct {
	// Digest is the normalized digest algorithm, currently "SHA-256".
	Digest string
	// Value is the base64-encoded digest as written in the configuration.
	Value string
}

// PinSet is an ordered set of certificate pins together with its optional
// expiration.
type PinSet struct {
	// Expiration is the UTC instant at which the set stops being enforced.
	// The zero value means that the set does not expire.
	Expiration time.Time
	Pins       []Pin
}

// EnforcedAt reports whether this pin set must be enforced at the given
// instant. An empty set or an expired set is not enforced.
func (p PinSet) EnforcedAt(at time.Time) bool {
	if len(p.Pins) == 0 {
		return false
	}
	return p.Expiration.IsZero() || at.Before(p.Expiration)
}

// Policy is the effective network policy for a host.
type Policy struct {
	CleartextTrafficPermitted bool
	TrustAnchors              TrustAnchors
	Pins                      PinSet
	// MatchedDomain is the domain rule that selected this policy. It is
	// empty when no domain-config matched and the base policy applies.
	MatchedDomain string
}

// PinsEnforcedAt reports whether the policy's pin set is active at the given
// instant. Expired pin sets are retained for diagnostics but are not
// enforced.
func (p Policy) PinsEnforcedAt(at time.Time) bool {
	return p.Pins.EnforcedAt(at)
}

// EffectivePins returns the pins that must be enforced at the given instant,
// or nil when pinning is inactive or the set has expired.
func (p Policy) EffectivePins(at time.Time) []Pin {
	if !p.Pins.EnforcedAt(at) {
		return nil
	}
	return clonePins(p.Pins.Pins)
}

// Config is a parsed network-security-config bound to a specific build.
type Config struct {
	build Build
	root  *configNode
	debug *debugOverrides
	order int
}

type debugOverrides struct {
	trust *TrustAnchors
}

type configNode struct {
	parent    *configNode
	domains   []domainRule
	cleartext *bool
	trust     *TrustAnchors
	pins      *PinSet
	trustSet  bool
	pinsSet   bool
	children  []*configNode
	depth     int
	order     int
}

type domainRule struct {
	name              string
	includeSubdomains bool
	subdomainsOnly    bool
}

const maxNestingDepth = 64

// ParseConfig parses an Android network-security-config XML document and
// binds it to build. The returned Config is safe for concurrent reads.
func ParseConfig(r io.Reader, build Build) (*Config, error) {
	if r == nil {
		return nil, errors.New("netsec: nil configuration reader")
	}
	if build.TargetSDK <= 0 {
		return nil, fmt.Errorf("netsec: targetSdkVersion must be positive, got %d", build.TargetSDK)
	}

	dec := xml.NewDecoder(r)
	dec.Strict = true

	start, err := findRoot(dec)
	if err != nil {
		return nil, err
	}
	if start.Name.Local != "network-security-config" {
		return nil, fmt.Errorf("netsec: root element must be <network-security-config>, got <%s>", start.Name.Local)
	}

	cfg := &Config{
		build: build,
		root: &configNode{
			cleartext: boolPtr(build.TargetSDK < 28),
			trust: &TrustAnchors{
				System: true,
				User:   build.TargetSDK < 24,
			},
		},
	}
	if err := cfg.parseRoot(dec, start); err != nil {
		return nil, err
	}
	if err := ensureDocumentEnd(dec); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ParseConfigBytes is a convenience wrapper around ParseConfig.
func ParseConfigBytes(data []byte, build Build) (*Config, error) {
	return ParseConfig(strings.NewReader(string(data)), build)
}

// Build returns the build properties bound to the configuration.
func (c *Config) Build() Build {
	return c.build
}

// BasePolicy returns the policy that applies when no domain-config matches.
func (c *Config) BasePolicy() Policy {
	p := effectivePolicy(c.root)
	c.applyDebug(&p)
	return p
}

// PolicyForURL evaluates the policy for an absolute URL.
func (c *Config) PolicyForURL(rawURL string) (Policy, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Policy{}, fmt.Errorf("netsec: invalid URL %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return Policy{}, fmt.Errorf("netsec: URL %q has no host", rawURL)
	}
	return c.PolicyFor(u.Hostname())
}

// PolicyFor evaluates the most specific matching domain-config for host.
// Domain matching is case-insensitive and follows Android's suffix rules:
// "example.com" also matches "api.example.com", but never "badexample.com".
// The most specific matching domain rule wins. Missing trust-anchor and
// pin-set elements are inherited from the enclosing config.
func (c *Config) PolicyFor(host string) (Policy, error) {
	host, err := normalizeHost(host)
	if err != nil {
		return Policy{}, err
	}

	var (
		matched   = c.root
		domain    string
		bestSpec  = -1
		bestDepth = -1
		bestOrder = -1
		found     bool
	)

	var walk func(*configNode)
	walk = func(n *configNode) {
		for _, rule := range n.domains {
			if !rule.matches(host) {
				continue
			}
			spec := rule.specificity(host)
			if !found ||
				spec > bestSpec ||
				(spec == bestSpec && n.depth > bestDepth) ||
				(spec == bestSpec && n.depth == bestDepth && n.order > bestOrder) {
				found = true
				matched = n
				domain = rule.name
				bestSpec = spec
				bestDepth = n.depth
				bestOrder = n.order
			}
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(c.root)

	p := effectivePolicy(matched)
	p.MatchedDomain = domain
	c.applyDebug(&p)
	return p, nil
}

func (c *Config) applyDebug(p *Policy) {
	if !c.build.Debug || c.debug == nil || c.debug.trust == nil {
		return
	}
	p.TrustAnchors.System = p.TrustAnchors.System || c.debug.trust.System
	p.TrustAnchors.User = p.TrustAnchors.User || c.debug.trust.User
}

func effectivePolicy(n *configNode) Policy {
	var chain []*configNode
	for cur := n; cur != nil; cur = cur.parent {
		chain = append(chain, cur)
	}

	var p Policy
	for i := len(chain) - 1; i >= 0; i-- {
		cur := chain[i]
		if cur.cleartext != nil {
			p.CleartextTrafficPermitted = *cur.cleartext
		}
		if cur.trust != nil {
			p.TrustAnchors = *cur.trust
		}
		if cur.pins != nil {
			p.Pins = clonePinSet(*cur.pins)
		}
	}
	return p
}

func (r domainRule) matches(host string) bool {
	if host == r.name {
		return !r.subdomainsOnly
	}
	if !r.includeSubdomains {
		return false
	}
	suffix := "." + r.name
	return len(host) > len(suffix) && strings.HasSuffix(host, suffix)
}

func (r domainRule) specificity(host string) int {
	score := len(r.name) * 2
	if host == r.name {
		score++
	}
	return score
}

func (c *Config) parseRoot(dec *xml.Decoder, start xml.StartElement) error {
	seenBase := false
	for {
		tok, err := nextToken(dec)
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "base-config":
				if seenBase {
					return errors.New("netsec: multiple <base-config> elements")
				}
				seenBase = true
				if err := c.parseBaseConfig(dec, t); err != nil {
					return err
				}
			case "domain-config":
				node, err := c.parseDomainConfig(dec, t, c.root, 1)
				if err != nil {
					return err
				}
				c.root.children = append(c.root.children, node)
			case "debug-overrides":
				if c.debug != nil {
					return errors.New("netsec: multiple <debug-overrides> elements")
				}
				d, err := parseDebugOverrides(dec, t)
				if err != nil {
					return err
				}
				c.debug = d
			default:
				return fmt.Errorf("netsec: unexpected element <%s> in <network-security-config>", t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return fmt.Errorf("netsec: unexpected end element </%s>", t.Name.Local)
			}
			return nil
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return errors.New("netsec: unexpected text in <network-security-config>")
			}
		}
	}
}

func (c *Config) parseBaseConfig(dec *xml.Decoder, start xml.StartElement) error {
	if err := c.applyCleartextAttr(start, c.root); err != nil {
		return err
	}

	for {
		tok, err := nextToken(dec)
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "trust-anchors":
				if c.root.trustSet {
					return errors.New("netsec: multiple <trust-anchors> elements in <base-config>")
				}
				anchors, err := parseTrustAnchors(dec, t)
				if err != nil {
					return err
				}
				c.root.trust = &anchors
				c.root.trustSet = true
			case "pin-set":
				if c.root.pinsSet {
					return errors.New("netsec: multiple <pin-set> elements in <base-config>")
				}
				pins, err := parsePinSet(dec, t)
				if err != nil {
					return err
				}
				c.root.pins = &pins
				c.root.pinsSet = true
			case "domain-config":
				node, err := c.parseDomainConfig(dec, t, c.root, 1)
				if err != nil {
					return err
				}
				c.root.children = append(c.root.children, node)
			default:
				return fmt.Errorf("netsec: unexpected element <%s> in <base-config>", t.Name.Local)
			}
		case xml.EndElement:
			return nil
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return errors.New("netsec: unexpected text in <base-config>")
			}
		}
	}
}

func (c *Config) parseDomainConfig(dec *xml.Decoder, start xml.StartElement, parent *configNode, depth int) (*configNode, error) {
	if depth > maxNestingDepth {
		return nil, fmt.Errorf("netsec: <domain-config> nesting exceeds %d levels", maxNestingDepth)
	}
	node := &configNode{
		parent: parent,
		depth:  depth,
		order:  c.order,
	}
	c.order++

	if err := c.applyCleartextAttr(start, node); err != nil {
		return nil, err
	}

	for {
		tok, err := nextToken(dec)
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "domain":
				rule, err := parseDomain(dec, t)
				if err != nil {
					return nil, err
				}
				node.domains = append(node.domains, rule)
			case "trust-anchors":
				if node.trustSet {
					return nil, fmt.Errorf("netsec: multiple <trust-anchors> elements in <domain-config> for %s", domainNames(node.domains))
				}
				anchors, err := parseTrustAnchors(dec, t)
				if err != nil {
					return nil, err
				}
				node.trust = &anchors
				node.trustSet = true
			case "pin-set":
				if node.pinsSet {
					return nil, fmt.Errorf("netsec: multiple <pin-set> elements in <domain-config> for %s", domainNames(node.domains))
				}
				pins, err := parsePinSet(dec, t)
				if err != nil {
					return nil, err
				}
				node.pins = &pins
				node.pinsSet = true
			case "domain-config":
				child, err := c.parseDomainConfig(dec, t, node, depth+1)
				if err != nil {
					return nil, err
				}
				node.children = append(node.children, child)
			default:
				return nil, fmt.Errorf("netsec: unexpected element <%s> in <domain-config>", t.Name.Local)
			}
		case xml.EndElement:
			if len(node.domains) == 0 {
				return nil, errors.New("netsec: <domain-config> contains no <domain>")
			}
			return node, nil
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("netsec: unexpected text in <domain-config>")
			}
		}
	}
}

func (c *Config) applyCleartextAttr(start xml.StartElement, node *configNode) error {
	value, ok, err := attrValue(start, "cleartextTrafficPermitted")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	permitted, err := parseXMLBool(value)
	if err != nil {
		return fmt.Errorf("netsec: invalid cleartextTrafficPermitted value %q: %w", value, err)
	}
	node.cleartext = boolPtr(permitted)
	return nil
}

func parseDebugOverrides(dec *xml.Decoder, start xml.StartElement) (*debugOverrides, error) {
	d := &debugOverrides{}
	for {
		tok, err := nextToken(dec)
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "trust-anchors":
				if d.trust != nil {
					return nil, errors.New("netsec: multiple <trust-anchors> elements in <debug-overrides>")
				}
				anchors, err := parseTrustAnchors(dec, t)
				if err != nil {
					return nil, err
				}
				d.trust = &anchors
			default:
				return nil, fmt.Errorf("netsec: unexpected element <%s> in <debug-overrides>", t.Name.Local)
			}
		case xml.EndElement:
			return d, nil
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return nil, errors.New("netsec: unexpected text in <debug-overrides>")
			}
		}
	}
}

func parseTrustAnchors(dec *xml.Decoder, start xml.StartElement) (TrustAnchors, error) {
	var anchors TrustAnchors
	for {
		tok, err := nextToken(dec)
		if err != nil {
			return anchors, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "certificates" {
				return anchors, fmt.Errorf("netsec: unexpected element <%s> in <trust-anchors>", t.Name.Local)
			}
			src, ok, err := attrValue(t, "src")
			if err != nil {
				return anchors, err
			}
			if !ok {
				return anchors, errors.New("netsec: <certificates> is missing the src attribute")
			}
			switch strings.ToLower(strings.TrimSpace(src)) {
			case "system":
				anchors.System = true
			case "user":
				anchors.User = true
			default:
				return anchors, fmt.Errorf("netsec: unsupported certificate source %q", src)
			}
			if err := ensureEmptyElement(dec, t); err != nil {
				return anchors, err
			}
		case xml.EndElement:
			return anchors, nil
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return anchors, errors.New("netsec: unexpected text in <trust-anchors>")
			}
		}
	}
}

func parsePinSet(dec *xml.Decoder, start xml.StartElement) (PinSet, error) {
	var set PinSet
	if value, ok, err := attrValue(start, "expiration"); err != nil {
		return set, err
	} else if ok {
		expiration, err := time.Parse("2006-01-02", strings.TrimSpace(value))
		if err != nil {
			return set, fmt.Errorf("netsec: invalid pin-set expiration %q (want YYYY-MM-DD): %w", value, err)
		}
		set.Expiration = expiration
	}

	for {
		tok, err := nextToken(dec)
		if err != nil {
			return set, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "pin" {
				return set, fmt.Errorf("netsec: unexpected element <%s> in <pin-set>", t.Name.Local)
			}
			digest, ok, err := attrValue(t, "digest")
			if err != nil {
				return set, err
			}
			if !ok {
				return set, errors.New("netsec: <pin> is missing the digest attribute")
			}
			digest, err = normalizeDigest(digest)
			if err != nil {
				return set, err
			}
			value, err := readText(dec, t)
			if err != nil {
				return set, err
			}
			value = strings.TrimSpace(value)
			if value == "" {
				return set, errors.New("netsec: <pin> has an empty value")
			}
			set.Pins = append(set.Pins, Pin{Digest: digest, Value: value})
		case xml.EndElement:
			if len(set.Pins) == 0 {
				return set, errors.New("netsec: <pin-set> contains no <pin>")
			}
			return set, nil
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return set, errors.New("netsec: unexpected text in <pin-set>")
			}
		}
	}
}

func parseDomain(dec *xml.Decoder, start xml.StartElement) (domainRule, error) {
	var rule domainRule
	rule.includeSubdomains = true
	if value, ok, err := attrValue(start, "includeSubdomains"); err != nil {
		return rule, err
	} else if ok {
		include, err := parseXMLBool(value)
		if err != nil {
			return rule, fmt.Errorf("netsec: invalid includeSubdomains value %q: %w", value, err)
		}
		rule.includeSubdomains = include
	}

	text, err := readText(dec, start)
	if err != nil {
		return rule, err
	}
	name, subdomainsOnly, err := normalizeDomainRule(text)
	if err != nil {
		return rule, err
	}
	if subdomainsOnly && !rule.includeSubdomains {
		return rule, fmt.Errorf("netsec: wildcard domain %q cannot set includeSubdomains=false", text)
	}
	rule.name = name
	rule.subdomainsOnly = subdomainsOnly
	return rule, nil
}

func findRoot(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return xml.StartElement{}, errors.New("netsec: empty XML document")
			}
			return xml.StartElement{}, fmt.Errorf("netsec: read XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return xml.StartElement{}, errors.New("netsec: unexpected text before root element")
			}
		}
	}
}

func nextToken(dec *xml.Decoder) (xml.Token, error) {
	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("netsec: unexpected end of XML document")
		}
		return nil, fmt.Errorf("netsec: read XML: %w", err)
	}
	return tok, nil
}

func ensureDocumentEnd(dec *xml.Decoder) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("netsec: read XML after root element: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return errors.New("netsec: unexpected text after root element")
			}
		case xml.StartElement:
			return fmt.Errorf("netsec: unexpected element <%s> after root element", t.Name.Local)
		case xml.EndElement:
			return fmt.Errorf("netsec: unexpected end element </%s> after root element", t.Name.Local)
		}
	}
}

func readText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var b strings.Builder
	for {
		tok, err := nextToken(dec)
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			return "", fmt.Errorf("netsec: unexpected element <%s> inside <%s>", t.Name.Local, start.Name.Local)
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return "", fmt.Errorf("netsec: unexpected end element </%s> inside <%s>", t.Name.Local, start.Name.Local)
			}
			return b.String(), nil
		}
	}
}

func ensureEmptyElement(dec *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := nextToken(dec)
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return fmt.Errorf("netsec: unexpected element <%s> inside <%s>", t.Name.Local, start.Name.Local)
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return fmt.Errorf("netsec: unexpected text inside <%s>", start.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local != start.Name.Local {
				return fmt.Errorf("netsec: unexpected end element </%s> inside <%s>", t.Name.Local, start.Name.Local)
			}
			return nil
		}
	}
}

func attrValue(start xml.StartElement, name string) (string, bool, error) {
	var (
		value string
		found bool
	)
	for _, a := range start.Attr {
		if a.Name.Local != name {
			continue
		}
		if found {
			return "", false, fmt.Errorf("netsec: duplicate %s attribute on <%s>", name, start.Name.Local)
		}
		value = a.Value
		found = true
	}
	return value, found, nil
}

func parseXMLBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("want true or false")
	}
}

func normalizeDomainRule(value string) (string, bool, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.TrimSuffix(name, ".")
	subdomainsOnly := false
	if strings.HasPrefix(name, "*.") {
		subdomainsOnly = true
		name = name[2:]
	} else if strings.Contains(name, "*") {
		return "", false, fmt.Errorf("netsec: unsupported domain wildcard %q (only *.example.com is supported)", value)
	}
	if err := validateName(name); err != nil {
		return "", false, fmt.Errorf("netsec: invalid domain %q: %w", value, err)
	}
	return name, subdomainsOnly, nil
}

func normalizeHost(value string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(value))
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", nil
	}
	if strings.Contains(host, "://") {
		return "", fmt.Errorf("netsec: %q is a URL, not a host; use PolicyForURL", value)
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if ip := net.ParseIP(host); ip != nil {
		return strings.ToLower(ip.String()), nil
	}
	if err := validateName(host); err != nil {
		return "", fmt.Errorf("netsec: invalid host %q: %w", value, err)
	}
	return host, nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if strings.ContainsAny(name, " \t\r\n/\\:@?#[]*") {
		return errors.New("contains an invalid character")
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return errors.New("has an empty label")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return errors.New("has an empty label")
		}
	}
	return nil
}

func normalizeDigest(value string) (string, error) {
	switch strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", "")) {
	case "SHA256":
		return "SHA-256", nil
	default:
		return "", fmt.Errorf("netsec: unsupported pin digest %q (want SHA-256)", value)
	}
}

func domainNames(rules []domainRule) string {
	names := make([]string, len(rules))
	for i, rule := range rules {
		names[i] = rule.name
	}
	if len(names) == 0 {
		return "(no domains)"
	}
	return strings.Join(names, ", ")
}

func boolPtr(v bool) *bool {
	return &v
}

func clonePins(pins []Pin) []Pin {
	if len(pins) == 0 {
		return nil
	}
	out := make([]Pin, len(pins))
	copy(out, pins)
	return out
}

func clonePinSet(set PinSet) PinSet {
	set.Pins = clonePins(set.Pins)
	return set
}
