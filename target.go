package netsec

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Target is the destination of a CONNECT tunnel.
type Target struct {
	// Network is the network passed to the dialer. It defaults to "tcp".
	Network string
	// Address is a host:port pair, for example "example.com:443".
	Address string
}

// NewTarget returns a TCP target.
func NewTarget(address string) Target {
	return Target{Network: "tcp", Address: address}
}

func (t Target) normalized() Target {
	if t.Network == "" {
		t.Network = "tcp"
	}
	return t
}

// String returns the dialable form of the target.
func (t Target) String() string {
	t = t.normalized()
	return t.Address
}

// Validate reports whether the target contains a usable host:port pair.
func (t Target) Validate() error {
	t = t.normalized()
	if t.Network == "" {
		return fmt.Errorf("%w: empty network", ErrInvalidTarget)
	}
	if t.Address == "" {
		return fmt.Errorf("%w: empty address", ErrInvalidTarget)
	}
	host, port, err := net.SplitHostPort(t.Address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%w: empty host", ErrInvalidTarget)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("%w: empty port", ErrInvalidTarget)
	}
	return nil
}

// Policy identifies the policy under which a target was authorized.
//
// ID is the policy name. Revision lets a caller represent changes to a policy
// without changing its name. Authorization is never carried across a policy
// change: every new tunnel and every ConfigStore revision is authorized again.
type Policy struct {
	ID       string
	Revision uint64
}

func (p Policy) String() string {
	if p.Revision == 0 {
		return p.ID
	}
	return fmt.Sprintf("%s@%d", p.ID, p.Revision)
}

// Config is the complete description of a tunnel admission decision.
type Config struct {
	Target Target
	Policy Policy
}

// Request is the immutable snapshot supplied to an Authorizer.
//
// Revision is set by ConfigStore. It identifies the exact target and policy
// snapshot that were authorized. A grant for one revision must not be reused
// for another revision.
type Request struct {
	Target   Target
	Policy   Policy
	Revision uint64
	Method   string
	Host     string
	Header   http.Header
}

// ConfigSource supplies an atomic target/policy snapshot and its revision.
type ConfigSource interface {
	Snapshot() (Config, uint64)
}

// ConfigStore is a concurrency-safe ConfigSource whose revision advances on
// every update. A running tunnel can observe a revision change at each commit
// barrier and is required to authorize the new snapshot again.
type ConfigStore struct {
	mu       sync.RWMutex
	config   Config
	revision uint64
}

// NewConfigStore creates a store at revision 1.
func NewConfigStore(config Config) *ConfigStore {
	return &ConfigStore{config: config, revision: 1}
}

// Snapshot returns a consistent copy of the current configuration.
func (s *ConfigStore) Snapshot() (Config, uint64) {
	if s == nil {
		return Config{}, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config, s.revision
}

// Update replaces the target and policy and advances the revision. The next
// commit barrier that observes the new revision must authorize it.
func (s *ConfigStore) Update(config Config) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = config
	s.revision++
	return s.revision
}

// ErrInvalidTarget indicates that a CONNECT target was not a usable host:port.
var ErrInvalidTarget = errors.New("netsec: invalid tunnel target")

// ErrUnauthorized indicates that the target was not authorized. No connection
// is dialed after this error.
var ErrUnauthorized = errors.New("netsec: tunnel target not authorized")

// ErrDialFailed indicates that dialing the authorized target failed.
var ErrDialFailed = errors.New("netsec: tunnel dial failed")

// ErrResponseFailed indicates that the successful CONNECT response could not
// be written. Buffered tunnel data is discarded and never reaches the target.
var ErrResponseFailed = errors.New("netsec: tunnel response failed")

// ErrConfigChanged indicates that the target or policy changed too late in the
// commit sequence to be authorized again without violating the response
// barrier. No buffered tunnel data is forwarded.
var ErrConfigChanged = errors.New("netsec: tunnel configuration changed during commit")

// ErrClosed indicates that the ingress was closed before data forwarding.
var ErrClosed = errors.New("netsec: tunnel ingress closed")
