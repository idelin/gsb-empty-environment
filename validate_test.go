package netsec

import (
	"errors"
	"net/http"
	"testing"
)

const testServer = "https://registry.example.com"

func authHeader() http.Header {
	header := http.Header{}
	header.Set(AuthorizationHeader, "Bearer secret-token")
	return header
}

// TestPlaintextMirrorRejected: 明文镜像站（http 或 skip_verify）必须被拒绝，
// 回环地址例外，且策略可连回环例外一并关闭。
func TestPlaintextMirrorRejected(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "http mirror",
			cfg: &Config{
				Server: testServer,
				Hosts: []HostEntry{
					{URL: "http://mirror.example.com"},
				},
			},
		},
		{
			name: "https mirror with skip_verify",
			cfg: &Config{
				Server: testServer,
				Hosts: []HostEntry{
					{URL: "https://mirror.example.com", SkipVerify: true},
				},
			},
		},
		{
			name: "plaintext upstream server",
			cfg: &Config{
				Server: "http://registry.example.com",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.cfg, nil)
			if !errors.Is(err, ErrInsecureHost) {
				t.Fatalf("expected ErrInsecureHost, got %v", err)
			}
		})
	}
}

func TestLoopbackInsecureException(t *testing.T) {
	loopbackHosts := []string{
		"http://localhost:5000",
		"http://127.0.0.1:5000",
		"http://[::1]:5000",
		"https://registry.localhost",
	}

	t.Run("default policy allows loopback", func(t *testing.T) {
		for _, endpoint := range loopbackHosts {
			cfg := &Config{
				Server: testServer,
				Hosts:  []HostEntry{{URL: endpoint, SkipVerify: true}},
			}
			if err := Validate(cfg, nil); err != nil {
				t.Fatalf("loopback endpoint %q should be allowed: %v", endpoint, err)
			}
		}
	})

	t.Run("strict policy rejects loopback too", func(t *testing.T) {
		strict := &Policy{AllowInsecureLoopback: false}
		for _, endpoint := range loopbackHosts {
			cfg := &Config{
				Server: testServer,
				Hosts:  []HostEntry{{URL: endpoint, SkipVerify: true}},
			}
			if err := Validate(cfg, strict); !errors.Is(err, ErrInsecureHost) {
				t.Fatalf("endpoint %q: expected ErrInsecureHost under strict policy, got %v", endpoint, err)
			}
		}
	})

	t.Run("non-loopback raw ip is not exempt", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts:  []HostEntry{{URL: "http://10.0.0.9:5000"}},
		}
		if err := Validate(cfg, nil); !errors.Is(err, ErrInsecureHost) {
			t.Fatalf("expected ErrInsecureHost for private non-loopback IP, got %v", err)
		}
	})
}

// TestCredentialHeaderExfiltration: Authorization 头出现在与 server 不同域的
// host 上时必须拒绝，避免凭证被发往攻击者控制的镜像站。
func TestCredentialHeaderExfiltration(t *testing.T) {
	t.Run("foreign host with authorization", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "https://evil-mirror.example.net", Header: authHeader()},
			},
		}
		if err := Validate(cfg, nil); !errors.Is(err, ErrCredentialLeak) {
			t.Fatalf("expected ErrCredentialLeak, got %v", err)
		}
	})

	t.Run("same-domain host with authorization is allowed", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "https://registry.example.com/v1", Header: authHeader()},
			},
		}
		if err := Validate(cfg, nil); err != nil {
			t.Fatalf("same-domain credential should be accepted: %v", err)
		}
	})

	t.Run("authorization on foreign loopback still rejected", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "http://localhost:5000", Header: authHeader()},
			},
		}
		err := Validate(cfg, nil)
		if !errors.Is(err, ErrCredentialLeak) {
			t.Fatalf("loopback must not excuse credential scoping, got %v", err)
		}
		if errors.Is(err, ErrInsecureHost) {
			t.Fatalf("loopback insecure entry is exempt under the default policy, got %v", err)
		}
	})

	t.Run("non-credential header is allowed cross-domain", func(t *testing.T) {
		header := http.Header{}
		header.Set("X-Mirror-Trace", "abc")
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "https://mirror.example.net", Header: header},
			},
		}
		if err := Validate(cfg, nil); err != nil {
			t.Fatalf("ordinary headers should not be scope-restricted: %v", err)
		}
	})
}

// TestSameDomainNormalization: 同域判断必须对大小写、默认端口和末尾点不敏感。
func TestSameDomainNormalization(t *testing.T) {
	spellings := []string{
		"https://REGISTRY.EXAMPLE.COM",
		"https://registry.example.com:443",
		"https://registry.example.com.",
		"https://Registry.Example.COM.:443",
		"registry.example.com",
	}

	for _, endpoint := range spellings {
		t.Run(endpoint, func(t *testing.T) {
			cfg := &Config{
				Server: testServer,
				Hosts:  []HostEntry{{URL: endpoint, Header: authHeader()}},
			}
			if err := Validate(cfg, nil); err != nil {
				t.Fatalf("spelling %q is same-domain and must keep its Authorization: %v", endpoint, err)
			}
		})
	}

	t.Run("different port is a different domain", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "https://registry.example.com:8443", Header: authHeader()},
			},
		}
		if err := Validate(cfg, nil); !errors.Is(err, ErrCredentialLeak) {
			t.Fatalf("expected ErrCredentialLeak for port mismatch, got %v", err)
		}
	})

	t.Run("trailing dot does not match a genuinely different host", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "https://notregistry.example.com.", Header: authHeader()},
			},
		}
		if err := Validate(cfg, nil); !errors.Is(err, ErrCredentialLeak) {
			t.Fatalf("expected ErrCredentialLeak, got %v", err)
		}
	})
}

// TestResolveOnlyHost: 只有 resolve 能力的 host 不能提供 pull。
func TestResolveOnlyHost(t *testing.T) {
	t.Run("resolve-only entry is valid configuration but skipped for pull", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "https://resolve-only.example.net", Capabilities: []string{CapResolve}},
				{URL: "https://mirror.example.net", Capabilities: []string{CapPull, CapResolve}},
			},
		}
		if err := Validate(cfg, nil); err != nil {
			t.Fatalf("resolve-only mirror is a legal entry: %v", err)
		}

		candidates, err := pullHosts(cfg)
		if err != nil {
			t.Fatalf("pullHosts failed: %v", err)
		}
		if len(candidates) != 1 || candidates[0].URL != "https://mirror.example.net" {
			t.Fatalf("resolve-only host must be excluded from pull candidates, got %+v", candidates)
		}
	})

	t.Run("no pull-capable host", func(t *testing.T) {
		cfg := &Config{
			Server: testServer,
			Hosts: []HostEntry{
				{URL: "https://resolve-only.example.net", Capabilities: []string{CapResolve}},
			},
		}
		candidates, err := pullHosts(cfg)
		if err != nil {
			t.Fatalf("pullHosts failed: %v", err)
		}
		if len(candidates) != 0 {
			t.Fatalf("expected zero pull candidates, got %+v", candidates)
		}
	})

	t.Run("unknown and duplicate capabilities rejected", func(t *testing.T) {
		bad := []Config{
			{
				Server: testServer,
				Hosts: []HostEntry{
					{URL: "https://mirror.example.net", Capabilities: []string{"frobnicate"}},
				},
			},
			{
				Server: testServer,
				Hosts: []HostEntry{
					{URL: "https://mirror.example.net", Capabilities: []string{CapPull, CapPull}},
				},
			},
		}
		for _, cfg := range bad {
			if err := Validate(&cfg, nil); err == nil {
				t.Fatalf("expected capability validation error for %+v", cfg.Hosts[0].Capabilities)
			}
		}
	})
}

func TestAllowlistPolicy(t *testing.T) {
	policy := &Policy{
		AllowInsecureLoopback: true,
		AllowedHosts:          []string{"mirror.example.com", "registry.example.com"},
	}

	t.Run("listed endpoints accepted", func(t *testing.T) {
		cfg := &Config{
			Server: "https://registry.example.com",
			Hosts: []HostEntry{
				{URL: "https://mirror.example.com/v2"},
			},
		}
		if err := Validate(cfg, policy); err != nil {
			t.Fatalf("allowlisted endpoints should pass: %v", err)
		}
	})

	t.Run("unlisted endpoint rejected", func(t *testing.T) {
		cfg := &Config{
			Server: "https://registry.example.com",
			Hosts: []HostEntry{
				{URL: "https://shadow.example.org"},
			},
		}
		if err := Validate(cfg, policy); err == nil {
			t.Fatal("expected allowlist rejection")
		}
	})
}

func TestClientCertificateShape(t *testing.T) {
	cfg := &Config{
		Server: testServer,
		Hosts: []HostEntry{
			{URL: "https://mirror.example.net", Client: []string{"/etc/cert.pem"}},
		},
	}
	if err := Validate(cfg, nil); err == nil {
		t.Fatal("single-element client pair should be rejected")
	}
}

func TestMalformedEndpoints(t *testing.T) {
	bad := []Config{
		{Server: "://missing-scheme"},
		{Server: testServer, Hosts: []HostEntry{{URL: "ftp://mirror.example.net"}}},
		{Server: testServer, Hosts: []HostEntry{{URL: "https://user:pass@mirror.example.net"}}},
		{Server: testServer, Hosts: []HostEntry{{URL: ""}}},
	}
	for idx, cfg := range bad {
		if err := Validate(&cfg, nil); err == nil {
			t.Fatalf("case %d: expected validation error", idx)
		}
	}
}

func TestMultipleViolationsJoined(t *testing.T) {
	cfg := &Config{
		Server: "http://registry.example.com",
		Hosts: []HostEntry{
			{URL: "http://evil.example.net", Header: authHeader()},
		},
	}
	err := Validate(cfg, nil)
	if err == nil {
		t.Fatal("expected joined violations")
	}
	if !errors.Is(err, ErrInsecureHost) || !errors.Is(err, ErrCredentialLeak) {
		t.Fatalf("expected both insecure and credential violations, got %v", err)
	}
}
