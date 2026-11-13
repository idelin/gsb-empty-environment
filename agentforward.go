package netsec

import (
	"errors"
	"fmt"
)

// ExtNoAgentForwarding is the OpenSSH certificate extension that forbids
// SSH agent forwarding for sessions authenticated with the certificate.
//
// Authorization is presence based, matching OpenSSH semantics: an entry in
// the certificate's Extensions map denies forwarding even when its value is
// the empty string.
const ExtNoAgentForwarding = "no-agent-forwarding"

// ErrAgentForwardingDenied is the sentinel error returned whenever an agent
// forwarding request must not be honored, regardless of the concrete reason.
var ErrAgentForwardingDenied = errors.New("ssh: agent forwarding denied")

// serviceConfigError reports that the service configuration does not permit
// agent forwarding. Its zero value is exposed through
// ErrAgentForwardingDisabled.
type serviceConfigError struct{}

func (serviceConfigError) Error() string {
	return "ssh: agent forwarding disabled by service configuration"
}

// Is matches ErrAgentForwardingDenied so callers can treat every denial
// uniformly through errors.Is.
func (serviceConfigError) Is(target error) bool {
	return target == ErrAgentForwardingDenied
}

// ErrAgentForwardingDisabled reports that the service configuration does not
// permit agent forwarding.
var ErrAgentForwardingDisabled error = serviceConfigError{}

// CertificateExtensionError reports that agent forwarding is forbidden by a
// certificate extension.
type CertificateExtensionError struct {
	// Extension is the certificate extension that caused the denial.
	Extension string
}

func (e *CertificateExtensionError) Error() string {
	return fmt.Sprintf("ssh: agent forwarding forbidden by certificate extension %q", e.Extension)
}

// Is matches ErrAgentForwardingDenied so callers can treat every denial
// uniformly through errors.Is.
func (e *CertificateExtensionError) Is(target error) bool {
	return target == ErrAgentForwardingDenied
}

// ServiceConfig holds the SSH service's own policy. Service policy is only
// consulted when the authenticated certificate does not forbid forwarding.
type ServiceConfig struct {
	// AllowAgentForwarding enables agent forwarding for sessions whose
	// certificate does not carry the no-agent-forwarding extension.
	AllowAgentForwarding bool
}

// AuthorizeAgentForwarding decides whether the SSH service may honor an
// auth-agent-req request for a session authenticated with a certificate whose
// extensions are given.
//
// The decision is:
//
//   - When the certificate carries the no-agent-forwarding extension, the
//     request is denied regardless of the service configuration.
//   - Otherwise the service configuration decides: the request is allowed
//     only when AllowAgentForwarding is set.
//
// The function is stateless and must be invoked for every agent forwarding
// request received on a connection, including repeated requests on the same
// session. A nil error means the request may be accepted.
func AuthorizeAgentForwarding(extensions map[string]string, cfg ServiceConfig) error {
	if _, present := extensions[ExtNoAgentForwarding]; present {
		return &CertificateExtensionError{Extension: ExtNoAgentForwarding}
	}
	if !cfg.AllowAgentForwarding {
		return ErrAgentForwardingDisabled
	}
	return nil
}

// AgentForwardingReply pairs the authorization decision with the boolean that
// an SSH service should send in its reply to an auth-agent-req channel
// request.
func AgentForwardingReply(extensions map[string]string, cfg ServiceConfig) (accept bool, reason error) {
	if err := AuthorizeAgentForwarding(extensions, cfg); err != nil {
		return false, err
	}
	return true, nil
}
