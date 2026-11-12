package laplacian

import "testing"

func benchmarkGrid(cols, rows int) *Mesh {
	mesh, _ := gridMesh(cols, rows)
	return mesh
}

func BenchmarkConstructAndDeform(b *testing.B) {
	mesh := benchmarkGrid(24, 24)
	anchors := []int{0, 23, 24 * 23, 24*24 - 1}
	targets := []Vec3{{Z: 1}, {X: 23, Z: 2}, {Y: 23, Z: -1}, {X: 23, Y: 23, Z: 3}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		editor, err := NewEditor(mesh, anchors, targets)
		if err != nil {
			b.Fatal(err)
		}
		editor.Deform()
	}
}

func BenchmarkReuseFactorization(b *testing.B) {
	mesh := benchmarkGrid(24, 24)
	anchors := []int{0, 23, 24 * 23, 24*24 - 1}
	targets := []Vec3{{Z: 1}, {X: 23, Z: 2}, {Y: 23, Z: -1}, {X: 23, Y: 23, Z: 3}}
	editor, err := NewEditor(mesh, anchors, targets)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t := targets
		t[0].Z = float64(i%5) * 0.5
		if err := editor.SetTargets(t); err != nil {
			b.Fatal(err)
		}
		editor.Deform()
	}
}
