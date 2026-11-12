package netsec

import "testing"

const (
	wildcardPattern = "*.ns.serviceaccount.identity.linkerd.cluster.local"
	webIdentity     = "web.ns.serviceaccount.identity.linkerd.cluster.local"
)

func baseConfig() Config {
	return Config{
		DefaultPolicy: DefaultAllow,
		NodeCIDRs:     []string{"10.0.0.0/8"},
		ProbePaths:    []string{"/healthz", "/readyz"},
		Servers:       []Server{{Name: "web", Port: 8080}},
		Policies: []AuthorizationPolicy{{
			Name: "web-authz",
			Port: 8080,
			AuthenticationRefs: []AuthenticationRef{
				{Kind: KindMeshTLSAuthentication, Name: "clients"},
				{Kind: KindNetworkAuthentication, Name: "corp"},
			},
		}},
		MeshTLSAuths: []MeshTLSAuthentication{{
			Name:       "clients",
			Identities: []string{wildcardPattern},
		}},
		NetworkAuths: []NetworkAuthentication{{
			Name:     "corp",
			Networks: []string{"192.168.0.0/16"},
		}},
	}
}

func mustResolver(t *testing.T, cfg Config) *Resolver {
	t.Helper()
	r, err := NewResolver(cfg)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func TestWildcardMatchesSingleLeftmostLabel(t *testing.T) {
	r := mustResolver(t, baseConfig())
	req := Request{SourceIP: "172.16.0.1", Port: 8080, Method: "GET", Path: "/api"}

	req.Identity = webIdentity
	if got := r.Evaluate(req); got != Allow {
		t.Fatalf("single-label identity %q: got %v, want Allow", req.Identity, got)
	}
}

func TestWildcardDoesNotSpanLabels(t *testing.T) {
	r := mustResolver(t, baseConfig())
	req := Request{
		SourceIP: "172.16.0.1",
		Identity: "web.eu.ns.serviceaccount.identity.linkerd.cluster.local",
		Port:     8080,
		Method:   "GET",
		Path:     "/api",
	}
	if got := r.Evaluate(req); got != Deny {
		t.Fatalf("multi-label identity %q: got %v, want Deny", req.Identity, got)
	}
}

func TestWildcardRejectsOtherSuffix(t *testing.T) {
	r := mustResolver(t, baseConfig())
	req := Request{
		SourceIP: "172.16.0.1",
		Identity: "web.other.serviceaccount.identity.linkerd.cluster.local",
		Port:     8080,
		Method:   "GET",
		Path:     "/api",
	}
	if got := r.Evaluate(req); got != Deny {
		t.Fatalf("wrong-suffix identity %q: got %v, want Deny", req.Identity, got)
	}
}

func TestNetworkAuthenticationMatchesCIDR(t *testing.T) {
	r := mustResolver(t, baseConfig())
	req := Request{SourceIP: "192.168.1.10", Port: 8080, Method: "GET", Path: "/api"}
	if got := r.Evaluate(req); got != Allow {
		t.Fatalf("in-CIDR source: got %v, want Allow", got)
	}

	req.SourceIP = "203.0.113.9"
	if got := r.Evaluate(req); got != Deny {
		t.Fatalf("out-of-CIDR source: got %v, want Deny", got)
	}
}

func TestProbeAllowedOnlyForDeclaredPathsAndGET(t *testing.T) {
	r := mustResolver(t, baseConfig())

	cases := []struct {
		name   string
		method string
		path   string
		source string
		want   Decision
	}{
		{"probe path GET from node", "GET", "/healthz", "10.1.2.3", Allow},
		{"probe path GET from non-node", "GET", "/healthz", "172.16.0.1", Deny},
		{"non-probe path from node", "GET", "/admin", "10.1.2.3", Deny},
		{"probe path POST from node", "POST", "/healthz", "10.1.2.3", Deny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{SourceIP: tc.source, Port: 8080, Method: tc.method, Path: tc.path}
			if got := r.Evaluate(req); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDuplicateServerSelectionAlwaysDenies(t *testing.T) {
	cfg := baseConfig()
	cfg.Servers = append(cfg.Servers, Server{Name: "web-shadow", Port: 8080})
	r := mustResolver(t, cfg)

	// Even a request that would otherwise match the AuthorizationPolicy.
	req := Request{SourceIP: "192.168.1.10", Identity: webIdentity, Port: 8080, Method: "GET", Path: "/api"}
	if got := r.Evaluate(req); got != Deny {
		t.Fatalf("duplicate Server on port: got %v, want Deny", got)
	}

	// Probe traffic is denied too.
	probe := Request{SourceIP: "10.1.2.3", Port: 8080, Method: "GET", Path: "/healthz"}
	if got := r.Evaluate(probe); got != Deny {
		t.Fatalf("duplicate Server probe: got %v, want Deny", got)
	}
}

func TestUnselectedPortFollowsClusterDefault(t *testing.T) {
	cfg := baseConfig()
	r := mustResolver(t, cfg)
	req := Request{SourceIP: "203.0.113.9", Port: 9090, Method: "GET", Path: "/api"}
	if got := r.Evaluate(req); got != Allow {
		t.Fatalf("unselected port with DefaultAllow: got %v, want Allow", got)
	}

	cfg.DefaultPolicy = DefaultDeny
	r = mustResolver(t, cfg)
	if got := r.Evaluate(req); got != Deny {
		t.Fatalf("unselected port with DefaultDeny: got %v, want Deny", got)
	}
}

func TestSelectedPortWithoutMatchingPolicyDenies(t *testing.T) {
	r := mustResolver(t, baseConfig())
	req := Request{SourceIP: "203.0.113.9", Port: 8080, Method: "GET", Path: "/api"}
	if got := r.Evaluate(req); got != Deny {
		t.Fatalf("selected port, no matching authn: got %v, want Deny", got)
	}
}

func TestInvalidCIDRRejected(t *testing.T) {
	cfg := baseConfig()
	cfg.NodeCIDRs = []string{"not-a-cidr"}
	if _, err := NewResolver(cfg); err == nil {
		t.Fatal("expected error for invalid node CIDR")
	}

	cfg = baseConfig()
	cfg.NetworkAuths[0].Networks = []string{"999.0.0.0/8"}
	if _, err := NewResolver(cfg); err == nil {
		t.Fatal("expected error for invalid NetworkAuthentication CIDR")
	}
}
