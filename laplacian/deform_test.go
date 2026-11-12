package laplacian

import "testing"

func TestAnchorsStrictlySatisfiedAndResidualReturned(t *testing.T) {
	mesh, grid := gridMesh(5, 5)
	anchors := []int{0, 4, 20, 24}
	targets := []Vec3{
		{0, 0, 0},
		{6, 0, 2},
		{2, 6, -1},
		{8, 8, 3},
	}

	editor, err := NewEditor(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	result := editor.Deform()

	if len(result.Positions) != len(mesh.Vertices) {
		t.Fatalf("positions = %d, want %d", len(result.Positions), len(mesh.Vertices))
	}
	if result.AnchorMax > testEps {
		t.Fatalf("anchor violation = %g", result.AnchorMax)
	}
	for i, idx := range anchors {
		if distance(result.Positions[idx], targets[i]) > testEps {
			t.Fatalf("anchor %d moved to %v, want %v", idx, result.Positions[idx], targets[i])
		}
	}
	if len(result.VertexResidual) != len(mesh.Vertices) {
		t.Fatalf("residual vector length = %d", len(result.VertexResidual))
	}
	if result.RMSResidual < 0 {
		t.Fatalf("negative RMS residual %g", result.RMSResidual)
	}
	if result.MaxResidual+testEps < result.RMSResidual {
		t.Fatalf("max %g below RMS %g", result.MaxResidual, result.RMSResidual)
	}

	moved := false
	for i := range mesh.Vertices {
		if distance(result.Positions[i], mesh.Vertices[i]) > 1e-6 {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("expected free vertices to deform")
	}
	if grid[0] != [2]int{0, 0} {
		t.Fatalf("grid layout mismatch: %v", grid[0])
	}
}

func TestIdentityTargetsPreserveMesh(t *testing.T) {
	mesh, _ := gridMesh(4, 4)
	anchors := []int{0, 3, 12, 15}
	targets := []Vec3{
		mesh.Vertices[0], mesh.Vertices[3],
		mesh.Vertices[12], mesh.Vertices[15],
	}
	result, err := Deform(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("Deform: %v", err)
	}
	if result.RMSResidual > testEps {
		t.Fatalf("identity deformation residual = %g", result.RMSResidual)
	}
	for i := range mesh.Vertices {
		if d := distance(result.Positions[i], mesh.Vertices[i]); d > testEps {
			t.Fatalf("vertex %d drifted by %g", i, d)
		}
	}
}

func TestTranslationEquivariance(t *testing.T) {
	mesh, _ := gridMesh(4, 3)
	shift := Vec3{X: 2.5, Y: -1.25, Z: 4}

	anchors := []int{0, 3, 6, 11}
	targets := make([]Vec3, len(anchors))
	shiftedTargets := make([]Vec3, len(anchors))
	for i, idx := range anchors {
		targets[i] = mesh.Vertices[idx]
		shiftedTargets[i] = addVec(targets[i], shift)
	}

	base, err := Deform(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("base Deform: %v", err)
	}
	shifted, err := Deform(mesh, anchors, shiftedTargets)
	if err != nil {
		t.Fatalf("shifted Deform: %v", err)
	}

	for i := range mesh.Vertices {
		want := addVec(base.Positions[i], shift)
		if d := distance(shifted.Positions[i], want); d > 1e-9 {
			t.Fatalf("vertex %d: got %v, want %v (gap %g)", i, shifted.Positions[i], want, d)
		}
	}
	if shifted.RMSResidual > 1e-9 {
		t.Fatalf("translated setup should preserve residual, got %g", shifted.RMSResidual)
	}
}

func TestMultipleComponentsEachAnchored(t *testing.T) {
	mesh := twoComponentMesh()
	anchors := []int{0, 3}
	targets := []Vec3{{0, 0, 0}, {10, 10, 10}}

	editor, err := NewEditor(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	ids, count := editor.Components()
	if count != 2 {
		t.Fatalf("components = %d, want 2", count)
	}
	if ids[0] == ids[3] {
		t.Fatal("triangles expected in distinct components")
	}

	result := editor.Deform()
	if result.AnchorMax > testEps {
		t.Fatalf("anchor violation = %g", result.AnchorMax)
	}
	if result.RMSResidual > testEps {
		t.Fatalf("rigid component residual = %g", result.RMSResidual)
	}
	if distance(result.Positions[3], targets[1]) > testEps {
		t.Fatal("second component anchor not satisfied")
	}
}

func TestFactorizationReusedOnTargetUpdate(t *testing.T) {
	mesh, _ := gridMesh(6, 6)
	anchors := []int{0, 5, 30, 35}
	editor, err := NewEditor(mesh, anchors, []Vec3{{}, {X: 5}, {Y: 5}, {X: 5, Y: 5}})
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}

	first := editor.Deform()
	if first.ReusedFactorization {
		t.Fatal("first solve reports reused factorization")
	}

	newTargets := []Vec3{{Z: 1}, {X: 5, Z: 2}, {Y: 5, Z: -1}, {X: 5, Y: 5, Z: 3}}
	if err := editor.SetTargets(newTargets); err != nil {
		t.Fatalf("SetTargets: %v", err)
	}
	second := editor.Deform()
	if !second.ReusedFactorization {
		t.Fatal("second solve did not reuse factorization")
	}

	if err := editor.SetTargets([]Vec3{{}}); err != ErrAnchorOutOfRange {
		t.Fatalf("length check got %v", err)
	}
	bad := append([]Vec3{}, newTargets...)
	bad[0] = Vec3{X: inf()}
	if err := editor.SetTargets(bad); err != ErrInvalidMesh {
		t.Fatalf("finiteness check got %v", err)
	}

	fresh, err := NewEditor(mesh, anchors, newTargets)
	if err != nil {
		t.Fatalf("fresh NewEditor: %v", err)
	}
	freshResult := fresh.Deform()
	for i := range mesh.Vertices {
		if d := distance(second.Positions[i], freshResult.Positions[i]); d > 1e-9 {
			t.Fatalf("vertex %d differs by %g after reuse", i, d)
		}
	}
}

func TestDuplicateAnchorsDeduplicated(t *testing.T) {
	mesh, _ := gridMesh(3, 3)
	anchors := []int{0, 0, 4}
	targets := []Vec3{{Z: 1}, {Z: 2}, {X: 2, Z: 1}}
	editor, err := NewEditor(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	if got := editor.Anchors(); len(got) != 2 || got[0] != 0 || got[1] != 4 {
		t.Fatalf("anchors = %v, want [0 4]", got)
	}
	result := editor.Deform()
	// First target for vertex 0 wins.
	if d := distance(result.Positions[0], targets[0]); d > testEps {
		t.Fatalf("duplicate anchor resolved to %v, want first target %v", result.Positions[0], targets[0])
	}
}

func TestAllVerticesAnchored(t *testing.T) {
	mesh, _ := gridMesh(2, 2)
	anchors := []int{0, 1, 2, 3}
	targets := []Vec3{{Z: 1}, {X: 1, Z: 2}, {Y: 1, Z: -1}, {X: 1, Y: 1, Z: 3}}
	result, err := Deform(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("Deform: %v", err)
	}
	if result.ReusedFactorization {
		t.Fatal("fully anchored mesh has no factorization to reuse")
	}
	for i, want := range targets {
		if d := distance(result.Positions[i], want); d > testEps {
			t.Fatalf("vertex %d = %v, want %v", i, result.Positions[i], want)
		}
	}
}

func TestIsolatedVertexAcceptedWhenAnchored(t *testing.T) {
	mesh, _ := gridMesh(2, 2)
	mesh.Vertices = append(mesh.Vertices, Vec3{X: 9, Y: 9, Z: 9})
	isolated := len(mesh.Vertices) - 1

	anchors := []int{0, isolated}
	targets := []Vec3{{Z: 1}, {X: -3, Y: -4, Z: 5}}
	result, err := Deform(mesh, anchors, targets)
	if err != nil {
		t.Fatalf("Deform: %v", err)
	}
	if d := distance(result.Positions[isolated], targets[1]); d > testEps {
		t.Fatalf("isolated anchor = %v, want %v", result.Positions[isolated], targets[1])
	}
	// With degree 0 the Laplacian row is just the identity, so its Laplacian
	// coordinate equals its position; hard-anchoring elsewhere yields a
	// residual equal to the imposed displacement.
	want := distance(mesh.Vertices[isolated], targets[1])
	if d := distance(Vec3{X: result.VertexResidual[isolated]}, Vec3{X: want}); d > 1e-9 {
		t.Fatalf("isolated vertex residual = %g, want %g", result.VertexResidual[isolated], want)
	}
}
