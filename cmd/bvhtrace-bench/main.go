// Command bvhtrace-bench builds a SAH BVH over a procedural scene and
// compares it against brute-force ray/triangle intersection, reporting build
// time, query throughput, closest-hit agreement and shadow-ray agreement.
package main

import (
	"flag"
	"fmt"
	"time"

	"example.org/bvhtrace"
)

func main() {
	triCount := flag.Int("tris", 20000, "number of random triangles in the scene")
	rayCount := flag.Int("rays", 5000, "number of rays to trace")
	seed := flag.Uint64("seed", 42, "deterministic scene seed")
	flag.Parse()

	fmt.Printf("Scene: %d triangles, %d rays (seed %d)\n", *triCount, *rayCount, *seed)
	tris := bvhtrace.RandomScene(*triCount, 20, *seed)
	rays := bvhtrace.RandomRays(*rayCount, 60, *seed+1)

	buildStart := time.Now()
	bvh := bvhtrace.Build(tris)
	buildElapsed := time.Since(buildStart)
	fmt.Printf("\nBuild\n")
	fmt.Printf("  SAH BVH build : %v\n", buildElapsed)
	fmt.Printf("  nodes         : %d (%.2f tris/node leaf target %d)\n",
		bvh.NodeCount(), float64(*triCount)/float64(bvh.NodeCount()), 4)

	// BVH closest-hit pass.
	bvhStart := time.Now()
	bvhHits := 0
	for i := range rays {
		if _, ok := bvh.Traverse(rays[i]); ok {
			bvhHits++
		}
	}
	bvhElapsed := time.Since(bvhStart)

	// Brute-force closest-hit pass.
	bfStart := time.Now()
	bfHits := 0
	for i := range rays {
		if _, ok := bvh.BruteForceTraverse(rays[i]); ok {
			bfHits++
		}
	}
	bfElapsed := time.Since(bfStart)

	// Exact agreement of closest triangle id and distance on every ray.
	mismatches := 0
	maxTErr := float32(0)
	for i := range rays {
		bvhHit, bvhOK := bvh.Traverse(rays[i])
		bfHit, bfOK := bvh.BruteForceTraverse(rays[i])
		if bvhOK != bfOK {
			mismatches++
			continue
		}
		if !bvhOK {
			continue
		}
		if bvhHit.Primitive != bfHit.Primitive {
			mismatches++
			continue
		}
		d := bvhHit.T - bfHit.T
		if d < 0 {
			d = -d
		}
		if d > maxTErr {
			maxTErr = d
		}
	}

	fmt.Printf("\nClosest-hit queries\n")
	fmt.Printf("  BVH time      : %v  (%d hits, %.2f ns/ray)\n",
		bvhElapsed, bvhHits, float64(bvhElapsed.Nanoseconds())/float64(*rayCount))
	fmt.Printf("  brute force   : %v  (%d hits, %.2f ns/ray)\n",
		bfElapsed, bfHits, float64(bfElapsed.Nanoseconds())/float64(*rayCount))
	fmt.Printf("  speedup       : %.1fx\n", float64(bfElapsed.Nanoseconds())/float64(bvhElapsed.Nanoseconds()))
	fmt.Printf("  disagreements : %d, max |t| error = %g\n", mismatches, maxTErr)

	// Shadow/occlusion any-hit queries.
	shadowRays := rays
	shStart := time.Now()
	shadowBlocked := 0
	for i := range shadowRays {
		if bvh.Occluded(shadowRays[i]) {
			shadowBlocked++
		}
	}
	shElapsed := time.Since(shStart)

	shadowMismatch := 0
	for i := range shadowRays {
		if bvh.Occluded(shadowRays[i]) != bvh.BruteForceOccluded(shadowRays[i]) {
			shadowMismatch++
		}
	}
	fmt.Printf("\nShadow (any-hit) queries\n")
	fmt.Printf("  BVH time      : %v  (%d blocked, %.2f ns/ray)\n",
		shElapsed, shadowBlocked, float64(shElapsed.Nanoseconds())/float64(len(shadowRays)))
	fmt.Printf("  disagreements : %d\n", shadowMismatch)

	// Watertight shared-edge stress test on a subdivided ground plane.
	grid := bvhtrace.GroundGrid(64, 8)
	gridBVH := bvhtrace.Build(grid)
	leaks := 0
	for i := 0; i < 100000; i++ {
		x := -8 + 16*(float32(i%997)+0.5)/997
		z := -8 + 16*(float32((i*13)%991)+0.5)/991
		ray := bvhtrace.NewRay(bvhtrace.Vec3{X: x, Y: -1, Z: z}, bvhtrace.Vec3{Y: 1}, 0, 0)
		if !gridBVH.Occluded(ray) {
			leaks++
		}
	}
	fmt.Printf("\nWatertight edges\n")
	fmt.Printf("  100k near/shared-edge rays: %d leaks\n", leaks)

	if mismatches != 0 || shadowMismatch != 0 || leaks != 0 {
		fmt.Printf("\nRESULT: FAIL\n")
	} else {
		fmt.Printf("\nRESULT: all BVH results match brute force; no edge leaks\n")
	}
}
