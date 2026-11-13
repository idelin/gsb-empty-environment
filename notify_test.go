package netsec

import (
	"errors"
	"net/netip"
	"testing"
)

func TestNotifyAdmissionRequiresPrimarySourceAndTSIG(t *testing.T) {
	primary := netip.MustParseAddr("192.0.2.10")
	primaryV6 := netip.MustParseAddr("2001:db8::10")
	config := ZoneConfig{
		Name:      "example.com.",
		Primaries: []netip.Addr{primary, primaryV6},
	}

	tests := []struct {
		name      string
		source    string
		tsigOK    bool
		wantFresh bool
	}{
		{
			name:      "trusted source and valid TSIG",
			source:    "192.0.2.10",
			tsigOK:    true,
			wantFresh: true,
		},
		{
			name:      "trusted IPv6 source and valid TSIG",
			source:    "2001:db8::10",
			tsigOK:    true,
			wantFresh: true,
		},
		{
			name:   "trusted source but forged TSIG",
			source: "192.0.2.10",
			tsigOK: false,
		},
		{
			name:   "spoofed source with claimed valid TSIG",
			source: "192.0.2.99",
			tsigOK: true,
		},
		{
			name:   "spoofed source and failed TSIG",
			source: "198.51.100.7",
			tsigOK: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := AdmitNotify(config, netip.MustParseAddr(test.source), test.tsigOK)
			if decision.Refresh != test.wantFresh {
				t.Fatalf("AdmitNotify() Refresh = %v, want %v (reason: %s)",
					decision.Refresh, test.wantFresh, decision.Reason)
			}
			if got := config.AdmitNotify(netip.MustParseAddr(test.source), test.tsigOK).Refresh; got != test.wantFresh {
				t.Fatalf("ZoneConfig.AdmitNotify() Refresh = %v, want %v", got, test.wantFresh)
			}
			if got := NotifyTriggersRefresh(config, netip.MustParseAddr(test.source), test.tsigOK); got != test.wantFresh {
				t.Fatalf("NotifyTriggersRefresh() = %v, want %v", got, test.wantFresh)
			}
		})
	}
}

func TestNotifyRejectsWhenNoPrimariesConfigured(t *testing.T) {
	config := ZoneConfig{Name: "example.com."}
	if decision := AdmitNotify(config, netip.MustParseAddr("192.0.2.10"), true); decision.Refresh {
		t.Fatalf("NOTIFY with no configured primaries triggered a refresh")
	}
}

func TestNotifyAcceptsIPv4MappedSourceForIPv4Primary(t *testing.T) {
	config := ZoneConfig{
		Name:      "example.com.",
		Primaries: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
	}
	source := netip.MustParseAddr("::ffff:192.0.2.10")
	if decision := AdmitNotify(config, source, true); !decision.Refresh {
		t.Fatalf("IPv4-mapped source was not matched to IPv4 primary: %s", decision.Reason)
	}
}

func TestNotifyInjectedTSIGVerifier(t *testing.T) {
	config := ZoneConfig{
		Name:      "example.com.",
		Primaries: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
	}
	trusted := netip.MustParseAddr("192.0.2.10")
	forged := netip.MustParseAddr("198.51.100.7")

	called := false
	decision := AdmitNotifyWithVerifier(config, trusted, func(zone string, source netip.Addr) error {
		called = true
		if zone != "example.com." || source != trusted {
			t.Fatalf("verifier received unexpected arguments: %q %v", zone, source)
		}
		return nil
	})
	if !decision.Refresh {
		t.Fatalf("valid injected TSIG verifier did not allow refresh: %s", decision.Reason)
	}
	if !called {
		t.Fatalf("TSIG verifier was not called for trusted source")
	}

	called = false
	decision = AdmitNotifyWithVerifier(config, trusted, func(string, netip.Addr) error {
		called = true
		return errors.New("bad signature")
	})
	if decision.Refresh {
		t.Fatalf("failed injected TSIG verifier allowed refresh")
	}
	if !called {
		t.Fatalf("TSIG verifier was not called for trusted source")
	}

	called = false
	decision = AdmitNotifyWithVerifier(config, forged, func(string, netip.Addr) error {
		called = true
		return nil
	})
	if decision.Refresh {
		t.Fatalf("forged source allowed refresh")
	}
	if called {
		t.Fatalf("TSIG verifier was called for an untrusted source")
	}

	if decision := AdmitNotifyWithVerifier(config, trusted, nil); decision.Refresh {
		t.Fatalf("nil TSIG verifier allowed refresh")
	}
}
