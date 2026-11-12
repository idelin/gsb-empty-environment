package netsec

import (
	"errors"
	"strconv"
	"strings"
)

// Reasons recorded on Finding to explain why an exported component is
// considered unprotected.
const (
	ReasonNoPermission        = "no permission required"
	ReasonPermissionUndefined = "permission not declared in manifest"
	ReasonProtectionMissing   = "protectionLevel missing"
	ReasonProtectionWeak      = "protectionLevel is normal or dangerous"
)

// ExportedAttributeError reports an android:exported attribute that the
// platform requires on targetSdk 31+ but the manifest omits.
type ExportedAttributeError struct {
	Kind      ComponentKind
	Name      string
	TargetSDK int
}

func (e *ExportedAttributeError) Error() string {
	return "component " + string(e.Kind) + " " + e.Name +
		" has intent-filter but omits android:exported (targetSdk " +
		strconv.Itoa(e.TargetSDK) + " requires an explicit value)"
}

// MissingTargetActivityError reports an activity-alias whose targetActivity
// cannot be resolved among the declared <activity> elements.
type MissingTargetActivityError struct {
	Alias          string
	TargetActivity string
}

func (e *MissingTargetActivityError) Error() string {
	return "activity-alias " + e.Alias + " references undeclared targetActivity " + e.TargetActivity
}

// Finding is an exported component that any third-party app can launch or
// bind without holding a signature-level permission.
type Finding struct {
	Kind       ComponentKind
	Name       string
	Permission string
	Reason     string
}

// Report holds the audit outcome.
type Report struct {
	Findings []Finding
}

// ResolveExported applies platform default rules to a component.
//
// An explicit android:exported always wins. Without it:
//   - targetSdk >= 31 with an intent-filter is a hard error because Android 12
//     requires the attribute to be explicit;
//   - a provider defaults to exported on targetSdk <= 16 and not exported from
//     targetSdk 17;
//   - any other component is exported only when it declares an intent-filter
//     on targetSdk < 31, and not exported otherwise.
//
// A TargetSDK of zero represents a manifest without uses-sdk and is treated
// as a legacy platform version.
func ResolveExported(c Component, targetSDK int) (bool, error) {
	if c.Exported != nil {
		return *c.Exported, nil
	}
	if targetSDK >= 31 && c.HasFilter {
		return false, &ExportedAttributeError{Kind: c.Kind, Name: c.Name, TargetSDK: targetSDK}
	}
	if c.Kind == KindProvider {
		return targetSDK <= 16, nil
	}
	return c.HasFilter, nil
}

// EffectivePermission resolves which permission guards a component: the
// component-level android:permission overrides the application-level one, and
// an empty component value falls back to the application default.
func EffectivePermission(componentPermission, applicationPermission string) string {
	if componentPermission != "" {
		return componentPermission
	}
	return applicationPermission
}

// IsSignatureProtection reports whether a declared protectionLevel belongs to
// the signature family. Combinations such as "signature|privileged" and the
// legacy "signatureOrSystem" count as signature protection; normal, dangerous
// and any unrecognized level do not.
func IsSignatureProtection(protectionLevel string) bool {
	for _, part := range strings.Split(protectionLevel, "|") {
		switch strings.TrimSpace(part) {
		case "signature", "signatureOrSystem":
			return true
		}
	}
	return false
}

// EvaluateProtection decides whether an enforced permission is strong enough.
// An undefined permission, a missing protectionLevel and normal/dangerous
// protection all leave the component unprotected; only signature-family
// protection counts as protected.
func EvaluateProtection(permission string, declared map[string]Permission) (protected bool, reason string) {
	if permission == "" {
		return false, ReasonNoPermission
	}
	defined, ok := declared[permission]
	if !ok {
		return false, ReasonPermissionUndefined
	}
	level := strings.TrimSpace(defined.ProtectionLevel)
	if level == "" {
		return false, ReasonProtectionMissing
	}
	if IsSignatureProtection(level) {
		return true, ""
	}
	return false, ReasonProtectionWeak
}

// Analyze audits a manifest and returns every exported but unprotected
// component. Components that cannot be evaluated (for example a missing
// android:exported on targetSdk 31+) are returned as joined errors rather
// than silently skipped; findings for the remaining components are still
// reported.
func Analyze(m *Manifest) (*Report, error) {
	if m == nil {
		return nil, errors.New("analyze: nil manifest")
	}

	declared := make(map[string]Permission, len(m.Permissions))
	for _, p := range m.Permissions {
		declared[p.Name] = p
	}

	report := &Report{Findings: []Finding{}}
	var errs []error

	check := func(kind ComponentKind, name, componentPermission string, exported bool) {
		if !exported {
			return
		}
		permission := EffectivePermission(componentPermission, m.Application.Permission)
		if protected, reason := EvaluateProtection(permission, declared); !protected {
			report.Findings = append(report.Findings, Finding{
				Kind:       kind,
				Name:       name,
				Permission: permission,
				Reason:     reason,
			})
		}
	}

	activities := make(map[string]Activity, len(m.Application.Activities))
	for _, a := range m.Application.Activities {
		activities[a.Name] = a
		exported, err := ResolveExported(a.Component, m.TargetSDK)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		check(KindActivity, a.Name, a.Permission, exported)
	}

	for _, s := range m.Application.Services {
		exported, err := ResolveExported(s.Component, m.TargetSDK)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		check(KindService, s.Name, s.Permission, exported)
	}

	for _, p := range m.Application.Providers {
		exported, err := ResolveExported(p.Component, m.TargetSDK)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		check(KindProvider, p.Name, p.Permission, exported)
	}

	for _, alias := range m.Application.ActivityAliases {
		target, ok := activities[alias.TargetActivity]
		if !ok {
			errs = append(errs, &MissingTargetActivityError{
				Alias:          alias.Name,
				TargetActivity: alias.TargetActivity,
			})
			continue
		}

		exported, err := resolveAliasExported(alias, target, m.TargetSDK)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		check(KindActivityAlias, alias.Name, alias.Permission, exported)
	}

	return report, errors.Join(errs...)
}

// resolveAliasExported makes an activity-alias follow its targetActivity. An
// explicit android:exported on the alias still wins; otherwise the alias
// inherits the target's resolved exposure, while still honoring the targetSdk
// 31 requirement for its own intent-filters.
func resolveAliasExported(alias ActivityAlias, target Activity, targetSDK int) (bool, error) {
	if alias.Exported != nil {
		return *alias.Exported, nil
	}
	if targetSDK >= 31 && alias.HasFilter {
		return false, &ExportedAttributeError{
			Kind:      KindActivityAlias,
			Name:      alias.Name,
			TargetSDK: targetSDK,
		}
	}
	return ResolveExported(target.Component, targetSDK)
}
