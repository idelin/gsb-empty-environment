package netsec

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors returned by Validate. The returned error may wrap more
// than one of these; use errors.Is to inspect individual violations.
var (
	ErrPlaintext         = errors.New("insecure host: http scheme or skip_verify is forbidden outside loopback")
	ErrCredentialLeak    = errors.New("Authorization header may only be sent to a host in the same domain as server")
	ErrResolveOnlyPull   = errors.New("resolve-only host must not provide the pull capability")
	ErrInvalidCapability = errors.New("invalid capability")
	ErrDuplicateHost     = errors.New("duplicate host entry")
	ErrInvalidConfig     = errors.New("invalid hosts.toml configuration")
)

// ValidationError describes one admission violation for a single host entry
// (or for the whole configuration when Host is empty).
type ValidationError struct {
	Host string
	Err  error
}

func (e *ValidationError) Error() string {
	if e.Host == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("host %q: %v", e.Host, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

// JoinErrors combines all admission violations. The result supports
// errors.Is against the individual wrapped sentinel errors.
func JoinErrors(errs ...error) error {
	return errors.Join(errs...)
}

// Validate runs the admission checks on a parsed hosts.toml Config against
// the caller Policy. It returns nil when the configuration is safe to write.
//
// Rules enforced:
//   - http scheme or skip_verify is rejected unless the host is loopback and
//     the policy allows loopback exceptions;
//   - an Authorization header is only accepted on hosts in the same domain
//     (normalized host name and port) as server;
//   - hosts listed in Policy.ResolveOnlyHosts must not carry pull;
//   - capability values are restricted to pull/resolve/push;
//   - CA and client TLS material references must be present when set;
//   - duplicate normalized host entries are rejected.
func Validate(cfg *Config, policy Policy) error {
	if cfg == nil {
		return &ValidationError{Err: fmt.Errorf("%w: nil config", ErrInvalidConfig)}
	}
	server, err := parseEndpoint(cfg.Server)
	if err != nil {
		return &ValidationError{Err: fmt.Errorf("%w: server: %v", ErrInvalidConfig, err)}
	}

	resolveOnly := make(map[string]bool, len(policy.ResolveOnlyHosts))
	for _, ref := range policy.ResolveOnlyHosts {
		key, err := canonicalKey(ref)
		if err != nil {
			return &ValidationError{Err: fmt.Errorf("%w: policy resolve-only host %q: %v", ErrInvalidConfig, ref, err)}
		}
		resolveOnly[key] = true
	}

	var violations []error
	seen := make(map[string]bool, len(cfg.Hosts))
	for i := range cfg.Hosts {
		host := &cfg.Hosts[i]
		ep, err := parseEndpoint(host.Host)
		if err != nil {
			violations = appendHostError(violations, host.Host, fmt.Errorf("%w: %v", ErrInvalidConfig, err))
			continue
		}

		if seen[ep.host] {
			violations = appendHostError(violations, host.Host, fmt.Errorf("%w: %s", ErrDuplicateHost, ep.host))
		}
		seen[ep.host] = true

		loopbackExempt := policy.AllowInsecureLoopback && ep.isLoopback()
		if !loopbackExempt && (ep.scheme == "http" || host.SkipVerify) {
			violations = appendHostError(violations, host.Host, ErrPlaintext)
		}

		if hasAuthorization(host.Header) && !sameDomain(server, ep) {
			violations = appendHostError(violations, host.Host, ErrCredentialLeak)
		}

		if resolveOnly[ep.host] && hasCapability(host.Capabilities, CapPull) {
			violations = appendHostError(violations, host.Host, ErrResolveOnlyPull)
		}

		if err := validateCapabilities(host.Capabilities); err != nil {
			violations = appendHostError(violations, host.Host, err)
		}

		for _, caPath := range host.CA {
			if caPath == "" {
				violations = appendHostError(violations, host.Host, fmt.Errorf("%w: empty ca path", ErrInvalidConfig))
			}
		}
		if host.Client != nil {
			if host.Client.Cert == "" || host.Client.Key == "" {
				violations = appendHostError(violations, host.Host, fmt.Errorf("%w: client cert/key paths must both be set", ErrInvalidConfig))
			}
		}
	}

	return errors.Join(violations...)
}

func appendHostError(violations []error, host string, err error) []error {
	if err == nil {
		return violations
	}
	return append(violations, &ValidationError{Host: host, Err: err})
}

func hasAuthorization(header http.Header) bool {
	if header == nil {
		return false
	}
	for name := range header {
		if http.CanonicalHeaderKey(name) == "Authorization" {
			return true
		}
	}
	return false
}

func hasCapability(caps []string, want string) bool {
	for _, capName := range caps {
		if capName == want {
			return true
		}
	}
	return false
}

func validateCapabilities(caps []string) error {
	seen := make(map[string]bool, len(caps))
	for _, capName := range caps {
		switch capName {
		case CapPull, CapResolve, CapPush:
		default:
			return fmt.Errorf("%w: %q", ErrInvalidCapability, capName)
		}
		if seen[capName] {
			return fmt.Errorf("%w: duplicate %q", ErrInvalidCapability, capName)
		}
		seen[capName] = true
	}
	return nil
}
