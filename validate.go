package netsec

import (
	"errors"
	"fmt"
)

// Validate checks a parsed hosts.toml config against policy before it is
// written to disk. All discovered problems are joined into a single
// error; per-host problems are wrapped in *HostError.
//
// Rules enforced:
//   - skip_verify=true and plaintext http are rejected on every
//     non-loopback endpoint (the server included), subject to policy's
//     loopback exceptions;
//   - an Authorization header is only accepted on a host whose
//     normalized host identity equals the server's;
//   - endpoint URLs must be absolute http(s) URLs without userinfo;
//   - a client entry must set both cert and key;
//   - capabilities must be among resolve/pull/push.
func Validate(cfg *Config, policy Policy) error {
	if cfg == nil {
		return errors.New("netsec: nil config")
	}

	var problems []error

	serverEP, err := parseEndpoint(cfg.Server)
	if err != nil {
		problems = append(problems, fmt.Errorf("server %q: %w", cfg.Server, err))
	} else if err := checkScheme(serverEP, policy); err != nil {
		problems = append(problems, fmt.Errorf("server %q: %w", cfg.Server, err))
	}

	for i := range cfg.Hosts {
		entry := &cfg.Hosts[i]
		if err := validateHost(entry, policy, serverEP); err != nil {
			problems = append(problems, &HostError{Host: entry.Host, Err: err})
		}
	}

	return errors.Join(problems...)
}

func validateHost(entry *HostEntry, policy Policy, server *endpoint) error {
	ep, err := parseEndpoint(entry.Host)
	if err != nil {
		return err
	}

	var problems []error

	if err := checkScheme(ep, policy); err != nil {
		problems = append(problems, err)
	}

	if entry.SkipVerify {
		if !(policy.AllowLoopbackSkipVerify && IsLoopback(ep.hostname)) {
			problems = append(problems, ErrSkipVerifyNotAllowed)
		}
	}

	if server != nil && hasAuthorization(entry.Header) && ep.normalized != server.normalized {
		problems = append(problems, ErrCredentialLeak)
	}

	if entry.Client != nil && (entry.Client.Cert == "" || entry.Client.Key == "") {
		problems = append(problems, ErrClientCredential)
	}

	for _, capability := range entry.Capabilities {
		switch capability {
		case CapabilityResolve, CapabilityPull, CapabilityPush:
		default:
			problems = append(problems, fmt.Errorf("%w: %q", ErrUnknownCapability, capability))
		}
	}

	return errors.Join(problems...)
}
