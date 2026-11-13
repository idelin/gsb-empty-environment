package netsec

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
)

var (
	ErrInvalidConfig   = errors.New("netsec: invalid configuration")
	ErrInvalidEvent    = errors.New("netsec: invalid event")
	ErrRuleReplacement = errors.New("netsec: rule replacement failed")
)

// RuleReplacer is the filter-layer adapter used by the state machine.
//
// Replace must install a complete ruleset atomically. The old ruleset must stay
// active until the new one is visible, and must remain active if Replace
// returns an error. The machine immediately follows a failed replacement with
// a deny-all replacement so that transient failures cannot leave old allows in
// place.
type RuleReplacer interface {
	Replace(rules Ruleset) error
}

// ReplacerFunc adapts a function to RuleReplacer.
type ReplacerFunc func(Ruleset) error

func (f ReplacerFunc) Replace(rules Ruleset) error {
	return f(rules)
}

// Config describes the static parts of the VPN and filter setup.
type Config struct {
	TunnelInterface    string
	PhysicalInterfaces []string
	VPNServers         []netip.Addr

	LANEnabled  bool
	LANPrefixes []netip.Prefix

	// InitialState defaults to StateConnecting.
	InitialState State
}

// Machine is a fail-closed VPN leak-protection state machine. Handle may be
// called from network, tunnel, UI, and timer callbacks and is safe for
// concurrent use.
type Machine struct {
	mu         sync.Mutex
	cfg        Config
	state      State
	replacer   RuleReplacer
	rules      Ruleset
	generation uint64
}

// New validates cfg and installs the initial ruleset. If the initial
// replacement fails, the returned machine is still usable and reports a
// deny-all ruleset together with the error.
func New(cfg Config, replacer RuleReplacer) (*Machine, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}

	m := &Machine{
		cfg:      cfg,
		state:    cfg.InitialState,
		replacer: replacer,
		rules: Ruleset{
			State:      cfg.InitialState,
			DNS:        DNSDeny,
			FailClosed: true,
		},
	}
	if err := m.commit(cfg.InitialState); err != nil {
		return m, err
	}
	return m, nil
}

// State returns the current logical state.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Ruleset returns a copy of the currently active ruleset.
func (m *Machine) Ruleset() Ruleset {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rules.clone()
}

// LANEnabled reports the caller-controlled LAN switch.
func (m *Machine) LANEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.LANEnabled
}

// SetLANEnabled changes the LAN switch and atomically replaces the ruleset for
// the current state.
func (m *Machine) SetLANEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.LANEnabled == enabled && !m.rules.FailClosed {
		return nil
	}
	m.cfg.LANEnabled = enabled
	if enabled && len(m.cfg.LANPrefixes) == 0 {
		m.cfg.LANPrefixes = DefaultLANPrefixes()
	}
	return m.commit(m.state)
}

// Handle processes one state-machine event.
func (m *Machine) Handle(event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch event.Type {
	case EventTunnelChanged:
		return m.handleTunnel(event.Tunnel)
	case EventNetworkChanged:
		return m.handleNetwork(event.Network)
	case EventUserAction:
		return m.handleUser(event.User)
	case EventTimeout:
		return m.handleTimeout()
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidEvent, event.Type)
	}
}

// TunnelChanged is a convenience callback for tunnel monitors.
func (m *Machine) TunnelChanged(status TunnelStatus) error {
	return m.Handle(Event{Type: EventTunnelChanged, Tunnel: status})
}

// NetworkChanged is a convenience callback for physical-network monitors.
func (m *Machine) NetworkChanged(change NetworkChange) error {
	return m.Handle(Event{Type: EventNetworkChanged, Network: &change})
}

// OnUserAction is a convenience callback for explicit user actions.
func (m *Machine) OnUserAction(action UserAction) error {
	return m.Handle(Event{Type: EventUserAction, User: action})
}

// Timeout is a convenience callback for connection and health timers.
func (m *Machine) Timeout() error {
	return m.Handle(Event{Type: EventTimeout})
}

func (m *Machine) handleTunnel(status TunnelStatus) error {
	if m.state == StateDisconnectedByUser {
		return nil
	}

	var target State
	switch status {
	case TunnelConnecting:
		target = StateConnecting
	case TunnelUp:
		target = StateUp
	case TunnelDown:
		target = StateReconnecting
	default:
		return fmt.Errorf("%w: unknown tunnel status %q", ErrInvalidEvent, status)
	}
	return m.commit(target)
}

func (m *Machine) handleNetwork(change *NetworkChange) error {
	if change == nil {
		return fmt.Errorf("%w: missing network change", ErrInvalidEvent)
	}

	interfaces, err := normalizeInterfaces(change.PhysicalInterfaces)
	if err != nil {
		return err
	}

	servers := m.cfg.VPNServers
	if change.VPNServers != nil {
		servers, err = normalizeServers(change.VPNServers)
		if err != nil {
			return err
		}
	}

	m.cfg.PhysicalInterfaces = interfaces
	m.cfg.VPNServers = servers

	target := StateSwitchingNetwork
	if m.state == StateDisconnectedByUser {
		target = StateDisconnectedByUser
	}
	return m.commit(target)
}

func (m *Machine) handleUser(action UserAction) error {
	switch action {
	case UserConnect:
		return m.commit(StateConnecting)
	case UserDisconnect:
		return m.commit(StateDisconnectedByUser)
	default:
		return fmt.Errorf("%w: unknown user action %q", ErrInvalidEvent, action)
	}
}

func (m *Machine) handleTimeout() error {
	switch m.state {
	case StateDisconnectedByUser, StateReconnecting:
		return nil
	default:
		return m.commit(StateReconnecting)
	}
}

func (m *Machine) commit(target State) error {
	candidate := m.buildRuleset(target, m.generation+1)
	if err := m.apply(candidate); err == nil {
		m.generation = candidate.Generation
		m.state = target
		m.rules = candidate
		return nil
	} else {
		deny := Ruleset{
			State:      target,
			Generation: candidate.Generation + 1,
			DNS:        DNSDeny,
			FailClosed: true,
		}
		m.generation = deny.Generation
		m.state = target
		m.rules = deny

		denyErr := m.apply(deny)
		if denyErr != nil {
			return fmt.Errorf("%w: deny-all replacement also failed: %v",
				ErrRuleReplacement, denyErr)
		}
		return fmt.Errorf("%w: %v", ErrRuleReplacement, err)
	}
}

func (m *Machine) apply(rules Ruleset) error {
	if m.replacer == nil {
		return nil
	}
	return m.replacer.Replace(rules.clone())
}

func (m *Machine) buildRuleset(state State, generation uint64) Ruleset {
	rules := Ruleset{
		State:      state,
		Generation: generation,
		DNS:        DNSDeny,
	}
	seen := make(map[string]struct{})

	add := func(iface string, destination netip.Prefix) {
		key := iface + "\x00" + destination.String()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		rules.Rules = append(rules.Rules, Rule{
			Interface:   iface,
			Destination: destination,
		})
	}
	addServerRules := func() {
		for _, iface := range m.cfg.PhysicalInterfaces {
			for _, server := range m.cfg.VPNServers {
				add(iface, serverPrefix(server))
			}
		}
	}
	addLANRules := func() {
		if !m.cfg.LANEnabled {
			return
		}
		for _, iface := range m.cfg.PhysicalInterfaces {
			for _, prefix := range m.cfg.LANPrefixes {
				add(iface, prefix)
			}
		}
	}

	switch state {
	case StateUp:
		add(m.cfg.TunnelInterface, netip.Prefix{})
		addServerRules()
		addLANRules()
		rules.DNS = DNSTunnel
	case StateConnecting, StateReconnecting, StateSwitchingNetwork:
		addServerRules()
		addLANRules()
	case StateDisconnectedByUser:
		for _, iface := range m.cfg.PhysicalInterfaces {
			add(iface, netip.Prefix{})
		}
		rules.DNS = DNSDirect
	}
	return rules
}

func normalizeConfig(cfg Config) (Config, error) {
	tunnel := strings.TrimSpace(cfg.TunnelInterface)
	if tunnel == "" {
		return Config{}, fmt.Errorf("%w: missing tunnel interface", ErrInvalidConfig)
	}
	interfaces, err := normalizeInterfaces(cfg.PhysicalInterfaces)
	if err != nil {
		return Config{}, err
	}
	servers, err := normalizeServers(cfg.VPNServers)
	if err != nil {
		return Config{}, err
	}
	if cfg.InitialState == "" {
		cfg.InitialState = StateConnecting
	}
	if !cfg.InitialState.valid() {
		return Config{}, fmt.Errorf("%w: unknown initial state %q",
			ErrInvalidConfig, cfg.InitialState)
	}
	lanPrefixes, err := normalizeLANPrefixes(cfg.LANPrefixes)
	if err != nil {
		return Config{}, err
	}
	if cfg.LANEnabled && len(lanPrefixes) == 0 {
		lanPrefixes = DefaultLANPrefixes()
	}

	cfg.TunnelInterface = tunnel
	cfg.PhysicalInterfaces = interfaces
	cfg.VPNServers = servers
	cfg.LANPrefixes = lanPrefixes
	return cfg, nil
}

func normalizeInterfaces(interfaces []string) ([]string, error) {
	out := make([]string, 0, len(interfaces))
	seen := make(map[string]struct{}, len(interfaces))
	for _, iface := range interfaces {
		iface = strings.TrimSpace(iface)
		if iface == "" {
			return nil, fmt.Errorf("%w: empty physical interface", ErrInvalidConfig)
		}
		if _, ok := seen[iface]; ok {
			continue
		}
		seen[iface] = struct{}{}
		out = append(out, iface)
	}
	return out, nil
}

func normalizeServers(servers []netip.Addr) ([]netip.Addr, error) {
	out := make([]netip.Addr, 0, len(servers))
	seen := make(map[netip.Addr]struct{}, len(servers))
	for _, server := range servers {
		if !server.IsValid() {
			return nil, fmt.Errorf("%w: invalid VPN server address", ErrInvalidConfig)
		}
		server = server.Unmap()
		if _, ok := seen[server]; ok {
			continue
		}
		seen[server] = struct{}{}
		out = append(out, server)
	}
	return out, nil
}

func normalizeLANPrefixes(prefixes []netip.Prefix) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(prefixes))
	seen := make(map[string]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		if !isLANPrefix(prefix) {
			return nil, fmt.Errorf("%w: LAN prefix %q is not local",
				ErrInvalidConfig, prefix)
		}
		prefix = prefix.Masked()
		if _, ok := seen[prefix.String()]; ok {
			continue
		}
		seen[prefix.String()] = struct{}{}
		out = append(out, prefix)
	}
	return out, nil
}

func serverPrefix(server netip.Addr) netip.Prefix {
	server = server.Unmap()
	return netip.PrefixFrom(server, server.BitLen())
}
