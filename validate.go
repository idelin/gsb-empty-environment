package netsec

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// validateServer is the op label used when the server URL itself is rejected.
const validateServer = "validate.server"

// normalizeCapabilities returns the effective capability set of a host.
//
// An empty declaration is the containerd default: pull, resolve and push.
// Unknown or duplicated capabilities produce an error so that a typo cannot
// silently broaden or narrow what a mirror is trusted to do.
func normalizeCapabilities(declared []string) (map[string]bool, error) {
	caps := make(map[string]bool)
	if len(declared) == 0 {
		for _, capName := range []string{CapPull, CapResolve, CapPush} {
			caps[capName] = true
		}
		return caps, nil
	}
	for _, capName := range declared {
		switch capName {
		case CapPull, CapResolve, CapPush:
		default:
			return nil, errors.New("unknown capability " + capName)
		}
		if caps[capName] {
			return nil, errors.New("duplicate capability " + capName)
		}
		caps[capName] = true
	}
	return caps, nil
}

// canonicalEndpoint parses an endpoint (full URL or bare host:port) and
// returns its scheme plus a canonical host:port key.
//
// Canonicalization lower-cases the host, strips every trailing dot and
// applies the default port for the scheme so that equivalent spellings such
// as "HTTPS://Example.COM:443.", "example.com" and "example.com." compare
// equal.
func canonicalEndpoint(raw string) (scheme, canonical string, err error) {
	if strings.TrimSpace(raw) == "" {
		return "", "", errors.New("empty endpoint URL")
	}

	parsed := raw
	scheme = ""
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme = strings.ToLower(raw[:i])
	} else {
		parsed = "//" + raw
	}

	u, parseErr := url.Parse(parsed)
	if parseErr != nil {
		return "", "", parseErr
	}
	if u.Host == "" {
		return "", "", errors.New("endpoint has no host")
	}
	if u.User != nil {
		return "", "", errors.New("userinfo is not permitted in endpoint URLs")
	}

	switch scheme {
	case "":
		// Bare host:port input: treat it as https, the secure default.
		scheme = "https"
	case "http", "https":
	default:
		return "", "", errors.New("unsupported scheme " + scheme)
	}

	host := strings.ToLower(u.Hostname())
	host = strings.TrimRight(host, ".")
	if host == "" {
		return "", "", errors.New("endpoint has no host")
	}

	port := u.Port()
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return scheme, net.JoinHostPort(host, port), nil
}

// isLoopback reports whether canonical host:port points at a loopback
// interface. The .localhost suffix and localhost name are included per
// RFC 2606 / RFC 6761, alongside IPv4 127.0.0.0/8 and IPv6 ::1.
func isLoopback(canonical string) bool {
	host, _, err := net.SplitHostPort(canonical)
	if err != nil {
		host = canonical
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sameDomain reports whether two canonical host:port keys identify the same
// host. Because canonicalEndpoint already folds case, trailing dots and
// default ports, equality here is the same-domain test.
func sameDomain(a, b string) bool { return a == b }

// Validate checks a parsed hosts.toml Config against the caller policy and
// returns every violation found as joined *ConfigError values. A nil policy
// selects DefaultPolicy.
func Validate(cfg *Config, policy *Policy) error {
	if cfg == nil {
		return errors.New("nil config")
	}
	if policy == nil {
		policy = DefaultPolicy()
	}

	var errs []error

	serverScheme, serverHost, err := canonicalEndpoint(cfg.Server)
	if err != nil {
		errs = append(errs, configError(cfg.Server, validateServer, err))
	} else {
		if violation := checkInsecure(cfg.Server, serverScheme, serverHost, false, policy); violation != nil {
			errs = append(errs, violation)
		}
		errs = append(errs, checkAllowlist(cfg.Server, serverHost, policy)...)
	}

	for idx := range cfg.Hosts {
		host := &cfg.Hosts[idx]
		entryErrs := validateHost(host, serverHost, policy)
		errs = append(errs, entryErrs...)
	}

	return errors.Join(errs...)
}

func validateHost(host *HostEntry, serverHost string, policy *Policy) []error {
	var errs []error

	scheme, canonical, err := canonicalEndpoint(host.URL)
	if err != nil {
		return []error{configError(host.URL, "validate.host", err)}
	}

	if violation := checkInsecure(host.URL, scheme, canonical, host.SkipVerify, policy); violation != nil {
		errs = append(errs, violation)
	}
	if violation := checkAllowlist(host.URL, canonical, policy); violation != nil {
		errs = append(errs, violation...)
	}

	if _, capErr := normalizeCapabilities(host.Capabilities); capErr != nil {
		errs = append(errs, configError(host.URL, "validate.capabilities", capErr))
	}

	if len(host.Header[AuthorizationHeader]) > 0 && !sameDomain(canonical, serverHost) {
		errs = append(errs, configError(host.URL, "validate.credential", ErrCredentialLeak))
	}

	if len(host.Client) != 0 && len(host.Client) != 2 {
		errs = append(errs, configError(host.URL, "validate.client",
			errors.New("client must hold exactly a certificate and a key, or be empty")))
	}

	return errs
}

func checkInsecure(endpoint, scheme, canonical string, skipVerify bool, policy *Policy) error {
	insecure := scheme == "http" || skipVerify
	if !insecure {
		return nil
	}
	if policy.AllowInsecureLoopback && isLoopback(canonical) {
		return nil
	}
	return configError(endpoint, "validate.insecure", ErrInsecureHost)
}

func checkAllowlist(endpoint, canonical string, policy *Policy) []error {
	if len(policy.AllowedHosts) == 0 {
		return nil
	}
	for _, allowed := range policy.AllowedHosts {
		_, allowedHost, err := canonicalEndpoint(allowed)
		if err != nil {
			continue
		}
		if sameDomain(canonical, allowedHost) {
			return nil
		}
	}
	return []error{configError(endpoint, "validate.allowlist",
		errors.New("endpoint is not in the policy allowlist"))}
}

// pullHosts returns the entries that may serve blob pulls in declared order.
// Hosts whose effective capability set lacks pull (i.e. resolve-only
// mirrors) are skipped.
func pullHosts(cfg *Config) ([]HostEntry, error) {
	var candidates []HostEntry
	for _, host := range cfg.Hosts {
		caps, err := normalizeCapabilities(host.Capabilities)
		if err != nil {
			return nil, configError(host.URL, "validate.capabilities", err)
		}
		if caps[CapPull] {
			candidates = append(candidates, host)
		}
	}
	return candidates, nil
}
