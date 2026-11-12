package netsec

import (
	"errors"
	"testing"
)

func mustDefine(t *testing.T, b *Builder, spec Spec) {
	t.Helper()
	if err := b.Define(spec); err != nil {
		t.Fatalf("Define(%q) unexpected error: %v", spec.Name, err)
	}
}

func mustBuild(t *testing.T, b *Builder) *Environment {
	t.Helper()
	env, err := b.Build()
	if err != nil {
		t.Fatalf("Build unexpected error: %v", err)
	}
	return env
}

func assertRunsCover(t *testing.T, value Value) {
	t.Helper()
	total := 0
	for _, run := range value.Runs {
		total += len(run.Data)
	}
	if total != len(value.Data) {
		t.Fatalf("provenance length %d does not match value length %d for %s",
			total, len(value.Data), value.Name)
	}
}

func TestExpansionAndEscape(t *testing.T) {
	b := NewBuilder()
	mustDefine(t, b, Spec{Name: "ROOT", Template: "/opt/${APP}/bin"})
	mustDefine(t, b, Spec{Name: "APP", Template: "svc"})
	mustDefine(t, b, Spec{Name: "PRICE", Template: "$$5 and ${DOLLAR}"})
	mustDefine(t, b, Spec{Name: "DOLLAR", Template: "$$"})
	mustDefine(t, b, Spec{Name: "TRAIL", Template: "a$$b${APP}"})

	env := mustBuild(t, b)

	cases := map[string]string{
		"ROOT":   "/opt/svc/bin",
		"APP":    "svc",
		"PRICE":  "$5 and $",
		"DOLLAR": "$",
		"TRAIL":  "a$bsvc",
	}
	for name, want := range cases {
		got, ok := env.Get(name)
		if !ok {
			t.Fatalf("missing variable %s", name)
		}
		if got.Data != want {
			t.Fatalf("%s = %q, want %q", name, got.Data, want)
		}
		assertRunsCover(t, got)
	}

	got := env.Env()
	wantEnv := []string{
		"ROOT=/opt/svc/bin",
		"APP=svc",
		"PRICE=$5 and $",
		"DOLLAR=$",
		"TRAIL=a$bsvc",
	}
	if len(got) != len(wantEnv) {
		t.Fatalf("Env length = %d, want %d", len(got), len(wantEnv))
	}
	for i := range wantEnv {
		if got[i] != wantEnv[i] {
			t.Fatalf("Env[%d] = %q, want %q", i, got[i], wantEnv[i])
		}
	}
}

func TestSyntaxErrors(t *testing.T) {
	badTemplates := []string{
		"$",
		"abc$",
		"$APP",
		"${APP",
		"${1APP}",
		"${APP-x}",
		"$ ",
		"a${}b",
	}
	for _, template := range badTemplates {
		b := NewBuilder()
		err := b.Define(Spec{Name: "X", Template: template})
		var syntaxErr *SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Fatalf("template %q: want *SyntaxError, got %v", template, err)
		}
	}
}

func TestInvalidAndDuplicateNames(t *testing.T) {
	b := NewBuilder()
	err := b.Define(Spec{Name: "1BAD", Template: "x"})
	var nameErr *NameError
	if !errors.As(err, &nameErr) {
		t.Fatalf("want *NameError, got %v", err)
	}
	mustDefine(t, b, Spec{Name: "DUP", Template: "a"})
	if err := b.Define(Spec{Name: "DUP", Template: "b"}); err == nil {
		t.Fatal("duplicate Define must be rejected")
	} else {
		var dupErr *DuplicateError
		if !errors.As(err, &dupErr) {
			t.Fatalf("duplicate Define: want *DuplicateError, got %v", err)
		}
	}
	if err := b.Import([]string{"DUP=x"}, []string{"DUP"}); err == nil {
		t.Fatal("import shadowing a defined name must be rejected")
	}
}

func TestUndefinedReference(t *testing.T) {
	b := NewBuilder()
	mustDefine(t, b, Spec{Name: "A", Template: "${MISSING}"})
	_, err := b.Build()
	var undefinedErr *UndefinedError
	if !errors.As(err, &undefinedErr) {
		t.Fatalf("want *UndefinedError, got %v", err)
	}
	if undefinedErr.Ref != "MISSING" || undefinedErr.In != "A" {
		t.Fatalf("unexpected undefined error fields: %+v", undefinedErr)
	}
}

func TestCycles(t *testing.T) {
	t.Run("self reference", func(t *testing.T) {
		b := NewBuilder()
		mustDefine(t, b, Spec{Name: "A", Template: "${A}"})
		assertCycle(t, b)
	})

	t.Run("indirect cycle", func(t *testing.T) {
		b := NewBuilder()
		mustDefine(t, b, Spec{Name: "A", Template: "${B}"})
		mustDefine(t, b, Spec{Name: "B", Template: "${C}"})
		mustDefine(t, b, Spec{Name: "C", Template: "${A}"})
		assertCycle(t, b)
	})
}

func assertCycle(t *testing.T, b *Builder) {
	t.Helper()
	_, err := b.Build()
	var cycleErr *CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("want *CycleError, got %v", err)
	}
	if len(cycleErr.Path) < 2 || cycleErr.Path[0] != cycleErr.Path[len(cycleErr.Path)-1] {
		t.Fatalf("cycle path must start and end at the same node: %v", cycleErr.Path)
	}
}
