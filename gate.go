package netsec

import (
	"fmt"
	"sort"
	"strings"

	"example.org/netsec/q0318/spdx"
)

// Policy is the release-gate license policy. Keys are SPDX identifiers
// (matched case-insensitively). A WITH exception may be granted explicitly
// with the key "id WITH exception"; otherwise the base identifier grant
// covers it.
type Policy struct {
	Allowed map[string]bool
}

// NewPolicy builds a Policy from a list of allowed SPDX identifiers.
func NewPolicy(allowed ...string) Policy {
	set := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		set[normalizeToken(id)] = true
	}
	return Policy{Allowed: set}
}

// Violation describes one reachable component whose license is not allowed.
type Violation struct {
	BOMRef     string
	Name       string
	Version    string
	PackageURL string
	License    string
	Reason     string
}

// Report is the outcome of evaluating a BOM against a policy.
type Report struct {
	InScope    []Component
	Violations []Violation
}

// Allowed reports whether every reachable component satisfies the policy.
func (r Report) Allowed() bool { return len(r.Violations) == 0 }

// Evaluate derives the reachable scope and applies the license policy. It
// fails hard on structural BOM problems (duplicate bom-refs, purl/version
// conflicts, dangling edges) and otherwise returns a per-component report:
// the gate never trusts the self-reported component scope.
func (p Policy) Evaluate(bom *BOM) (Report, error) {
	scope, err := bom.ReachableComponents()
	if err != nil {
		return Report{}, err
	}

	report := Report{InScope: scope}
	for _, comp := range scope {
		expr, licenseText, err := componentExpression(comp)
		if err != nil {
			report.Violations = append(report.Violations, Violation{
				BOMRef: comp.BOMRef, Name: comp.Name, Version: comp.Version,
				PackageURL: comp.PackageURL, License: licenseText, Reason: err.Error(),
			})
			continue
		}
		if !expr.Allowed(p.Allowed) {
			report.Violations = append(report.Violations, Violation{
				BOMRef: comp.BOMRef, Name: comp.Name, Version: comp.Version,
				PackageURL: comp.PackageURL, License: expr.String(),
				Reason: "license not permitted by release policy",
			})
		}
	}
	sort.SliceStable(report.Violations, func(i, j int) bool {
		return report.Violations[i].BOMRef < report.Violations[j].BOMRef
	})
	return report, nil
}

// componentExpression turns a CycloneDX licenses array into one SPDX
// expression. Multiple array entries are alternative licenses (OR semantics);
// an explicit expression entry is parsed with full AND/OR/WITH semantics.
func componentExpression(comp Component) (spdx.Expr, string, error) {
	if len(comp.Licenses) == 0 {
		return nil, "", fmt.Errorf("component declares no license")
	}

	var branches []spdx.Expr
	var rendered []string
	for _, entry := range comp.Licenses {
		switch {
		case entry.Expression != "":
			expr, err := spdx.Parse(entry.Expression)
			if err != nil {
				return nil, entry.Expression, err
			}
			branches = append(branches, expr)
			rendered = append(rendered, expr.String())
		case entry.License != nil:
			token := licenseToken(*entry.License)
			if token == "" {
				return nil, "", fmt.Errorf("component has empty license entry")
			}
			expr, err := spdx.Parse(token)
			if err != nil {
				return nil, token, err
			}
			branches = append(branches, expr)
			rendered = append(rendered, expr.String())
		default:
			return nil, "", fmt.Errorf("component has empty license entry")
		}
	}

	expr := branches[0]
	for _, extra := range branches[1:] {
		expr = spdx.Or{Left: expr, Right: extra}
	}
	return expr, strings.Join(rendered, " OR "), nil
}

func licenseToken(choice LicenseChoice) string {
	switch {
	case choice.ID != "":
		return choice.ID
	case choice.Name != "":
		return choice.Name
	default:
		return choice.Text
	}
}

func normalizeToken(token string) string {
	return strings.ToLower(strings.Join(strings.Fields(token), " "))
}
