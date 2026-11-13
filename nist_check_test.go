package netsec

import (
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestNISTPKITSPolicy runs the NIST PKITS policy vectors when explicitly
// requested. It is skipped by default because the certificate corpus lives in
// the Go distribution and is not part of this module.
func TestNISTPKITSPolicy(t *testing.T) {
	if os.Getenv("RUN_NIST") == "" {
		t.Skip("set RUN_NIST=1 to run the NIST PKITS policy corpus")
	}

	goroot := runtime.GOROOT()
	dataDir := filepath.Join(goroot, "src", "crypto", "x509", "testdata", "nist-pkits")
	vectorsPath := filepath.Join(dataDir, "vectors.json")
	b, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Skipf("NIST corpus unavailable: %v", err)
	}

	var testcases []struct {
		Name                        string
		CertPath                    []string
		InitialPolicySet            []string
		InitialPolicyMappingInhibit bool
		InitialExplicitPolicy       bool
		InitialAnyPolicyInhibit     bool
		ShouldValidate              bool
	}
	if err := json.Unmarshal(b, &testcases); err != nil {
		t.Fatal(err)
	}

	policyNames := map[string]OID{
		"anyPolicy":          AnyPolicyOID,
		"NIST-test-policy-1": mustOIDFromInts([]uint64{2, 16, 840, 1, 101, 3, 2, 1, 48, 1}),
		"NIST-test-policy-2": mustOIDFromInts([]uint64{2, 16, 840, 1, 101, 3, 2, 1, 48, 2}),
		"NIST-test-policy-3": mustOIDFromInts([]uint64{2, 16, 840, 1, 101, 3, 2, 1, 48, 3}),
		"NIST-test-policy-6": mustOIDFromInts([]uint64{2, 16, 840, 1, 101, 3, 2, 1, 48, 6}),
	}

	for _, tc := range testcases {
		if !isPolicyVector(tc.Name) {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			var chain []*x509.Certificate
			for _, name := range tc.CertPath {
				der, err := os.ReadFile(filepath.Join(dataDir, "certs", name))
				if err != nil {
					t.Fatal(err)
				}
				cert, err := x509.ParseCertificate(der)
				if err != nil {
					t.Fatal(err)
				}
				chain = append(chain, cert)
			}
			slices.Reverse(chain)

			var initialPolicies []OID
			for _, name := range tc.InitialPolicySet {
				initialPolicies = append(initialPolicies, policyNames[name])
			}

			valid := policiesValid(chain, VerifyOptions{
				CertificatePolicies:   initialPolicies,
				inhibitPolicyMapping:  tc.InitialPolicyMappingInhibit,
				requireExplicitPolicy: tc.InitialExplicitPolicy,
				inhibitAnyPolicy:      tc.InitialAnyPolicyInhibit,
			})
			if valid != tc.ShouldValidate {
				t.Errorf("policiesValid = %v, want %v", valid, tc.ShouldValidate)
			}
		})
	}
}

func isPolicyVector(name string) bool {
	for _, prefix := range []string{"4.8.", "4.9.", "4.10.", "4.11.", "4.12."} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
