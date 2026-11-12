package taskpkg

import (
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// bruteForce is the per-face reference: it tests every triangle directly.
func bruteForce(m *Mesh, f *Frame, ray Ray) (Hit, bool) {
	best := Hit{T: math.Inf(1)}
	hit := false
	for tri := range m.Tris {
		v0 := m.Vertex(f, tri, 0, ray.Time)
		v1 := m.Vertex(f, tri, 1, ray.Time)
		v2 := m.Vertex(f, tri, 2, ray.Time)
		if t, u, v, ok := intersectTri(ray.Orig, ray.Dir, v0, v1, v2); ok && t < best.T {
			best = Hit{T: t, U: u, V: v, Tri: tri}
			hit = true
		}
	}
	return best, hit
}

func randomVec(rng *rand.Rand, lo, hi float64) Vec3 {
	span := hi - lo
	return Vec3{lo + rng.Float64()*span, lo + rng.Float64()*span, lo + rng.Float64()*span}
}

// randomMesh builds a mesh with nTris random triangles and random linear
// velocities, returning the mesh and the per-vertex velocities.
func randomMesh(rng *rand.Rand, nTris int) (*Mesh, []Vec3) {
	nVerts := nTris * 3
	pos0 := make([]Vec3, nVerts)
	vel := make([]Vec3, nVerts)
	for i := range pos0 {
		pos0[i] = randomVec(rng, -1, 1)
		vel[i] = randomVec(rng, -0.4, 0.4)
	}
	pos1 := make([]Vec3, nVerts)
	for i := range pos1 {
		pos1[i] = pos0[i].Add(vel[i])
	}
	tris := make([][3]int32, nTris)
	for i := range tris {
		tris[i] = [3]int32{int32(3 * i), int32(3*i + 1), int32(3*i + 2)}
	}
	return NewMesh(tris, pos0, pos1), vel
}

// advance moves the mesh one frame forward along the velocities.
func advance(m *Mesh, vel []Vec3) {
	f := m.Frame()
	pos0 := make([]Vec3, len(f.Pos1))
	copy(pos0, f.Pos1)
	pos1 := make([]Vec3, len(f.Pos1))
	for i := range pos1 {
		pos1[i] = pos0[i].Add(vel[i])
	}
	m.SetFrame(pos0, pos1)
}

func randomRay(rng *rand.Rand) Ray {
	dir := randomVec(rng, -1, 1)
	for dir.Dot(dir) < 1e-6 {
		dir = randomVec(rng, -1, 1)
	}
	return Ray{Orig: randomVec(rng, -2, 2), Dir: dir}
}

func checkAgainstReference(t *testing.T, snap *Snapshot, rng *rand.Rand, nRays int, times []float64) {
	t.Helper()
	for _, tm := range times {
		for i := 0; i < nRays; i++ {
			ray := randomRay(rng)
			ray.Time = tm
			got, gotOK := snap.Intersect(ray)
			want, wantOK := bruteForce(snap.mesh, snap.frame, ray)
			if gotOK != wantOK {
				t.Fatalf("time=%v ray=%+v: bvh hit=%v, reference hit=%v", tm, ray, gotOK, wantOK)
			}
			if !gotOK {
				continue
			}
			if got.Tri != want.Tri || math.Abs(got.T-want.T) > 1e-9 {
				t.Fatalf("time=%v ray=%+v: bvh hit tri=%d t=%v, reference tri=%d t=%v",
					tm, ray, got.Tri, got.T, want.Tri, want.T)
			}
		}
	}
}

// validateStructure checks that every box covers its contents (the light
// leak suspect) and that the leaf index agrees with the tree.
func validateStructure(t *testing.T, snap *Snapshot) {
	t.Helper()
	st := snap.st
	var walk func(id int32) AABB
	walk = func(id int32) AABB {
		n := &st.nodes[id]
		if n.isLeaf() {
			want := emptyAABB()
			for i := n.start; i < n.start+n.count; i++ {
				tri := int(st.triRefs[i])
				want = want.Union(snap.mesh.TriBounds(snap.frame, tri))
				if snap.LeafOf(tri) != id {
					t.Fatalf("leaf index: tri %d maps to node %d, actually in leaf %d", tri, snap.LeafOf(tri), id)
				}
			}
			if n.bounds != want {
				t.Fatalf("leaf %d bounds %+v do not cover swept triangles %+v", id, n.bounds, want)
			}
			return n.bounds
		}
		lb := walk(n.left)
		rb := walk(n.right)
		want := lb.Union(rb)
		if n.bounds != want {
			t.Fatalf("internal node %d bounds %+v do not cover children %+v", id, n.bounds, want)
		}
		return n.bounds
	}
	if st.root >= 0 {
		walk(st.root)
	}
}

func TestUpdateMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	mesh, vel := randomMesh(rng, 150)
	b := NewBVH(mesh, 1.1)
	times := []float64{0, 0.25, 0.5, 0.75, 1}

	prevNodes := 0
	rebuilt := false
	for frame := 0; frame < 4; frame++ {
		if frame > 0 {
			advance(mesh, vel)
			b.Update()
		}
		snap := b.Snapshot()
		validateStructure(t, snap)
		checkAgainstReference(t, snap, rng, 400, times)
		if n := len(snap.st.nodes); prevNodes > 0 && n > prevNodes {
			rebuilt = true
		}
		prevNodes = len(snap.st.nodes)
	}
	if !rebuilt {
		t.Fatal("expected at least one local rebuild across updates")
	}
}

func TestConcurrentQueriesPinnedSnapshot(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	mesh, _ := randomMesh(rng, 120)
	// Multiplier < 1 forces local rebuilds on every Update, churning node
	// ids while readers traverse a pinned version.
	b := NewBVH(mesh, 0.5)
	snap := b.Snapshot()

	const nRays = 256
	type result struct {
		hit Hit
		ok  bool
	}
	rays := make([]Ray, nRays)
	ref := make([]result, nRays)
	for i := range rays {
		rays[i] = randomRay(rng)
		rays[i].Time = rng.Float64()
		ref[i].hit, ref[i].ok = snap.Intersect(rays[i])
	}

	var stop atomic.Bool
	var mismatches atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for i, ray := range rays {
					h, ok := snap.Intersect(ray)
					if ok != ref[i].ok || h != ref[i].hit {
						mismatches.Add(1)
					}
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		b.Update()
		if i%25 == 0 {
			// Every published version must be a valid structure.
			fresh := b.Snapshot()
			for j := 0; j < 16; j++ {
				ray := rays[rng.Intn(nRays)]
				got, gotOK := fresh.Intersect(ray)
				want, wantOK := bruteForce(mesh, mesh.Frame(), ray)
				if gotOK != wantOK || (gotOK && (got.Tri != want.Tri || math.Abs(got.T-want.T) > 1e-9)) {
					t.Errorf("fresh snapshot after update %d disagrees with reference", i)
				}
			}
		}
	}
	stop.Store(true)
	wg.Wait()
	if mismatches.Load() != 0 {
		t.Fatalf("pinned snapshot returned %d inconsistent results during concurrent updates", mismatches.Load())
	}
}

func TestDegenerateTrianglesNoFalseHit(t *testing.T) {
	// A degenerate triangle (all vertices collinear, moving) sits directly
	// in front of the ray; a valid triangle behind it must be hit instead.
	pos0 := []Vec3{
		{0, 0, -5}, {0.1, 0, -5}, {0.2, 0, -5}, // degenerate: collinear
		{-1, -1, -10}, {1, -1, -10}, {0, 1, -10}, // valid, behind
	}
	pos1 := []Vec3{
		{0, 0.1, -5}, {0.1, 0.1, -5}, {0.2, 0.1, -5},
		{-1, -1, -10}, {1, -1, -10}, {0, 1, -10},
	}
	tris := [][3]int32{{0, 1, 2}, {3, 4, 5}}
	mesh := NewMesh(tris, pos0, pos1)
	b := NewBVH(mesh, 1.5)

	for _, tm := range []float64{0, 0.5, 1} {
		ray := Ray{Orig: Vec3{0.1, 0, 0}, Dir: Vec3{0, 0, -1}, Time: tm}
		h, ok := b.Snapshot().Intersect(ray)
		if !ok {
			t.Fatalf("time=%v: expected hit on triangle behind degenerate one", tm)
		}
		if h.Tri != 1 {
			t.Fatalf("time=%v: false hit on degenerate triangle 0 (t=%v)", tm, h.T)
		}
		if math.Abs(h.T-10) > 1e-9 {
			t.Fatalf("time=%v: hit distance %v, want 10", tm, h.T)
		}
	}

	// Zero-area triangles collapsed to a point must never be hit.
	pos0 = []Vec3{{1, 1, -3}, {1, 1, -3}, {1, 1, -3}}
	pos1 = []Vec3{{2, 1, -3}, {2, 1, -3}, {2, 1, -3}}
	mesh = NewMesh([][3]int32{{0, 1, 2}}, pos0, pos1)
	b = NewBVH(mesh, 1.5)
	for _, tm := range []float64{0, 0.5, 1} {
		ray := Ray{Orig: Vec3{1, 1, 0}, Dir: Vec3{0, 0, -1}, Time: tm}
		if h, ok := b.Snapshot().Intersect(ray); ok {
			t.Fatalf("time=%v: false hit on point-degenerate triangle: %+v", tm, h)
		}
	}

	// Degenerate triangles mixed into a random mesh must not disturb
	// agreement with the per-face reference.
	rng := rand.New(rand.NewSource(3))
	mesh, vel := randomMesh(rng, 100)
	f := mesh.Frame()
	for i := 0; i < 10; i++ {
		f.Pos0[i*3+1] = f.Pos0[i*3]
		f.Pos0[i*3+2] = f.Pos0[i*3]
		f.Pos1[i*3+1] = f.Pos1[i*3]
		f.Pos1[i*3+2] = f.Pos1[i*3]
	}
	b = NewBVH(mesh, 1.2)
	times := []float64{0, 0.5, 1}
	for frame := 0; frame < 3; frame++ {
		if frame > 0 {
			advance(mesh, vel)
			b.Update()
		}
		checkAgainstReference(t, b.Snapshot(), rng, 300, times)
	}
}
