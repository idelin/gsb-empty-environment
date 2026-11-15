package netsec

import "net/http"

// Capability strings understood by containerd hosts.toml.
const (
	CapPull    = "pull"
	CapResolve = "resolve"
	CapPush    = "push"
)

// AuthorizationHeader is the only credential header this package enforces
// same-domain scoping for. http.Header canonicalizes the key automatically.
const AuthorizationHeader = "Authorization"

// Config is the parsed representation of a single registry hosts.toml file.
//
// It mirrors the containerd certs.d layout: Server is the upstream registry
// URL and Hosts holds the ordered [host."..."] tables. Host order expresses
// mirror priority and is preserved when pull candidates are selected.
type Config struct {
	// Server is the upstream registry URL, e.g. https://registry-1.docker.io
	Server string
	// Hosts are the parsed [host."..."] entries in declaration order.
	Hosts []HostEntry
}

// HostEntry is one [host."..."] table of a hosts.toml file.
type HostEntry struct {
	// URL is the host endpoint URL used as the TOML table key, e.g.
	// https://mirror.example.com/v1.
	URL string
	// Capabilities is the capabilities array. An empty slice means the
	// containerd default of pull/resolve/push, matching containerd behavior.
	Capabilities []string
	// SkipVerify maps to skip_verify and disables TLS certificate checking.
	SkipVerify bool
	// CA maps to ca: the CA certificate bundle used for this host.
	CA string
	// Client maps to client: the [cert, key] pair for mTLS.
	Client []string
	// Header maps to the [host."...".header] table.
	Header http.Header
	// OverridePath maps to override_path.
	OverridePath bool
}

// Policy is the caller-supplied admission policy evaluated together with the
// parsed configuration before it is written to disk.
type Policy struct {
	// AllowInsecureLoopback permits http:// URLs and SkipVerify on loopback
	// hosts (localhost, 127.0.0.0/8, ::1, .localhost). Default policy allows
	// it; set to false to reject every insecure entry without exception.
	AllowInsecureLoopback bool

	// AllowedHosts, when non-empty, is an allowlist of endpoints that both
	// Server and every host URL must belong to. Entries may be plain
	// host:port values or full URLs; they are canonicalized the same way as
	// the configuration under check.
	AllowedHosts []string

	// AllowedDigestAlgorithms restricts the digest algorithms accepted by
	// digest-verified pulls. Empty means the default set (sha256, sha512).
	AllowedDigestAlgorithms []string
}

// DefaultPolicy returns the policy used when Validate or PullVerified is
// called with a nil policy: loopback insecure entries are tolerated and
// sha256/sha512 digests are accepted.
func DefaultPolicy() *Policy {
	return &Policy{
		AllowInsecureLoopback:   true,
		AllowedDigestAlgorithms: []string{"sha256", "sha512"},
	}
}

func (p *Policy) digestAlgorithms() []string {
	if p != nil && len(p.AllowedDigestAlgorithms) > 0 {
		return p.AllowedDigestAlgorithms
	}
	return DefaultPolicy().AllowedDigestAlgorithms
}
