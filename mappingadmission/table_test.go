package mappingadmission

import (
	"errors"
	"net/netip"
	"testing"
)

func installMapping(t *testing.T, table *Table, client string, port int, protocol string) {
	t.Helper()
	if err := table.Install(Mapping{
		ExternalPort:   port,
		Protocol:       protocol,
		InternalPort:   port + 1,
		InternalClient: mustParseAddr(client),
		LeaseDuration:  600,
	}); err != nil {
		t.Fatalf("Install(%d/%s): %v", port, protocol, err)
	}
}

func TestConflictAndRefresh(t *testing.T) {
	const sourceA = "192.0.2.10:53000"
	const sourceB = "192.0.2.11:53001"

	t.Run("other client cannot overwrite occupied external endpoint", func(t *testing.T) {
		table := NewTable()
		installMapping(t, table, "192.0.2.10", 5000, ProtocolTCP)

		req := baseRequest()
		req.InternalClient = "192.0.2.11"
		_, err := table.Admit(req, sourceB, testConfig())
		if !errors.Is(err, ErrMappingConflict) {
			t.Fatalf("Admit() error = %v, want ErrMappingConflict", err)
		}

		existing := table.Mappings[MappingKey{ExternalPort: 5000, Protocol: ProtocolTCP}]
		if existing.InternalClient != mustParseAddr("192.0.2.10") {
			t.Fatalf("mapping was overwritten by foreign client: %+v", existing)
		}
	})

	t.Run("same external port with different protocol is independent", func(t *testing.T) {
		table := NewTable()
		installMapping(t, table, "192.0.2.10", 5000, ProtocolTCP)

		req := baseRequest()
		req.Protocol = ProtocolUDP
		if _, err := table.Admit(req, sourceA, testConfig()); err != nil {
			t.Fatalf("Admit() UDP on same port: %v", err)
		}
	})

	t.Run("owner may refresh its own mapping without quota impact", func(t *testing.T) {
		table := NewTable()
		installMapping(t, table, "192.0.2.10", 5000, ProtocolTCP)

		cfg := testConfig()
		cfg.MaxMappingsPerClient = 1
		req := baseRequest()
		req.InternalPort = 9999
		req.LeaseDuration = 7200
		mapping, err := table.Admit(req, sourceA, cfg)
		if err != nil {
			t.Fatalf("Admit() refresh: %v", err)
		}
		if err := table.Install(*mapping); err != nil {
			t.Fatalf("Install refresh: %v", err)
		}
		got := table.Mappings[MappingKey{ExternalPort: 5000, Protocol: ProtocolTCP}]
		if got.InternalPort != 9999 || got.LeaseDuration != 7200 {
			t.Fatalf("refresh not applied: %+v", got)
		}
	})
}

func TestClientQuota(t *testing.T) {
	const sourceA = "192.0.2.10:53000"
	const sourceB = "192.0.2.11:53001"

	build := func(t *testing.T, quota int) (*Table, Config) {
		table := NewTable()
		cfg := testConfig()
		cfg.MaxMappingsPerClient = quota
		for port := 4000; port < 4000+quota; port++ {
			installMapping(t, table, "192.0.2.10", port, ProtocolTCP)
		}
		return table, cfg
	}

	t.Run("quota exhausted rejects new endpoint", func(t *testing.T) {
		table, cfg := build(t, 3)
		req := baseRequest()
		req.ExternalPort = 5555
		_, err := table.Admit(req, sourceA, cfg)
		if !errors.Is(err, ErrClientQuotaExceeded) {
			t.Fatalf("Admit() error = %v, want ErrClientQuotaExceeded", err)
		}
	})

	t.Run("default quota is sixteen", func(t *testing.T) {
		table, cfg := build(t, DefaultMaxMappingsPerClient)
		req := baseRequest()
		req.ExternalPort = 5555
		if _, err := table.Admit(req, sourceA, cfg); !errors.Is(err, ErrClientQuotaExceeded) {
			t.Fatalf("Admit() error = %v, want ErrClientQuotaExceeded", err)
		}
		if got := table.countForClient(mustParseAddr("192.0.2.10")); got != DefaultMaxMappingsPerClient {
			t.Fatalf("countForClient = %d, want %d", got, DefaultMaxMappingsPerClient)
		}
	})

	t.Run("quota is per client, not global", func(t *testing.T) {
		table, cfg := build(t, 3)
		req := baseRequest()
		req.ExternalPort = 5555
		req.InternalClient = "192.0.2.11"
		mapping, err := table.Admit(req, sourceB, cfg)
		if err != nil {
			t.Fatalf("Admit() for second client: %v", err)
		}
		if err := table.Install(*mapping); err != nil {
			t.Fatalf("Install: %v", err)
		}
	})

	t.Run("mapped-v4 identity counts toward same quota", func(t *testing.T) {
		table, cfg := build(t, 3)
		req := baseRequest()
		req.ExternalPort = 5555
		req.InternalClient = "::ffff:192.0.2.10"
		_, err := table.Admit(req, "[::ffff:192.0.2.10]:53000", cfg)
		if !errors.Is(err, ErrClientQuotaExceeded) {
			t.Fatalf("Admit() error = %v, want ErrClientQuotaExceeded", err)
		}
	})
}

func TestHostHeader(t *testing.T) {
	const source = "192.0.2.10:53000"

	cases := []struct {
		name    string
		header  string
		wantErr error
	}{
		{"external domain rebinding", "evil.example.com", ErrHostHeaderMismatch},
		{"external IP rebinding", "203.0.113.66:49152", ErrHostHeaderMismatch},
		{"empty header", "", ErrHostHeaderMismatch},
		{"malformed bracketed header", "[192.0.2.1", ErrHostHeaderMismatch},
		{"gateway IP with port", "192.0.2.1:49152", nil},
		{"gateway IP bare", "192.0.2.1", nil},
		{"gateway hostname with port", "gw.lan:49152", nil},
		{"gateway hostname mixed case", "GW.LAN", nil},
		{"gateway v4-mapped literal", "[::ffff:192.0.2.1]:49152", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRequest()
			req.HostHeader = tc.header
			_, err := NewTable().Admit(req, source, testConfig())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Admit() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	req := baseRequest()

	t.Run("zero max lease rejected at config level", func(t *testing.T) {
		cfg := testConfig()
		cfg.MaxLeaseSeconds = 0
		if _, err := NewTable().Admit(req, "192.0.2.10:53000", cfg); err == nil {
			t.Fatal("Admit() with zero MaxLeaseSeconds should fail")
		}
	})

	t.Run("missing gateway hosts rejected", func(t *testing.T) {
		cfg := testConfig()
		cfg.GatewayHosts = nil
		if _, err := NewTable().Admit(req, "192.0.2.10:53000", cfg); err == nil {
			t.Fatal("Admit() without GatewayHosts should fail")
		}
	})

	t.Run("negative quota rejected", func(t *testing.T) {
		cfg := testConfig()
		cfg.MaxMappingsPerClient = -1
		if _, err := NewTable().Admit(req, "192.0.2.10:53000", cfg); err == nil {
			t.Fatal("Admit() with negative quota should fail")
		}
	})

	t.Run("nil table still admits", func(t *testing.T) {
		var table *Table
		mapping, err := table.Admit(req, "192.0.2.10:53000", testConfig())
		if err != nil {
			t.Fatalf("Admit() on nil table: %v", err)
		}
		if mapping.InternalClient != netip.MustParseAddr("192.0.2.10") {
			t.Fatalf("unexpected client %s", mapping.InternalClient)
		}
	})
}
