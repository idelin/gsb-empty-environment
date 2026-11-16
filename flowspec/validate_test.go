package flowspec

import (
	"errors"
	"net/netip"
	"slices"
	"testing"
)

const (
	asOne ASN = 64501
	asTwo ASN = 64502
)

type alertRecorder struct {
	alerts []Alert
}

func (r *alertRecorder) FlowSpecAlert(alert Alert) {
	r.alerts = append(r.alerts, alert)
}

func mustPrefix(t *testing.T, text string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", text, err)
	}
	return prefix
}

func destComponent(t *testing.T, prefix netip.Prefix) Component {
	t.Helper()
	component, err := PrefixComponent(TypeDestinationPrefix, prefix)
	if err != nil {
		t.Fatalf("build destination component: %v", err)
	}
	return component
}

func matchComponent(typ ComponentType, value byte) Component {
	// Operator 0x81 = "eq" end-of-length bit set (RFC 8955 section 4.2.3).
	return Component{Type: typ, Value: []byte{0x81, value}}
}

func newTestRIB(t *testing.T) *MemoryRIB {
	t.Helper()
	rib := NewMemoryRIB()
	routes := []UnicastRoute{
		{Prefix: mustPrefix(t, "10.1.0.0/16"), OriginAS: asOne, PeerAS: asOne},
		{Prefix: mustPrefix(t, "10.2.0.0/16"), OriginAS: asTwo, PeerAS: asTwo},
		{Prefix: mustPrefix(t, "10.3.0.0/16"), OriginAS: asOne, PeerAS: asOne},
		// Covering best path for 10.3.0.0/19 is the same-neighbor /16, but a
		// more-specific route inside the /19 is reached via another neighbor AS.
		{Prefix: mustPrefix(t, "10.3.1.0/24"), OriginAS: asTwo, PeerAS: asTwo},
		{Prefix: mustPrefix(t, "10.4.0.0/16"), OriginAS: asOne, PeerAS: asOne},
		{Prefix: mustPrefix(t, "10.4.0.0/24"), OriginAS: asOne, PeerAS: asOne},
		{Prefix: mustPrefix(t, "10.5.0.0/16"), OriginAS: asTwo, PeerAS: asTwo, Internal: true},
		{Prefix: mustPrefix(t, "2001:db8:1::/48"), OriginAS: asOne, PeerAS: asOne},
	}
	for _, route := range routes {
		rib.Set(route)
	}
	return rib
}

func TestReceiveValidation(t *testing.T) {
	rib := newTestRIB(t)

	tests := []struct {
		name        string
		rule        Rule
		wantCode    FailureCode
		wantInstall bool
	}{
		{
			name: "legal rule for own prefix is installed",
			rule: Rule{
				Key:        "legal",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI:        AFIIPv4,
					Components: []Component{destComponent(t, mustPrefix(t, "10.1.0.0/16"))},
				},
			},
			wantInstall: true,
		},
		{
			name: "foreign prefix rule rejected",
			rule: Rule{
				Key:        "foreign-prefix",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI:        AFIIPv4,
					Components: []Component{destComponent(t, mustPrefix(t, "10.2.0.0/16"))},
				},
			},
			wantCode: FailureForeignOrigin,
		},
		{
			name: "more-specific route from another neighbor AS rejected",
			rule: Rule{
				Key:        "more-specific-foreign",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI: AFIIPv4,
					// /19 best path is the same-neighbor /16; the /25 inside
					// it is reached via another neighbor AS.
					Components: []Component{destComponent(t, mustPrefix(t, "10.3.0.0/19"))},
				},
			},
			wantCode: FailureMoreSpecificForeignRoute,
		},
		{
			name: "same-neighbor-AS more-specific route allowed",
			rule: Rule{
				Key:        "more-specific-same-as",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI:        AFIIPv4,
					Components: []Component{destComponent(t, mustPrefix(t, "10.4.0.0/16"))},
				},
			},
			wantInstall: true,
		},
		{
			name: "out-of-order components rejected",
			rule: Rule{
				Key:        "out-of-order",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI: AFIIPv4,
					// Types 1 (destination), 5 (dport), 3 (protocol): not ascending.
					Components: []Component{
						destComponent(t, mustPrefix(t, "10.1.0.0/16")),
						matchComponent(TypeDestinationPort, 22),
						matchComponent(TypeIPProtocol, 6),
					},
				},
			},
			wantCode: FailureComponentsOutOfOrder,
		},
		{
			name: "duplicate component type rejected",
			rule: Rule{
				Key:        "duplicate",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI: AFIIPv4,
					Components: []Component{
						destComponent(t, mustPrefix(t, "10.1.0.0/16")),
						matchComponent(TypeIPProtocol, 6),
						matchComponent(TypeIPProtocol, 17),
					},
				},
			},
			wantCode: FailureDuplicateComponent,
		},
		{
			name: "no covering unicast route rejected",
			rule: Rule{
				Key:        "no-route",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI:        AFIIPv4,
					Components: []Component{destComponent(t, mustPrefix(t, "10.9.0.0/16"))},
				},
			},
			wantCode: FailureNoBestRoute,
		},
		{
			name: "internal best path satisfies same-neighbor condition",
			rule: Rule{
				Key:        "internal-path",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI:        AFIIPv4,
					Components: []Component{destComponent(t, mustPrefix(t, "10.5.0.0/16"))},
				},
			},
			wantInstall: true,
		},
		{
			name: "rule without destination prefix only needs structural validity",
			rule: Rule{
				Key:        "no-dest-prefix",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI: AFIIPv4,
					Components: []Component{
						matchComponent(TypeIPProtocol, 6),
						matchComponent(TypeDestinationPort, 22),
					},
				},
			},
			wantInstall: true,
		},
		{
			name: "legal IPv6 rule installed",
			rule: Rule{
				Key:        "ipv6-legal",
				NeighborAS: asOne,
				NLRI: NLRI{
					AFI:        AFIIPv6,
					Components: []Component{destComponent(t, mustPrefix(t, "2001:db8:1::/48"))},
				},
			},
			wantInstall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &alertRecorder{}
			manager := NewManager(rib, recorder)

			installed, err := manager.Receive(tt.rule)
			if installed != tt.wantInstall {
				t.Fatalf("installed = %v, want %v", installed, tt.wantInstall)
			}
			if manager.IsInstalled(tt.rule.Key) != tt.wantInstall {
				t.Fatalf("installed set inconsistent for key %q", tt.rule.Key)
			}

			if tt.wantInstall {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				if len(recorder.alerts) != 0 {
					t.Fatalf("expected no alerts, got %d: %+v", len(recorder.alerts), recorder.alerts)
				}
				return
			}

			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			if validationErr.Code != tt.wantCode {
				t.Fatalf("failure code = %q, want %q", validationErr.Code, tt.wantCode)
			}
			if len(recorder.alerts) != 1 {
				t.Fatalf("expected exactly one alert, got %d", len(recorder.alerts))
			}
			if recorder.alerts[0].Code != tt.wantCode {
				t.Fatalf("alert code = %q, want %q", recorder.alerts[0].Code, tt.wantCode)
			}
			if recorder.alerts[0].NeighborAS != tt.rule.NeighborAS {
				t.Fatalf("alert neighbor AS = %d, want %d", recorder.alerts[0].NeighborAS, tt.rule.NeighborAS)
			}
		})
	}
}

func TestWireRoundTrip(t *testing.T) {
	original := NLRI{
		AFI: AFIIPv4,
		Components: []Component{
			destComponent(t, mustPrefix(t, "10.1.2.0/24")),
			matchComponent(TypeIPProtocol, 6),
			matchComponent(TypeDestinationPort, 80),
		},
	}

	wire, err := EncodeNLRI(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeNLRI(AFIIPv4, wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.EqualFunc(decoded.Components, original.Components, func(a, b Component) bool {
		return a.Type == b.Type && slices.Equal(a.Value, b.Value)
	}) {
		t.Fatalf("round trip mismatch:\n got  %+v\n want %+v", decoded, original)
	}
	if err := CheckComponentOrder(decoded); err != nil {
		t.Fatalf("decoded NLRI ordering check failed: %v", err)
	}

	prefix, ok, err := decoded.DestinationPrefix()
	if err != nil || !ok {
		t.Fatalf("destination prefix: ok=%v err=%v", ok, err)
	}
	if prefix.String() != "10.1.2.0/24" {
		t.Fatalf("decoded prefix = %s, want 10.1.2.0/24", prefix)
	}
}

func TestDecodedOutOfOrderRuleRejected(t *testing.T) {
	// Build components first, then place them in descending on-wire order.
	prefixComp := destComponent(t, mustPrefix(t, "10.1.2.0/24"))
	protocolComp := matchComponent(TypeIPProtocol, 6)

	outOfOrder := NLRI{
		AFI:        AFIIPv4,
		Components: []Component{protocolComp, prefixComp},
	}
	wire, err := EncodeNLRI(outOfOrder)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	nlri, err := DecodeNLRI(AFIIPv4, wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	rib := newTestRIB(t)
	recorder := &alertRecorder{}
	manager := NewManager(rib, recorder)

	installed, err := manager.Receive(Rule{
		Key:        "wire-out-of-order",
		NeighborAS: asOne,
		NLRI:       nlri,
	})
	if installed {
		t.Fatal("out-of-order wire NLRI must not be installed")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Code != FailureComponentsOutOfOrder {
		t.Fatalf("expected components_out_of_order, got %v", err)
	}
	if len(recorder.alerts) != 1 {
		t.Fatalf("expected one alert, got %d", len(recorder.alerts))
	}
}

func TestWithdraw(t *testing.T) {
	rib := newTestRIB(t)
	manager := NewManager(rib, &alertRecorder{})
	rule := Rule{
		Key:        "withdraw-me",
		NeighborAS: asOne,
		NLRI: NLRI{
			AFI:        AFIIPv4,
			Components: []Component{destComponent(t, mustPrefix(t, "10.1.0.0/16"))},
		},
	}

	if installed, err := manager.Receive(rule); err != nil || !installed {
		t.Fatalf("receive: installed=%v err=%v", installed, err)
	}
	if !manager.Withdraw(rule.Key) {
		t.Fatal("withdraw reported missing rule")
	}
	if manager.IsInstalled(rule.Key) {
		t.Fatal("rule still installed after withdraw")
	}
	if manager.Withdraw(rule.Key) {
		t.Fatal("second withdraw should report false")
	}
}
