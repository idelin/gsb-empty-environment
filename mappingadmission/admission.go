// Package mappingadmission enforces a security policy for UPnP/IGD
// AddPortMapping requests before a mapping is installed into the gateway's
// NAT table.
//
// The policy binds a requested mapping to the real TCP/UDP source of the
// control connection, restricts the usable external ports, forbids permanent
// leases, limits per-client mappings, rejects overwrites of mappings owned by
// another client and validates the HTTP Host header to mitigate DNS rebinding
// attacks from a browser running on a LAN host.
//
// The package has no dependency on any particular SOAP stack: an action
// handler only needs to translate its parsed action arguments into a Request
// and pass the connection's real remote address together with its current
// mapping table and a Config.
package mappingadmission

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// DefaultMaxMappingsPerClient is the per-client quota used when
// Config.MaxMappingsPerClient is left at zero.
const DefaultMaxMappingsPerClient = 16

// Protocol values accepted by the policy. UPnP/IGD only defines TCP and UDP.
const (
	ProtocolTCP = "TCP"
	ProtocolUDP = "UDP"
)

// MappingKey identifies an external endpoint that can only be mapped once,
// regardless of which internal client claims it.
type MappingKey struct {
	ExternalPort int
	Protocol     string
}

// Mapping is a normalized, policy-approved port mapping ready to be installed
// by the gateway's NAT layer.
type Mapping struct {
	// RemoteHost is the canonical text form of the restricted remote peer, or
	// empty when the mapping applies to every remote host. It is never a
	// wildcard or unspecified address.
	RemoteHost string

	ExternalPort int
	Protocol     string
	InternalPort int

	// InternalClient always carries an Unmap-ed address, so IPv4 and
	// IPv4-mapped IPv6 text forms collapse onto the same identity.
	InternalClient netip.Addr

	LeaseDuration uint32
}

// Request holds the raw AddPortMapping action arguments plus the HTTP Host
// header of the control request.
type Request struct {
	RemoteHost     string
	ExternalPort   int
	Protocol       string
	InternalPort   int
	InternalClient string
	LeaseDuration  uint32

	// HostHeader is the raw HTTP Host header, including any port.
	HostHeader string
}

// Config carries caller-supplied policy limits.
type Config struct {
	// MaxLeaseSeconds is the caller-defined upper bound for a lease. It must
	// be greater than zero; zero (permanent) leases are always rejected.
	MaxLeaseSeconds uint32

	// ReservedExternalPorts blocks specific external ports above 1024, such
	// as the gateway's own service ports.
	ReservedExternalPorts map[uint16]struct{}

	// GatewayHosts lists every acceptable HTTP Host value (bare IP literal or
	// hostname, without port) for the UPnP control endpoint. Requests whose
	// Host header normalizes to none of these values are rejected.
	GatewayHosts []string

	// MaxMappingsPerClient defaults to DefaultMaxMappingsPerClient when zero.
	MaxMappingsPerClient int
}

// Table is the caller's current set of installed mappings.
type Table struct {
	Mappings map[MappingKey]Mapping
}

// NewTable returns an empty mapping table.
func NewTable() *Table {
	return &Table{Mappings: make(map[MappingKey]Mapping)}
}

// Install records an approved mapping, replacing any previous entry for the
// same external endpoint only when it belongs to the same internal client.
// Admit already guarantees that condition, so Install is the convenience
// writer used by an action handler after a successful admission check.
func (t *Table) Install(m Mapping) error {
	if t.Mappings == nil {
		t.Mappings = make(map[MappingKey]Mapping)
	}
	key := MappingKey{ExternalPort: m.ExternalPort, Protocol: m.Protocol}
	if existing, ok := t.Mappings[key]; ok && existing.InternalClient != m.InternalClient {
		return fmt.Errorf("mappingadmission: external %d/%s is owned by %s",
			m.ExternalPort, m.Protocol, existing.InternalClient)
	}
	t.Mappings[key] = m
	return nil
}

// Sentinel errors returned by Admit. A SOAP handler maps each one onto the
// appropriate UPnP error code; tests and callers should use errors.Is.
var (
	ErrHostHeaderMismatch     = errors.New("mappingadmission: Host header is not a gateway address")
	ErrSourceAddressInvalid   = errors.New("mappingadmission: connection source address is invalid")
	ErrInternalClientInvalid  = errors.New("mappingadmission: InternalClient is not a valid IP address")
	ErrInternalClientMismatch = errors.New("mappingadmission: InternalClient must match the connection source address")
	ErrUnsupportedProtocol    = errors.New("mappingadmission: only TCP and UDP mappings are allowed")
	ErrExternalPortPrivileged = errors.New("mappingadmission: external ports below 1024 are not allowed")
	ErrExternalPortReserved   = errors.New("mappingadmission: external port is reserved by the gateway")
	ErrExternalPortOutOfRange = errors.New("mappingadmission: external port is outside 1024..65535")
	ErrInternalPortInvalid    = errors.New("mappingadmission: internal port must be in 1..65535")
	ErrRemoteHostInvalid      = errors.New("mappingadmission: RemoteHost is not a valid IP address")
	ErrRemoteHostWildcard     = errors.New("mappingadmission: a non-empty RemoteHost must name one peer, not a wildcard")
	ErrMappingConflict        = errors.New("mappingadmission: external endpoint is already mapped by another internal client")
	ErrLeasePermanent         = errors.New("mappingadmission: permanent leases (duration 0) are not allowed")
	ErrLeaseTooLong           = errors.New("mappingadmission: lease duration exceeds the configured maximum")
	ErrClientQuotaExceeded    = errors.New("mappingadmission: per-client mapping quota exceeded")
)

// Admit evaluates one AddPortMapping request against the policy.
//
// sourceAddr is the real remote address of the control connection, typically
// conn.RemoteAddr().String(); it may include a port and an IPv6 zone. On
// success the returned *Mapping is normalized and ready for Table.Install and
// the gateway's NAT layer. The table is never modified by Admit.
func (t *Table) Admit(req Request, sourceAddr string, cfg Config) (*Mapping, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if t == nil {
		t = NewTable()
	}

	if err := checkHostHeader(req.HostHeader, cfg.GatewayHosts); err != nil {
		return nil, err
	}

	protocol := strings.ToUpper(strings.TrimSpace(req.Protocol))
	if protocol != ProtocolTCP && protocol != ProtocolUDP {
		return nil, ErrUnsupportedProtocol
	}

	source, err := parseSockAddr(sourceAddr)
	if err != nil {
		return nil, ErrSourceAddressInvalid
	}
	source = source.Unmap()

	client, err := netip.ParseAddr(strings.TrimSpace(req.InternalClient))
	if err != nil {
		return nil, ErrInternalClientInvalid
	}
	client = client.Unmap()
	if client != source {
		return nil, ErrInternalClientMismatch
	}

	if req.ExternalPort < 1024 {
		return nil, ErrExternalPortPrivileged
	}
	if req.ExternalPort > 65535 {
		return nil, ErrExternalPortOutOfRange
	}
	if _, reserved := cfg.ReservedExternalPorts[uint16(req.ExternalPort)]; reserved {
		return nil, ErrExternalPortReserved
	}

	if req.InternalPort <= 0 || req.InternalPort > 65535 {
		return nil, ErrInternalPortInvalid
	}

	remote, err := normalizeRemoteHost(req.RemoteHost)
	if err != nil {
		return nil, err
	}

	key := MappingKey{ExternalPort: req.ExternalPort, Protocol: protocol}
	if existing, occupied := t.Mappings[key]; occupied && existing.InternalClient != client {
		return nil, ErrMappingConflict
	}

	if req.LeaseDuration == 0 {
		return nil, ErrLeasePermanent
	}
	if req.LeaseDuration > cfg.MaxLeaseSeconds {
		return nil, ErrLeaseTooLong
	}

	if _, refresh := t.Mappings[key]; !refresh {
		if t.countForClient(client) >= cfg.effectiveQuota() {
			return nil, ErrClientQuotaExceeded
		}
	}

	mapping := &Mapping{
		RemoteHost:     remote,
		ExternalPort:   req.ExternalPort,
		Protocol:       protocol,
		InternalPort:   req.InternalPort,
		InternalClient: client,
		LeaseDuration:  req.LeaseDuration,
	}
	return mapping, nil
}

func (c Config) validate() error {
	if c.MaxLeaseSeconds == 0 {
		return errors.New("mappingadmission: Config.MaxLeaseSeconds must be greater than zero")
	}
	if c.MaxMappingsPerClient < 0 {
		return errors.New("mappingadmission: Config.MaxMappingsPerClient must not be negative")
	}
	if len(c.GatewayHosts) == 0 {
		return errors.New("mappingadmission: Config.GatewayHosts must contain at least one gateway address")
	}
	for _, host := range c.GatewayHosts {
		if strings.TrimSpace(host) == "" {
			return errors.New("mappingadmission: Config.GatewayHosts must not contain empty entries")
		}
	}
	return nil
}

func (c Config) effectiveQuota() int {
	if c.MaxMappingsPerClient == 0 {
		return DefaultMaxMappingsPerClient
	}
	return c.MaxMappingsPerClient
}

func (t *Table) countForClient(client netip.Addr) int {
	count := 0
	for _, mapping := range t.Mappings {
		if mapping.InternalClient == client {
			count++
		}
	}
	return count
}

// parseSockAddr accepts either a bare IP literal or an IP:port socket address
// as produced by net.Addr.String(), including bracketed IPv6 forms.
func parseSockAddr(value string) (netip.Addr, error) {
	trimmed := strings.TrimSpace(value)
	if addr, err := netip.ParseAddr(trimmed); err == nil {
		return addr, nil
	}
	host, _, err := net.SplitHostPort(trimmed)
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(host)
}

func normalizeRemoteHost(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		// An empty RemoteHost intentionally means "all remote peers"; it is
		// the only wildcard form the policy permits.
		return "", nil
	}
	if value == "*" {
		return "", ErrRemoteHostWildcard
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", ErrRemoteHostInvalid
	}
	if addr.IsUnspecified() {
		return "", ErrRemoteHostWildcard
	}
	return addr.Unmap().String(), nil
}

// checkHostHeader compares the request Host against the gateway's own
// addresses. A port suffix and IPv6 brackets are stripped first, IP literals
// are compared after Unmap so v4-mapped forms match, and hostnames use a
// case-insensitive exact match.
func checkHostHeader(header string, gatewayHosts []string) error {
	host := strings.TrimSpace(header)
	if host == "" {
		return ErrHostHeaderMismatch
	}
	if stripped, _, err := net.SplitHostPort(host); err == nil {
		host = stripped
	} else if strings.HasPrefix(host, "[") {
		// A bracketed address without a port is malformed.
		return ErrHostHeaderMismatch
	}
	host = strings.ToLower(host)

	for _, gateway := range gatewayHosts {
		gateway = strings.ToLower(strings.TrimSpace(gateway))
		if gateway == host {
			return nil
		}
		gatewayIP, gatewayErr := netip.ParseAddr(gateway)
		hostIP, hostErr := netip.ParseAddr(host)
		if gatewayErr == nil && hostErr == nil && gatewayIP.Unmap() == hostIP.Unmap() {
			return nil
		}
	}
	return ErrHostHeaderMismatch
}
