package bvhtrace

import "math/rand/v2"

// GroundGrid builds a subdivided quad at y = 0 spanning [-size, size] on the
// x/z axes. Neighboring cells share the exact same vertex values, which is the
// condition required for watertight shared-edge intersection.
func GroundGrid(cells int, size float32) []Triangle {
	if cells < 1 {
		cells = 1
	}
	step := 2 * size / float32(cells)
	verts := make([][]Vec3, cells+1)
	for i := 0; i <= cells; i++ {
		verts[i] = make([]Vec3, cells+1)
		for j := 0; j <= cells; j++ {
			verts[i][j] = Vec3{-size + float32(i)*step, 0, -size + float32(j)*step}
		}
	}
	tris := make([]Triangle, 0, 2*cells*cells)
	for i := 0; i < cells; i++ {
		for j := 0; j < cells; j++ {
			v00 := verts[i][j]
			v10 := verts[i+1][j]
			v11 := verts[i+1][j+1]
			v01 := verts[i][j+1]
			tris = append(tris, Triangle{v00, v10, v11}, Triangle{v00, v11, v01})
		}
	}
	return tris
}

// RandomScene builds count non-degenerate triangles with uniformly random
// vertex positions in the given cuboid. It is deterministic for a fixed seed.
func RandomScene(count int, extent float32, seed uint64) []Triangle {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	tris := make([]Triangle, count)
	for i := range tris {
		for {
			a := randomVec3(rng, extent)
			b := randomVec3(rng, extent)
			c := randomVec3(rng, extent)
			if !b.Sub(a).Cross(c.Sub(a)).Equal(Vec3{}) {
				tris[i] = Triangle{a, b, c}
				break
			}
		}
	}
	return tris
}

func randomVec3(rng *rand.Rand, extent float32) Vec3 {
	return Vec3{
		(rng.Float32()*2 - 1) * extent,
		(rng.Float32()*2 - 1) * extent,
		(rng.Float32()*2 - 1) * extent,
	}
}

// Equal reports whether two vectors are component-wise identical.
func (v Vec3) Equal(o Vec3) bool {
	return v.X == o.X && v.Y == o.Y && v.Z == o.Z
}

// RandomRays produces count deterministic rays with origins on a sphere shell
// and inward-facing directions, suitable for query benchmarks.
func RandomRays(count int, originRadius float32, seed uint64) []Ray {
	rng := rand.New(rand.NewPCG(seed^0xdeadbeef, seed))
	rays := make([]Ray, count)
	for i := range rays {
		origin := Vec3{
			(rng.Float32()*2 - 1) * originRadius,
			(rng.Float32()*2 - 1) * originRadius,
			(rng.Float32()*2 - 1) * originRadius,
		}
		target := Vec3{
			(rng.Float32()*2 - 1) * originRadius * 0.25,
			(rng.Float32()*2 - 1) * originRadius * 0.25,
			(rng.Float32()*2 - 1) * originRadius * 0.25,
		}
		rays[i] = NewRay(origin, target.Sub(origin).Normalize(), 0, 0)
	}
	return rays
}
