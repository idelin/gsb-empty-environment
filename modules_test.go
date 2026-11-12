package netsec

import "testing"

const (
	modPlugin = "com.acme.plugin"
	modOther  = "com.acme.other"
	modMid    = "com.acme.mid"
	modBase   = "com.acme.base"
	modAuto   = "com.acme.auto"
	modOpen   = "com.acme.openmod"

	pkgInternal = "com.acme.base.internal"
	pkgPublic   = "com.acme.base.api"
	pkgSecret   = "com.acme.base.secret"
)

// newTestUniverse 构建一张固定模块图：
//
//	plugin --requires(plain)--> mid --requires transitive--> base
//	other  （与 base 无任何边）
//	auto   （automatic module）
//	openmod（open module）
//
// base 对 pkgInternal 只限定 opens 给 mid，对 pkgPublic 无限定 opens，
// pkgSecret 不开放；Packages 显式登记以验证包归属。
func newTestUniverse(t *testing.T) *Universe {
	t.Helper()
	u := NewUniverse()
	mustAdd(t, u, Descriptor{
		Name:     modBase,
		Packages: []string{pkgInternal, pkgPublic, pkgSecret},
		Exports:  []PackageDirective{{Package: pkgPublic}},
		Opens: []PackageDirective{
			{Package: pkgInternal, To: []string{modMid}},
			{Package: pkgPublic},
		},
	})
	mustAdd(t, u, Descriptor{
		Name:     modMid,
		Packages: []string{"com.acme.mid.core"},
		Requires: []Require{{Module: modBase, Transitive: true}},
	})
	mustAdd(t, u, Descriptor{
		Name:     modPlugin,
		Packages: []string{"com.acme.plugin.core"},
		Requires: []Require{{Module: modMid}, {Module: modOpen}},
	})
	mustAdd(t, u, Descriptor{
		Name:     modOther,
		Packages: []string{"com.acme.other.core"},
	})
	mustAdd(t, u, Descriptor{
		Name:      modAuto,
		Automatic: true,
		Packages:  []string{"com.acme.auto.core"},
	})
	mustAdd(t, u, Descriptor{
		Name:     modOpen,
		Open:     true,
		Packages: []string{"com.acme.openmod.deep"},
	})
	if err := u.AddUnnamed(); err != nil {
		t.Fatalf("AddUnnamed: %v", err)
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return u
}

func mustAdd(t *testing.T, u *Universe, d Descriptor) {
	t.Helper()
	if err := u.Add(d); err != nil {
		t.Fatalf("Add %s: %v", d.Name, err)
	}
}

func TestCanReadTransitive(t *testing.T) {
	u := newTestUniverse(t)
	tests := []struct {
		name   string
		caller string
		target string
		want   bool
	}{
		{"direct requires", modPlugin, modMid, true},
		{"transitive propagation", modPlugin, modBase, true},
		{"no edge fails closed", modOther, modBase, false},
		{"self is readable", modPlugin, modPlugin, true},
		{"unnamed reads named", UnnamedModule, modBase, true},
		{"named does not implicitly read unnamed", modPlugin, UnnamedModule, false},
		{"automatic reads unnamed", modAuto, UnnamedModule, true},
		{"automatic reads every named", modAuto, modBase, true},
		{"unknown caller", "ghost", modBase, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := u.CanRead(tt.caller, tt.target); got != tt.want {
				t.Fatalf("CanRead(%q, %q) = %v, want %v", tt.caller, tt.target, got, tt.want)
			}
		})
	}
}

func TestCanSetAccessibleQualifiedOpens(t *testing.T) {
	u := newTestUniverse(t)
	tests := []struct {
		name   string
		caller string
		target string
		pkg    string
		want   bool
	}{
		{
			name:   "qualified opens names mid directly",
			caller: modMid,
			target: modBase,
			pkg:    pkgInternal,
			want:   true,
		},
		{
			name:   "qualified opens does not name plugin despite transitive read",
			caller: modPlugin,
			target: modBase,
			pkg:    pkgInternal,
			want:   false,
		},
		{
			name:   "qualified opens names other module, plugin denied",
			caller: modOther,
			target: modBase,
			pkg:    pkgInternal,
			want:   false,
		},
		{
			name:   "unqualified opens plus transitive read",
			caller: modPlugin,
			target: modBase,
			pkg:    pkgPublic,
			want:   true,
		},
		{
			name:   "unqualified opens but unreadable target",
			caller: modOther,
			target: modBase,
			pkg:    pkgPublic,
			want:   false,
		},
		{
			name:   "package never opened",
			caller: modMid,
			target: modBase,
			pkg:    pkgSecret,
			want:   false,
		},
		{
			name:   "exports alone do not grant reflection",
			caller: modOther,
			target: modBase,
			pkg:    pkgPublic,
			want:   false,
		},
		{
			name:   "package not owned by target",
			caller: modMid,
			target: modBase,
			pkg:    "com.acme.base.missing",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := u.CanSetAccessible(tt.caller, tt.target, tt.pkg); got != tt.want {
				t.Fatalf("CanSetAccessible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanSetAccessibleOpenAndAutomatic(t *testing.T) {
	u := newTestUniverse(t)
	t.Run("open module exposes every package to a readable caller", func(t *testing.T) {
		if !u.CanRead(modPlugin, modOpen) {
			t.Fatal("test setup: plugin must read openmod")
		}
		if !u.CanSetAccessible(modPlugin, modOpen, "com.acme.openmod.deep") {
			t.Fatal("open module opens all its packages to readable modules")
		}
	})
	t.Run("open module still requires readability", func(t *testing.T) {
		// other 与 openmod 之间没有 requires 边：包虽开放但不可读，必须拒绝。
		if u.CanRead(modOther, modOpen) {
			t.Fatal("test setup: other must not read openmod")
		}
		if u.CanSetAccessible(modOther, modOpen, "com.acme.openmod.deep") {
			t.Fatal("open package must stay inaccessible without a read edge")
		}
	})
	t.Run("automatic module reads all and is fully open", func(t *testing.T) {
		// 自动模块作为调用方读取所有命名模块，但目标 base 的 secret 包
		// 并未开放，自动性只作用于自动模块自身，不能替别人开门。
		if u.CanSetAccessible(modAuto, modBase, pkgSecret) {
			t.Fatal("automatic caller cannot open packages of a normal named module")
		}
		// other 不读自动模块：即便目标包全开放，可读性缺失仍须拒绝。
		if u.CanRead(modOther, modAuto) {
			t.Fatal("test setup: other must not read auto")
		}
		if u.CanSetAccessible(modOther, modAuto, "com.acme.auto.core") {
			t.Fatal("fully open automatic module still requires a read edge")
		}
		// 自动模块读取一切且自身全开放：从自动模块访问自己的包放行。
		if !u.CanSetAccessible(modAuto, modAuto, "com.acme.auto.core") {
			t.Fatal("automatic module accesses its own open packages")
		}
	})
	t.Run("unnamed module packages are open to everyone", func(t *testing.T) {
		// 未命名模块的包全部开放，但可读性仍是前置条件：普通命名模块
		// 不隐式读取未命名模块，自动模块则隐式读取，二者形成对照。
		if u.CanSetAccessible(modPlugin, UnnamedModule, "dynamic.pkg") {
			t.Fatal("normal named module does not read the unnamed module")
		}
		if !u.CanSetAccessible(modAuto, UnnamedModule, "dynamic.pkg") {
			t.Fatal("automatic module reads the unnamed module whose packages are all open")
		}
		if !u.CanSetAccessible(UnnamedModule, modBase, pkgPublic) {
			t.Fatal("unnamed caller reads all modules; unqualified opens allows it")
		}
		if u.CanSetAccessible(UnnamedModule, modBase, pkgInternal) {
			t.Fatal("qualified opens to mid must not cover the unnamed module")
		}
	})
}

func TestAddOpensOverrides(t *testing.T) {
	u := newTestUniverse(t)

	// 残留补丁把 base.secret 开放给没有任何 requires 边的 other，
	// 补丁同时隐含可读性，因此应当放行。
	if err := u.AddPatch(AddOpensPatch{
		Source:  modBase,
		Package: pkgSecret,
		Targets: []string{modOther},
	}); err != nil {
		t.Fatalf("AddPatch: %v", err)
	}
	if !u.CanRead(modOther, modBase) {
		t.Fatal("add-opens must imply a read edge")
	}
	if !u.CanSetAccessible(modOther, modBase, pkgSecret) {
		t.Fatal("add-opens must override the absent opens directive")
	}
	if u.CanSetAccessible(modPlugin, modBase, pkgSecret) {
		t.Fatal("patch naming other must not leak to plugin")
	}

	// ALL-UNNAMED 只覆盖未命名模块，不覆盖命名模块。
	if err := u.AddPatch(AddOpensPatch{
		Source:  modBase,
		Package: pkgInternal,
		Targets: []string{AllUnnamed},
	}); err != nil {
		t.Fatalf("AddPatch ALL-UNNAMED: %v", err)
	}
	if !u.CanSetAccessible(UnnamedModule, modBase, pkgInternal) {
		t.Fatal("ALL-UNNAMED patch must open the package to unnamed modules")
	}
	if u.CanSetAccessible(modPlugin, modBase, pkgInternal) {
		t.Fatal("ALL-UNNAMED must not grant named modules access")
	}

	if err := u.Validate(); err != nil {
		t.Fatalf("Validate after patches: %v", err)
	}
}

func TestValidationFailures(t *testing.T) {
	t.Run("unknown requires target", func(t *testing.T) {
		u := NewUniverse()
		_ = u.Add(Descriptor{Name: "a", Requires: []Require{{Module: "missing"}}})
		if err := u.Validate(); err == nil {
			t.Fatal("expected error for unresolved requires")
		}
	})
	t.Run("qualified opens to unknown module", func(t *testing.T) {
		u := NewUniverse()
		_ = u.Add(Descriptor{
			Name:  "a",
			Opens: []PackageDirective{{Package: "p", To: []string{"ghost"}}},
		})
		if err := u.Validate(); err == nil {
			t.Fatal("expected error for qualified opens to unknown module")
		}
	})
	t.Run("package outside descriptor", func(t *testing.T) {
		u := NewUniverse()
		_ = u.Add(Descriptor{
			Name:     "a",
			Packages: []string{"owned.pkg"},
			Opens:    []PackageDirective{{Package: "foreign.pkg"}},
		})
		if err := u.Validate(); err == nil {
			t.Fatal("expected error for opens on unowned package")
		}
	})
	t.Run("ALL-UNNAMED rejected in descriptor", func(t *testing.T) {
		u := NewUniverse()
		_ = u.Add(Descriptor{
			Name:  "a",
			Opens: []PackageDirective{{Package: "p", To: []string{AllUnnamed}}},
		})
		if err := u.Validate(); err == nil {
			t.Fatal("ALL-UNNAMED is reserved for add-opens, not descriptors")
		}
	})
	t.Run("duplicate module rejected", func(t *testing.T) {
		u := NewUniverse()
		if err := u.Add(Descriptor{Name: "a"}); err != nil {
			t.Fatalf("first Add: %v", err)
		}
		if err := u.Add(Descriptor{Name: "a"}); err == nil {
			t.Fatal("duplicate module registration must fail")
		}
	})
	t.Run("malformed patch rejected", func(t *testing.T) {
		u := NewUniverse()
		if err := u.AddPatch(AddOpensPatch{Source: "a", Package: "p"}); err == nil {
			t.Fatal("patch without targets must fail")
		}
	})
}

func TestUnknownInputsFailClosed(t *testing.T) {
	u := newTestUniverse(t)
	if u.CanSetAccessible("ghost", modBase, pkgPublic) {
		t.Fatal("unknown caller must fail closed")
	}
	if u.CanSetAccessible(modPlugin, "ghost", pkgPublic) {
		t.Fatal("unknown target must fail closed")
	}
	if u.IsOpen(modMid, modBase, "") {
		t.Fatal("empty package must fail closed")
	}

	empty := NewUniverse()
	if empty.CanSetAccessible(UnnamedModule, UnnamedModule, "p") {
		t.Fatal("unnamed module without AddUnnamed must fail closed")
	}
}
