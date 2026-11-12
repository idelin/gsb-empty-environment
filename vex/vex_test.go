package vex

import (
	"testing"
	"time"
)

const (
	testBOMRef   = "comp-libxml2-2.9.10"
	testPURL     = "pkg:generic/acme/libxml2@2.9.10"
	testSupplier = "acme"
	testVersion  = "2.9.10"
	testVulnID   = "CVE-2026-0001"
	trustedPSIRT = "issuer-acme-psirt"
	attackerID   = "issuer-attacker"
)

var (
	releasedAt = time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	issuedAt   = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	staleAt    = time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
)

func testComponent() Component {
	return Component{
		BOMRef:     testBOMRef,
		PURL:       testPURL,
		Supplier:   testSupplier,
		Version:    testVersion,
		ReleasedAt: releasedAt,
	}
}

func testEngine() Engine {
	return Engine{Policy: NewTrustPolicy(map[string][]string{
		testSupplier: {trustedPSIRT},
	})}
}

// validStatement returns a fully compliant not_affected claim; individual
// tests mutate one field to exercise each rejection path.
func validStatement() Statement {
	return Statement{
		VulnerabilityID: testVulnID,
		State:           StateNotAffected,
		Justification:   JustificationCodeNotReachable,
		Affects: []Affect{{
			BOMRef:    testBOMRef,
			PURLRange: "pkg:generic/acme/libxml2@vers:semver/>=2.9.0|<3.0.0",
		}},
		Issuer:   trustedPSIRT,
		IssuedAt: issuedAt,
	}
}

func hasReason(reasons []RejectReason, want RejectReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func TestAdoptRequiredScenarios(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*Statement)
		wantAdopt  bool
		wantReason RejectReason
	}{
		{
			name:      "legitimate not_affected suppresses",
			mutate:    func(*Statement) {},
			wantAdopt: true,
		},
		{
			name:       "missing justification is not trusted",
			mutate:     func(s *Statement) { s.Justification = "" },
			wantAdopt:  false,
			wantReason: ReasonMissingJustification,
		},
		{
			name:       "untrusted issuer is rejected",
			mutate:     func(s *Statement) { s.Issuer = attackerID },
			wantAdopt:  false,
			wantReason: ReasonIssuerNotTrusted,
		},
		{
			name: "version outside purl range is rejected",
			mutate: func(s *Statement) {
				s.Affects[0].PURLRange = "pkg:generic/acme/libxml2@vers:semver/>=3.0.0|<4.0.0"
			},
			wantAdopt:  false,
			wantReason: ReasonVersionOutsideRange,
		},
		{
			name:       "statement older than component release is rejected",
			mutate:     func(s *Statement) { s.IssuedAt = staleAt },
			wantAdopt:  false,
			wantReason: ReasonStaleTimestamp,
		},
	}

	engine := testEngine()
	comp := testComponent()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := validStatement()
			tt.mutate(&stmt)
			decision := engine.Adopt(stmt, comp)

			if decision.Adopted != tt.wantAdopt {
				t.Fatalf("Adopted = %v, want %v (reasons: %v)",
					decision.Adopted, tt.wantAdopt, decision.RejectReasons)
			}
			if tt.wantAdopt {
				if !decision.Suppresses {
					t.Fatalf("adopted not_affected must suppress, reasons: %v", decision.RejectReasons)
				}
				return
			}
			if decision.Suppresses {
				t.Fatalf("rejected statement must not suppress, reasons: %v", decision.RejectReasons)
			}
			if !hasReason(decision.RejectReasons, tt.wantReason) {
				t.Fatalf("reasons = %v, want to contain %q", decision.RejectReasons, tt.wantReason)
			}
		})
	}
}

func TestAdoptAffectsMatching(t *testing.T) {
	engine := testEngine()
	comp := testComponent()

	t.Run("bom-ref mismatch", func(t *testing.T) {
		stmt := validStatement()
		stmt.Affects[0].BOMRef = "comp-other"
		decision := engine.Adopt(stmt, comp)
		if decision.Adopted || !hasReason(decision.RejectReasons, ReasonBOMRefMismatch) {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})

	t.Run("purl coordinate mismatch", func(t *testing.T) {
		stmt := validStatement()
		stmt.Affects[0].PURLRange = "pkg:generic/acme/libxslt@2.9.10"
		decision := engine.Adopt(stmt, comp)
		if decision.Adopted || !hasReason(decision.RejectReasons, ReasonPURLMismatch) {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})

	t.Run("concrete purl exact version matches", func(t *testing.T) {
		stmt := validStatement()
		stmt.Affects[0].PURLRange = testPURL
		decision := engine.Adopt(stmt, comp)
		if !decision.Adopted || !decision.Suppresses {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})

	t.Run("interval range excludes component", func(t *testing.T) {
		stmt := validStatement()
		stmt.Affects[0].PURLRange = "pkg:generic/acme/libxml2@[2.0.0,2.9.0)"
		decision := engine.Adopt(stmt, comp)
		if decision.Adopted || !hasReason(decision.RejectReasons, ReasonVersionOutsideRange) {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})

	t.Run("one matching entry among several is sufficient", func(t *testing.T) {
		stmt := validStatement()
		stmt.Affects = append([]Affect{{
			BOMRef:    "comp-other",
			PURLRange: testPURL,
		}}, stmt.Affects...)
		decision := engine.Adopt(stmt, comp)
		if !decision.Adopted {
			t.Fatalf("got reasons=%v", decision.RejectReasons)
		}
	})
}

func TestAdoptStatesAndJustifications(t *testing.T) {
	engine := testEngine()
	comp := testComponent()

	for _, state := range []State{
		StateExploitable,
		StateInTriage,
		StateFalsePositive,
		StateResolved,
	} {
		t.Run(string(state)+" never suppresses", func(t *testing.T) {
			stmt := validStatement()
			stmt.State = state
			decision := engine.Adopt(stmt, comp)
			if !decision.Adopted {
				t.Fatalf("state %s should be adopted, reasons=%v", state, decision.RejectReasons)
			}
			if decision.Suppresses {
				t.Fatalf("state %s must not suppress alerts", state)
			}
		})
	}

	t.Run("unknown state rejected", func(t *testing.T) {
		stmt := validStatement()
		stmt.State = State("totally_mitigated")
		decision := engine.Adopt(stmt, comp)
		if decision.Adopted || !hasReason(decision.RejectReasons, ReasonUnknownState) {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})

	for _, justification := range []Justification{
		JustificationCodeNotPresent,
		JustificationCodeNotReachable,
		JustificationRequiresConfiguration,
		JustificationProtectedByMitigatingControl,
	} {
		t.Run("justification "+string(justification), func(t *testing.T) {
			stmt := validStatement()
			stmt.Justification = justification
			decision := engine.Adopt(stmt, comp)
			if !decision.Adopted || !decision.Suppresses {
				t.Fatalf("justification %s rejected: %v", justification, decision.RejectReasons)
			}
		})
	}

	t.Run("unrecognized justification rejected", func(t *testing.T) {
		stmt := validStatement()
		stmt.Justification = Justification("trust_me_bro")
		decision := engine.Adopt(stmt, comp)
		if decision.Adopted || !hasReason(decision.RejectReasons, ReasonUnknownJustification) {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})

	t.Run("justification ignored outside not_affected", func(t *testing.T) {
		stmt := validStatement()
		stmt.State = StateExploitable
		stmt.Justification = ""
		decision := engine.Adopt(stmt, comp)
		if !decision.Adopted {
			t.Fatalf("empty justification should not matter for exploitable, reasons=%v",
				decision.RejectReasons)
		}
	})
}

func TestAdoptFreshnessBoundary(t *testing.T) {
	engine := testEngine()
	comp := testComponent()

	t.Run("statement issued exactly at release is accepted", func(t *testing.T) {
		stmt := validStatement()
		stmt.IssuedAt = releasedAt
		decision := engine.Adopt(stmt, comp)
		if !decision.Adopted || hasReason(decision.RejectReasons, ReasonStaleTimestamp) {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})

	t.Run("missing timestamp rejected", func(t *testing.T) {
		stmt := validStatement()
		stmt.IssuedAt = time.Time{}
		decision := engine.Adopt(stmt, comp)
		if decision.Adopted || !hasReason(decision.RejectReasons, ReasonMissingTimestamp) {
			t.Fatalf("got adopted=%v reasons=%v", decision.Adopted, decision.RejectReasons)
		}
	})
}

func TestApplyRetainsOriginalAlerts(t *testing.T) {
	engine := testEngine()
	comp := testComponent()

	suppressible := validStatement()

	missingJustification := validStatement()
	missingJustification.VulnerabilityID = "CVE-2026-0002"
	missingJustification.Justification = ""

	forged := validStatement()
	forged.VulnerabilityID = "CVE-2026-0003"
	forged.Issuer = attackerID

	explorable := validStatement()
	explorable.VulnerabilityID = "CVE-2026-0004"
	explorable.State = StateExploitable

	alerts := []Alert{
		{VulnerabilityID: "CVE-2026-0001", BOMRef: testBOMRef},
		{VulnerabilityID: "CVE-2026-0002", BOMRef: testBOMRef},
		{VulnerabilityID: "CVE-2026-0003", BOMRef: testBOMRef},
		{VulnerabilityID: "CVE-2026-0004", BOMRef: testBOMRef},
	}
	statements := []Statement{suppressible, missingJustification, forged, explorable}

	results := engine.Apply(alerts, []Component{comp}, statements)
	if len(results) != len(alerts) {
		t.Fatalf("got %d results, want %d", len(results), len(alerts))
	}

	wantSuppressed := map[string]bool{"CVE-2026-0001": true}
	for _, result := range results {
		if result.Suppressed != wantSuppressed[result.Alert.VulnerabilityID] {
			t.Errorf("alert %s suppressed=%v, want %v",
				result.Alert.VulnerabilityID, result.Suppressed,
				wantSuppressed[result.Alert.VulnerabilityID])
		}
	}

	retained := Retained(results)
	if len(retained) != 3 {
		t.Fatalf("got %d retained alerts, want 3: %+v", len(retained), retained)
	}
	for _, alert := range retained {
		if alert.VulnerabilityID == "CVE-2026-0001" {
			t.Fatal("suppressed alert must not appear in retained output")
		}
	}
}

func TestApplyUnknownComponentFailsClosed(t *testing.T) {
	engine := testEngine()
	alert := Alert{VulnerabilityID: testVulnID, BOMRef: "comp-missing"}
	results := engine.Apply([]Alert{alert}, []Component{testComponent()},
		[]Statement{validStatement()})
	if len(results) != 1 || results[0].Suppressed {
		t.Fatalf("alert without component must be retained, got %+v", results)
	}
}
