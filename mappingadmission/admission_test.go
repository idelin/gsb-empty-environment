package mappingadmission

import (
	"errors"
	"net/netip"
	"testing"
)

func reservedPorts(ports ...uint16) map[uint16]struct{} {
	set := make(map[uint16]struct{}, len(ports))
	for _, port := range ports {
		set[port] = struct{}{}
	}
	return set
}

func testConfig() Config {
	return Config{
		MaxLeaseSeconds:       86400,
		ReservedExternalPorts: reservedPorts(8080),
		GatewayHosts:          []string{"192.0.2.1", "gw.lan"},
	}
}

func baseRequest() Request {
	return Request{
		ExternalPort:   5000,
		Protocol:       "TCP",
		InternalPort:   8080,
		InternalClient: "192.0.2.10",
		LeaseDuration:  3600,
		HostHeader:     "192.0.2.1:49152",
	}
}

func mustParseAddr(value string) netip.Addr {
	return netip.MustParseAddr(value).Unmap()
}

func TestAdmit(t *testing.T) {
	const sourceA = "192.0.2.10:53000"
	const sourceB = "192.0.2.11:53001"

	tests := []struct {
		name    string
		req     Request
		source  string
		table   func() *Table
		wantErr error
		want    func(*Mapping) bool
	}{
		{
			name:   "valid mapping",
			req:    baseRequest(),
			source: sourceA,
			want: func(m *Mapping) bool {
				return m.InternalClient.String() == "192.0.2.10" &&
					m.ExternalPort == 5000 && m.Protocol == ProtocolTCP &&
					m.InternalPort == 8080 && m.LeaseDuration == 3600 &&
					m.RemoteHost == ""
			},
		},
		{
			name:    "InternalClient points at another host",
			req:     baseRequest(),
			source:  sourceB,
			wantErr: ErrInternalClientMismatch,
		},
		{
			name: "IPv4-mapped InternalClient equals IPv4 source",
			req: func() Request {
				req := baseRequest()
				req.InternalClient = "::ffff:192.0.2.10"
				return req
			}(),
			source: sourceA,
			want: func(m *Mapping) bool {
				return m.InternalClient.String() == "192.0.2.10"
			},
		},
		{
			name: "IPv4 InternalClient equals IPv4-mapped source",
			req: func() Request {
				req := baseRequest()
				req.InternalClient = "192.0.2.10"
				return req
			}(),
			source: "[::ffff:192.0.2.10]:53000",
			want: func(m *Mapping) bool {
				return m.InternalClient.String() == "192.0.2.10"
			},
		},
		{
			name: "native IPv6 source and client",
			req: func() Request {
				req := baseRequest()
				req.InternalClient = "2001:db8::10"
				req.HostHeader = "gw.lan"
				return req
			}(),
			source: "[2001:db8::10]:53000",
			want: func(m *Mapping) bool {
				return m.InternalClient.String() == "2001:db8::10"
			},
		},
		{
			name: "invalid InternalClient literal",
			req: func() Request {
				req := baseRequest()
				req.InternalClient = "not-an-ip"
				return req
			}(),
			source:  sourceA,
			wantErr: ErrInternalClientInvalid,
		},
		{
			name:    "invalid source address",
			req:     baseRequest(),
			source:  "garbage",
			wantErr: ErrSourceAddressInvalid,
		},
		{
			name: "ExternalPort 22 is privileged",
			req: func() Request {
				req := baseRequest()
				req.ExternalPort = 22
				return req
			}(),
			source:  sourceA,
			wantErr: ErrExternalPortPrivileged,
		},
		{
			name: "ExternalPort 1023 is privileged",
			req: func() Request {
				req := baseRequest()
				req.ExternalPort = 1023
				return req
			}(),
			source:  sourceA,
			wantErr: ErrExternalPortPrivileged,
		},
		{
			name: "ExternalPort 1024 is allowed",
			req: func() Request {
				req := baseRequest()
				req.ExternalPort = 1024
				return req
			}(),
			source: sourceA,
			want:   func(m *Mapping) bool { return m.ExternalPort == 1024 },
		},
		{
			name: "ExternalPort 70000 out of range",
			req: func() Request {
				req := baseRequest()
				req.ExternalPort = 70000
				return req
			}(),
			source:  sourceA,
			wantErr: ErrExternalPortOutOfRange,
		},
		{
			name: "ExternalPort 8080 is reserved",
			req: func() Request {
				req := baseRequest()
				req.ExternalPort = 8080
				return req
			}(),
			source:  sourceA,
			wantErr: ErrExternalPortReserved,
		},
		{
			name: "InternalPort zero rejected",
			req: func() Request {
				req := baseRequest()
				req.InternalPort = 0
				return req
			}(),
			source:  sourceA,
			wantErr: ErrInternalPortInvalid,
		},
		{
			name: "InternalPort above 65535 rejected",
			req: func() Request {
				req := baseRequest()
				req.InternalPort = 65536
				return req
			}(),
			source:  sourceA,
			wantErr: ErrInternalPortInvalid,
		},
		{
			name: "InternalPort 65535 accepted",
			req: func() Request {
				req := baseRequest()
				req.InternalPort = 65535
				return req
			}(),
			source: sourceA,
			want:   func(m *Mapping) bool { return m.InternalPort == 65535 },
		},
		{
			name:    "ICMP protocol unsupported",
			req:     Request{ExternalPort: 5000, Protocol: "ICMP", InternalPort: 8080, InternalClient: "192.0.2.10", LeaseDuration: 3600, HostHeader: "192.0.2.1"},
			source:  sourceA,
			wantErr: ErrUnsupportedProtocol,
		},
		{
			name: "lowercase tcp normalized",
			req: func() Request {
				req := baseRequest()
				req.Protocol = "tcp"
				return req
			}(),
			source: sourceA,
			want:   func(m *Mapping) bool { return m.Protocol == ProtocolTCP },
		},
		{
			name: "RemoteHost wildcard star rejected",
			req: func() Request {
				req := baseRequest()
				req.RemoteHost = "*"
				return req
			}(),
			source:  sourceA,
			wantErr: ErrRemoteHostWildcard,
		},
		{
			name: "RemoteHost unspecified IPv4 rejected",
			req: func() Request {
				req := baseRequest()
				req.RemoteHost = "0.0.0.0"
				return req
			}(),
			source:  sourceA,
			wantErr: ErrRemoteHostWildcard,
		},
		{
			name: "RemoteHost unspecified IPv6 rejected",
			req: func() Request {
				req := baseRequest()
				req.RemoteHost = "::"
				return req
			}(),
			source:  sourceA,
			wantErr: ErrRemoteHostWildcard,
		},
		{
			name: "RemoteHost malformed rejected",
			req: func() Request {
				req := baseRequest()
				req.RemoteHost = "203.0.113.9:1234"
				return req
			}(),
			source:  sourceA,
			wantErr: ErrRemoteHostInvalid,
		},
		{
			name: "RemoteHost v4-mapped canonicalized",
			req: func() Request {
				req := baseRequest()
				req.RemoteHost = "::ffff:203.0.113.9"
				return req
			}(),
			source: sourceA,
			want:   func(m *Mapping) bool { return m.RemoteHost == "203.0.113.9" },
		},
		{
			name: "permanent lease zero rejected",
			req: func() Request {
				req := baseRequest()
				req.LeaseDuration = 0
				return req
			}(),
			source:  sourceA,
			wantErr: ErrLeasePermanent,
		},
		{
			name: "lease above maximum rejected",
			req: func() Request {
				req := baseRequest()
				req.LeaseDuration = 86401
				return req
			}(),
			source:  sourceA,
			wantErr: ErrLeaseTooLong,
		},
		{
			name: "lease equal to maximum accepted",
			req: func() Request {
				req := baseRequest()
				req.LeaseDuration = 86400
				return req
			}(),
			source: sourceA,
			want:   func(m *Mapping) bool { return m.LeaseDuration == 86400 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := NewTable()
			if tt.table != nil {
				table = tt.table()
			}
			mapping, err := table.Admit(tt.req, tt.source, testConfig())
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Admit() error = %v, want %v", err, tt.wantErr)
				}
				if mapping != nil {
					t.Fatalf("Admit() returned mapping %+v for rejected request", mapping)
				}
				return
			}
			if err != nil {
				t.Fatalf("Admit() unexpected error: %v", err)
			}
			if mapping == nil {
				t.Fatal("Admit() returned nil mapping without error")
			}
			if tt.want != nil && !tt.want(mapping) {
				t.Fatalf("Admit() mapping %+v failed predicate", mapping)
			}
		})
	}
}
