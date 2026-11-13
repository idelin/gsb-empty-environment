package netsec

import "testing"

func TestReferenceGraphCycleRejected(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), nil)

	nodes := map[string]*Kustomization{
		"kustomization.yaml": {
			Resources: []string{"overlays/a"},
		},
		"overlays/a/kustomization.yaml": {
			Resources: []string{"../b"},
		},
		"overlays/b/kustomization.yaml": {
			Resources: []string{"../a"},
		},
	}

	err := admitter.ValidateReferenceGraph(nodes)
	assertViolation(t, err, ReasonReferenceCycle)
}

func TestReferenceGraphSelfCycleRejected(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), nil)

	nodes := map[string]*Kustomization{
		"kustomization.yaml": {
			Resources: []string{"."},
		},
	}

	err := admitter.ValidateReferenceGraph(nodes)
	assertViolation(t, err, ReasonReferenceCycle)
}

func TestReferenceGraphAcyclicAllowed(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), nil)

	nodes := map[string]*Kustomization{
		"kustomization.yaml": {
			Resources: []string{"overlays/dev", "base"},
		},
		"overlays/dev/kustomization.yaml": {
			Resources: []string{"../../base"},
			Patches:   []Patch{{Path: "../../base/patch.yaml"}},
		},
		"base/kustomization.yml": {},
	}

	err := admitter.ValidateReferenceGraph(nodes)
	assertAllowed(t, err)
}

func TestReferenceGraphCycleWithExplicitFileTargets(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), nil)

	nodes := map[string]*Kustomization{
		"a/kustomization.yaml": {
			Resources: []string{"../b/kustomization.yaml"},
		},
		"b/kustomization.yaml": {
			Resources: []string{"../a/kustomization.yaml"},
		},
	}

	err := admitter.ValidateReferenceGraph(nodes)
	assertViolation(t, err, ReasonReferenceCycle)
}

func TestReferenceGraphReportsContainmentViolations(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), nil)

	nodes := map[string]*Kustomization{
		"kustomization.yaml": {
			Resources: []string{"../outside"},
		},
	}

	err := admitter.ValidateReferenceGraph(nodes)
	assertViolation(t, err, ReasonPathEscapesRoot)
}
