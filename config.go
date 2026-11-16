package netsec

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Options contains the build inputs that affect Android's interpretation of a
// network security configuration. TargetSDK is the app's targetSdkVersion.
// Debug reports whether the build sets android:debuggable=true.
//
// A zero TargetSDK is treated as 28 so that an omitted value uses the modern,
// restrictive platform defaults. A zero Now is replaced with time.Now().UTC().
type Options struct {
	TargetSDK int
	Debug     bool
	Now       time.Time
}

// TrustAnchors is the set of certificate stores trusted by an evaluated
// configuration. Custom contains non-system, non-user sources such as
// "@raw/company_ca".
type TrustAnchors struct {
	System bool
	User   bool
	Custom []string
}

// HasUser reports whether user-installed CAs are trusted.
func (t TrustAnchors) HasUser() bool {
	return t.User
}

// HasSystem reports whether system CAs are trusted.
func (t TrustAnchors) HasSystem() bool {
	return t.System
}

// Pin is one public-key pin. Digest is normally "SHA-256".
type Pin struct {
	Digest string
	Value  string
}

// PinSet is a configured set of pins. Expiration is nil when the XML omitted
// the expiration attribute. Pins are not enforced after Expiration.
type PinSet struct {
	Expiration *time.Time
	Pins       []Pin
}

// Expired reports whether the pin set is expired at now.
func (p *PinSet) Expired(now time.Time) bool {
	return p != nil && p.Expiration != nil && now.After(*p.Expiration)
}

// Policy is the effective network policy for one host.
type Policy struct {
	Host                      string
	MatchedDomain             string
	CleartextTrafficPermitted bool
	TrustAnchors              TrustAnchors

	// PinSet is the inherited configuration, including an expired pin set.
	// Pins contains only pins that are currently enforced. Pinning is false
	// when there is no pin set, the pin set is empty, or it has expired.
	PinSet        *PinSet
	Pins          []Pin
	Pinning       bool
	PinSetExpired bool

	DebugOverridesApplied bool
}

// AllowsCleartext reports whether HTTP (non-TLS) traffic is permitted.
func (p Policy) AllowsCleartext() bool {
	return p.CleartextTrafficPermitted
}

// PinsEnforced reports whether certificate pinning applies to this host.
func (p Policy) PinsEnforced() bool {
	return p.Pinning
}

// Config is a parsed network-security-config that can be evaluated repeatedly
// with different target SDK, debug, and time values.
type Config struct {
	base  *configNode
	rules []domainRule
	debug *debugOverrides
}

// Parse parses an Android network-security-config XML document.
func Parse(data []byte) (*Config, error) {
	return ParseConfig(data)
}

// ParseConfig parses an Android network-security-config XML document.
func ParseConfig(data []byte) (*Config, error) {
	var document xmlNetworkSecurityConfig
	if err := xml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("netsec: parse XML: %w", err)
	}
	if document.XMLName.Local != "network-security-config" {
		return nil, fmt.Errorf("netsec: root element is %q, want %q", document.XMLName.Local, "network-security-config")
	}
	if len(document.BaseConfigs) > 1 {
		return nil, fmt.Errorf("netsec: multiple base-config elements")
	}
	if len(document.DebugOverrides) > 1 {
		return nil, fmt.Errorf("netsec: multiple debug-overrides elements")
	}

	base := &configNode{}
	if len(document.BaseConfigs) == 1 {
		var err error
		base, err = parseNode(document.BaseConfigs[0], nil)
		if err != nil {
			return nil, err
		}
	}

	for _, rawDomainConfig := range document.DomainConfigs {
		if _, err := parseNode(rawDomainConfig, base); err != nil {
			return nil, err
		}
	}

	cfg := &Config{base: base}
	cfg.collectRules(base)

	if len(document.DebugOverrides) == 1 {
		debug, err := parseDebugOverrides(document.DebugOverrides[0])
		if err != nil {
			return nil, err
		}
		cfg.debug = debug
	}
	return cfg, nil
}

// ParseReader parses an Android network-security-config XML document from r.
func ParseReader(r io.Reader) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("netsec: read config: %w", err)
	}
	return Parse(data)
}

// ParseFile parses an Android network-security-config XML file.
func ParseFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: read %s: %w", path, err)
	}
	return Parse(data)
}

// LoadFile is an alias for ParseFile.
func LoadFile(path string) (*Config, error) {
	return ParseFile(path)
}

// Evaluate parses and evaluates a configuration for host.
func Evaluate(data []byte, host string, opts Options) (Policy, error) {
	cfg, err := Parse(data)
	if err != nil {
		return Policy{}, err
	}
	return cfg.Evaluate(host, opts)
}

// Resolve is an alias for Evaluate.
func Resolve(data []byte, host string, opts Options) (Policy, error) {
	return Evaluate(data, host, opts)
}

// EvaluateFile parses and evaluates a configuration file for host.
func EvaluateFile(path, host string, opts Options) (Policy, error) {
	cfg, err := ParseFile(path)
	if err != nil {
		return Policy{}, err
	}
	return cfg.Evaluate(host, opts)
}

type optionalBool struct {
	Set   bool
	Value bool
}

func (b *optionalBool) UnmarshalXMLAttr(attr xml.Attr) error {
	value, err := strconv.ParseBool(strings.TrimSpace(attr.Value))
	if err != nil {
		return fmt.Errorf("invalid %s value %q", attr.Name.Local, attr.Value)
	}
	b.Set = true
	b.Value = value
	return nil
}

type xmlNetworkSecurityConfig struct {
	XMLName        xml.Name            `xml:"network-security-config"`
	BaseConfigs    []xmlConfig         `xml:"base-config"`
	DomainConfigs  []xmlConfig         `xml:"domain-config"`
	DebugOverrides []xmlDebugOverrides `xml:"debug-overrides"`
}

type xmlConfig struct {
	CleartextTrafficPermitted optionalBool      `xml:"cleartextTrafficPermitted,attr"`
	Domains                   []xmlDomain       `xml:"domain"`
	DomainConfigs             []xmlConfig       `xml:"domain-config"`
	TrustAnchors              []xmlTrustAnchors `xml:"trust-anchors"`
	PinSets                   []xmlPinSet       `xml:"pin-set"`
}

type xmlDomain struct {
	IncludeSubdomains optionalBool `xml:"includeSubdomains,attr"`
	Value             string       `xml:",chardata"`
}

type xmlTrustAnchors struct {
	Certificates []xmlCertificate `xml:"certificates"`
}

type xmlCertificate struct {
	Src string `xml:"src,attr"`
}

type xmlPinSet struct {
	Expiration string   `xml:"expiration,attr"`
	Pins       []xmlPin `xml:"pin"`
}

type xmlPin struct {
	Digest string `xml:"digest,attr"`
	Value  string `xml:",chardata"`
}

type xmlDebugOverrides struct {
	TrustAnchors []xmlTrustAnchors `xml:"trust-anchors"`
}

type debugOverrides struct {
	trust *TrustAnchors
}

func parseDebugOverrides(raw xmlDebugOverrides) (*debugOverrides, error) {
	debug := &debugOverrides{}
	if len(raw.TrustAnchors) > 1 {
		return nil, fmt.Errorf("netsec: multiple debug-overrides trust-anchors elements")
	}
	if len(raw.TrustAnchors) == 1 {
		trust, err := parseTrustAnchors(raw.TrustAnchors[0])
		if err != nil {
			return nil, err
		}
		debug.trust = &trust
	}
	return debug, nil
}

func parseNode(raw xmlConfig, parent *configNode) (*configNode, error) {
	node := &configNode{parent: parent}
	if raw.CleartextTrafficPermitted.Set {
		value := raw.CleartextTrafficPermitted.Value
		node.cleartext = &value
	}
	if len(raw.TrustAnchors) > 1 {
		return nil, fmt.Errorf("netsec: multiple trust-anchors elements in one config")
	}
	if len(raw.TrustAnchors) == 1 {
		trust, err := parseTrustAnchors(raw.TrustAnchors[0])
		if err != nil {
			return nil, err
		}
		node.trust = &trust
	}
	if len(raw.PinSets) > 1 {
		return nil, fmt.Errorf("netsec: multiple pin-set elements in one config")
	}
	if len(raw.PinSets) == 1 {
		pins, err := parsePinSet(raw.PinSets[0])
		if err != nil {
			return nil, err
		}
		node.pins = pins
	}

	for _, rawDomain := range raw.Domains {
		domain, err := parseDomain(rawDomain)
		if err != nil {
			return nil, err
		}
		node.domains = append(node.domains, domain)
	}
	for _, rawChild := range raw.DomainConfigs {
		if _, err := parseNode(rawChild, node); err != nil {
			return nil, err
		}
	}
	if parent != nil {
		parent.children = append(parent.children, node)
	}
	return node, nil
}

func parseTrustAnchors(raw xmlTrustAnchors) (TrustAnchors, error) {
	var trust TrustAnchors
	for _, certificate := range raw.Certificates {
		src := strings.TrimSpace(certificate.Src)
		switch {
		case src == "system":
			trust.System = true
		case src == "user":
			trust.User = true
		case src == "":
			return TrustAnchors{}, fmt.Errorf("netsec: certificates element is missing src")
		default:
			trust.Custom = appendUnique(trust.Custom, src)
		}
	}
	return trust, nil
}

func parsePinSet(raw xmlPinSet) (*PinSet, error) {
	set := &PinSet{}
	if strings.TrimSpace(raw.Expiration) != "" {
		expiration, err := parseExpiration(raw.Expiration)
		if err != nil {
			return nil, err
		}
		set.Expiration = &expiration
	}
	for _, rawPin := range raw.Pins {
		digest := strings.TrimSpace(rawPin.Digest)
		value := strings.TrimSpace(rawPin.Value)
		if digest == "" {
			return nil, fmt.Errorf("netsec: pin element is missing digest")
		}
		if value == "" {
			return nil, fmt.Errorf("netsec: pin element is empty")
		}
		set.Pins = append(set.Pins, Pin{Digest: digest, Value: value})
	}
	return set, nil
}

func parseExpiration(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if expiration, err := time.Parse("2006-01-02", value); err == nil {
		return expiration, nil
	}
	if expiration, err := time.Parse(time.RFC3339, value); err == nil {
		return expiration.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("netsec: invalid pin-set expiration %q", value)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
