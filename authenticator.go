package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"hash"
	"strings"
	"sync"
)

type tunnelState uint8

const (
	tunnelStateOuter tunnelState = iota + 1
	tunnelStateEstablished
	tunnelStateAuthenticated
	tunnelStateFailed
)

type tunnel struct {
	id               TunnelID
	port             Port
	outerIdentity    string
	state            tunnelState
	exporter         []byte
	exporterDigest   [32]byte
	transcript       hash.Hash
	exchangeMessages int
	innerIdentity    string
	transcriptDigest [32]byte
	bindingDigest    [32]byte
}

type grantRecord struct {
	tunnel           TunnelID
	port             Port
	innerIdentity    string
	exporterDigest   [32]byte
	transcriptDigest [32]byte
	bindingDigest    [32]byte
}

// Authenticator owns all tunnel and physical port instance state for an access
// host. It is safe for concurrent use.
type Authenticator struct {
	mu             sync.Mutex
	ports          map[string]Port
	tunnels        map[TunnelID]*tunnel
	grants         map[[16]byte]grantRecord
	nextGeneration uint64
}

// NewAuthenticator returns an empty Authenticator.
func NewAuthenticator() *Authenticator {
	return &Authenticator{
		ports:   make(map[string]Port),
		tunnels: make(map[TunnelID]*tunnel),
		grants:  make(map[[16]byte]grantRecord),
	}
}

func (a *Authenticator) initLocked() {
	if a.ports == nil {
		a.ports = make(map[string]Port)
	}
	if a.tunnels == nil {
		a.tunnels = make(map[TunnelID]*tunnel)
	}
	if a.grants == nil {
		a.grants = make(map[[16]byte]grantRecord)
	}
}

// AttachPort creates a fresh physical port attachment instance.
//
// The host must call AttachPort for every physical connection event, including
// a reconnect on the same interface name. A previous attachment with the same
// name is invalidated first, together with all of its tunnels and grants.
func (a *Authenticator) AttachPort(name string) (Port, error) {
	if strings.TrimSpace(name) == "" {
		return Port{}, ErrInvalidPortName
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	var instance [16]byte
	if _, err := rand.Read(instance[:]); err != nil {
		return Port{}, fmt.Errorf("netsec: generate port instance: %w", err)
	}

	if previous, ok := a.ports[name]; ok {
		a.invalidatePortLocked(previous)
	}

	a.nextGeneration++
	port := Port{
		name:       name,
		instance:   instance,
		generation: a.nextGeneration,
	}
	a.ports[name] = port
	return port, nil
}

// DetachPort invalidates port and all tunnels and grants bound to it. It
// returns false when port is not the current attachment for its name.
func (a *Authenticator) DetachPort(port Port) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if !a.isCurrentPortLocked(port) {
		return false
	}
	a.invalidatePortLocked(port)
	return true
}

func (a *Authenticator) isCurrentPortLocked(port Port) bool {
	current, ok := a.ports[port.name]
	return ok && current == port
}

func (a *Authenticator) portStateErrorLocked(port Port) error {
	current, ok := a.ports[port.name]
	if !ok {
		return ErrPortNotAttached
	}
	if current != port {
		return ErrStalePort
	}
	return nil
}

func (a *Authenticator) invalidatePortLocked(port Port) {
	if a.isCurrentPortLocked(port) {
		delete(a.ports, port.name)
	}

	for id, tunnel := range a.tunnels {
		if tunnel.port == port {
			wipe(tunnel.exporter)
			delete(a.tunnels, id)
		}
	}
	for nonce, grant := range a.grants {
		if grant.port == port {
			delete(a.grants, nonce)
		}
	}
}

// BeginTunnel starts a tunnel after the host has received an outer identity.
//
// outerIdentity is untrusted routing metadata. It is retained for Route and
// is never used to construct a Principal.
func (a *Authenticator) BeginTunnel(port Port, outerIdentity string) (TunnelID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if err := a.portStateErrorLocked(port); err != nil {
		return TunnelID{}, err
	}

	id, err := a.newTunnelIDLocked()
	if err != nil {
		return TunnelID{}, err
	}

	transcript := sha256.New()
	writeField(transcript, []byte(transcriptLabel))
	a.tunnels[id] = &tunnel{
		id:            id,
		port:          port,
		outerIdentity: outerIdentity,
		state:         tunnelStateOuter,
		transcript:    transcript,
	}
	return id, nil
}

func (a *Authenticator) newTunnelIDLocked() (TunnelID, error) {
	for {
		var id TunnelID
		if _, err := rand.Read(id[:]); err != nil {
			return TunnelID{}, fmt.Errorf("netsec: generate tunnel ID: %w", err)
		}
		if id.IsZero() {
			continue
		}
		if _, exists := a.tunnels[id]; !exists {
			return id, nil
		}
	}
}

// BindExporter records the protected tunnel exporter value supplied by the
// host's TLS/EAP tunnel implementation. The exporter must be specific to this
// tunnel instance.
func (a *Authenticator) BindExporter(id TunnelID, exporter []byte) error {
	if len(exporter) == 0 {
		return ErrExporterRequired
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	tunnel, err := a.tunnelLocked(id)
	if err != nil {
		return err
	}
	if tunnel.state != tunnelStateOuter {
		return fmt.Errorf("%w: cannot bind exporter in state %s", ErrTunnelState, stateName(tunnel.state))
	}

	tunnel.exporter = append([]byte(nil), exporter...)
	tunnel.exporterDigest = computeExporterDigest(exporter)
	tunnel.state = tunnelStateEstablished
	return nil
}

// RecordInnerExchange appends one protected inner EAP packet to the exchange
// digest. The host must call it for every inner packet that contributes to
// authentication, in order, including the final success packet.
func (a *Authenticator) RecordInnerExchange(id TunnelID, packet []byte) error {
	if len(packet) == 0 {
		return ErrEmptyExchangePacket
	}
	if len(packet) > maxExchangePacket {
		return ErrExchangePacketTooBig
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	tunnel, err := a.tunnelLocked(id)
	if err != nil {
		return err
	}
	if tunnel.state != tunnelStateEstablished {
		return fmt.Errorf("%w: cannot record exchange in state %s", ErrTunnelState, stateName(tunnel.state))
	}

	writeField(tunnel.transcript, packet)
	tunnel.exchangeMessages++
	return nil
}

// CompleteInner finishes the protected inner method.
//
// innerIdentity is the identity authenticated inside the tunnel. If success is
// false, the tunnel is failed and no grant is produced. An empty inner identity
// is always rejected; the outer identity is never used as a fallback.
func (a *Authenticator) CompleteInner(id TunnelID, innerIdentity string, success bool) (Grant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	tunnel, err := a.tunnelLocked(id)
	if err != nil {
		return Grant{}, err
	}
	switch tunnel.state {
	case tunnelStateFailed:
		return Grant{}, ErrInnerAuthFailed
	case tunnelStateAuthenticated:
		return Grant{}, ErrTunnelCompleted
	case tunnelStateEstablished:
	default:
		return Grant{}, fmt.Errorf("%w: cannot complete inner method in state %s", ErrTunnelState, stateName(tunnel.state))
	}

	if !success {
		tunnel.state = tunnelStateFailed
		wipe(tunnel.exporter)
		return Grant{}, ErrInnerAuthFailed
	}
	if strings.TrimSpace(innerIdentity) == "" {
		return Grant{}, ErrInnerIdentityRequired
	}
	if tunnel.exchangeMessages == 0 {
		return Grant{}, ErrExchangeRequired
	}

	var transcriptDigest [32]byte
	copy(transcriptDigest[:], tunnel.transcript.Sum(nil))
	bindingDigest := computeBindingDigest(
		tunnel.exporter,
		tunnel.id,
		tunnel.port,
		transcriptDigest,
		innerIdentity,
	)

	nonce, err := newGrantNonce()
	if err != nil {
		return Grant{}, err
	}

	grant := Grant{
		nonce:            nonce,
		tunnel:           tunnel.id,
		port:             tunnel.port,
		innerIdentity:    innerIdentity,
		exporterDigest:   tunnel.exporterDigest,
		transcriptDigest: transcriptDigest,
		bindingDigest:    bindingDigest,
	}
	a.grants[nonce] = grantRecord{
		tunnel:           grant.tunnel,
		port:             grant.port,
		innerIdentity:    grant.innerIdentity,
		exporterDigest:   grant.exporterDigest,
		transcriptDigest: grant.transcriptDigest,
		bindingDigest:    grant.bindingDigest,
	}
	tunnel.innerIdentity = innerIdentity
	tunnel.transcriptDigest = transcriptDigest
	tunnel.bindingDigest = bindingDigest
	tunnel.state = tunnelStateAuthenticated
	return grant, nil
}

func newGrantNonce() ([16]byte, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return [16]byte{}, fmt.Errorf("netsec: generate grant nonce: %w", err)
	}
	return nonce, nil
}

// Authorize validates grant against the current physical port attachment and
// the tunnel currently being authenticated. It returns a Principal whose Name
// is the protected inner identity.
//
// Passing a grant from another tunnel, another port attachment, or a previous
// connection fails even when the outer identity and port name are identical.
func (a *Authenticator) Authorize(grant Grant, port Port, id TunnelID) (Principal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if err := a.portStateErrorLocked(port); err != nil {
		return Principal{}, err
	}
	record, ok := a.grants[grant.nonce]
	if !ok || !grantMatchesRecord(grant, record) {
		return Principal{}, ErrGrantInvalid
	}
	if grant.tunnel != id {
		return Principal{}, fmt.Errorf("%w: %w", ErrBindingMismatch, ErrSessionMismatch)
	}
	if grant.port != port {
		return Principal{}, fmt.Errorf("%w: physical port instance differs", ErrBindingMismatch)
	}

	tunnel, ok := a.tunnels[id]
	if !ok {
		return Principal{}, fmt.Errorf("%w: %w", ErrBindingMismatch, ErrTunnelNotFound)
	}
	if tunnel.state != tunnelStateAuthenticated {
		return Principal{}, fmt.Errorf("%w: tunnel is in state %s", ErrTunnelState, stateName(tunnel.state))
	}
	if tunnel.port != port {
		return Principal{}, fmt.Errorf("%w: tunnel is bound to another port attachment", ErrBindingMismatch)
	}

	var transcriptDigest [32]byte
	copy(transcriptDigest[:], tunnel.transcript.Sum(nil))
	expectedBinding := computeBindingDigest(
		tunnel.exporter,
		tunnel.id,
		tunnel.port,
		transcriptDigest,
		tunnel.innerIdentity,
	)
	if !hmac.Equal(expectedBinding[:], grant.bindingDigest[:]) ||
		!hmac.Equal(transcriptDigest[:], grant.transcriptDigest[:]) ||
		!hmac.Equal(tunnel.exporterDigest[:], grant.exporterDigest[:]) ||
		tunnel.innerIdentity != grant.innerIdentity {
		return Principal{}, ErrBindingMismatch
	}

	return Principal{
		Name:   tunnel.innerIdentity,
		Tunnel: tunnel.id,
		Port:   tunnel.port,
	}, nil
}

func grantMatchesRecord(grant Grant, record grantRecord) bool {
	return grant.tunnel == record.tunnel &&
		grant.port == record.port &&
		grant.innerIdentity == record.innerIdentity &&
		hmac.Equal(grant.exporterDigest[:], record.exporterDigest[:]) &&
		hmac.Equal(grant.transcriptDigest[:], record.transcriptDigest[:]) &&
		hmac.Equal(grant.bindingDigest[:], record.bindingDigest[:])
}

func (a *Authenticator) tunnelLocked(id TunnelID) (*tunnel, error) {
	tunnel, ok := a.tunnels[id]
	if !ok {
		return nil, ErrTunnelNotFound
	}
	if err := a.portStateErrorLocked(tunnel.port); err != nil {
		wipe(tunnel.exporter)
		delete(a.tunnels, id)
		return nil, err
	}
	return tunnel, nil
}

// Route returns the unauthenticated outer identity associated with a tunnel.
// It is suitable only for selecting a proxy route. Callers must not pass this
// value to authorization decisions.
func (a *Authenticator) Route(id TunnelID) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	tunnel, err := a.tunnelLocked(id)
	if err != nil {
		return "", err
	}
	return tunnel.outerIdentity, nil
}

// OuterIdentity is an explicit alias for Route. The result is routing metadata,
// never an authorization principal.
func (a *Authenticator) OuterIdentity(id TunnelID) (string, error) {
	return a.Route(id)
}

// State returns the current state of a tunnel.
func (a *Authenticator) State(id TunnelID) (TunnelState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	tunnel, err := a.tunnelLocked(id)
	if err != nil {
		return 0, err
	}
	return TunnelState(tunnel.state), nil
}

// CloseTunnel removes a tunnel and invalidates all grants issued for it.
func (a *Authenticator) CloseTunnel(id TunnelID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initLocked()

	if tunnel, ok := a.tunnels[id]; ok {
		wipe(tunnel.exporter)
		delete(a.tunnels, id)
	}
	for nonce, grant := range a.grants {
		if grant.tunnel == id {
			delete(a.grants, nonce)
		}
	}
}

func stateName(state tunnelState) string {
	switch state {
	case tunnelStateOuter:
		return TunnelStateOuterIdentity.String()
	case tunnelStateEstablished:
		return TunnelStateTunnelEstablished.String()
	case tunnelStateAuthenticated:
		return TunnelStateInnerAuthenticated.String()
	case tunnelStateFailed:
		return TunnelStateFailed.String()
	default:
		return "unknown"
	}
}
