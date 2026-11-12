package netsec

// Capability mirrors a capability entry of a containerd hosts.toml
// host table.
type Capability string

const (
	// CapabilityResolve allows the host to resolve image names to
	// descriptors.
	CapabilityResolve Capability = "resolve"
	// CapabilityPull allows the host to serve blob/manifest pulls.
	CapabilityPull Capability = "pull"
	// CapabilityPush allows the host to receive pushes.
	CapabilityPush Capability = "push"
)

// ClientCredential holds the mTLS cert/key file paths configured on a
// host entry.
type ClientCredential struct {
	Cert string
	Key  string
}

// HostEntry is one parsed [host."..."] table from a hosts.toml file.
type HostEntry struct {
	// Host is the endpoint URL of the mirror or origin host.
	Host string
	// Capabilities is the decoded capabilities list. An empty list means
	// containerd's default of all capabilities, including pull.
	Capabilities []Capability
	// SkipVerify disables TLS certificate verification when true.
	SkipVerify bool
	// CA is the path to a CA certificate bundle.
	CA string
	// Client holds optional mTLS cert/key file paths.
	Client *ClientCredential
	// Header holds the static request headers sent to this host.
	Header map[string]string
	// OverridePath mirrors the hosts.toml override_path flag.
	OverridePath bool
}

// Config is the parsed content of a certs.d/<server>/hosts.toml file.
type Config struct {
	// Server is the registry endpoint the hosts.toml file is registered
	// for.
	Server string
	// Hosts is the ordered host table, mirror entries first.
	Hosts []HostEntry
}

// Policy is supplied by the configuration service when calling Validate
// and controls the loopback exceptions to the transport-security
// requirements.
type Policy struct {
	// AllowLoopbackHTTP permits plaintext http endpoints that resolve to
	// a loopback address.
	AllowLoopbackHTTP bool
	// AllowLoopbackSkipVerify permits skip_verify=true on endpoints that
	// resolve to a loopback address.
	AllowLoopbackSkipVerify bool
}

// DefaultPolicy allows the loopback exceptions used by single-node test
// registries but still forbids plaintext HTTP and unverified TLS on
// every non-loopback endpoint.
func DefaultPolicy() Policy {
	return Policy{
		AllowLoopbackHTTP:       true,
		AllowLoopbackSkipVerify: true,
	}
}
