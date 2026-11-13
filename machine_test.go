package netsec

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
)

var (
	testServer = netip.MustParseAddr("203.0.113.10")
	testPublic = netip.MustParseAddr("198.51.100.25")
	testLAN    = netip.MustParseAddr("192.168.50.7")
)

type recordingReplacer struct {
	mu       sync.Mutex
	active   Ruleset
	calls    []Ruleset
	failNext bool
	failAll  bool
}

func (r *recordingReplacer) Replace(rules Ruleset) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, rules.clone())
	if r.failAll || r.failNext {
		r.failNext = false
		return errors.New("injected replace failure")
	}
	r.active = rules.clone()
	return nil
}

func (r *recordingReplacer) current() Ruleset {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active.clone()
}

func (r *recordingReplacer) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newMachine(t *testing.T, cfg Config, replacer *recordingReplacer) *Machine {
	t.Helper()
	m, err := New(cfg, replacer)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func baseConfig() Config {
	return Config{
		TunnelInterface:    "utun9",
		PhysicalInterfaces: []string{"en0"},
		VPNServers:         []netip.Addr{testServer},
	}
}

func mustTunnel(t *testing.T, m *Machine, status TunnelStatus, want State) {
	t.Helper()
	if err := m.TunnelChanged(status); err != nil {
		t.Fatalf("TunnelChanged(%s): %v", status, err)
	}
	if got := m.State(); got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
}

func assertStrict(t *testing.T, rules Ruleset, physical string) {
	t.Helper()
	assertStrictWithLAN(t, rules, physical, false)
}

func assertStrictWithLAN(t *testing.T, rules Ruleset, physical string, lanAllowed bool) {
	t.Helper()
	if rules.DNS != DNSDeny {
		t.Fatalf("DNS = %s, want deny", rules.DNS)
	}
	if rules.AllowsDNS() {
		t.Fatal("DNS must not be allowed in a reconnect window")
	}
	if !rules.Allows(physical, testServer) {
		t.Fatalf("%s cannot reach VPN server", physical)
	}
	for _, iface := range []string{physical, "utun9"} {
		if rules.Allows(iface, testPublic) {
			t.Fatalf("public destination allowed on %s", iface)
		}
		if !lanAllowed && rules.Allows(iface, testLAN) {
			t.Fatalf("LAN destination allowed on %s", iface)
		}
	}
	for _, rule := range rules.Rules {
		if rule.Interface == "utun9" {
			t.Fatal("tunnel permits must be removed while reconnecting")
		}
		if rule.IsAnyDestination() {
			t.Fatalf("wildcard permit on %s during reconnect", rule.Interface)
		}
	}
}

func hasWildcard(rules Ruleset, iface string) bool {
	for _, rule := range rules.Rules {
		if rule.Interface == iface && rule.IsAnyDestination() {
			return true
		}
	}
	return false
}

func TestInitialConnectingOnlyPermitsVPNServer(t *testing.T) {
	replacer := &recordingReplacer{}
	m := newMachine(t, baseConfig(), replacer)

	rules := m.Ruleset()
	if rules.State != StateConnecting {
		t.Fatalf("ruleset state = %s", rules.State)
	}
	assertStrict(t, rules, "en0")
	if len(rules.Rules) != 1 {
		t.Fatalf("got %d rules, want exactly the VPN server rule", len(rules.Rules))
	}
	if replacer.callCount() != 1 {
		t.Fatalf("initial rules installed %d times", replacer.callCount())
	}
}

func TestUpOnlyPermitsTunnelAndVPNServer(t *testing.T) {
	m := newMachine(t, baseConfig(), &recordingReplacer{})
	mustTunnel(t, m, TunnelUp, StateUp)

	rules := m.Ruleset()
	if rules.DNS != DNSTunnel {
		t.Fatalf("DNS = %s, want tunnel", rules.DNS)
	}
	if !hasWildcard(rules, "utun9") {
		t.Fatal("up state must permit traffic through the tunnel")
	}
	if !rules.Allows("en0", testServer) {
		t.Fatal("up state must permit the VPN server on the physical interface")
	}
	if rules.Allows("en0", testPublic) || rules.Allows("en0", testLAN) {
		t.Fatal("up state leaked non-server traffic to the physical interface")
	}
	if len(rules.Rules) != 2 {
		t.Fatalf("got %d rules, want tunnel wildcard plus server permit", len(rules.Rules))
	}
}

func TestTunnelLossImmediatelyEntersStrictWindow(t *testing.T) {
	m := newMachine(t, baseConfig(), &recordingReplacer{})
	mustTunnel(t, m, TunnelUp, StateUp)
	mustTunnel(t, m, TunnelDown, StateReconnecting)

	rules := m.Ruleset()
	assertStrict(t, rules, "en0")
	if rules.Allows("utun9", testPublic) {
		t.Fatal("dead tunnel remained usable")
	}
}

func TestNetworkSwitchRevokesOldInterfaceImmediately(t *testing.T) {
	m := newMachine(t, baseConfig(), &recordingReplacer{})
	mustTunnel(t, m, TunnelUp, StateUp)

	if err := m.NetworkChanged(NetworkChange{
		PhysicalInterfaces: []string{"en1"},
		VPNServers:         []netip.Addr{testServer},
	}); err != nil {
		t.Fatalf("NetworkChanged: %v", err)
	}
	if got := m.State(); got != StateSwitchingNetwork {
		t.Fatalf("state = %s, want switching_network", got)
	}
	rules := m.Ruleset()
	assertStrict(t, rules, "en1")
	if rules.Allows("en0", testServer) || hasWildcard(rules, "en0") {
		t.Fatal("old physical interface permit survived the switch")
	}
	if rules.Allows("utun9", testServer) {
		t.Fatal("tunnel permit survived the switch")
	}

	newServer := netip.MustParseAddr("203.0.113.20")
	if err := m.NetworkChanged(NetworkChange{
		PhysicalInterfaces: []string{"en1"},
		VPNServers:         []netip.Addr{newServer},
	}); err != nil {
		t.Fatalf("server route change: %v", err)
	}
	rules = m.Ruleset()
	if rules.Allows("en1", testServer) {
		t.Fatal("old VPN server route survived the route change")
	}
	if !rules.Allows("en1", newServer) {
		t.Fatal("new VPN server route was not installed")
	}

	mustTunnel(t, m, TunnelUp, StateUp)
	if !hasWildcard(m.Ruleset(), "utun9") {
		t.Fatal("tunnel did not recover after network switch")
	}
}

func TestReconnectWindowDeniesDNS(t *testing.T) {
	m := newMachine(t, baseConfig(), &recordingReplacer{})

	for _, tc := range []struct {
		name   string
		change func() error
	}{
		{"connecting", func() error { return m.TunnelChanged(TunnelConnecting) }},
		{"tunnel down", func() error { return m.TunnelChanged(TunnelDown) }},
		{"network switch", func() error {
			return m.NetworkChanged(NetworkChange{PhysicalInterfaces: []string{"en0"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.change(); err != nil {
				t.Fatalf("event: %v", err)
			}
			rules := m.Ruleset()
			if rules.DNS != DNSDeny {
				t.Fatalf("DNS = %s, want deny while %s", rules.DNS, m.State())
			}
			if rules.AllowsDNS() {
				t.Fatalf("system resolver fallback allowed while %s", m.State())
			}
		})
	}
}

func TestTimeoutNeverRelaxesFiltering(t *testing.T) {
	m := newMachine(t, baseConfig(), &recordingReplacer{})

	if err := m.Timeout(); err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	if got := m.State(); got != StateReconnecting {
		t.Fatalf("state = %s, want reconnecting", got)
	}
	assertStrict(t, m.Ruleset(), "en0")

	mustTunnel(t, m, TunnelUp, StateUp)
	if err := m.Timeout(); err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	if got := m.State(); got != StateReconnecting {
		t.Fatalf("state = %s, want reconnecting", got)
	}
	if hasWildcard(m.Ruleset(), "utun9") {
		t.Fatal("health timeout left the tunnel wildcard active")
	}
	assertStrict(t, m.Ruleset(), "en0")
}

func TestReplacementFailureFallsBackToDenyAll(t *testing.T) {
	replacer := &recordingReplacer{}
	m := newMachine(t, baseConfig(), replacer)
	mustTunnel(t, m, TunnelUp, StateUp)

	replacer.failNext = true
	err := m.TunnelChanged(TunnelDown)
	if !errors.Is(err, ErrRuleReplacement) {
		t.Fatalf("error = %v, want ErrRuleReplacement", err)
	}
	if got := m.State(); got != StateReconnecting {
		t.Fatalf("state = %s, want reconnecting", got)
	}

	rules := m.Ruleset()
	if !rules.FailClosed || len(rules.Rules) != 0 || rules.DNS != DNSDeny {
		t.Fatalf("failed replacement did not produce deny-all: %+v", rules)
	}
	if rules.Allows("utun9", testPublic) ||
		rules.Allows("en0", testServer) ||
		rules.Allows("en0", testPublic) {
		t.Fatal("an old permit survived a failed replacement")
	}
	active := replacer.current()
	if !active.FailClosed || len(active.Rules) != 0 || active.DNS != DNSDeny {
		t.Fatalf("filter did not receive deny-all after failure: %+v", active)
	}
	if len(replacer.calls) != 4 {
		t.Fatalf("calls = %d, want initial, up, failed candidate, deny-all", len(replacer.calls))
	}
	if replacer.calls[2].FailClosed || !replacer.calls[3].FailClosed {
		t.Fatal("replacement order was not candidate followed by deny-all")
	}

	mustTunnel(t, m, TunnelUp, StateUp)
	rules = m.Ruleset()
	if rules.FailClosed {
		t.Fatal("fail-closed latch did not clear after a successful replacement")
	}
	if !hasWildcard(rules, "utun9") {
		t.Fatal("filter did not recover after replacement success")
	}
}

func TestDenyAllReplacementFailureRemainsFailClosed(t *testing.T) {
	replacer := &recordingReplacer{}
	m := newMachine(t, baseConfig(), replacer)
	mustTunnel(t, m, TunnelUp, StateUp)

	replacer.failAll = true
	err := m.TunnelChanged(TunnelDown)
	if !errors.Is(err, ErrRuleReplacement) {
		t.Fatalf("error = %v, want ErrRuleReplacement", err)
	}
	rules := m.Ruleset()
	if !rules.FailClosed || len(rules.Rules) != 0 || rules.DNS != DNSDeny {
		t.Fatalf("rules = %+v, want reported deny-all", rules)
	}
}

func TestInitialReplacementFailureIsReturnedAndFailClosed(t *testing.T) {
	replacer := &recordingReplacer{failNext: true}
	m, err := New(baseConfig(), replacer)
	if !errors.Is(err, ErrRuleReplacement) {
		t.Fatalf("error = %v, want ErrRuleReplacement", err)
	}
	if m == nil {
		t.Fatal("machine must remain usable after initial install failure")
	}
	rules := m.Ruleset()
	if !rules.FailClosed || len(rules.Rules) != 0 || rules.DNS != DNSDeny {
		t.Fatalf("initial failure was not fail-closed: %+v", rules)
	}
	if rules.Allows("en0", testServer) || rules.Allows("en0", testPublic) {
		t.Fatal("initial failure left a permit active")
	}
}

func TestNetworkSwitchFailureDoesNotKeepOldInterface(t *testing.T) {
	replacer := &recordingReplacer{}
	m := newMachine(t, baseConfig(), replacer)
	mustTunnel(t, m, TunnelUp, StateUp)

	replacer.failNext = true
	change := NetworkChange{PhysicalInterfaces: []string{"en1"}}
	if err := m.NetworkChanged(change); !errors.Is(err, ErrRuleReplacement) {
		t.Fatalf("error = %v, want ErrRuleReplacement", err)
	}
	rules := m.Ruleset()
	if !rules.FailClosed || len(rules.Rules) != 0 {
		t.Fatalf("switch failure kept rules: %+v", rules)
	}
	if rules.Allows("en0", testServer) || rules.Allows("en1", testServer) {
		t.Fatal("switch failure preserved a physical-interface permit")
	}

	if err := m.NetworkChanged(change); err != nil {
		t.Fatalf("retry switch: %v", err)
	}
	rules = m.Ruleset()
	if rules.FailClosed || !rules.Allows("en1", testServer) ||
		rules.Allows("en0", testServer) {
		t.Fatalf("retry switch did not install new-interface-only rules: %+v", rules)
	}
}

func TestLANSwitchReplacementFailureIsFailClosed(t *testing.T) {
	replacer := &recordingReplacer{}
	m := newMachine(t, baseConfig(), replacer)
	mustTunnel(t, m, TunnelUp, StateUp)

	replacer.failNext = true
	if err := m.SetLANEnabled(true); !errors.Is(err, ErrRuleReplacement) {
		t.Fatalf("error = %v, want ErrRuleReplacement", err)
	}
	rules := m.Ruleset()
	if !rules.FailClosed || len(rules.Rules) != 0 || rules.DNS != DNSDeny {
		t.Fatalf("LAN switch failure was not fail-closed: %+v", rules)
	}
	if rules.Allows("en0", testLAN) || rules.Allows("en0", testPublic) {
		t.Fatal("failed LAN switch installed a permit")
	}

	if err := m.SetLANEnabled(true); err != nil {
		t.Fatalf("retry LAN switch: %v", err)
	}
	rules = m.Ruleset()
	if rules.FailClosed || !rules.Allows("en0", testLAN) ||
		rules.Allows("en0", testPublic) {
		t.Fatalf("retry LAN switch rules = %+v", rules)
	}
}

func TestOnlyUserDisconnectAllowsDirect(t *testing.T) {
	m := newMachine(t, baseConfig(), &recordingReplacer{})
	mustTunnel(t, m, TunnelUp, StateUp)

	if err := m.OnUserAction(UserDisconnect); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	rules := m.Ruleset()
	if m.State() != StateDisconnectedByUser {
		t.Fatalf("state = %s", m.State())
	}
	if !hasWildcard(rules, "en0") || !rules.Allows("en0", testPublic) {
		t.Fatal("explicit user disconnect did not permit direct traffic")
	}
	if rules.Allows("utun9", testPublic) {
		t.Fatal("user-disconnected state still routed through the tunnel")
	}
	if rules.DNS != DNSDirect {
		t.Fatalf("DNS = %s, want direct", rules.DNS)
	}

	if err := m.NetworkChanged(NetworkChange{PhysicalInterfaces: []string{"en1"}}); err != nil {
		t.Fatalf("network change: %v", err)
	}
	rules = m.Ruleset()
	if m.State() != StateDisconnectedByUser {
		t.Fatalf("user state overwritten by network change: %s", m.State())
	}
	if !hasWildcard(rules, "en1") || hasWildcard(rules, "en0") {
		t.Fatal("direct rules did not move to the new physical interface")
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"tunnel connecting", func() error { return m.TunnelChanged(TunnelConnecting) }},
		{"tunnel up", func() error { return m.TunnelChanged(TunnelUp) }},
		{"tunnel down", func() error { return m.TunnelChanged(TunnelDown) }},
		{"timeout", m.Timeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("event: %v", err)
			}
			if m.State() != StateDisconnectedByUser {
				t.Fatalf("state = %s, user intent was overwritten", m.State())
			}
			if !hasWildcard(m.Ruleset(), "en1") {
				t.Fatal("direct traffic was revoked without a user connect action")
			}
		})
	}

	if err := m.OnUserAction(UserConnect); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if m.State() != StateConnecting {
		t.Fatalf("state = %s, want connecting", m.State())
	}
	assertStrict(t, m.Ruleset(), "en1")
}

func TestUserDisconnectReplacementFailureDoesNotOpenDirect(t *testing.T) {
	replacer := &recordingReplacer{}
	m := newMachine(t, baseConfig(), replacer)
	mustTunnel(t, m, TunnelUp, StateUp)

	replacer.failNext = true
	err := m.OnUserAction(UserDisconnect)
	if !errors.Is(err, ErrRuleReplacement) {
		t.Fatalf("error = %v, want ErrRuleReplacement", err)
	}
	if m.State() != StateDisconnectedByUser {
		t.Fatalf("state = %s", m.State())
	}
	rules := m.Ruleset()
	if !rules.FailClosed || len(rules.Rules) != 0 || rules.DNS != DNSDeny {
		t.Fatalf("failed direct-rule install opened the network: %+v", rules)
	}
	if rules.Allows("en0", testPublic) {
		t.Fatal("direct traffic leaked when replacement failed")
	}

	if err := m.OnUserAction(UserDisconnect); err != nil {
		t.Fatalf("retry disconnect: %v", err)
	}
	rules = m.Ruleset()
	if rules.FailClosed || !hasWildcard(rules, "en0") || rules.DNS != DNSDirect {
		t.Fatalf("successful retry did not install direct rules: %+v", rules)
	}
}

func TestLANSwitchNeverPermitsPublicInternet(t *testing.T) {
	cfg := baseConfig()
	cfg.LANEnabled = true
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("192.168.50.0/24")}
	m := newMachine(t, cfg, &recordingReplacer{})

	rules := m.Ruleset()
	assertStrictWithLAN(t, rules, "en0", true)
	if !rules.Allows("en0", testLAN) {
		t.Fatal("LAN destination was not permitted with the switch on")
	}
	if rules.Allows("en0", testPublic) {
		t.Fatal("LAN switch opened public traffic")
	}

	mustTunnel(t, m, TunnelUp, StateUp)
	rules = m.Ruleset()
	if !rules.Allows("en0", testLAN) {
		t.Fatal("LAN destination was not permitted in up state")
	}
	if rules.Allows("en0", testPublic) || !rules.Allows("en0", testServer) {
		t.Fatal("up state public rules are not limited to the VPN server")
	}

	if err := m.SetLANEnabled(false); err != nil {
		t.Fatalf("disable LAN: %v", err)
	}
	rules = m.Ruleset()
	if rules.Allows("en0", testLAN) {
		t.Fatal("LAN permit survived switch-off")
	}
	if rules.Allows("en0", testPublic) || !rules.Allows("en0", testServer) {
		t.Fatal("switch-off changed the server-only public policy")
	}

	if err := m.SetLANEnabled(true); err != nil {
		t.Fatalf("enable LAN: %v", err)
	}
	if !m.Ruleset().Allows("en0", testLAN) {
		t.Fatal("LAN permit was not restored on switch-on")
	}
}

func TestDefaultLANPrefixesAreLocalOnly(t *testing.T) {
	for _, prefix := range DefaultLANPrefixes() {
		if !isLANPrefix(prefix) {
			t.Fatalf("default LAN prefix %s is not local", prefix)
		}
	}

	cfg := baseConfig()
	cfg.LANEnabled = true
	m := newMachine(t, cfg, &recordingReplacer{})
	if !m.Ruleset().Allows("en0", testLAN) {
		t.Fatal("default LAN prefixes do not include a private address")
	}
	if m.Ruleset().Allows("en0", testPublic) {
		t.Fatal("default LAN configuration leaked public traffic")
	}
}

func TestInvalidConfigRejectsPublicLANPrefix(t *testing.T) {
	cfg := baseConfig()
	cfg.LANPrefixes = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	if _, err := New(cfg, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestRepeatingStrictEventDoesNotWeakenRules(t *testing.T) {
	m := newMachine(t, baseConfig(), &recordingReplacer{})
	mustTunnel(t, m, TunnelDown, StateReconnecting)
	before := m.Ruleset()

	if err := m.Timeout(); err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	after := m.Ruleset()
	if after.Generation != before.Generation {
		t.Fatal("no-op timeout replaced the ruleset")
	}
	if !after.Allows("en0", testServer) || after.Allows("en0", testPublic) {
		t.Fatal("strict ruleset changed while waiting for reconnect")
	}
}
