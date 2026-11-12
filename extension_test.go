package netsec

import (
	"errors"
	"testing"
)

func TestNonSuperuserTrustedExtensionAllowed(t *testing.T) {
	ext := Extension{Name: "safe", Trusted: true}
	if err := EvaluateInstall(Role{Name: "alice"}, ext, nil); err != nil {
		t.Fatalf("trusted extension rejected: %v", err)
	}
}

func TestNonSuperuserUntrustedExtensionRejected(t *testing.T) {
	ext := Extension{Name: "unsafe", Trusted: false}
	err := EvaluateInstall(Role{Name: "alice"}, ext, nil)
	if !errors.Is(err, ErrExtensionNotTrusted) {
		t.Fatalf("error = %v, want ErrExtensionNotTrusted", err)
	}
}

func TestNonSuperuserSuperuserExtensionRejected(t *testing.T) {
	ext := Extension{Name: "admin_only", Trusted: true, Superuser: true}
	err := EvaluateInstall(Role{Name: "alice"}, ext, nil)
	if !errors.Is(err, ErrSuperuserRequired) {
		t.Fatalf("error = %v, want ErrSuperuserRequired", err)
	}
}

func TestSuperuserMayInstallUntrustedExtension(t *testing.T) {
	ext := Extension{Name: "admin_tool", Trusted: false, Superuser: true}
	if err := EvaluateInstall(Role{Name: "postgres", Superuser: true}, ext, nil); err != nil {
		t.Fatalf("superuser rejected: %v", err)
	}
}

func TestDependencyClosureAllTrustedAndInstalled(t *testing.T) {
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"middle"}}
	installed := InstalledExtensions{
		"middle": {Name: "middle", Trusted: true, Requires: []string{"leaf"}},
		"leaf":   {Name: "leaf", Trusted: true},
	}
	if err := EvaluateInstall(Role{Name: "alice"}, ext, installed); err != nil {
		t.Fatalf("valid dependency closure rejected: %v", err)
	}
}

func TestDependencyMissingAtAnyDepthRejected(t *testing.T) {
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"middle"}}
	installed := InstalledExtensions{
		"middle": {Name: "middle", Trusted: true, Requires: []string{"leaf"}},
	}
	err := EvaluateInstall(Role{Name: "alice"}, ext, installed)
	if !errors.Is(err, ErrDependencyNotInstalled) {
		t.Fatalf("error = %v, want ErrDependencyNotInstalled", err)
	}
}

func TestDependencyUntrustedAtAnyDepthRejected(t *testing.T) {
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"middle"}}
	installed := InstalledExtensions{
		"middle": {Name: "middle", Trusted: true, Requires: []string{"leaf"}},
		"leaf":   {Name: "leaf", Trusted: false},
	}
	err := EvaluateInstall(Role{Name: "alice"}, ext, installed)
	if !errors.Is(err, ErrDependencyNotTrusted) {
		t.Fatalf("error = %v, want ErrDependencyNotTrusted", err)
	}
}

func TestDependencySuperuserOnlyRejected(t *testing.T) {
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"admin"}}
	installed := InstalledExtensions{
		"admin": {Name: "admin", Trusted: true, Superuser: true},
	}
	err := EvaluateInstall(Role{Name: "alice"}, ext, installed)
	if !errors.Is(err, ErrDependencyRequiresSuperuser) {
		t.Fatalf("error = %v, want ErrDependencyRequiresSuperuser", err)
	}
}

func TestDependencyCycleTerminates(t *testing.T) {
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"a"}}
	installed := InstalledExtensions{
		"a": {Name: "a", Trusted: true, Requires: []string{"b"}},
		"b": {Name: "b", Trusted: true, Requires: []string{"a"}},
	}
	if err := EvaluateInstall(Role{Name: "alice"}, ext, installed); err != nil {
		t.Fatalf("cyclic installed closure rejected: %v", err)
	}
}

func TestDirectDependencyMissingRejected(t *testing.T) {
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"missing"}}
	err := EvaluateInstall(Role{Name: "alice"}, ext, InstalledExtensions{})
	if !errors.Is(err, ErrDependencyNotInstalled) {
		t.Fatalf("error = %v, want ErrDependencyNotInstalled", err)
	}
}

func TestInstalledBoolMapMeansInstalledAndTrusted(t *testing.T) {
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"dep"}}
	installed := map[string]bool{"dep": true}
	if err := CheckExtensionInstall(false, ext, installed); err != nil {
		t.Fatalf("bool catalog rejected: %v", err)
	}
}

func TestAdmissionAliasesAcceptEitherArgumentOrder(t *testing.T) {
	ext := Extension{Name: "safe", Trusted: true}
	role := Role{Name: "alice"}
	if err := CheckInstall(role, ext); err != nil {
		t.Fatalf("role-first order rejected: %v", err)
	}
	if err := CheckInstall(ext, role); err != nil {
		t.Fatalf("extension-first order rejected: %v", err)
	}
	if err := CheckExtension(role, ext); err != nil {
		t.Fatalf("CheckExtension rejected: %v", err)
	}
}

func TestAdmitterPolicy(t *testing.T) {
	policy := NewPolicy(Role{Name: "alice"}, map[string]Extension{
		"dep": {Name: "dep", Trusted: true},
	})
	ext := Extension{Name: "top", Trusted: true, Requires: []string{"dep"}}
	if err := policy.CheckInstall(ext); err != nil {
		t.Fatalf("policy rejected valid request: %v", err)
	}
}

func TestModulePathnameTraversalRejected(t *testing.T) {
	ext := Extension{
		Name:           "safe",
		Trusted:        true,
		ModulePathname: "$libdir/../evil",
	}
	err := EvaluateInstall(Role{Name: "alice"}, ext, nil)
	if !errors.Is(err, ErrInvalidLibraryPath) {
		t.Fatalf("error = %v, want ErrInvalidLibraryPath", err)
	}
}
