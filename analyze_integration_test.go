package netsec_test

import (
	"errors"
	"strings"
	"testing"

	netsec "example.org/netsec/q0215"
)

func findingNames(report *netsec.Report) []string {
	names := make([]string, 0, len(report.Findings))
	for _, f := range report.Findings {
		names = append(names, f.Name)
	}
	return names
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// TestAnalyzePermissionOverride verifies component permission precedence over
// the application-level permission, including weak and strong combinations.
func TestAnalyzePermissionOverride(t *testing.T) {
	m := &netsec.Manifest{
		TargetSDK: 30,
		Permissions: []netsec.Permission{
			{Name: "app.normal", ProtectionLevel: "normal"},
			{Name: "comp.sig", ProtectionLevel: "signature"},
		},
		Application: netsec.Application{
			Permission: "app.normal",
			Activities: []netsec.Activity{
				{Component: netsec.Component{Name: "weak", HasFilter: true}},
				{Component: netsec.Component{Name: "guarded", HasFilter: true, Permission: "comp.sig"}},
			},
		},
	}

	report, err := netsec.Analyze(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]
	if f.Name != "weak" || f.Permission != "app.normal" || f.Reason != netsec.ReasonProtectionWeak {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

// TestAnalyzeUndefinedPermission ensures a referenced but undeclared
// permission is treated as no real protection.
func TestAnalyzeUndefinedPermission(t *testing.T) {
	m := &netsec.Manifest{
		TargetSDK: 30,
		Application: netsec.Application{
			Services: []netsec.Service{
				{Component: netsec.Component{
					Name:       "s",
					HasFilter:  true,
					Permission: "com.example.GHOST",
				}},
			},
		},
	}

	report, err := netsec.Analyze(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %+v", report.Findings)
	}
	if report.Findings[0].Reason != netsec.ReasonPermissionUndefined {
		t.Fatalf("expected undefined permission reason, got %q", report.Findings[0].Reason)
	}
}

// TestAnalyzeProviderBoundary covers the sdk 16/17 provider default inside a
// full Analyze run.
func TestAnalyzeProviderBoundary(t *testing.T) {
	build := func(targetSDK int) *netsec.Manifest {
		return &netsec.Manifest{
			TargetSDK: targetSDK,
			Application: netsec.Application{
				Providers: []netsec.Provider{
					{Component: netsec.Component{Kind: netsec.KindProvider, Name: "p"}},
				},
			},
		}
	}

	legacy, err := netsec.Analyze(build(16))
	if err != nil {
		t.Fatalf("sdk 16 unexpected error: %v", err)
	}
	if len(legacy.Findings) != 1 || legacy.Findings[0].Reason != netsec.ReasonNoPermission {
		t.Fatalf("sdk 16 provider must be flagged, got %+v", legacy.Findings)
	}

	modern, err := netsec.Analyze(build(17))
	if err != nil {
		t.Fatalf("sdk 17 unexpected error: %v", err)
	}
	if len(modern.Findings) != 0 {
		t.Fatalf("sdk 17 provider must default private, got %+v", modern.Findings)
	}
}

// TestAnalyzeMissingExportedError checks the targetSdk 31 hard error while
// still reporting other evaluable components.
func TestAnalyzeMissingExportedError(t *testing.T) {
	m := &netsec.Manifest{
		TargetSDK: 31,
		Application: netsec.Application{
			Activities: []netsec.Activity{
				{Component: netsec.Component{Name: "broken", HasFilter: true}},
				{Component: netsec.Component{Name: "open", Exported: ptr(true)}},
			},
		},
	}

	report, err := netsec.Analyze(m)
	if err == nil {
		t.Fatal("expected an error for missing android:exported")
	}
	var attrErr *netsec.ExportedAttributeError
	if !errors.As(err, &attrErr) || attrErr.Name != "broken" {
		t.Fatalf("expected ExportedAttributeError for broken, got %v", err)
	}
	if !strings.Contains(err.Error(), "android:exported") || !strings.Contains(err.Error(), "31") {
		t.Fatalf("error message should mention android:exported and targetSdk: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Name != "open" {
		t.Fatalf("expected the explicit exported component to still be flagged, got %+v", report.Findings)
	}
}

// TestAnalyzeExportedErrorForEveryKind ensures every component kind surfaces
// the sdk 31 missing-exported error, not just activities.
func TestAnalyzeExportedErrorForEveryKind(t *testing.T) {
	m := &netsec.Manifest{
		TargetSDK: 31,
		Application: netsec.Application{
			Activities: []netsec.Activity{
				{Component: netsec.Component{Kind: netsec.KindActivity, Name: "a", HasFilter: true}},
			},
			Services: []netsec.Service{
				{Component: netsec.Component{Kind: netsec.KindService, Name: "s", HasFilter: true}},
			},
			Providers: []netsec.Provider{
				{Component: netsec.Component{Kind: netsec.KindProvider, Name: "p", HasFilter: true}},
			},
		},
	}

	report, err := netsec.Analyze(m)
	if err == nil {
		t.Fatal("expected joined exported-attribute errors")
	}
	var attrErr *netsec.ExportedAttributeError
	failed := map[netsec.ComponentKind]bool{}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("expected a joined error, got %T", err)
	}
	for _, sub := range joined.Unwrap() {
		if errors.As(sub, &attrErr) {
			failed[attrErr.Kind] = true
		}
	}
	for _, kind := range []netsec.ComponentKind{netsec.KindActivity, netsec.KindService, netsec.KindProvider} {
		if !failed[kind] {
			t.Fatalf("expected ExportedAttributeError for kind %s", kind)
		}
	}
	if len(report.Findings) != 0 {
		t.Fatalf("no evaluable exported components expected, got %+v", report.Findings)
	}
}

// TestAnalyzeActivityAlias covers alias exposure following targetActivity,
// alias permission overrides, missing target activities and the sdk 31 rule.
func TestAnalyzeActivityAlias(t *testing.T) {
	t.Run("alias follows exported target", func(t *testing.T) {
		m := &netsec.Manifest{
			TargetSDK: 30,
			Application: netsec.Application{
				Activities: []netsec.Activity{
					{Component: netsec.Component{Name: ".Target", HasFilter: true}},
				},
				ActivityAliases: []netsec.ActivityAlias{
					{Name: ".Alias", TargetActivity: ".Target"},
				},
			},
		}
		report, err := netsec.Analyze(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		names := findingNames(report)
		if !contains(names, ".Target") || !contains(names, ".Alias") {
			t.Fatalf("expected target and alias findings, got %v", names)
		}
	})

	t.Run("alias follows private target", func(t *testing.T) {
		m := &netsec.Manifest{
			TargetSDK: 30,
			Application: netsec.Application{
				Activities: []netsec.Activity{
					{Component: netsec.Component{Name: ".Target"}},
				},
				ActivityAliases: []netsec.ActivityAlias{
					{Name: ".Alias", TargetActivity: ".Target", HasFilter: true},
				},
			},
		}
		report, err := netsec.Analyze(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(report.Findings) != 0 {
			t.Fatalf("alias inheriting private target must not be flagged, got %+v", report.Findings)
		}
	})

	t.Run("explicit exported false stays private", func(t *testing.T) {
		m := &netsec.Manifest{
			TargetSDK: 30,
			Application: netsec.Application{
				Activities: []netsec.Activity{
					{Component: netsec.Component{Name: ".Target", HasFilter: true}},
				},
				ActivityAliases: []netsec.ActivityAlias{
					{Name: ".Alias", TargetActivity: ".Target", Exported: ptr(false)},
				},
			},
		}
		report, err := netsec.Analyze(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, f := range report.Findings {
			if f.Name == ".Alias" {
				t.Fatalf("explicit exported=false alias must not be flagged: %+v", f)
			}
		}
	})

	t.Run("missing target activity errors", func(t *testing.T) {
		m := &netsec.Manifest{
			TargetSDK: 30,
			Application: netsec.Application{
				ActivityAliases: []netsec.ActivityAlias{
					{Name: ".Alias", TargetActivity: ".Ghost", Exported: ptr(true)},
				},
			},
		}
		_, err := netsec.Analyze(m)
		var missingErr *netsec.MissingTargetActivityError
		if !errors.As(err, &missingErr) || missingErr.Alias != ".Alias" {
			t.Fatalf("expected MissingTargetActivityError, got %v", err)
		}
		if !strings.Contains(err.Error(), ".Ghost") {
			t.Fatalf("error message should name the missing targetActivity: %v", err)
		}
	})

	t.Run("alias missing exported errors at sdk 31", func(t *testing.T) {
		m := &netsec.Manifest{
			TargetSDK: 31,
			Application: netsec.Application{
				Activities: []netsec.Activity{
					{Component: netsec.Component{Name: ".Target", Exported: ptr(true)}},
				},
				ActivityAliases: []netsec.ActivityAlias{
					{Name: ".Alias", TargetActivity: ".Target", HasFilter: true},
				},
			},
		}
		_, err := netsec.Analyze(m)
		var attrErr *netsec.ExportedAttributeError
		if !errors.As(err, &attrErr) || attrErr.Kind != netsec.KindActivityAlias {
			t.Fatalf("expected ExportedAttributeError for alias, got %v", err)
		}
	})

	t.Run("alias signature permission overrides application default", func(t *testing.T) {
		m := &netsec.Manifest{
			TargetSDK: 30,
			Permissions: []netsec.Permission{
				{Name: "app.normal", ProtectionLevel: "normal"},
				{Name: "alias.sig", ProtectionLevel: "signatureOrSystem"},
			},
			Application: netsec.Application{
				Permission: "app.normal",
				Activities: []netsec.Activity{
					{Component: netsec.Component{Name: ".Target", HasFilter: true}},
				},
				ActivityAliases: []netsec.ActivityAlias{
					{Name: ".Alias", TargetActivity: ".Target", Permission: "alias.sig"},
				},
			},
		}
		report, err := netsec.Analyze(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, f := range report.Findings {
			if f.Name == ".Alias" {
				t.Fatalf("signature-guarded alias must not be flagged: %+v", f)
			}
		}
	})
}
