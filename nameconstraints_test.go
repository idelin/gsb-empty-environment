package netsec_test

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"example.org/netsec/q0277"
)

func TestValidChainWithCAConstraint(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"api.example.com"}})

	if err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert}); err != nil {
		t.Fatalf("VerifyChain returned error for valid chain: %v", err)
	}
}

func TestRootFirstChainAccepted(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"api.example.com"}})

	if err := netsec.VerifyChain([]*x509.Certificate{root.cert, leaf}); err != nil {
		t.Fatalf("VerifyChain returned error for root-first chain: %v", err)
	}
}

func TestLeafOutsideCAConstraintRejected(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"evil.example.net"}})

	err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
	if !errors.Is(err, netsec.ErrNameNotPermitted) {
		t.Fatalf("got %v, want ErrNameNotPermitted", err)
	}
}

func TestLeafNameConstraintsAlwaysRejected(t *testing.T) {
	tests := []struct {
		name string
		opts certOptions
	}{
		{
			name: "wider permitted subtree",
			opts: certOptions{
				dnsNames:  []string{"evil.example.net"},
				permitted: []string{"net"},
			},
		},
		{
			name: "wider permitted subtree with otherwise valid name",
			opts: certOptions{
				dnsNames:  []string{"api.example.com"},
				permitted: []string{"com"},
			},
		},
		{
			name: "excluded subtree",
			opts: certOptions{
				dnsNames: []string{"api.example.com"},
				excluded: []string{"bad.example.com"},
			},
		},
		{
			name: "constraints without any SAN",
			opts: certOptions{
				permitted: []string{"com"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
			leaf := newLeaf(t, "leaf", root, tc.opts)

			err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
			if !errors.Is(err, netsec.ErrNameConstraintsOnNonCA) {
				t.Fatalf("got %v, want ErrNameConstraintsOnNonCA", err)
			}
		})
	}
}

func TestNonCriticalLeafConstraintsRejected(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})

	tmpl := defaultTemplate("leaf", certOptions{
		dnsNames:  []string{"api.example.com"},
		permitted: []string{"com"},
	})
	tmpl.PermittedDNSDomainsCritical = false
	leaf := issue(t, tmpl, root)

	err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
	if !errors.Is(err, netsec.ErrNameConstraintsOnNonCA) {
		t.Fatalf("got %v, want ErrNameConstraintsOnNonCA", err)
	}
}

func TestWiderConstraintsOnCAEndEntityCannotRelaxPath(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})

	// Marking the end entity as a CA must not turn its wider constraint into
	// a way to escape the root's constraint. The root constraint is still
	// evaluated against the end entity's SAN.
	tmpl := defaultTemplate("ca-end-entity", certOptions{
		isCA:      true,
		dnsNames:  []string{"evil.example.net"},
		permitted: []string{"net"},
	})
	leaf := issue(t, tmpl, root)

	err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
	if !errors.Is(err, netsec.ErrNameNotPermitted) {
		t.Fatalf("got %v, want ErrNameNotPermitted from the root constraint", err)
	}
}

func TestCheckNameConstraintsRejectsLeafExtension(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	leaf := newLeaf(t, "leaf", root, certOptions{
		dnsNames:  []string{"api.example.com"},
		permitted: []string{"com"},
	})

	err := netsec.CheckNameConstraints([]*x509.Certificate{leaf, root.cert})
	if !errors.Is(err, netsec.ErrNameConstraintsOnNonCA) {
		t.Fatalf("got %v, want ErrNameConstraintsOnNonCA", err)
	}
}

func TestVerifyCertificateWrapper(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	roots := x509.NewCertPool()
	roots.AddCert(root.cert)

	t.Run("valid leaf", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"api.example.com"}})
		if _, err := netsec.VerifyCertificate(leaf, &x509.VerifyOptions{Roots: roots}); err != nil {
			t.Fatalf("VerifyCertificate returned error: %v", err)
		}
	})

	t.Run("leaf constraints rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{
			dnsNames:  []string{"api.example.com"},
			permitted: []string{"com"},
		})
		_, err := netsec.VerifyCertificate(leaf, &x509.VerifyOptions{Roots: roots})
		if !errors.Is(err, netsec.ErrNameConstraintsOnNonCA) {
			t.Fatalf("got %v, want ErrNameConstraintsOnNonCA", err)
		}
	})
}

func TestIntermediateCannotWidenRootConstraint(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	intermediate := newIntermediate(t, "intermediate", root, certOptions{
		permitted: []string{"net"},
	})
	leaf := newLeaf(t, "leaf", intermediate, certOptions{dnsNames: []string{"evil.example.net"}})

	err := netsec.VerifyChain([]*x509.Certificate{leaf, intermediate.cert, root.cert})
	if !errors.Is(err, netsec.ErrNameNotPermitted) {
		t.Fatalf("got %v, want ErrNameNotPermitted from the root constraint", err)
	}
}

func TestIntermediateConstraintNarrowsRoot(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"com"}})
	intermediate := newIntermediate(t, "intermediate", root, certOptions{
		permitted: []string{"example.com"},
	})

	t.Run("inside both constraints", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", intermediate, certOptions{dnsNames: []string{"api.example.com"}})
		if err := netsec.VerifyChain([]*x509.Certificate{leaf, intermediate.cert, root.cert}); err != nil {
			t.Fatalf("VerifyChain returned error: %v", err)
		}
	})

	t.Run("outside intermediate constraint", func(t *testing.T) {
		leaf := newLeaf(t, "other", intermediate, certOptions{dnsNames: []string{"other.example.net"}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, intermediate.cert, root.cert})
		if !errors.Is(err, netsec.ErrNameNotPermitted) {
			t.Fatalf("got %v, want ErrNameNotPermitted", err)
		}
	})
}

func TestExcludedSubtree(t *testing.T) {
	root := newRoot(t, "root", certOptions{
		permitted: []string{"example.com"},
		excluded:  []string{"bad.example.com"},
	})

	t.Run("excluded name rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"bad.example.com"}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
		if !errors.Is(err, netsec.ErrNameExcluded) {
			t.Fatalf("got %v, want ErrNameExcluded", err)
		}
	})

	t.Run("subdomain of excluded name rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"x.bad.example.com"}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
		if !errors.Is(err, netsec.ErrNameExcluded) {
			t.Fatalf("got %v, want ErrNameExcluded", err)
		}
	})

	t.Run("other name accepted", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"good.example.com"}})
		if err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert}); err != nil {
			t.Fatalf("VerifyChain returned error: %v", err)
		}
	})
}

func TestConstraintsOnIntermediateSANs(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	intermediate := newIntermediate(t, "intermediate", root, certOptions{
		dnsNames: []string{"ca.example.net"},
	})
	leaf := newLeaf(t, "leaf", intermediate, certOptions{dnsNames: []string{"api.example.com"}})

	err := netsec.VerifyChain([]*x509.Certificate{leaf, intermediate.cert, root.cert})
	if !errors.Is(err, netsec.ErrNameNotPermitted) {
		t.Fatalf("got %v, want ErrNameNotPermitted for intermediate SAN", err)
	}
}

func TestEmailConstraints(t *testing.T) {
	root := newRoot(t, "root", certOptions{permittedE: []string{"example.com"}})

	t.Run("permitted domain", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{emails: []string{"alice@mail.example.com"}})
		if err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert}); err != nil {
			t.Fatalf("VerifyChain returned error: %v", err)
		}
	})

	t.Run("other domain rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{emails: []string{"alice@example.net"}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
		if !errors.Is(err, netsec.ErrNameNotPermitted) {
			t.Fatalf("got %v, want ErrNameNotPermitted", err)
		}
	})
}

func TestExactMailboxConstraint(t *testing.T) {
	root := newRoot(t, "root", certOptions{permittedE: []string{"alice@example.com"}})

	t.Run("exact mailbox accepted", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{emails: []string{"alice@example.com"}})
		if err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert}); err != nil {
			t.Fatalf("VerifyChain returned error: %v", err)
		}
	})

	t.Run("other mailbox rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{emails: []string{"bob@example.com"}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
		if !errors.Is(err, netsec.ErrNameNotPermitted) {
			t.Fatalf("got %v, want ErrNameNotPermitted", err)
		}
	})
}

func TestIPConstraints(t *testing.T) {
	root := newRoot(t, "root", certOptions{permittedI: []*net.IPNet{ipNet(t, "10.0.0.0/8")}})

	t.Run("permitted range", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{ips: []net.IP{net.ParseIP("10.1.2.3")}})
		if err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert}); err != nil {
			t.Fatalf("VerifyChain returned error: %v", err)
		}
	})

	t.Run("other range rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{ips: []net.IP{net.ParseIP("192.168.1.1")}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
		if !errors.Is(err, netsec.ErrNameNotPermitted) {
			t.Fatalf("got %v, want ErrNameNotPermitted", err)
		}
	})
}

func TestURIConstraints(t *testing.T) {
	root := newRoot(t, "root", certOptions{permittedU: []string{"example.com"}})

	t.Run("permitted host", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{uris: []*url.URL{mustURL(t, "https://api.example.com/service")}})
		if err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert}); err != nil {
			t.Fatalf("VerifyChain returned error: %v", err)
		}
	})

	t.Run("other host rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{uris: []*url.URL{mustURL(t, "https://example.net/service")}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
		if !errors.Is(err, netsec.ErrNameNotPermitted) {
			t.Fatalf("got %v, want ErrNameNotPermitted", err)
		}
	})

	t.Run("IP host rejected", func(t *testing.T) {
		leaf := newLeaf(t, "leaf", root, certOptions{uris: []*url.URL{mustURL(t, "https://10.0.0.1/service")}})
		err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
		if !errors.Is(err, netsec.ErrUnmatchableName) {
			t.Fatalf("got %v, want ErrUnmatchableName", err)
		}
	})
}

func TestConstraintTypeOnlyRestrictsItsOwnNames(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com"}})
	leaf := newLeaf(t, "leaf", root, certOptions{
		emails: []string{"alice@example.net"},
		ips:    []net.IP{net.ParseIP("203.0.113.10")},
	})

	if err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert}); err != nil {
		t.Fatalf("DNS-only constraint must not restrict email or IP: %v", err)
	}
}

func TestExcludedEmailIPAndURI(t *testing.T) {
	root := newRoot(t, "root", certOptions{
		excludedE: []string{"example.com"},
		excludedI: []*net.IPNet{ipNet(t, "10.0.0.0/8")},
		excludedU: []string{"example.com"},
	})

	tests := []struct {
		name string
		opts certOptions
	}{
		{name: "email", opts: certOptions{emails: []string{"alice@example.com"}}},
		{name: "IP", opts: certOptions{ips: []net.IP{net.ParseIP("10.1.2.3")}}},
		{name: "URI", opts: certOptions{uris: []*url.URL{mustURL(t, "https://api.example.com/")}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			leaf := newLeaf(t, "leaf", root, tc.opts)
			err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
			if !errors.Is(err, netsec.ErrNameExcluded) {
				t.Fatalf("got %v, want ErrNameExcluded", err)
			}
		})
	}
}

func TestConstraintComparisonLimit(t *testing.T) {
	root := newRoot(t, "root", certOptions{permitted: []string{"example.com", "example.net"}})
	leaf := newLeaf(t, "leaf", root, certOptions{dnsNames: []string{"api.example.net"}})

	err := netsec.VerifyChainWithOptions([]*x509.Certificate{leaf, root.cert}, &netsec.Options{
		MaxConstraintComparisons: 1,
	})
	if !errors.Is(err, netsec.ErrTooManyConstraints) {
		t.Fatalf("got %v, want ErrTooManyConstraints", err)
	}
}

func TestUnhandledCriticalExtensionRejected(t *testing.T) {
	root := newRoot(t, "root", certOptions{})
	tmpl := defaultTemplate("leaf", certOptions{
		dnsNames: []string{"api.example.com"},
		extraExtensions: []pkix.Extension{{
			Id:       []int{1, 2, 3, 4},
			Critical: true,
			Value:    []byte{5, 0},
		}},
	})
	leaf := issue(t, tmpl, root)

	err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
	if !errors.Is(err, netsec.ErrUnhandledCriticalExtension) {
		t.Fatalf("got %v, want ErrUnhandledCriticalExtension", err)
	}
}

func TestExpiredCertificateRejected(t *testing.T) {
	root := newRoot(t, "root", certOptions{})
	leaf := newLeaf(t, "leaf", root, certOptions{
		dnsNames:  []string{"api.example.com"},
		notBefore: time.Now().Add(-48 * time.Hour),
		notAfter:  time.Now().Add(-24 * time.Hour),
	})

	err := netsec.VerifyChain([]*x509.Certificate{leaf, root.cert})
	if !errors.Is(err, netsec.ErrCertificateExpired) {
		t.Fatalf("got %v, want ErrCertificateExpired", err)
	}
}
