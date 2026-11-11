package bvhtrace

import "testing"

func approxEqual(a, b, eps float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestTriangleBasicHitAndBarycentric(t *testing.T) {
	tri := Triangle{
		A: Vec3{0, 0, 0},
		B: Vec3{1, 0, 0},
		C: Vec3{0, 1, 0},
	}
	ray := NewRay(Vec3{0.2, 0.3, -1}, Vec3{0, 0, 1}, 0, 0)
	tt, wa, wb, wc, hit := intersectTriangle(&ray, &tri)
	if !hit {
		t.Fatal("expected a hit")
	}
	if !approxEqual(tt, 1, 1e-6) {
		t.Fatalf("distance = %v, want 1", tt)
	}
	if !approxEqual(wa+wb+wc, 1, 1e-6) {
		t.Fatalf("barycentric sum = %v, want 1", wa+wb+wc)
	}
	hitPoint := tri.A.Scale(wa).Add(tri.B.Scale(wb)).Add(tri.C.Scale(wc))
	if !hitPoint.Equal(Vec3{0.2, 0.3, 0}) {
		t.Fatalf("interpolated point = %+v, want (0.2,0.3,0)", hitPoint)
	}
}

func TestTriangleMissAndInterval(t *testing.T) {
	tri := Triangle{
		A: Vec3{0, 0, 0},
		B: Vec3{1, 0, 0},
		C: Vec3{0, 1, 0},
	}
	miss := NewRay(Vec3{5, 5, -1}, Vec3{0, 0, 1}, 0, 0)
	if _, _, _, _, hit := intersectTriangle(&miss, &tri); hit {
		t.Fatal("ray outside the triangle must miss")
	}

	// Ray hits at t = 1; an interval ending before that must reject it.
	blocked := NewRay(Vec3{0.2, 0.3, -1}, Vec3{0, 0, 1}, 0, 0.5)
	if _, _, _, _, hit := intersectTriangle(&blocked, &tri); hit {
		t.Fatal("hit beyond Tmax must be rejected")
	}
	behind := NewRay(Vec3{0.2, 0.3, -1}, Vec3{0, 0, 1}, 2, 0)
	if _, _, _, _, hit := intersectTriangle(&behind, &tri); hit {
		t.Fatal("hit before Tmin must be rejected")
	}
}

// TestWatertightSharedEdge fires rays at every interior grid edge, including
// rays landing exactly on the edge and rays jittered to either side. Every
// ray must hit exactly one triangle: no leaks, and the surface must remain
// closed for shadow/occlusion queries as well.
func TestWatertightSharedEdge(t *testing.T) {
	const cells float32 = 32
	const size float32 = 8
	tris := GroundGrid(int(cells), size)
	bvh := Build(tris)

	step := 2 * size / cells
	edgeCount := 0
	leaks := 0
	exactEdgeHits := 0
	for i := float32(1); i < cells; i++ {
		x := -size + float32(i)*step
		for j := float32(0); j < cells; j++ {
			zMid := -size + (float32(j)+0.5)*step
			for _, dx := range []float32{0, -1e-6, 1e-6} {
				origin := Vec3{x + dx, -1, zMid}
				ray := NewRay(origin, Vec3{0, 1, 0}, 0, 0)
				if !bvh.Occluded(ray) {
					leaks++
				}
				hit, ok := bvh.Traverse(ray)
				if !ok {
					leaks++
					continue
				}
				edgeCount++
				if dx == 0 && approxEqual(ray.Origin.X, x, 0) {
					exactEdgeHits++
				}
				if hit.Primitive < 0 || hit.Primitive >= len(tris) {
					t.Fatalf("invalid primitive id %d", hit.Primitive)
				}
				if !approxEqual(hit.T, 1, 1e-5) {
					t.Fatalf("edge ray distance = %v, want 1", hit.T)
				}
			}
		}
	}
	if leaks != 0 {
		t.Fatalf("watertight mesh leaked on %d of %d edge rays", leaks, edgeCount)
	}
	if exactEdgeHits == 0 {
		t.Fatal("test configuration produced no exact shared-edge rays")
	}

	// Sweep a dense set of near-edge rays across the whole grid.
	sweepLeaks := 0
	for k := 0; k < 20000; k++ {
		// Stay strictly inside the grid: its outer perimeter is an open
		// boundary, not a shared edge between two triangles.
		x := -size + 2*size*(float32(k%257)+0.5)/257
		z := -size + 2*size*(float32((k*7)%251)+0.5)/251
		ray := NewRay(Vec3{x, -2, z}, Vec3{0, 1, 0}, 0, 0)
		if !bvh.Occluded(ray) {
			sweepLeaks++
		}
	}
	if sweepLeaks != 0 {
		t.Fatalf("dense sweep leaked on %d rays", sweepLeaks)
	}
}

func TestBVHMatchesBruteForce(t *testing.T) {
	tris := RandomScene(3000, 10, 42)
	bvh := Build(tris)
	rays := RandomRays(400, 30, 7)

	mismatches := 0
	for i := range rays {
		bvhHit, bvhOK := bvh.Traverse(rays[i])
		bfHit, bfOK := bvh.BruteForceTraverse(rays[i])
		if bvhOK != bfOK {
			t.Fatalf("ray %d: hit flag bvh=%v brute=%v", i, bvhOK, bfOK)
		}
		if !bvhOK {
			continue
		}
		if bvhHit.Primitive != bfHit.Primitive {
			mismatches++
			continue
		}
		eps := float32(1e-5)
		if bfHit.T > 1 {
			eps = 1e-5 * bfHit.T
		}
		if !approxEqual(bvhHit.T, bfHit.T, eps) {
			t.Fatalf("ray %d: distance bvh=%v brute=%v", i, bvhHit.T, bfHit.T)
		}
	}
	if mismatches != 0 {
		t.Fatalf("bvh/brute force disagree on closest triangle for %d rays", mismatches)
	}
}

func TestShadowQueries(t *testing.T) {
	tris := RandomScene(2000, 10, 99)
	bvh := Build(tris)
	rays := RandomRays(300, 30, 13)

	for i := range rays {
		if bvh.Occluded(rays[i]) != bvh.BruteForceOccluded(rays[i]) {
			t.Fatalf("ray %d: occlusion disagrees with brute force", i)
		}
		// Occluded must imply a closest-hit query also finds geometry.
		if bvh.Occluded(rays[i]) {
			if _, ok := bvh.Traverse(rays[i]); !ok {
				t.Fatalf("ray %d: occluded but no closest hit", i)
			}
		}
	}

	// A ray with a truncated interval entirely before the surface is unblocked.
	grid := Build(GroundGrid(8, 4))
	shortRay := NewRay(Vec3{0, -1, 0}, Vec3{0, 1, 0}, 0, 0.5)
	if grid.Occluded(shortRay) {
		t.Fatal("shadow ray ending before the surface must be unblocked")
	}
}

func TestEmptyAndDegenerateScene(t *testing.T) {
	empty := Build(nil)
	ray := NewRay(Vec3{}, Vec3{0, 1, 0}, 0, 0)
	if _, ok := empty.Traverse(ray); ok {
		t.Fatal("empty BVH must never report a hit")
	}
	if empty.Occluded(ray) {
		t.Fatal("empty BVH must never occlude")
	}

	single := Build([]Triangle{{Vec3{0, 0, 0}, Vec3{1, 0, 0}, Vec3{0, 1, 0}}})
	if single.NodeCount() != 1 {
		t.Fatalf("single triangle node count = %d, want 1", single.NodeCount())
	}
}

func TestConcurrentQueries(t *testing.T) {
	tris := RandomScene(4000, 10, 5)
	bvh := Build(tris)
	rays := RandomRays(200, 30, 9)

	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func(seed int) {
			defer func() { done <- struct{}{} }()
			for k := 0; k < 500; k++ {
				r := rays[(seed*500+k)%len(rays)]
				_, _ = bvh.Traverse(r)
				_ = bvh.Occluded(r)
			}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}
}
