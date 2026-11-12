package vex

import (
	"testing"
	"time"
)

const cdxVEXDoc = `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "metadata": {
    "timestamp": "2026-02-01T00:00:00Z",
    "authors": [{"bom-ref": "issuer-acme-psirt", "name": "ACME PSIRT"}]
  },
  "components": [
    {
      "bom-ref": "comp-libxml2-2.9.10",
      "type": "library",
      "name": "libxml2",
      "version": "2.9.10",
      "purl": "pkg:generic/acme/libxml2@2.9.10",
      "supplier": {"name": "acme"},
      "release": "2026-01-15T00:00:00Z"
    }
  ],
  "vulnerabilities": [
    {
      "id": "CVE-2026-0001",
      "analysis": {
        "state": "not_affected",
        "justification": "code_not_reachable"
      },
      "affects": [
        {
          "ref": "comp-libxml2-2.9.10",
          "range": {"purl": "pkg:generic/acme/libxml2@vers:semver/>=2.9.0|<3.0.0"}
        }
      ]
    },
    {
      "id": "CVE-2026-0002",
      "analysis": {"state": "not_affected"},
      "affects": [
        {"ref": "comp-libxml2-2.9.10", "range": {"purl": "pkg:generic/acme/libxml2@2.9.10"}}
      ]
    },
    {
      "id": "CVE-2026-0003",
      "properties": [{"name": "netsec:issuer_id", "value": "issuer-attacker"}],
      "analysis": {
        "state": "not_affected",
        "justification": "protected_by_mitigating_control"
      },
      "affects": [
        {"ref": "comp-libxml2-2.9.10", "range": {"purl": "pkg:generic/acme/libxml2@2.9.10"}}
      ]
    }
  ]
}`

func TestParseCycloneDXAndAdopt(t *testing.T) {
	statements, components, err := ParseCycloneDX([]byte(cdxVEXDoc))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(components) != 1 {
		t.Fatalf("got %d components, want 1", len(components))
	}
	comp := components[0]
	if comp.BOMRef != testBOMRef || comp.Supplier != testSupplier ||
		!comp.ReleasedAt.Equal(releasedAt) {
		t.Fatalf("unexpected component: %+v", comp)
	}

	// Two document-author statements plus one attacker statement.
	if len(statements) != 3 {
		t.Fatalf("got %d statements, want 3", len(statements))
	}

	engine := testEngine()
	alerts := []Alert{
		{VulnerabilityID: "CVE-2026-0001", BOMRef: testBOMRef},
		{VulnerabilityID: "CVE-2026-0002", BOMRef: testBOMRef},
		{VulnerabilityID: "CVE-2026-0003", BOMRef: testBOMRef},
	}
	results := engine.Apply(alerts, components, statements)

	want := map[string]bool{
		"CVE-2026-0001": true,  // fully compliant vendor suppression
		"CVE-2026-0002": false, // not_affected without justification
		"CVE-2026-0003": false, // issuer outside trusted set
	}
	for _, result := range results {
		if result.Suppressed != want[result.Alert.VulnerabilityID] {
			t.Errorf("%s suppressed=%v, want %v (decisions: %+v)",
				result.Alert.VulnerabilityID, result.Suppressed,
				want[result.Alert.VulnerabilityID], result.Decisions)
		}
	}

	byID := map[string]AggregationResult{}
	for _, result := range results {
		byID[result.Alert.VulnerabilityID] = result
	}
	if !hasReason(byID["CVE-2026-0002"].Decisions[0].RejectReasons,
		ReasonMissingJustification) {
		t.Fatalf("CVE-2026-0002 should fail on missing justification: %+v",
			byID["CVE-2026-0002"].Decisions[0])
	}
	if !hasReason(byID["CVE-2026-0003"].Decisions[0].RejectReasons,
		ReasonIssuerNotTrusted) {
		t.Fatalf("CVE-2026-0003 should fail on untrusted issuer: %+v",
			byID["CVE-2026-0003"].Decisions[0])
	}

	if !statements[0].IssuedAt.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("document timestamp fallback failed: %v", statements[0].IssuedAt)
	}
	if statements[2].Issuer != attackerID {
		t.Fatalf("per-vulnerability issuer property not honored: %+v", statements[2])
	}
}

func TestParseCycloneDXRejectsMalformedJSON(t *testing.T) {
	if _, _, err := ParseCycloneDX([]byte("{not json")); err == nil {
		t.Fatal("malformed document must return an error")
	}
}

func TestParseCycloneDXMalformedTimestampFailsClosed(t *testing.T) {
	doc := `{
	  "bomFormat": "CycloneDX",
	  "metadata": {"timestamp": "not-a-timestamp"},
	  "vulnerabilities": [{
	    "id": "CVE-2026-0001",
	    "analysis": {"state": "not_affected", "justification": "code_not_present"},
	    "affects": [{"ref": "ref", "range": {"purl": "pkg:generic/acme/libxml2@2.9.10"}}]
	  }]
	}`
	statements, _, err := ParseCycloneDX([]byte(doc))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !statements[0].IssuedAt.IsZero() {
		t.Fatalf("malformed timestamp must decode to zero time, got %v", statements[0].IssuedAt)
	}
}
