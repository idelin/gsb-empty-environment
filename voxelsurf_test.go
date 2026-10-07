package voxelsurf

import (
	"math/rand"
	"sort"
	"testing"
)

func TestEmpty(t *testing.T) {
	r := Analyze(nil)
	if r.TotalSurface != 0 || r.OuterSurface != 0 || r.CavityVolume != 0 || len(r.Cavities) != 0 {
		t.Fatalf("empty input: got %+v", r)
	}
}

func TestSingleVoxel(t *testing.T) {
	r := Analyze([]Vec3{{5, -7, 10}})
	if r.TotalSurface != 6 || r.OuterSurface != 6 || r.InnerSurface != 0 {
		t.Fatalf("single voxel: got total=%d outer=%d inner=%d", r.TotalSurface, r.OuterSurface, r.InnerSurface)
	}
	if len(r.Faces) != 6 {
		t.Fatalf("expected 6 face evidence entries, got %d", len(r.Faces))
	}
}

func TestDuplicatesCountOnce(t *testing.T) {
	a := Analyze([]Vec3{{0, 0, 0}, {0, 0, 0}, {1, 0, 0}, {1, 0, 0}, {1, 0, 0}})
	b := Analyze([]Vec3{{0, 0, 0}, {1, 0, 0}})
	if a.TotalSurface != 10 || a.TotalSurface != b.TotalSurface || a.OuterSurface != b.OuterSurface {
		t.Fatalf("duplicates: got total=%d want 10", a.TotalSurface)
	}
	if a.DuplicatesIgnored != 3 {
		t.Fatalf("duplicates ignored = %d, want 3", a.DuplicatesIgnored)
	}
}

func TestCube2x2x2(t *testing.T) {
	var vs []Vec3
	for x := int64(0); x < 2; x++ {
		for y := int64(0); y < 2; y++ {
			for z := int64(0); z < 2; z++ {
				vs = append(vs, Vec3{x, y, z})
			}
		}
	}
	r := Analyze(vs)
	if r.TotalSurface != 24 || r.OuterSurface != 24 || r.InnerSurface != 0 || r.CavityVolume != 0 {
		t.Fatalf("2x2x2 cube: got %+v", r)
	}
}

func TestHollowShell3x3x3(t *testing.T) {
	// 3x3x3 cube without its center: one sealed unit cavity.
	var vs []Vec3
	for x := int64(0); x < 3; x++ {
		for y := int64(0); y < 3; y++ {
			for z := int64(0); z < 3; z++ {
				if x == 1 && y == 1 && z == 1 {
					continue
				}
				vs = append(vs, Vec3{x, y, z})
			}
		}
	}
	r := Analyze(vs)
	if r.TotalSurface != 60 || r.OuterSurface != 54 || r.InnerSurface != 6 {
		t.Fatalf("shell: total=%d outer=%d inner=%d, want 60/54/6", r.TotalSurface, r.OuterSurface, r.InnerSurface)
	}
	if len(r.Cavities) != 1 || r.Cavities[0].Volume != 1 || r.CavityVolume != 1 {
		t.Fatalf("shell: cavities=%+v", r.Cavities)
	}
	if len(r.Cavities[0].Faces) != 6 {
		t.Fatalf("shell: cavity faces = %v", r.Cavities[0].Faces)
	}
}

func TestShellWithHoleHasNoCavity(t *testing.T) {
	// 3x3x3 shell with the face-center voxel (0,1,1) removed: the center
	// cell connects to the outside through the hole.
	var vs []Vec3
	for x := int64(0); x < 3; x++ {
		for y := int64(0); y < 3; y++ {
			for z := int64(0); z < 3; z++ {
				if (x == 1 && y == 1 && z == 1) || (x == 0 && y == 1 && z == 1) {
					continue
				}
				vs = append(vs, Vec3{x, y, z})
			}
		}
	}
	r := Analyze(vs)
	if r.CavityVolume != 0 || r.InnerSurface != 0 || len(r.Cavities) != 0 {
		t.Fatalf("shell with hole: cavities=%+v inner=%d", r.Cavities, r.InnerSurface)
	}
	if r.OuterSurface != r.TotalSurface {
		t.Fatalf("shell with hole: outer=%d total=%d", r.OuterSurface, r.TotalSurface)
	}
}

func TestCheckerboardUnitCavities(t *testing.T) {
	// 9^3 checkerboard: every interior odd-parity cell is a sealed unit
	// cavity enclosed by 6 face-adjacent voxels.
	var vs []Vec3
	for x := int64(0); x < 9; x++ {
		for y := int64(0); y < 9; y++ {
			for z := int64(0); z < 9; z++ {
				if (x+y+z)%2 == 0 {
					vs = append(vs, Vec3{x, y, z})
				}
			}
		}
	}
	r := Analyze(vs)
	b := AnalyzeBrute(vs)
	compareResults(t, r, b)
	if len(r.Cavities) == 0 {
		t.Fatal("checkerboard: expected unit cavities")
	}
	for _, c := range r.Cavities {
		if c.Volume != 1 {
			t.Fatalf("checkerboard: cavity volume %d, want 1", c.Volume)
		}
	}
}

func TestHugeCoordinateSpan(t *testing.T) {
	// Same hollow shell placed at enormous coordinates.
	var vs []Vec3
	const off = int64(1) << 60
	for x := int64(0); x < 3; x++ {
		for y := int64(0); y < 3; y++ {
			for z := int64(0); z < 3; z++ {
				if x == 1 && y == 1 && z == 1 {
					continue
				}
				vs = append(vs, Vec3{x + off, y - off, z + off/7})
			}
		}
	}
	r := Analyze(vs)
	if r.TotalSurface != 60 || r.OuterSurface != 54 || r.CavityVolume != 1 {
		t.Fatalf("huge span shell: %+v", r.Summary())
	}
}

func TestSparseGridNoCavities(t *testing.T) {
	// 10000 voxels spaced far apart: nothing touches, every face is outer.
	var vs []Vec3
	i := 0
	for x := int64(0); x < 22 && len(vs) < 10000; x++ {
		for y := int64(0); y < 22 && len(vs) < 10000; y++ {
			for z := int64(0); z < 22 && len(vs) < 10000; z++ {
				vs = append(vs, Vec3{x * 1000, y * 1000, z * 1000})
				i++
			}
		}
	}
	r := Analyze(vs)
	if want := int64(6 * len(vs)); r.TotalSurface != want || r.OuterSurface != want {
		t.Fatalf("sparse grid: total=%d outer=%d want %d", r.TotalSurface, r.OuterSurface, want)
	}
	if r.CavityVolume != 0 || len(r.Cavities) != 0 {
		t.Fatalf("sparse grid: unexpected cavities %+v", r.Cavities)
	}
}

func TestDiagonalPermutationStress(t *testing.T) {
	// 10000 voxels with pairwise distinct consecutive coordinates: the
	// compressed grid would have ~2e4 planes per axis (worst case for a
	// naive compressed flood fill), but no two voxels touch.
	var vs []Vec3
	for i := int64(0); i < 10000; i++ {
		vs = append(vs, Vec3{i, (i * 7919) % 10000, (i * 104729) % 10000})
	}
	r := Analyze(vs)
	if want := int64(6 * len(vs)); r.TotalSurface != want || r.OuterSurface != want || r.CavityVolume != 0 {
		t.Fatalf("diagonal: total=%d outer=%d cav=%d want %d", r.TotalSurface, r.OuterSurface, r.CavityVolume, want)
	}
}

func TestBigCavity(t *testing.T) {
	// 25^3 shell enclosing a 23^3 cavity (12167 cells).
	var vs []Vec3
	const s = 25
	for x := int64(0); x < s; x++ {
		for y := int64(0); y < s; y++ {
			for z := int64(0); z < s; z++ {
				if x == 0 || y == 0 || z == 0 || x == s-1 || y == s-1 || z == s-1 {
					vs = append(vs, Vec3{x, y, z})
				}
			}
		}
	}
	r := Analyze(vs)
	if len(r.Cavities) != 1 || r.CavityVolume != 23*23*23 {
		t.Fatalf("big cavity: %+v", r.Cavities)
	}
	if r.OuterSurface != 6*s*s {
		t.Fatalf("big cavity: outer=%d want %d", r.OuterSurface, 6*s*s)
	}
	compareResults(t, r, AnalyzeBrute(vs))
}

func TestDeterministicEvidence(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	vs := genUniform(rng, 300, 7, Vec3{}, 1)
	a := Analyze(vs)
	b := Analyze(vs)
	compareResults(t, a, b)
	for i := range a.Faces {
		if a.Faces[i] != b.Faces[i] {
			t.Fatalf("face %d differs between runs", i)
		}
	}
}

// compareResults asserts that two Results agree on every quantity,
// including per-face evidence (cavity IDs may be numbered differently, so
// cavity faces are compared through the volume of their cavity).
func compareResults(t *testing.T, a, b Result) {
	t.Helper()
	if a.TotalSurface != b.TotalSurface || a.OuterSurface != b.OuterSurface ||
		a.InnerSurface != b.InnerSurface || a.CavityVolume != b.CavityVolume {
		t.Fatalf("summary mismatch:\nA:\n%sB:\n%s", a.Summary(), b.Summary())
	}
	if len(a.Cavities) != len(b.Cavities) {
		t.Fatalf("cavity count %d != %d", len(a.Cavities), len(b.Cavities))
	}
	va := cavityVolumes(a)
	vb := cavityVolumes(b)
	for i := range va {
		if va[i] != vb[i] {
			t.Fatalf("cavity volumes %v != %v", va, vb)
		}
	}
	if len(a.Faces) != len(b.Faces) {
		t.Fatalf("face count %d != %d", len(a.Faces), len(b.Faces))
	}
	for i := range a.Faces {
		fa, fb := a.Faces[i], b.Faces[i]
		if fa.Voxel != fb.Voxel || fa.Dir != fb.Dir || fa.Class != fb.Class {
			t.Fatalf("face %d mismatch: %+v vs %+v", i, fa, fb)
		}
		if fa.Class == FaceCavity {
			if a.Cavities[fa.Cavity].Volume != b.Cavities[fb.Cavity].Volume {
				t.Fatalf("face %d cavity volume %d != %d", i,
					a.Cavities[fa.Cavity].Volume, b.Cavities[fb.Cavity].Volume)
			}
		}
	}
}

func cavityVolumes(r Result) []int64 {
	v := make([]int64, len(r.Cavities))
	for i, c := range r.Cavities {
		v[i] = c.Volume
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return v
}

func genUniform(rng *rand.Rand, n, side int, off Vec3, scale int64) []Vec3 {
	vs := make([]Vec3, n)
	for i := range vs {
		vs[i] = Vec3{
			off.X + int64(rng.Intn(side))*scale,
			off.Y + int64(rng.Intn(side))*scale,
			off.Z + int64(rng.Intn(side))*scale,
		}
	}
	return vs
}

func genWalk(rng *rand.Rand, n int) []Vec3 {
	vs := []Vec3{{0, 0, 0}}
	cur := Vec3{}
	for len(vs) < n {
		d := dirDelta[rng.Intn(6)]
		cur = Vec3{cur.X + d.X, cur.Y + d.Y, cur.Z + d.Z}
		vs = append(vs, cur)
	}
	return vs
}

func genShell(rng *rand.Rand, side int64, deleteProb float64) []Vec3 {
	var vs []Vec3
	for x := int64(0); x < side; x++ {
		for y := int64(0); y < side; y++ {
			for z := int64(0); z < side; z++ {
				if x != 0 && y != 0 && z != 0 && x != side-1 && y != side-1 && z != side-1 {
					continue
				}
				if rng.Float64() < deleteProb {
					continue
				}
				vs = append(vs, Vec3{x, y, z})
			}
		}
	}
	return vs
}

func TestDifferentialRandom(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var vs []Vec3
		switch seed % 5 {
		case 0: // dense cluster in a small box: cavities likely
			vs = genUniform(rng, 1+rng.Intn(400), 4+rng.Intn(5), Vec3{}, 1)
		case 1: // sparse with gaps between planes
			vs = genUniform(rng, 1+rng.Intn(200), 5+rng.Intn(4), Vec3{}, 2+int64(rng.Intn(3)))
		case 2: // random walk cluster
			vs = genWalk(rng, 1+rng.Intn(500))
		case 3: // box shell with random holes
			vs = genShell(rng, 3+int64(rng.Intn(5)), rng.Float64()*0.3)
		case 4: // dense cluster far from the origin
			vs = genUniform(rng, 1+rng.Intn(300), 6, Vec3{1 << 40, -(1 << 38), 1 << 35}, 1)
		}
		if seed%7 == 0 { // inject duplicates
			for i := 0; i < len(vs)/3; i++ {
				vs = append(vs, vs[rng.Intn(len(vs))])
			}
		}
		a := Analyze(vs)
		b := AnalyzeBrute(vs)
		compareResults(t, a, b)
		if a.TotalSurface != a.OuterSurface+a.InnerSurface {
			t.Fatalf("seed %d: total != outer+inner", seed)
		}
	}
}
