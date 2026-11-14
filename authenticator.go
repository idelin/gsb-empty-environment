package netsec

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultMaxTicketLifetime = 8 * time.Hour

// AuthenticatorConfig configures an AAA-side EAP-TLS resumption authorizer.
// Keyring, ReplayStore, RevocationChecker, and TrustRoots are required; a
// missing value fails closed.
type AuthenticatorConfig struct {
	Keyring           *Keyring
	ReplayStore       ReplayStore
	RevocationChecker RevocationChecker
	TrustRoots        *x509.CertPool
	Clock             func() time.Time

	// MaxTicketLifetime caps the absolute lifetime of a ticket. Zero uses
	// eight hours. Rotation never extends an existing absolute expiry.
	MaxTicketLifetime time.Duration
}

// Authenticator issues and resumes EAP-TLS authorization tickets. It is safe
// for concurrent use when the configured components are safe for concurrent
// use (the provided memory implementations are).
type Authenticator struct {
	keyring     *Keyring
	replay      ReplayStore
	revocations RevocationChecker
	roots       *x509.CertPool
	now         func() time.Time
	maxLifetime time.Duration
}

func NewAuthenticator(cfg AuthenticatorConfig) (*Authenticator, error) {
	if cfg.Keyring == nil {
		return nil, fmt.Errorf("%w: keyring is required", ErrInvalidArgument)
	}
	if cfg.ReplayStore == nil {
		return nil, fmt.Errorf("%w: replay store is required", ErrInvalidArgument)
	}
	if cfg.RevocationChecker == nil {
		return nil, fmt.Errorf("%w: revocation checker is required", ErrInvalidArgument)
	}
	if cfg.TrustRoots == nil {
		return nil, fmt.Errorf("%w: trust roots are required", ErrInvalidArgument)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	maxLifetime := cfg.MaxTicketLifetime
	if maxLifetime == 0 {
		maxLifetime = defaultMaxTicketLifetime
	}
	if maxLifetime < 0 {
		return nil, fmt.Errorf("%w: maximum ticket lifetime must be positive", ErrInvalidArgument)
	}
	return &Authenticator{
		keyring:     cfg.Keyring,
		replay:      cfg.ReplayStore,
		revocations: cfg.RevocationChecker,
		roots:       cfg.TrustRoots,
		now:         clock,
		maxLifetime: maxLifetime,
	}, nil
}

// IssueRequest describes a completed initial EAP-TLS authentication. The AAA
// service must populate it from the authoritative RADIUS/TLS session context,
// not from unauthenticated controller input.
type IssueRequest struct {
	OuterEAPIdentity      string
	InnerEAPIdentity      string
	ServerVirtualIdentity string

	AccessPoint AccessPoint
	Policy      Policy

	ClientCertificateChain   [][]byte
	CertValidationGeneration uint64

	TLS TLSState

	RequestedVLAN uint16
	ACL           []ACLRule

	// Lifetime is the requested absolute lifetime. Zero uses the
	// authenticator maximum. Values above the maximum are rejected.
	Lifetime time.Duration
}

// Issued is the result of a successful initial authorization.
type Issued struct {
	Ticket Ticket
	Grant  Grant
}

// ResumeMode selects whether a successful resume consumes the ticket
// permanently or atomically rotates it to a fresh single-use ticket.
type ResumeMode int

const (
	// ResumeRotate consumes the presented ticket and returns a replacement
	// with a new ID and the same absolute expiry.
	ResumeRotate ResumeMode = iota
	// ResumeConsume permanently consumes the presented ticket.
	ResumeConsume
)

// ResumeRequest describes a resumption attempt. EarlyData and Finished are
// supplied by the TLS terminator; only Finished=true is eligible for
// authorization side effects.
type ResumeRequest struct {
	Ticket []byte

	OuterEAPIdentity      string
	InnerEAPIdentity      string
	ServerVirtualIdentity string

	AccessPoint AccessPoint
	Policy      Policy

	ClientCertificateChain   [][]byte
	CertValidationGeneration uint64

	TLS TLSState

	Mode ResumeMode
}

// Resumed is the result of a successful resumption. Ticket is nil when
// ResumeConsume was selected.
type Resumed struct {
	Grant     Grant
	Ticket    *Ticket
	ExpiresAt time.Time
}

// Issue freezes an authenticated session into an AEAD ticket. It rejects
// early data, incomplete handshakes, cross-domain access points, anonymous
// principals, revoked certificates, and policy violations.
func (a *Authenticator) Issue(ctx context.Context, req IssueRequest) (*Issued, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: nil authenticator", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := a.now().UTC()

	if req.TLS.EarlyData {
		return nil, ErrEarlyDataNotAuthorized
	}
	if !req.TLS.Finished {
		return nil, ErrHandshakeIncomplete
	}
	if req.TLS.Resumed {
		return nil, fmt.Errorf("%w: initial authentication must not be TLS-resumed", ErrInvalidArgument)
	}

	policy := req.Policy.normalized()
	if err := policy.validate(); err != nil {
		return nil, err
	}
	ap := req.AccessPoint.normalized()
	if err := ap.validate(); err != nil {
		return nil, err
	}
	if ap.DomainID != policy.DomainID || ap.SSID != policy.SSID {
		return nil, fmt.Errorf("%w: access point and policy belong to different domains", ErrPolicyDomainMismatch)
	}
	if !policy.permitsAP(ap) {
		return nil, ErrAccessPointNotAllowed
	}

	server := strings.TrimSpace(req.ServerVirtualIdentity)
	if server == "" || !policy.permitsServer(server) {
		return nil, fmt.Errorf("%w: server virtual identity is not permitted by policy", ErrServerIdentityMismatch)
	}
	if !policy.permitsTLS(req.TLS.Version, req.TLS.CipherSuite) {
		return nil, ErrTLSBindingMismatch
	}
	if err := validateInitialTLS(req.TLS); err != nil {
		return nil, err
	}

	chain, err := verifyClientChain(req.ClientCertificateChain, a.roots, now)
	if err != nil {
		return nil, err
	}
	inner, err := bindIdentity(req.OuterEAPIdentity, req.InnerEAPIdentity, chain)
	if err != nil {
		return nil, err
	}

	revocation, err := a.checkRevocation(ctx, chain, now)
	if err != nil {
		return nil, err
	}
	if revocation.Revoked {
		return nil, fmt.Errorf("%w: %s", ErrCertificateRevoked, revocation.Reason)
	}

	grant, err := Grant{
		Principal: chain.identity,
		DomainID:  policy.DomainID,
		VLAN:      req.RequestedVLAN,
		ACL:       req.ACL,
	}.normalized()
	if err != nil {
		return nil, err
	}
	if !policy.permitsGrant(grant) {
		return nil, fmt.Errorf("%w: VLAN %d", ErrGrantNotPermitted, grant.VLAN)
	}

	lifetime := req.Lifetime
	if lifetime == 0 {
		lifetime = a.maxLifetime
	}
	if lifetime <= 0 || lifetime > a.maxLifetime {
		return nil, fmt.Errorf("%w: ticket lifetime must be within (0, %s]", ErrInvalidArgument, a.maxLifetime)
	}

	payload := ticketPayload{
		FormatVersion:            ticketFormatVersion,
		IssuedAt:                 now,
		ExpiresAt:                now.Add(lifetime),
		OuterEAPIdentity:         strings.TrimSpace(req.OuterEAPIdentity),
		InnerIdentity:            inner,
		CertificateIdentity:      chain.identity,
		CertificateSubject:       chain.subject,
		ServerVirtualIdentity:    server,
		AccessPoint:              ap,
		Policy:                   policy,
		PolicyFingerprint:        policyFingerprint(policy),
		ClientLeafFingerprint:    chain.leafFingerprint,
		ClientChainFingerprint:   chain.chainFingerprint,
		CertValidationGeneration: req.CertValidationGeneration,
		RevocationEpoch:          revocation.Epoch,
		TLSVersion:               req.TLS.Version,
		TLSCipherSuite:           req.TLS.CipherSuite,
		MSKIdentity:              req.TLS.mskCopy(),
		ChannelBindingType:       strings.TrimSpace(req.TLS.ChannelBindingType),
		ChannelBinding:           req.TLS.bindingCopy(),
		Grant:                    grant,
	}
	ticket, err := sealTicket(payload, a.keyring, nil)
	if err != nil {
		return nil, err
	}
	return &Issued{Ticket: ticket, Grant: grant.clone()}, nil
}

// Resume validates a ticket against the current session and atomically consumes
// or rotates it only after the handshake Finished message has arrived. Any
// failure leaves the ticket untouched.
func (a *Authenticator) Resume(ctx context.Context, req ResumeRequest) (*Resumed, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: nil authenticator", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.Ticket) == 0 {
		return nil, fmt.Errorf("%w: empty ticket", ErrInvalidArgument)
	}
	if req.Mode != ResumeRotate && req.Mode != ResumeConsume {
		return nil, fmt.Errorf("%w: unknown resume mode", ErrInvalidArgument)
	}

	// No authorization side effect, including replay-store mutation, may
	// happen for 0-RTT/early-data input.
	if req.TLS.EarlyData {
		return nil, ErrEarlyDataNotAuthorized
	}
	if !req.TLS.Finished {
		return nil, ErrHandshakeIncomplete
	}

	now := a.now().UTC()
	payload, err := openTicket(req.Ticket, a.keyring, now, a.maxLifetime)
	if err != nil {
		return nil, err
	}
	ticketID, err := parseTicketID(payload.TicketID)
	if err != nil {
		return nil, err
	}

	policy := req.Policy.normalized()
	if err := policy.validate(); err != nil {
		return nil, err
	}
	ap := req.AccessPoint.normalized()
	if err := ap.validate(); err != nil {
		return nil, err
	}
	if err := checkResumeDomain(payload, ap, policy); err != nil {
		return nil, err
	}
	if err := policyChangeError(payload.Policy, policy); err != nil {
		return nil, fmt.Errorf("%w: issued revision %d, current revision %d", err, payload.Policy.Revision, policy.Revision)
	}

	server := strings.TrimSpace(req.ServerVirtualIdentity)
	if !secureEqual([]byte(payload.ServerVirtualIdentity), []byte(server)) {
		return nil, ErrServerIdentityMismatch
	}
	if !policy.permitsServer(server) {
		return nil, fmt.Errorf("%w: current policy no longer permits the server identity", ErrServerIdentityMismatch)
	}
	if !policy.permitsTLS(payload.TLSVersion, payload.TLSCipherSuite) {
		return nil, ErrTLSBindingMismatch
	}
	if !policy.permitsGrant(payload.Grant) {
		return nil, fmt.Errorf("%w: VLAN %d", ErrGrantNotPermitted, payload.Grant.VLAN)
	}

	chain, err := verifyClientChain(req.ClientCertificateChain, a.roots, now)
	if err != nil {
		return nil, err
	}
	if chain.leafFingerprint != payload.ClientLeafFingerprint || chain.chainFingerprint != payload.ClientChainFingerprint {
		return nil, ErrCertificateMismatch
	}
	if req.CertValidationGeneration != payload.CertValidationGeneration {
		return nil, ErrCertificatePolicyChanged
	}
	inner, err := bindIdentity(req.OuterEAPIdentity, req.InnerEAPIdentity, chain)
	if err != nil {
		return nil, err
	}
	if !secureEqual([]byte(payload.OuterEAPIdentity), []byte(strings.TrimSpace(req.OuterEAPIdentity))) {
		return nil, ErrIdentityMismatch
	}
	if !secureEqual([]byte(payload.InnerIdentity), []byte(inner)) {
		return nil, ErrIdentityMismatch
	}
	if !secureEqual([]byte(payload.CertificateIdentity), []byte(chain.identity)) {
		return nil, ErrIdentityMismatch
	}
	if !secureEqual([]byte(payload.Grant.Principal), []byte(chain.identity)) {
		return nil, ErrIdentityMismatch
	}

	revocation, err := a.checkRevocation(ctx, chain, now)
	if err != nil {
		return nil, err
	}
	if revocation.Revoked {
		return nil, fmt.Errorf("%w: %s", ErrCertificateRevoked, revocation.Reason)
	}
	if revocation.Epoch != payload.RevocationEpoch {
		return nil, fmt.Errorf("%w: revocation epoch changed from %d to %d", ErrCertificateRevoked, payload.RevocationEpoch, revocation.Epoch)
	}

	if err := validateResumeTLS(payload, req.TLS, ticketID); err != nil {
		return nil, err
	}

	grant := payload.Grant.clone()
	resumed := &Resumed{Grant: grant, ExpiresAt: payload.ExpiresAt}

	if req.Mode == ResumeRotate {
		rotated := payload
		rotated.IssuedAt = now
		rotated.ExpiresAt = payload.ExpiresAt
		rotated.AccessPoint = ap
		rotated.Policy = policy
		rotated.PolicyFingerprint = policyFingerprint(policy)
		rotated.ChannelBindingType = strings.TrimSpace(req.TLS.ChannelBindingType)
		rotated.ChannelBinding = req.TLS.bindingCopy()
		rotated.Grant = grant.clone()

		ticket, err := sealTicket(rotated, a.keyring, nil)
		if err != nil {
			return nil, err
		}
		resumed.Ticket = &ticket
	}

	if err := a.replay.Consume(ctx, ticketID, now, payload.ExpiresAt); err != nil {
		if errors.Is(err, ErrTicketReplayed) {
			return nil, ErrTicketReplayed
		}
		return nil, fmt.Errorf("netsec: consume ticket: %w", err)
	}
	return resumed, nil
}

func checkResumeDomain(payload ticketPayload, ap AccessPoint, policy Policy) error {
	if ap.DomainID != payload.AccessPoint.DomainID ||
		ap.DomainID != payload.Policy.DomainID ||
		policy.DomainID != payload.Policy.DomainID ||
		ap.DomainID != policy.DomainID ||
		ap.SSID != payload.AccessPoint.SSID ||
		policy.SSID != payload.Policy.SSID {
		return fmt.Errorf("%w: ticket=%s/%s, request=%s/%s", ErrPolicyDomainMismatch,
			payload.AccessPoint.DomainID, payload.AccessPoint.SSID, ap.DomainID, ap.SSID)
	}
	if !policy.permitsAP(ap) {
		return fmt.Errorf("%w: %s", ErrAccessPointNotAllowed, ap.BSSID)
	}
	return nil
}

func validateInitialTLS(tls TLSState) error {
	if !tls.ChannelBindingVerified {
		return ErrChannelBindingInvalid
	}
	if strings.TrimSpace(tls.ChannelBindingType) == "" || !validBindingValue(tls.ChannelBinding) {
		return ErrChannelBindingInvalid
	}
	if !validBindingValue(tls.MSKIdentity) {
		return fmt.Errorf("%w: MSK identity is empty or malformed", ErrInvalidArgument)
	}
	return nil
}

func validateResumeTLS(payload ticketPayload, tls TLSState, id TicketID) error {
	if tls.Version != payload.TLSVersion || tls.CipherSuite != payload.TLSCipherSuite {
		return fmt.Errorf("%w: ticket=%#x/%#x, request=%#x/%#x", ErrTLSBindingMismatch,
			payload.TLSVersion, payload.TLSCipherSuite, tls.Version, tls.CipherSuite)
	}
	if !tls.ChannelBindingVerified {
		return ErrChannelBindingInvalid
	}
	if strings.TrimSpace(tls.ChannelBindingType) == "" || !validBindingValue(tls.ChannelBinding) {
		return ErrChannelBindingInvalid
	}
	if !secureEqual([]byte(payload.ChannelBindingType), []byte(strings.TrimSpace(tls.ChannelBindingType))) {
		return fmt.Errorf("%w: channel binding type changed", ErrChannelBindingInvalid)
	}
	if !secureEqual(payload.MSKIdentity, tls.MSKIdentity) {
		return ErrMSKIdentityMismatch
	}
	if tls.Resumed {
		if !pskIdentityMatches(tls.PSKIdentity, id) {
			return fmt.Errorf("%w: TLS PSK identity does not match the ticket", ErrChannelBindingInvalid)
		}
		return nil
	}
	// Fast BSS transition on the same TLS channel: the exporter/channel
	// binding must be unchanged.
	if !bytes.Equal(payload.ChannelBinding, tls.ChannelBinding) {
		return fmt.Errorf("%w: same-session channel binding changed", ErrChannelBindingInvalid)
	}
	return nil
}

func pskIdentityMatches(psk []byte, id TicketID) bool {
	if len(psk) == 0 {
		return false
	}
	if secureEqual(psk, id[:]) {
		return true
	}
	return secureEqual(psk, []byte(id.String()))
}

func bindIdentity(outer, inner string, chain *verifiedClientChain) (string, error) {
	if chain == nil {
		return "", ErrCertificateInvalid
	}
	outer = strings.TrimSpace(outer)
	if outer == "" {
		return "", fmt.Errorf("%w: empty outer EAP identity", ErrIdentityMismatch)
	}
	certIdentity := strings.TrimSpace(chain.identity)
	if certIdentity == "" || IsAnonymousIdentity(certIdentity) {
		return "", fmt.Errorf("%w: certificate has no usable subject identity", ErrAnonymousIdentity)
	}

	inner = strings.TrimSpace(inner)
	if inner == "" {
		inner = certIdentity
	}
	if IsAnonymousIdentity(inner) {
		return "", ErrAnonymousIdentity
	}
	if !secureEqual([]byte(inner), []byte(certIdentity)) {
		return "", ErrIdentityMismatch
	}
	return inner, nil
}

func (a *Authenticator) checkRevocation(ctx context.Context, chain *verifiedClientChain, now time.Time) (RevocationStatus, error) {
	status, err := a.revocations.Check(ctx, chain.certs, now)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return RevocationStatus{}, err
		}
		return RevocationStatus{}, fmt.Errorf("%w: %v", ErrRevocationUnavailable, err)
	}
	return status, nil
}

func secureEqual(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}
