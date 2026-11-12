package netsec_test

import (
	"testing"

	"example.org/netsec/q0480"
)

func TestExternalAPI(t *testing.T) {
	u := netsec.NewUniverse()
	_ = u.Add(netsec.Descriptor{
		Name:     "java.base",
		Packages: []string{"java.lang", "jdk.internal.misc"},
		Opens:    []netsec.PackageDirective{{Package: "java.lang", To: []string{"plugin"}}},
	})
	_ = u.Add(netsec.Descriptor{
		Name:     "plugin",
		Requires: []netsec.Require{{Module: "java.base"}},
	})
	_ = u.AddUnnamed()
	_ = u.AddPatch(netsec.AddOpensPatch{
		Source: "java.base", Package: "jdk.internal.misc",
		Targets: []string{netsec.AllUnnamed},
	})
	if !u.CanSetAccessible("plugin", "java.base", "java.lang") {
		t.Fatal("qualified opens must allow plugin")
	}
	if !u.CanSetAccessible(netsec.UnnamedModule, "java.base", "jdk.internal.misc") {
		t.Fatal("ALL-UNNAMED patch must allow unnamed caller")
	}
}
