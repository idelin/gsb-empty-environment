package netsec

import "testing"

// Fixture modules:
//
//	app        requires base and automod
//	base       requires transitive util and openmod
//	util       ordinary, opens util.internal to friend and exports util.api
//	openmod    open module
//	automod    automatic module
//	friend     friend named in util's qualified opens, no requires
//	sidecar    requires app (plain edge) to prove readability is not
//	           re-exported through non-transitive requires
func fixtureDescriptors() []ModuleDescriptor {
	return []ModuleDescriptor{
		{
			Name: "app",
			Requires: []RequireDirective{
				{Module: "base"},
				{Module: "automod"},
			},
		},
		{
			Name: "base",
			Requires: []RequireDirective{{
				Module:     "util",
				Transitive: true,
			}, {
				Module:     "openmod",
				Transitive: true,
			}},
		},
		{
			Name: "util",
			Opens: []PackageDirective{
				{Package: "util.internal", Targets: []string{"friend"}},
				{Package: "util.wide"},
			},
			Exports: []PackageDirective{
				{Package: "util.api"},
			},
		},
		{Name: "openmod", Open: true},
		{Name: "automod", Automatic: true},
		{Name: "friend"},
		{
			Name:     "sidecar",
			Requires: []RequireDirective{{Module: "app"}},
		},
	}
}

func TestCanRead(t *testing.T) {
	layer := NewLayer(fixtureDescriptors(), nil)

	cases := []struct {
		name   string
		caller string
		target string
		want   bool
	}{
		{"self always readable", "app", "app", true},
		{"plain requires is readable", "app", "base", true},
		{"transitive requires propagates one hop", "app", "util", true},
		{"plain requires is not re-exported", "sidecar", "base", false},
		{"transitive chain does not reach unrelated module", "sidecar", "util", false},
		{"no requires edge means not readable", "friend", "util", false},
		{"unnamed reads named", "", "util", true},
		{"automatic module reads everything", "automod", "util", true},
		{"named module does not read unnamed", "app", "", false},
		{"unknown named module reads nothing", "ghost", "util", false},
		{"unknown caller cannot read unnamed either", "ghost", "", false},
		{"unknown target is not readable", "app", "ghost", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := layer.CanRead(tc.caller, tc.target); got != tc.want {
				t.Fatalf("CanRead(%q, %q) = %v, want %v", tc.caller, tc.target, got, tc.want)
			}
		})
	}
}

func TestIsOpen(t *testing.T) {
	layer := NewLayer(fixtureDescriptors(), nil)

	cases := []struct {
		name   string
		target string
		pkg    string
		caller string
		want   bool
	}{
		{"qualified opens names caller", "util", "util.internal", "friend", true},
		{"qualified opens rejects other caller", "util", "util.internal", "app", false},
		{"unqualified opens allows any caller", "util", "util.wide", "app", true},
		{"exported but not opened stays closed", "util", "util.api", "app", false},
		{"unopened package is closed", "util", "util.secret", "app", false},
		{"open module opens every package", "openmod", "openmod.deep", "app", true},
		{"automatic module opens every package", "automod", "automod.deep", "app", true},
		{"unnamed module opens every package", "", "any.package", "app", true},
		{"unknown target is not open", "ghost", "ghost.p", "app", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := layer.IsOpen(tc.target, tc.pkg, tc.caller); got != tc.want {
				t.Fatalf("IsOpen(%q, %q, %q) = %v, want %v", tc.target, tc.pkg, tc.caller, got, tc.want)
			}
		})
	}
}

func TestCanSetAccessible(t *testing.T) {
	layer := NewLayer(fixtureDescriptors(), nil)

	cases := []struct {
		name   string
		caller string
		target string
		pkg    string
		want   bool
	}{
		{"own packages are accessible", "util", "util", "util.internal", true},
		{"qualified friend with readability", "friend", "util", "util.internal", false},
		{"qualified opens names another module", "app", "util", "util.internal", false},
		{"unqualified opens after transitive read", "app", "util", "util.wide", true},
		{"export alone never opens for deep reflection", "app", "util", "util.api", false},
		{"openness without readability is useless", "friend", "openmod", "openmod.x", false},
		{"open module behind transitive read", "app", "openmod", "openmod.x", true},
		{"automatic module is fully open", "app", "automod", "automod.x", true},
		{"named module never reads unnamed target", "app", "", "app.impl", false},
		{"unnamed modules access each other freely", "", "", "app.impl", true},
		{"unnamed caller reads and is opened via ALL-UNNAMED", "", "util", "util.wide", true},
		{"unnamed caller blocked by qualified opens", "", "util", "util.internal", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := layer.CanSetAccessible(tc.caller, tc.target, tc.pkg); got != tc.want {
				t.Fatalf("CanSetAccessible(caller=%q, target=%q, pkg=%q) = %v, want %v",
					tc.caller, tc.target, tc.pkg, got, tc.want)
			}
		})
	}
}

func TestAddOpensPatchOverride(t *testing.T) {
	patches, err := ParseAddOpensAll(
		"--add-opens util/util.internal=app",
		"--add-opens=util/util.secret=ALL-UNNAMED",
	)
	if err != nil {
		t.Fatalf("ParseAddOpensAll: %v", err)
	}
	layer := NewLayer(fixtureDescriptors(), patches)

	// Patch widens a qualified opens to a readable caller.
	if !layer.CanSetAccessible("app", "util", "util.internal") {
		t.Fatal("expected --add-opens to force util.internal open to app")
	}
	// Unnamed caller benefits from ALL-UNNAMED and reads every module.
	if !layer.CanSetAccessible("", "util", "util.secret") {
		t.Fatal("expected ALL-UNNAMED patch to open util.secret to the unnamed module")
	}
	// The patch is package-scoped: neighbouring packages stay closed.
	if layer.IsOpen("util", "util.other", "app") {
		t.Fatal("patch on one package must not open sibling packages")
	}
	// A named --add-opens target also gets a read edge: friend never required
	// util, yet the patch makes it read and open the package.
	friendPatches, err := ParseAddOpensAll("--add-opens util/util.internal=friend")
	if err != nil {
		t.Fatalf("ParseAddOpensAll: %v", err)
	}
	friendLayer := NewLayer(fixtureDescriptors(), friendPatches)
	if !friendLayer.CanSetAccessible("friend", "util", "util.internal") {
		t.Fatal("expected named --add-opens target to gain readability and openness")
	}
}

func TestParseAddOpens(t *testing.T) {
	cases := []struct {
		arg     string
		module  string
		pkg     string
		target  string
		wantErr bool
	}{
		{arg: "--add-opens java.base/java.lang=ALL-UNNAMED", module: "java.base", pkg: "java.lang", target: AllUnnamed},
		{arg: "--add-opens=java.base/java.lang=ALL-UNNAMED", module: "java.base", pkg: "java.lang", target: AllUnnamed},
		{arg: "java.base/java.lang=plugin", module: "java.base", pkg: "java.lang", target: "plugin"},
		{arg: "--add-opens java.base/java.lang", wantErr: true},
		{arg: "--add-opens javabase=ALL-UNNAMED", wantErr: true},
		{arg: "--add-opens /java.lang=ALL-UNNAMED", wantErr: true},
		{arg: "--add-opens java.base/=ALL-UNNAMED", wantErr: true},
		{arg: "--add-opens java.base/java.lang=", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			patch, err := ParseAddOpens(tc.arg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseAddOpens(%q) = %+v, want error", tc.arg, patch)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAddOpens(%q): %v", tc.arg, err)
			}
			if patch.Module != tc.module || patch.Package != tc.pkg || patch.Target != tc.target {
				t.Fatalf("ParseAddOpens(%q) = %+v, want {%s %s %s}",
					tc.arg, patch, tc.module, tc.pkg, tc.target)
			}
		})
	}
}

func TestLongTransitiveChain(t *testing.T) {
	descriptors := []ModuleDescriptor{
		{Name: "a", Requires: []RequireDirective{{Module: "b", Transitive: true}}},
		{Name: "b", Requires: []RequireDirective{{Module: "c", Transitive: true}}},
		{Name: "c", Requires: []RequireDirective{{Module: "d", Transitive: true}}},
		{Name: "d", Opens: []PackageDirective{{Package: "d.internal"}}},
	}
	layer := NewLayer(descriptors, nil)

	if !layer.CanRead("a", "d") {
		t.Fatal("readability should propagate along the whole transitive chain")
	}
	if !layer.CanSetAccessible("a", "d", "d.internal") {
		t.Fatal("unqualified opens at the end of a transitive chain must be accessible")
	}
}

func TestLayerEdgeCases(t *testing.T) {
	// Unnamed descriptors are not registered; dangling requires are skipped
	// during readability expansion instead of panicking.
	descriptors := []ModuleDescriptor{
		{Name: ""},
		{Name: "lonely", Requires: []RequireDirective{{Module: "missing", Transitive: true}}},
	}
	layer := NewLayer(descriptors, nil)

	// The dangling edge must be skipped while expanding transitive
	// readability instead of panicking, and the unknown target can never be
	// opened.
	if layer.CanRead("lonely", "never") {
		t.Fatal("readability expansion must skip absent modules without panicking")
	}
	if layer.CanSetAccessible("lonely", "missing", "missing.p") {
		t.Fatal("an absent target module can never be opened")
	}
	if _, ok := layer.modules[""]; ok {
		t.Fatal("unnamed descriptors must not be registered as named modules")
	}

	if _, err := ParseAddOpensAll("util/p=app", "bad-input"); err == nil {
		t.Fatal("ParseAddOpensAll must propagate the first parse error")
	}
}

func TestDuplicateRequiresAreDeduplicated(t *testing.T) {
	descriptors := []ModuleDescriptor{
		{
			Name: "dup",
			Requires: []RequireDirective{
				{Module: "mid", Transitive: true},
				{Module: "mid", Transitive: true},
			},
		},
		{
			Name: "mid",
			Requires: []RequireDirective{
				{Module: "leaf", Transitive: true},
				{Module: "leaf", Transitive: true},
			},
		},
		{Name: "leaf", Opens: []PackageDirective{{Package: "leaf.p"}}},
	}
	layer := NewLayer(descriptors, nil)

	if !layer.CanSetAccessible("dup", "leaf", "leaf.p") {
		t.Fatal("duplicate requires edges must not break transitive expansion")
	}
}
