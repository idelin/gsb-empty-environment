package bvh

import "math/rand"

// makeScene 生成确定性场景：nClusters 个星团散布在大空间中，
// 每个星团内含若干随机朝向的三角形，共约 nTris 个。
// 返回图元列表与星团中心（供构造瞄准射线）。
func makeScene(seed int64, nClusters, nTris int) ([]Primitive, []Vec3) {
	rng := rand.New(rand.NewSource(seed))
	per := nTris / nClusters
	prims := make([]Primitive, 0, nTris)
	centers := make([]Vec3, 0, nClusters)
	for c := 0; c < nClusters; c++ {
		center := Vec3{
			(rng.Float64()*2 - 1) * 60,
			(rng.Float64()*2 - 1) * 60,
			(rng.Float64()*2 - 1) * 60,
		}
		centers = append(centers, center)
		for i := 0; i < per; i++ {
			v0 := Vec3{
				center[0] + (rng.Float64()*2-1)*4,
				center[1] + (rng.Float64()*2-1)*4,
				center[2] + (rng.Float64()*2-1)*4,
			}
			v1 := Vec3{v0[0] + rng.Float64()*2, v0[1] + rng.Float64()*2, v0[2] + rng.Float64()*2}
			v2 := Vec3{v0[0] + rng.Float64()*2, v0[1] + rng.Float64()*2, v0[2] + rng.Float64()*2}
			prims = append(prims, Triangle{V0: v0, V1: v1, V2: v2})
		}
	}
	return prims, centers
}

// makeRays 生成确定性射线：约 2/3 瞄准星团中心（带抖动，保证命中密集区），
// 其余指向随机方向（覆盖未命中情形）。
func makeRays(seed int64, n int, centers []Vec3) []Ray {
	rng := rand.New(rand.NewSource(seed))
	rays := make([]Ray, 0, n)
	for i := 0; i < n; i++ {
		orig := Vec3{
			(rng.Float64()*2 - 1) * 90,
			(rng.Float64()*2 - 1) * 90,
			(rng.Float64()*2 - 1) * 90,
		}
		var target Vec3
		if i%3 != 0 && len(centers) > 0 {
			c := centers[rng.Intn(len(centers))]
			target = Vec3{
				c[0] + (rng.Float64()*2-1)*2,
				c[1] + (rng.Float64()*2-1)*2,
				c[2] + (rng.Float64()*2-1)*2,
			}
		} else {
			target = Vec3{
				(rng.Float64()*2 - 1) * 50,
				(rng.Float64()*2 - 1) * 50,
				(rng.Float64()*2 - 1) * 50,
			}
		}
		rays = append(rays, Ray{Orig: orig, Dir: target.Sub(orig).Normalize()})
	}
	return rays
}
