package vex

import (
	"encoding/json"
	"time"
)

// CycloneDX field names used for VEX adoption.
const (
	// PropertyIssuerID carries a per-vulnerability issuer identifier in a
	// vulnerability's properties array. When absent, the document-level
	// authors are used as fallback issuers.
	PropertyIssuerID = "netsec:issuer_id"
)

// cdxDocument is the subset of a CycloneDX 1.5/1.6 BOM/VEX document needed
// for adoption. Unknown fields are ignored by encoding/json.
type cdxDocument struct {
	BomFormat       string         `json:"bomFormat"`
	SpecVersion     string         `json:"specVersion"`
	Metadata        cdxMetadata    `json:"metadata"`
	Vulnerabilities []cdxVuln      `json:"vulnerabilities"`
	Components      []cdxComponent `json:"components"`
}

type cdxMetadata struct {
	Timestamp string      `json:"timestamp"`
	Authors   []cdxAuthor `json:"authors"`
}

type cdxAuthor struct {
	BOMRef string `json:"bom-ref"`
	Name   string `json:"name"`
}

type cdxComponent struct {
	BOMRef   string       `json:"bom-ref"`
	Type     string       `json:"type"`
	Name     string       `json:"name"`
	Version  string       `json:"version"`
	PURL     string       `json:"purl"`
	Supplier *cdxSupplier `json:"supplier"`
	Released string       `json:"release"`
}

type cdxSupplier struct {
	Name string `json:"name"`
}

type cdxVuln struct {
	BOMRef     string        `json:"bom-ref"`
	ID         string        `json:"id"`
	Analysis   cdxAnalysis   `json:"analysis"`
	Affects    []cdxAffect   `json:"affects"`
	Published  string        `json:"published"`
	Properties []cdxProperty `json:"properties"`
}

type cdxAnalysis struct {
	State         string `json:"state"`
	Justification string `json:"justification"`
}

type cdxAffect struct {
	Ref   string   `json:"ref"`
	Range cdxRange `json:"range"`
}

type cdxRange struct {
	PURL string `json:"purl"`
}

type cdxProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ParseCycloneDX decodes a CycloneDX VEX document into the native Statement
// and Component values consumed by Engine.
//
// Issuer identity for a vulnerability comes from its netsec:issuer_id
// property; otherwise each document author (bom-ref, then name) yields one
// statement. Statement time comes from the vulnerability's published field,
// falling back to metadata.timestamp. Malformed timestamps make the decoded
// statement carry a zero time, which adoption rejects as missing/stale.
func ParseCycloneDX(data []byte) (statements []Statement, components []Component, err error) {
	var doc cdxDocument
	if err = json.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}

	documentTime := parseCDXTime(doc.Metadata.Timestamp)
	issuers := make([]string, 0, len(doc.Metadata.Authors))
	for _, author := range doc.Metadata.Authors {
		if author.BOMRef != "" {
			issuers = append(issuers, author.BOMRef)
		} else if author.Name != "" {
			issuers = append(issuers, author.Name)
		}
	}

	for _, c := range doc.Components {
		comp := Component{
			BOMRef:     c.BOMRef,
			PURL:       c.PURL,
			Version:    c.Version,
			ReleasedAt: parseCDXTime(c.Released),
		}
		if c.Supplier != nil {
			comp.Supplier = c.Supplier.Name
		}
		components = append(components, comp)
	}

	for _, vuln := range doc.Vulnerabilities {
		stmt := Statement{
			VulnerabilityID: vuln.ID,
			State:           State(vuln.Analysis.State),
			Justification:   Justification(vuln.Analysis.Justification),
			IssuedAt:        firstTime(parseCDXTime(vuln.Published), documentTime),
		}
		for _, affect := range vuln.Affects {
			stmt.Affects = append(stmt.Affects, Affect{
				BOMRef:    affect.Ref,
				PURLRange: affect.Range.PURL,
			})
		}

		stmtIssuers := issuers
		if id := propertyValue(vuln.Properties, PropertyIssuerID); id != "" {
			stmtIssuers = []string{id}
		}
		if len(stmtIssuers) == 0 {
			// Emit an issuer-less statement so Engine.Adopt fails it as
			// untrusted, rather than silently dropping the claim.
			stmtIssuers = []string{""}
		}
		for _, issuer := range stmtIssuers {
			copy := stmt
			copy.Issuer = issuer
			statements = append(statements, copy)
		}
	}
	return statements, components, nil
}

func propertyValue(props []cdxProperty, name string) string {
	for _, prop := range props {
		if prop.Name == name {
			return prop.Value
		}
	}
	return ""
}

func firstTime(times ...time.Time) time.Time {
	for _, t := range times {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// parseCDXTime parses CycloneDX RFC 3339 timestamps, tolerating a trailing
// "Z" even when fractional seconds are absent. Unparseable input yields the
// zero time so adoption fails closed.
func parseCDXTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
