package netsec

import "net/http"

// Capability values supported in a hosts.toml host entry.
const (
	CapPull    = "pull"
	CapResolve = "resolve"
	CapPush    = "push"
)

// ClientCert is the mTLS client credential pair referenced by a host entry.
type ClientCert struct {
	Cert string
	Key  string
}

// Host is one parsed [[hosts]] table entry from a certs.d hosts.toml file.
//
// Field names mirror the TOML keys: capabilities, skip_verify, ca, client,
// header and override_path.
type Host struct {
	// Host is the endpoint URL, e.g. "https://mirror.example.com:5000".
	Host         string
	Capabilities []string
	SkipVerify   bool
	CA           []string
	Client       *ClientCert
	Header       http.Header
	OverridePath bool
}

// Config is the parsed hosts.toml structure handed to the admission check
// before the configuration is written to disk.
type Config struct {
	// Server is the registry the hosts.toml file is registered for,
	// e.g. "https://registry.example.com".
	Server string
	// Hosts is the ordered table of mirror/endpoint entries.
	Hosts []Host
}

// Policy carries the caller (node configuration service) controls that are
// not derivable from the parsed configuration itself.
type Policy struct {
	// AllowInsecureLoopback exempts loopback endpoints from the plaintext /
	// skip_verify rejection. Use DefaultPolicy to enable it.
	AllowInsecureLoopback bool

	// ResolveOnlyHosts lists endpoints (host or host:port, normalized with
	// the same rules as same-domain comparison) which the operator
	// designates as resolve-only mirrors. Such endpoints must not carry the
	// pull capability and are skipped when blobs are fetched.
	ResolveOnlyHosts []string
}

// DefaultPolicy returns the policy used by node configuration services:
// loopback endpoints are allowed to use http / skip_verify.
func DefaultPolicy() Policy {
	return Policy{AllowInsecureLoopback: true}
}
