package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0215"
)

func ptr(b bool) *bool { return &b }

// TestResolveExportedDefaults exercises the platform default boundaries at
// targetSdk 16/17 and 30/31, including explicit overrides.
func TestResolveExportedDefaults(t *testing.T) {
	tests := []struct {
		name      string
		component netsec.Component
		targetSDK int
		want      bool
		wantErr   bool
	}{
		{
			name:      "activity with filter defaults exported below 31",
			component: netsec.Component{Kind: netsec.KindActivity, Name: "a", HasFilter: true},
			targetSDK: 30,
			want:      true,
		},
		{
			name:      "activity with filter errors on 31 without exported",
			component: netsec.Component{Kind: netsec.KindActivity, Name: "a", HasFilter: true},
			targetSDK: 31,
			wantErr:   true,
		},
		{
			name:      "activity without filter stays private below 31",
			component: netsec.Component{Kind: netsec.KindActivity, Name: "a"},
			targetSDK: 30,
			want:      false,
		},
		{
			name:      "activity without filter stays private on 31",
			component: netsec.Component{Kind: netsec.KindActivity, Name: "a"},
			targetSDK: 31,
			want:      false,
		},
		{
			name:      "explicit exported false overrides intent-filter",
			component: netsec.Component{Kind: netsec.KindActivity, Name: "a", Exported: ptr(false), HasFilter: true},
			targetSDK: 31,
			want:      false,
		},
		{
			name:      "service with filter defaults exported below 31",
			component: netsec.Component{Kind: netsec.KindService, Name: "s", HasFilter: true},
			targetSDK: 16,
			want:      true,
		},
		{
			name:      "service without filter defaults private",
			component: netsec.Component{Kind: netsec.KindService, Name: "s"},
			targetSDK: 28,
			want:      false,
		},
		{
			name:      "provider defaults exported at sdk 16 boundary",
			component: netsec.Component{Kind: netsec.KindProvider, Name: "p"},
			targetSDK: 16,
			want:      true,
		},
		{
			name:      "provider defaults private at sdk 17 boundary",
			component: netsec.Component{Kind: netsec.KindProvider, Name: "p"},
			targetSDK: 17,
			want:      false,
		},
		{
			name:      "provider stays private without filter at sdk 31",
			component: netsec.Component{Kind: netsec.KindProvider, Name: "p"},
			targetSDK: 31,
			want:      false,
		},
		{
			name:      "provider explicit exported true overrides sdk 17 default",
			component: netsec.Component{Kind: netsec.KindProvider, Name: "p", Exported: ptr(true)},
			targetSDK: 30,
			want:      true,
		},
		{
			name:      "provider with filter missing exported errors at sdk 31",
			component: netsec.Component{Kind: netsec.KindProvider, Name: "p", HasFilter: true},
			targetSDK: 31,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := netsec.ResolveExported(tt.component, tt.targetSDK)
			if tt.wantErr {
				var attrErr *netsec.ExportedAttributeError
				if !errors.As(err, &attrErr) {
					t.Fatalf("expected *ExportedAttributeError, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveExported = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectivePermissionOverride(t *testing.T) {
	if got := netsec.EffectivePermission("comp.perm", "app.perm"); got != "comp.perm" {
		t.Fatalf("component permission must override application permission, got %q", got)
	}
	if got := netsec.EffectivePermission("", "app.perm"); got != "app.perm" {
		t.Fatalf("empty component permission must fall back to application, got %q", got)
	}
	if got := netsec.EffectivePermission("", ""); got != "" {
		t.Fatalf("expected empty permission, got %q", got)
	}
}

func TestIsSignatureProtection(t *testing.T) {
	signatureLevels := []string{"signature", "signatureOrSystem", "signature|privileged", "signature|appop"}
	for _, level := range signatureLevels {
		if !netsec.IsSignatureProtection(level) {
			t.Errorf("%q should be signature protection", level)
		}
	}
	weakLevels := []string{"", "normal", "dangerous", "privileged", "0x0", "unknown"}
	for _, level := range weakLevels {
		if netsec.IsSignatureProtection(level) {
			t.Errorf("%q must not be treated as signature protection", level)
		}
	}
}

func TestEvaluateProtection(t *testing.T) {
	declared := map[string]netsec.Permission{
		"p.sig":      {Name: "p.sig", ProtectionLevel: "signature"},
		"p.normal":   {Name: "p.normal", ProtectionLevel: "normal"},
		"p.danger":   {Name: "p.danger", ProtectionLevel: "dangerous"},
		"p.nolevel":  {Name: "p.nolevel"},
		"p.sigflags": {Name: "p.sigflags", ProtectionLevel: "signature|privileged"},
	}

	tests := []struct {
		name          string
		permission    string
		wantProtected bool
		wantReason    string
	}{
		{"no permission", "", false, netsec.ReasonNoPermission},
		{"undefined permission", "p.missing", false, netsec.ReasonPermissionUndefined},
		{"missing protectionLevel", "p.nolevel", false, netsec.ReasonProtectionMissing},
		{"normal is weak", "p.normal", false, netsec.ReasonProtectionWeak},
		{"dangerous is weak", "p.danger", false, netsec.ReasonProtectionWeak},
		{"signature is protected", "p.sig", true, ""},
		{"signature with flags is protected", "p.sigflags", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protected, reason := netsec.EvaluateProtection(tt.permission, declared)
			if protected != tt.wantProtected || reason != tt.wantReason {
				t.Fatalf("EvaluateProtection(%q) = (%v, %q), want (%v, %q)",
					tt.permission, protected, reason, tt.wantProtected, tt.wantReason)
			}
		})
	}
}
