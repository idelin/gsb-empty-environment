package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func export(name, version string, strong bool) Export {
	return Export{Name: name, Version: version, Strong: strong}
}

func TestResolveWeakOverriddenByLaterStrong(t *testing.T) {
	modules := []Module{
		{ID: "weak", Trusted: true, Exports: []Export{export("auth", "1.0", false)}},
		{ID: "strong", Trusted: true, Exports: []Export{export("auth", "1.0", true)}},
	}
	binding, err := Resolve(modules, []string{"weak", "strong"}, Relocation{
		Origin:          "weak",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !binding.Found || binding.ModuleID != "strong" || !binding.Strong {
		t.Fatalf("binding = %+v, want strong definition", binding)
	}
	if binding.Selection != SelectionFirstStrong {
		t.Fatalf("selection = %q", binding.Selection)
	}
	if got, want := binding.Sequence, []string{"weak", "strong"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence = %v, want %v", got, want)
	}
	if len(binding.Lookup) != 2 || binding.Lookup[0].Selected || !binding.Lookup[1].Selected {
		t.Fatalf("lookup = %+v", binding.Lookup)
	}
}

func TestResolveDependencyBreadthFirstAndCycle(t *testing.T) {
	modules := []Module{
		{ID: "origin", Dependencies: []string{"d1", "d2"}},
		{ID: "d1", Dependencies: []string{"d3"}},
		{ID: "d2", Dependencies: []string{"d1"}},
		{ID: "d3", Dependencies: []string{"d1"}},
	}
	binding, err := Resolve(modules, []string{"origin"}, Relocation{
		Origin:          "origin",
		Name:            "missing",
		Version:         "1.0",
		AllowPreemption: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"origin", "d1", "d2", "d3"}
	if !reflect.DeepEqual(binding.Sequence, want) {
		t.Fatalf("sequence = %v, want %v", binding.Sequence, want)
	}
	if binding.Found {
		t.Fatalf("unexpected binding: %+v", binding)
	}

	// The origin is the root, not a dependency candidate reached by a cycle,
	// even when the host did not place it in the global list.
	modules = []Module{
		{ID: "origin", Dependencies: []string{"d1"}},
		{ID: "d1", Dependencies: []string{"origin"}},
		{ID: "other"},
	}
	binding, err = Resolve(modules, []string{"other"}, Relocation{
		Origin:          "origin",
		Name:            "missing",
		Version:         "1.0",
		AllowPreemption: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"other", "d1"}; !reflect.DeepEqual(binding.Sequence, want) {
		t.Fatalf("cycle sequence = %v, want %v", binding.Sequence, want)
	}
}

func TestResolveRequiresExactVersion(t *testing.T) {
	modules := []Module{
		{ID: "v1", Trusted: true, Exports: []Export{export("auth", "1.0", false)}},
		{ID: "v2", Trusted: true, Exports: []Export{export("auth", "2.0", true)}},
	}
	binding, err := Resolve(modules, []string{"v1", "v2"}, Relocation{
		Origin:          "v1",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !binding.Found || binding.ModuleID != "v1" || binding.Strong {
		t.Fatalf("binding = %+v, want weak v1", binding)
	}
	if binding.Lookup[1].HasMatch {
		t.Fatalf("v2 must not match exact version 1.0: %+v", binding.Lookup[1])
	}
}

func TestNoPreemptionKeepsLocalStrongDefinition(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: true, Exports: []Export{export("auth", "1.0", true)}},
		{ID: "evil", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
	}
	binding, err := Resolve(modules, []string{"evil", "origin"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: false,
		RequireTrusted:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !binding.Found || binding.ModuleID != "origin" {
		t.Fatalf("binding = %+v, want local origin", binding)
	}
	if !binding.TrustSatisfied {
		t.Fatalf("trust should be satisfied: %+v", binding)
	}
	if got, want := binding.Sequence, []string{"origin", "evil"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence = %v, want %v", got, want)
	}
}

func TestPreemptionAllowsEarlierExternalStrong(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: true, Exports: []Export{export("auth", "1.0", true)}},
		{ID: "evil", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
	}
	binding, err := Resolve(modules, []string{"evil", "origin"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: true,
		RequireTrusted:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !binding.Found || binding.ModuleID != "evil" {
		t.Fatalf("binding = %+v, want evil", binding)
	}
	if binding.TrustSatisfied {
		t.Fatalf("untrusted target must not satisfy trust: %+v", binding)
	}
	if !strings.Contains(binding.Explanation, "不可信") {
		t.Fatalf("explanation does not state the trust failure: %q", binding.Explanation)
	}
}

func TestResolveDeduplicatesWithoutChangingFirstOccurrence(t *testing.T) {
	modules := []Module{
		{ID: "a", Dependencies: []string{"b", "c"}},
		{ID: "b"},
		{ID: "c"},
	}
	binding, err := Resolve(modules, []string{"c", "a", "b"}, Relocation{
		Origin:          "a",
		Name:            "missing",
		Version:         "1.0",
		AllowPreemption: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "c", "b"}; !reflect.DeepEqual(binding.Sequence, want) {
		t.Fatalf("sequence = %v, want %v", binding.Sequence, want)
	}
}

func TestDependencyAlreadyGlobalIsStillExpanded(t *testing.T) {
	modules := []Module{
		{ID: "origin", Dependencies: []string{"already"}},
		{ID: "already", Dependencies: []string{"child"}},
		{ID: "child"},
	}
	for _, allow := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow=%t", allow), func(t *testing.T) {
			binding, err := Resolve(modules, []string{"already", "origin"}, Relocation{
				Origin:          "origin",
				Name:            "missing",
				Version:         "1.0",
				AllowPreemption: allow,
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"already", "origin", "child"}
			if !allow {
				want = []string{"origin", "already", "child"}
			}
			if !reflect.DeepEqual(binding.Sequence, want) {
				t.Fatalf("sequence = %v, want %v", binding.Sequence, want)
			}
		})
	}
}

func TestInputValidation(t *testing.T) {
	tooMany := make([]Module, MaxModules+1)
	for i := range tooMany {
		tooMany[i].ID = string(rune('a' + i))
	}

	tests := []struct {
		name       string
		modules    []Module
		global     []string
		relocation Relocation
	}{
		{
			name:       "too many modules",
			modules:    tooMany,
			relocation: Relocation{Origin: "a", Name: "auth", Version: "1"},
		},
		{
			name:       "duplicate module ID",
			modules:    []Module{{ID: "a"}, {ID: "a"}},
			relocation: Relocation{Origin: "a", Name: "auth", Version: "1"},
		},
		{
			name:       "missing dependency",
			modules:    []Module{{ID: "a", Dependencies: []string{"missing"}}},
			relocation: Relocation{Origin: "a", Name: "auth", Version: "1"},
		},
		{
			name: "duplicate export",
			modules: []Module{{ID: "a", Exports: []Export{
				export("auth", "1", false), export("auth", "1", true),
			}}},
			relocation: Relocation{Origin: "a", Name: "auth", Version: "1"},
		},
		{
			name:       "empty export name",
			modules:    []Module{{ID: "a", Exports: []Export{{Version: "1"}}}},
			relocation: Relocation{Origin: "a", Name: "auth", Version: "1"},
		},
		{
			name:       "duplicate global entry",
			modules:    []Module{{ID: "a"}},
			global:     []string{"a", "a"},
			relocation: Relocation{Origin: "a", Name: "auth", Version: "1"},
		},
		{
			name:       "unknown global module",
			modules:    []Module{{ID: "a"}},
			global:     []string{"b"},
			relocation: Relocation{Origin: "a", Name: "auth", Version: "1"},
		},
		{
			name:       "unknown origin",
			modules:    []Module{{ID: "a"}},
			relocation: Relocation{Origin: "b", Name: "auth", Version: "1"},
		},
		{
			name:       "empty symbol",
			modules:    []Module{{ID: "a"}},
			relocation: Relocation{Origin: "a", Version: "1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Resolve(test.modules, test.global, test.relocation)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestPluginValidation(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: true},
		{ID: "p1"}, {ID: "p2"}, {ID: "p3"}, {ID: "p4"},
	}
	relocation := Relocation{
		Origin: "origin", Name: "auth", Version: "1", RequireTrusted: true,
	}
	tests := []struct {
		name    string
		global  []string
		plugins []string
	}{
		{name: "too many plugins", global: []string{"origin"}, plugins: []string{"p1", "p2", "p3", "p4"}},
		{name: "duplicate plugin", global: []string{"origin"}, plugins: []string{"p1", "p1"}},
		{name: "unknown plugin", global: []string{"origin"}, plugins: []string{"missing"}},
		{name: "plugin already global", global: []string{"origin", "p1"}, plugins: []string{"p1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Analyze(modules, test.global, test.plugins, relocation)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
