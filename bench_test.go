package bvhtrace

import "testing"

func benchmarkScene() ([]Triangle, []Ray) {
	tris := RandomScene(20000, 20, 123)
	rays := RandomRays(2000, 60, 456)
	return tris, rays
}

func BenchmarkBuild(b *testing.B) {
	tris, _ := benchmarkScene()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Build(tris)
	}
}

func BenchmarkBVHTraverse(b *testing.B) {
	tris, rays := benchmarkScene()
	bvh := Build(tris)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range rays {
			_, _ = bvh.Traverse(rays[j])
		}
	}
}

func BenchmarkBVHOccluded(b *testing.B) {
	tris, rays := benchmarkScene()
	bvh := Build(tris)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range rays {
			_ = bvh.Occluded(rays[j])
		}
	}
}

func BenchmarkBruteForceTraverse(b *testing.B) {
	tris, rays := benchmarkScene()
	bvh := Build(tris)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range rays {
			_, _ = bvh.BruteForceTraverse(rays[j])
		}
	}
}

func BenchmarkBruteForceOccluded(b *testing.B) {
	tris, rays := benchmarkScene()
	bvh := Build(tris)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range rays {
			_ = bvh.BruteForceOccluded(rays[j])
		}
	}
}
