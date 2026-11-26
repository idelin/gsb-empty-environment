package spiffefed_test

import (
	"errors"
	"testing"

	spiffefed "formal.local/w27-6accbac653"
)

func TestWorkloadMatcherRequiresExactSelectorSets(t *testing.T) {
	t.Parallel()
	registration := spiffefed.WorkloadRegistration{
		ID:               "payment-api",
		NodeSelectors:    []string{"node:region:cn-east", "node:role:worker"},
		ProcessSelectors: []string{"k8s:ns:payment", "k8s:sa:api", "unix:uid:1001"},
	}
	matcher, err := spiffefed.NewWorkloadMatcher([]spiffefed.WorkloadRegistration{registration})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	valid := validEvidence()
	matched, err := matcher.Match(valid)
	if err != nil {
		t.Fatalf("match valid evidence: %v", err)
	}
	if matched.ID != registration.ID {
		t.Fatalf("matched %q, want %q", matched.ID, registration.ID)
	}

	cases := []struct {
		name   string
		mutate func(*spiffefed.AttestationEvidence)
	}{
		{
			name: "extra node selector",
			mutate: func(evidence *spiffefed.AttestationEvidence) {
				evidence.Node.Selectors = append(evidence.Node.Selectors, "node:zone:extra")
			},
		},
		{
			name: "missing node selector",
			mutate: func(evidence *spiffefed.AttestationEvidence) {
				evidence.Node.Selectors = evidence.Node.Selectors[:1]
			},
		},
		{
			name: "extra process selector",
			mutate: func(evidence *spiffefed.AttestationEvidence) {
				evidence.Process.Selectors = append(evidence.Process.Selectors, "k8s:sa:admin")
			},
		},
		{
			name: "missing process selector",
			mutate: func(evidence *spiffefed.AttestationEvidence) {
				evidence.Process.Selectors = evidence.Process.Selectors[:2]
			},
		},
		{
			name: "wildcard evidence selector",
			mutate: func(evidence *spiffefed.AttestationEvidence) {
				evidence.Process.Selectors = []string{"k8s:ns:payment", "k8s:sa:*", "unix:uid:1001"}
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			evidence := validEvidence()
			tc.mutate(&evidence)
			if _, err := matcher.Match(evidence); !errors.Is(err, spiffefed.ErrAttestationDenied) {
				t.Fatalf("error = %v, want ErrAttestationDenied", err)
			}
		})
	}
}

func TestWorkloadMatcherRejectsNamespaceSpoofing(t *testing.T) {
	t.Parallel()
	registration := spiffefed.WorkloadRegistration{
		ID:               "payment-api",
		NodeSelectors:    []string{"node:region:cn-east", "node:role:worker"},
		ProcessSelectors: []string{"k8s:ns:payment", "k8s:sa:api", "unix:uid:1001"},
	}
	matcher, err := spiffefed.NewWorkloadMatcher([]spiffefed.WorkloadRegistration{registration})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	t.Run("selector namespace does not match attestation", func(t *testing.T) {
		evidence := validEvidence()
		evidence.Process.Namespace = "default"
		if _, err := matcher.Match(evidence); !errors.Is(err, spiffefed.ErrAttestationDenied) {
			t.Fatalf("error = %v, want ErrAttestationDenied", err)
		}
	})

	t.Run("privileged host ancestor crosses namespace", func(t *testing.T) {
		evidence := validEvidence()
		evidence.Process.Ancestors[0] = spiffefed.ProcessRecord{
			PID:        evidence.Process.ParentPID,
			ParentPID:  1,
			Namespace:  "host",
			UID:        0,
			Privileged: true,
		}
		if _, err := matcher.Match(evidence); !errors.Is(err, spiffefed.ErrAttestationDenied) {
			t.Fatalf("error = %v, want ErrAttestationDenied", err)
		}
	})

	t.Run("missing ancestry", func(t *testing.T) {
		evidence := validEvidence()
		evidence.Process.Ancestors = nil
		if _, err := matcher.Match(evidence); !errors.Is(err, spiffefed.ErrAttestationDenied) {
			t.Fatalf("error = %v, want ErrAttestationDenied", err)
		}
	})
}

func TestWorkloadMatcherRejectsWildcardAndAmbiguousRegistrations(t *testing.T) {
	t.Parallel()
	wildcard := spiffefed.WorkloadRegistration{
		ID:               "wildcard",
		NodeSelectors:    []string{"node:region:*"},
		ProcessSelectors: []string{"k8s:ns:payment", "k8s:sa:api"},
	}
	if _, err := spiffefed.NewWorkloadMatcher([]spiffefed.WorkloadRegistration{wildcard}); !errors.Is(err, spiffefed.ErrInvalidSelector) {
		t.Fatalf("wildcard error = %v, want ErrInvalidSelector", err)
	}

	registration := spiffefed.WorkloadRegistration{
		ID:               "one",
		NodeSelectors:    []string{"node:region:cn-east", "node:role:worker"},
		ProcessSelectors: []string{"k8s:ns:payment", "k8s:sa:api", "unix:uid:1001"},
	}
	duplicate := registration
	duplicate.ID = "two"
	if _, err := spiffefed.NewWorkloadMatcher([]spiffefed.WorkloadRegistration{registration, duplicate}); !errors.Is(err, spiffefed.ErrAmbiguousRegistration) {
		t.Fatalf("ambiguous registration error = %v, want ErrAmbiguousRegistration", err)
	}
}

func validEvidence() spiffefed.AttestationEvidence {
	return spiffefed.AttestationEvidence{
		Node: spiffefed.NodeAttestation{
			NodeID:    "node-1",
			Selectors: []string{"node:region:cn-east", "node:role:worker"},
		},
		Process: spiffefed.ProcessAttestation{
			ProcessRecord: spiffefed.ProcessRecord{
				PID:       100,
				ParentPID: 50,
				Namespace: "payment",
				UID:       1001,
			},
			Selectors: []string{"k8s:ns:payment", "k8s:sa:api", "unix:uid:1001"},
			Ancestors: []spiffefed.ProcessRecord{
				{PID: 50, ParentPID: 1, Namespace: "payment", UID: 0, Privileged: true},
				{PID: 1, ParentPID: 0, Namespace: "payment", UID: 0, Privileged: true},
			},
		},
	}
}
