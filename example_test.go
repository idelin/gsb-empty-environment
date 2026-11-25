package workloadid_test

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	workloadid "prescreen.local/ps-174fa6fd5f"
)

// Example shows the control-plane wiring: build a manager, activate an atomic
// bundle generation, configure caller/target policies and attach the verifier
// to a tls.Config so every connection (and any resumed session) is re-authorized.
func Example() {
	// In production the clock is workloadid.RealClock{}; tests inject a fake.
	manager := workloadid.NewManager(workloadid.RealClock{})

	var roots []*x509.Certificate // loaded from the mesh trust bundle
	var intermediates []*x509.Certificate
	config := workloadid.BundleConfig{
		TrustDomain:   "prod.example.com",
		MaxChainDepth: 5,
		MaxLeafTTL:    24 * time.Hour,
		Revocation: workloadid.RevocationConfig{
			OCSPGrace:        30 * time.Second,
			CRLGrace:         time.Minute,
			UnknownIsRevoked: true,
			MaxSkew:          time.Minute,
		},
	}
	// Generation 1 becomes active atomically; no partial state is observable.
	if _, err := manager.Activate(1, config, workloadid.RootBundle{
		Roots: roots, Intermediates: intermediates,
	}); err != nil {
		fmt.Println("activate:", err)
		return
	}

	callerPolicy := &workloadid.Policy{
		Name:                "caller-payments",
		TrustDomain:         "prod.example.com",
		AllowedPathPrefixes: [][]string{{"ns", "payments"}},
		MaxPathSegments:     6,
		AllowedEKUs:         []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		// Wildcards are opt-in, left-most-label only, single label, no IDN mix.
		AllowedDNS: &workloadid.DNSRule{
			AllowedPatterns: []string{"*.prod.example.com"},
			MaxLabels:       8,
		},
		RequireAttestation: &workloadid.NodeAttestation{
			Type:              "k8s-sa",
			AllowedPrincipals: []string{"payments-sa"},
			MaxAge:            5 * time.Minute,
		},
	}
	targetPolicy := &workloadid.Policy{
		Name:                "target-payments",
		TrustDomain:         "prod.example.com",
		AllowedPathPrefixes: [][]string{{"ns", "payments"}},
		MaxPathSegments:     6,
		AllowedEKUs:         []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	verifier := workloadid.NewVerifier(manager)
	authorizer := &workloadid.TLSAuthorizer{
		Verifier:     verifier,
		CallerPolicy: callerPolicy,
		TargetPolicy: targetPolicy,
		ServerName:   "api.prod.example.com",
		Attest:       func() *workloadid.NodeAttestationClaim { return nil },
	}
	tlsConfig, err := authorizer.TLSConfig(manager)
	if err != nil {
		fmt.Println("tls config:", err)
		return
	}

	// Tickets are disabled by default, so every connection is fully
	// re-authenticated. Setting AllowResumption opts in but VerifyConnection
	// still re-runs both policies against the newest bundle generation.
	_ = tls.VersionTLS12
	fmt.Println(tlsConfig.SessionTicketsDisabled)
	// Output: true
}
