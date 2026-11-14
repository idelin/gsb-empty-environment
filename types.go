package netsec

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Sentinel errors returned by the package. Callers should use errors.Is
// because errors may wrap these values with additional context.
var (
	ErrInvalidArgument          = errors.New("netsec: invalid argument")
	ErrEarlyDataNotAuthorized   = errors.New("netsec: TLS early data is not authorized")
	ErrHandshakeIncomplete      = errors.New("netsec: TLS handshake has not finished")
	ErrTicketInvalid            = errors.New("netsec: ticket is invalid")
	ErrTicketExpired            = errors.New("netsec: ticket has expired")
	ErrTicketReplayed           = errors.New("netsec: ticket has already been consumed")
	ErrTicketKeyRotated         = errors.New("netsec: ticket key generation is no longer active")
	ErrPolicyDomainMismatch     = errors.New("netsec: policy domain mismatch")
	ErrPolicyChanged            = errors.New("netsec: authorization policy changed")
	ErrPolicyTightened          = errors.New("netsec: authorization policy tightened")
	ErrAccessPointNotAllowed    = errors.New("netsec: access point is not allowed by policy")
	ErrCertificateInvalid       = errors.New("netsec: client certificate chain is invalid")
	ErrCertificateMismatch      = errors.New("netsec: client certificate chain changed")
	ErrCertificatePolicyChanged = errors.New("netsec: certificate validation policy generation changed")
	ErrCertificateRevoked       = errors.New("netsec: client certificate is revoked")
	ErrRevocationUnavailable    = errors.New("netsec: certificate revocation status is unavailable")
	ErrIdentityMismatch         = errors.New("netsec: EAP identity does not match the certificate subject")
	ErrAnonymousIdentity        = errors.New("netsec: anonymous identity cannot be used as a principal")
	ErrServerIdentityMismatch   = errors.New("netsec: server virtual identity mismatch")
	ErrTLSBindingMismatch       = errors.New("netsec: TLS version, cipher suite, or key binding mismatch")
	ErrChannelBindingInvalid    = errors.New("netsec: channel binding is missing or unverified")
	ErrMSKIdentityMismatch      = errors.New("netsec: exported MSK identity mismatch")
	ErrGrantNotPermitted        = errors.New("netsec: frozen grant is not permitted by current policy")
	ErrNoTrustRoots             = errors.New("netsec: no client certificate trust roots configured")
)

// TLS version and cipher-suite identifiers used by TLSState.
const (
	TLSVersion12 uint16 = 0x0303
	TLSVersion13 uint16 = 0x0304

	TLSAES128GCMSHA256        uint16 = 0x1301
	TLSAES256GCMSHA384        uint16 = 0x1302
	TLSChaCha20Poly1305SHA256 uint16 = 0x1303
)

// AccessPoint is the concrete network attachment being authorized. DomainID
// names the policy domain, SSID is the wireless network name, and BSSID is the
// basic service set identifier of the radio.
//
// A ticket is valid across BSSIDs only when all of them belong to the same
// DomainID/SSID and are present in the current Policy's AllowedBSSIDs.
type AccessPoint struct {
	DomainID string
	SSID     string
	BSSID    string
}

func (ap AccessPoint) normalized() AccessPoint {
	return AccessPoint{
		DomainID: strings.TrimSpace(ap.DomainID),
		SSID:     ap.SSID,
		BSSID:    strings.ToLower(strings.TrimSpace(ap.BSSID)),
	}
}

func (ap AccessPoint) validate() error {
	n := ap.normalized()
	if n.DomainID == "" || n.SSID == "" || n.BSSID == "" {
		return fmt.Errorf("%w: access point has empty domain, SSID, or BSSID", ErrInvalidArgument)
	}
	return nil
}

// Policy is the authorization policy snapshot for one network domain. Resume
// decisions are made against the current Policy supplied by the AAA service;
// the ticket carries the Policy that was in force at initial authentication.
//
// Revision is a monotonic policy generation. A decrease is a rollback and an
// increase is treated as a tightening for error reporting. Even when the
// revision is unchanged, the complete policy fingerprint is compared so that
// changes to any allow-list are detected.
type Policy struct {
	DomainID string
	Revision uint64
	SSID     string

	AllowedBSSIDs           []string
	AllowedServerIdentities []string
	AllowedTLSVersions      []uint16
	AllowedCipherSuites     []uint16
	AllowedVLANs            []uint16
}

func (p Policy) normalized() Policy {
	return Policy{
		DomainID:                strings.TrimSpace(p.DomainID),
		Revision:                p.Revision,
		SSID:                    p.SSID,
		AllowedBSSIDs:           normalizedStrings(p.AllowedBSSIDs, true),
		AllowedServerIdentities: normalizedStrings(p.AllowedServerIdentities, false),
		AllowedTLSVersions:      normalizedUint16s(p.AllowedTLSVersions),
		AllowedCipherSuites:     normalizedUint16s(p.AllowedCipherSuites),
		AllowedVLANs:            normalizedUint16s(p.AllowedVLANs),
	}
}

func (p Policy) validate() error {
	n := p.normalized()
	if n.DomainID == "" || n.SSID == "" {
		return fmt.Errorf("%w: policy has empty domain or SSID", ErrInvalidArgument)
	}
	if len(n.AllowedBSSIDs) == 0 {
		return fmt.Errorf("%w: policy has no allowed BSSIDs", ErrInvalidArgument)
	}
	if len(n.AllowedServerIdentities) == 0 {
		return fmt.Errorf("%w: policy has no allowed server identities", ErrInvalidArgument)
	}
	if len(n.AllowedTLSVersions) == 0 {
		return fmt.Errorf("%w: policy has no allowed TLS versions", ErrInvalidArgument)
	}
	if len(n.AllowedCipherSuites) == 0 {
		return fmt.Errorf("%w: policy has no allowed cipher suites", ErrInvalidArgument)
	}
	if len(n.AllowedVLANs) == 0 {
		return fmt.Errorf("%w: policy has no allowed VLANs", ErrInvalidArgument)
	}
	return nil
}

func (p Policy) permitsAP(ap AccessPoint) bool {
	an := ap.normalized()
	if an.DomainID != p.DomainID || an.SSID != p.SSID {
		return false
	}
	for _, b := range p.AllowedBSSIDs {
		if strings.EqualFold(b, an.BSSID) {
			return true
		}
	}
	return false
}

func (p Policy) permitsServer(identity string) bool {
	identity = strings.TrimSpace(identity)
	for _, allowed := range p.AllowedServerIdentities {
		if subtle.ConstantTimeCompare([]byte(allowed), []byte(identity)) == 1 {
			return true
		}
	}
	return false
}

func (p Policy) permitsTLS(version, cipherSuite uint16) bool {
	return containsUint16(p.AllowedTLSVersions, version) && containsUint16(p.AllowedCipherSuites, cipherSuite)
}

func (p Policy) permitsVLAN(vlan uint16) bool {
	return containsUint16(p.AllowedVLANs, vlan)
}

func (p Policy) permitsGrant(g Grant) bool {
	return p.permitsVLAN(g.VLAN)
}

// ACLRule is one frozen network-access rule. Action is "allow" or "deny".
// Destination may be a CIDR, IP address, host name, or "*". Ports is a
// comma-separated list, a range, or "any".
type ACLRule struct {
	Action      string
	Protocol    string
	Destination string
	Ports       string
}

func (r ACLRule) normalized() ACLRule {
	return ACLRule{
		Action:      strings.ToLower(strings.TrimSpace(r.Action)),
		Protocol:    strings.ToLower(strings.TrimSpace(r.Protocol)),
		Destination: strings.TrimSpace(r.Destination),
		Ports:       strings.TrimSpace(r.Ports),
	}
}

func (r ACLRule) validate() error {
	n := r.normalized()
	if n.Action != "allow" && n.Action != "deny" {
		return fmt.Errorf("%w: ACL action must be allow or deny", ErrInvalidArgument)
	}
	if n.Protocol == "" || n.Destination == "" || n.Ports == "" {
		return fmt.Errorf("%w: ACL rule has empty protocol, destination, or ports", ErrInvalidArgument)
	}
	if n.Destination != "*" {
		if ip := net.ParseIP(n.Destination); ip == nil {
			if _, _, err := net.ParseCIDR(n.Destination); err != nil {
				return fmt.Errorf("%w: invalid ACL destination %q", ErrInvalidArgument, n.Destination)
			}
		}
	}
	return nil
}

func (r ACLRule) key() string {
	n := r.normalized()
	return n.Action + "\x00" + n.Protocol + "\x00" + n.Destination + "\x00" + n.Ports
}

// Grant is the complete authorization outcome frozen into a ticket. Principal
// is always the certificate-derived identity, never the outer EAP identity.
type Grant struct {
	Principal string
	DomainID  string
	VLAN      uint16
	ACL       []ACLRule
}

func (g Grant) normalized() (Grant, error) {
	out := Grant{
		Principal: strings.TrimSpace(g.Principal),
		DomainID:  strings.TrimSpace(g.DomainID),
		VLAN:      g.VLAN,
		ACL:       make([]ACLRule, 0, len(g.ACL)),
	}
	for _, rule := range g.ACL {
		if err := rule.validate(); err != nil {
			return Grant{}, err
		}
		out.ACL = append(out.ACL, rule.normalized())
	}
	sort.Slice(out.ACL, func(i, j int) bool {
		return out.ACL[i].key() < out.ACL[j].key()
	})
	out.ACL = dedupeACL(out.ACL)
	return out, nil
}

func (g Grant) clone() Grant {
	out := g
	if g.ACL != nil {
		out.ACL = append([]ACLRule(nil), g.ACL...)
	}
	return out
}

// TLSState is the TLS/EAP-TLS evidence supplied by the TLS terminator. The
// terminator is responsible for setting Finished only after the handshake
// Finished message has been received and for verifying ChannelBinding.
//
// MSKIdentity identifies the exported EAP-TLS MSK/resumption PSK and must stay
// stable across a TLS resumption. It is an identity, not the raw key.
//
// On a TLS 1.3 PSK resumption, PSKIdentity must be the ticket ID (raw 16
// bytes). For a fast BSS transition that keeps the same TLS channel, Resumed is
// false and the channel binding must equal the value frozen in the ticket.
type TLSState struct {
	Version     uint16
	CipherSuite uint16

	Resumed   bool
	EarlyData bool
	Finished  bool

	ChannelBindingType     string
	ChannelBinding         []byte
	ChannelBindingVerified bool

	MSKIdentity []byte
	PSKIdentity []byte
}

func (t TLSState) bindingCopy() []byte {
	return append([]byte(nil), t.ChannelBinding...)
}

func (t TLSState) mskCopy() []byte {
	return append([]byte(nil), t.MSKIdentity...)
}

// IsAnonymousIdentity reports whether an EAP identity is an anonymous
// placeholder and therefore unsuitable as an authorization principal.
func IsAnonymousIdentity(identity string) bool {
	s := strings.ToLower(strings.TrimSpace(identity))
	s = strings.TrimPrefix(s, "<")
	if i := strings.IndexByte(s, '>'); i >= 0 {
		s = s[:i]
	}
	local := s
	if i := strings.IndexByte(local, '@'); i >= 0 {
		local = local[:i]
	}
	switch local {
	case "", "anonymous", "anon", "anonymous-user", "anonymous_user":
		return true
	default:
		return false
	}
}

func normalizedStrings(in []string, fold bool) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if fold {
			s = strings.ToLower(s)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func normalizedUint16s(in []uint16) []uint16 {
	out := append([]uint16(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) == 0 {
		return nil
	}
	deduped := out[:1]
	for _, v := range out[1:] {
		if v != deduped[len(deduped)-1] {
			deduped = append(deduped, v)
		}
	}
	return deduped
}

func dedupeACL(in []ACLRule) []ACLRule {
	if len(in) == 0 {
		return nil
	}
	out := in[:1]
	for _, rule := range in[1:] {
		if rule.key() != out[len(out)-1].key() {
			out = append(out, rule)
		}
	}
	return out
}

func containsUint16(haystack []uint16, needle uint16) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func allUint16Subset(a, b []uint16) bool {
	for _, v := range a {
		if !containsUint16(b, v) {
			return false
		}
	}
	return true
}

func allStringsSubset(a, b []string) bool {
	for _, v := range a {
		found := false
		for _, candidate := range b {
			if v == candidate {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func cloneTime(t time.Time) time.Time {
	return t.UTC()
}
