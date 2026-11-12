package taskpkg

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

type motionInfo struct {
	affine   Affine3
	normal   Mat3
	valid    bool
	negative bool
}

type sourceHit struct {
	target int
	weight float64
	depth  float64
	object ObjectID
	gen    uint32
	src    int
	color  RGBA
}

type tileStats struct {
	missing   int
	alphaZero int
	collision int
}

// Reproject resolves the previous frame into the current frame's geometry.
//
// The algorithm is a deterministic forward scatter:
//
//  1. Every previous pixel center is unprojected using its previous camera and
//     camera-space depth.
//  2. The object's previous-to-current affine transform is applied in world
//     space, then the point is projected through the current camera.
//  3. Its history color is distributed over the four current pixels of its
//     bilinear footprint.
//  4. A destination accepts the contribution only when its current object ID
//     and generation match, its current depth is valid, the camera-space depth
//     error is within the configured tolerance, and the transformed previous
//     normal is within the configured angle of the current normal.
//  5. Collisions are ordered by current camera depth, then object ID, then
//     generation, then source index. Contributions on the frontmost surface
//     are weight-normalized.
//  6. The normalized history is clamped to the current pixel's same-object
//     3x3 neighborhood. The current pixel is then blended with the clamped
//     history using CurrentWeight. A pixel with no usable history takes the
//     current color exactly.
//
// Missing motion entries are treated as identity transforms, which is the
// correct previous-to-current transform for static world geometry. Non-affine
// and singular matrices are rejected; a negative determinant is a negative/
// mirrored scale and is rejected explicitly.
//
// Neither previous nor current is mutated. The returned frame owns fresh
// storage, so it is safe to publish as the next history only after the caller
// considers the frame complete.
func Reproject(previous, current Frame, motions map[ObjectID]Mat4, cfg Config) (Result, error) {
	if err := previous.Validate(); err != nil {
		return Result{}, fmt.Errorf("previous frame: %w", err)
	}
	if err := current.Validate(); err != nil {
		return Result{}, fmt.Errorf("current frame: %w", err)
	}
	if previous.Width != current.Width || previous.Height != current.Height {
		return Result{}, fmt.Errorf("%w: previous %dx%d, current %dx%d",
			ErrDimensionMismatch, previous.Width, previous.Height,
			current.Width, current.Height)
	}
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return Result{}, err
	}
	if cfg.TilesX > current.Width {
		cfg.TilesX = current.Width
	}
	if cfg.TilesY > current.Height {
		cfg.TilesY = current.Height
	}

	width, height := current.Width, current.Height
	pixelCount := width * height
	motionTable := prepareMotions(motions)
	tileXOf := make([]int, width)
	for tx := 0; tx < cfg.TilesX; tx++ {
		x0 := tx * width / cfg.TilesX
		x1 := (tx + 1) * width / cfg.TilesX
		for x := x0; x < x1; x++ {
			tileXOf[x] = tx
		}
	}
	tileYOf := make([]int, height)
	for ty := 0; ty < cfg.TilesY; ty++ {
		y0 := ty * height / cfg.TilesY
		y1 := (ty + 1) * height / cfg.TilesY
		for y := y0; y < y1; y++ {
			tileYOf[y] = ty
		}
	}

	tileCount := cfg.TilesX * cfg.TilesY
	hits := make([][]sourceHit, tileCount)
	var stats ReprojectionStats
	normalCheck := cfg.MaxNormalAngle >= 0
	var cosNormalLimit float64
	if normalCheck {
		cosNormalLimit = math.Cos(cfg.MaxNormalAngle)
	}

	prevInvVP, ok := previous.Camera.InverseViewProjection()
	if !ok {
		return Result{}, fmt.Errorf("%w: previous view-projection is singular", ErrInvalidCamera)
	}
	curVP := current.Camera.ViewProjection()

	for y := 0; y < previous.Height; y++ {
		for x := 0; x < previous.Width; x++ {
			src := y*previous.Width + x
			stats.SourcePixels++

			srcColor := previous.Color[src]
			if !srcColor.Finite() {
				stats.SourceInvalidColor++
				continue
			}
			if !(srcColor.A > cfg.AlphaEpsilon) {
				stats.SourceAlphaZero++
				continue
			}
			srcDepth := previous.Depth[src]
			if !isFinite(srcDepth) || srcDepth <= 0 {
				stats.SourceInvalidDepth++
				continue
			}
			if srcDepth <= previous.Camera.Near+cfg.NearEpsilon {
				stats.SourceNearPlane++
				continue
			}

			object := previous.ObjectIDs[src]
			info, found := motionTable[object]
			if !found {
				info = identityMotion()
			}
			if !info.valid {
				if info.negative {
					stats.SourceNegativeScale++
				} else {
					stats.SourceInvalidTransform++
				}
				continue
			}

			var srcNormal Vec3
			if normalCheck {
				n := previous.Normals[src]
				if !n.Finite() || n.Length() <= 1e-15 {
					stats.SourceInvalidNormal++
					continue
				}
				srcNormal = info.normal.MulVec(n)
				var normalized bool
				srcNormal, normalized = srcNormal.Normalized()
				if !normalized {
					stats.SourceInvalidNormal++
					continue
				}
			}

			world, ok := unprojectPixelWithInverse(previous.Camera, prevInvVP, x, y, width, height, srcDepth)
			if !ok {
				stats.SourceBackprojectFailed++
				continue
			}
			world = info.affine.TransformPoint(world)
			if !world.Finite() {
				stats.SourceForwardProjectFail++
				continue
			}

			// Full projection is used for pixel coordinates; the split
			// view/projection multiplication below gives a robust camera depth.
			view := current.Camera.View.MulVec(Vec4{X: world.X, Y: world.Y, Z: world.Z, W: 1})
			candidateDepth := -view.Z
			if !isFinite(candidateDepth) || candidateDepth <= current.Camera.Near+cfg.NearEpsilon {
				stats.SourceNearPlane++
				continue
			}
			clip := curVP.MulVec(Vec4{X: world.X, Y: world.Y, Z: world.Z, W: 1})
			if !isFinite(clip.X) || !isFinite(clip.Y) || !isFinite(clip.W) || clip.W <= 1e-15 {
				stats.SourceForwardProjectFail++
				continue
			}
			ndcX := clip.X / clip.W
			ndcY := clip.Y / clip.W
			if !isFinite(ndcX) || !isFinite(ndcY) {
				stats.SourceForwardProjectFail++
				continue
			}
			destX := ((ndcX + 1.0) * 0.5 * float64(width)) - 0.5
			destY := ((1.0 - ndcY) * 0.5 * float64(height)) - 0.5
			if !isFinite(destX) || !isFinite(destY) {
				stats.SourceForwardProjectFail++
				continue
			}

			ix := int(math.Floor(destX))
			iy := int(math.Floor(destY))
			fx := destX - float64(ix)
			fy := destY - float64(iy)
			for oy := 0; oy <= 1; oy++ {
				wy := fy
				if oy == 0 {
					wy = 1.0 - fy
				}
				ty := iy + oy
				if ty < 0 || ty >= height {
					stats.RejectedOutOfBounds++
					continue
				}
				for ox := 0; ox <= 1; ox++ {
					wx := fx
					if ox == 0 {
						wx = 1.0 - fx
					}
					tx := ix + ox
					weight := wx * wy
					if weight <= 1e-15 {
						continue
					}
					if tx < 0 || tx >= width {
						stats.RejectedOutOfBounds++
						continue
					}
					target := ty*width + tx
					curColor := current.Color[target]
					if !(curColor.A > cfg.AlphaEpsilon) {
						stats.RejectedTargetAlphaZero++
						continue
					}
					if !curColor.Finite() {
						stats.RejectedTargetColor++
						continue
					}
					if current.ObjectIDs[target] != object ||
						current.Generations[target] != previous.Generations[src] {
						stats.RejectedObjectGeneration++
						continue
					}
					curDepth := current.Depth[target]
					if !isFinite(curDepth) || curDepth <= current.Camera.Near+cfg.NearEpsilon {
						stats.RejectedTargetDepth++
						continue
					}
					allowed := cfg.DepthTolerance + cfg.RelativeDepthTolerance*math.Abs(curDepth)
					if math.Abs(candidateDepth-curDepth) > allowed {
						stats.RejectedDepthError++
						continue
					}
					if normalCheck {
						curNormal := current.Normals[target]
						if !curNormal.Finite() || curNormal.Length() <= 1e-15 {
							stats.RejectedNormalAngle++
							continue
						}
						curNormal, _ = curNormal.Normalized()
						if srcNormal.Dot(curNormal) < cosNormalLimit-1e-9 {
							stats.RejectedNormalAngle++
							continue
						}
					}

					ti := tileYOf[ty]*cfg.TilesX + tileXOf[tx]
					hits[ti] = append(hits[ti], sourceHit{
						target: target,
						weight: weight,
						depth:  candidateDepth,
						object: object,
						gen:    previous.Generations[src],
						src:    src,
						color:  srcColor,
					})
					stats.AcceptedContributions++
				}
			}
		}
	}

	outColor := make([]RGBA, pixelCount)
	outHistory := make([]RGBA, pixelCount)
	outWeight := make([]float64, pixelCount)
	perTile := make([]tileStats, tileCount)
	processTile := func(ti int) {
		tx := ti % cfg.TilesX
		ty := ti / cfg.TilesX
		x0 := tx * width / cfg.TilesX
		x1 := (tx + 1) * width / cfg.TilesX
		y0 := ty * height / cfg.TilesY
		y1 := (ty + 1) * height / cfg.TilesY
		if x0 >= x1 || y0 >= y1 {
			return
		}
		tileW := x1 - x0
		buckets := make([][]sourceHit, tileW*(y1-y0))
		for _, hit := range hits[ti] {
			hx := hit.target % width
			hy := hit.target / width
			local := (hy-y0)*tileW + (hx - x0)
			buckets[local] = append(buckets[local], hit)
		}
		var local tileStats
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				idx := y*width + x
				cur := current.Color[idx]
				if !(cur.A > cfg.AlphaEpsilon) {
					outColor[idx] = RGBA{}
					outHistory[idx] = RGBA{}
					outWeight[idx] = 0
					local.alphaZero++
					continue
				}
				localIdx := (y-y0)*tileW + (x - x0)
				candidates := buckets[localIdx]
				if len(candidates) == 0 || !cur.Finite() {
					outColor[idx] = cur
					outHistory[idx] = cur
					outWeight[idx] = 0
					local.missing++
					continue
				}

				sort.SliceStable(candidates, func(i, j int) bool {
					a, b := candidates[i], candidates[j]
					if a.depth != b.depth {
						return a.depth < b.depth
					}
					if a.object != b.object {
						return a.object < b.object
					}
					if a.gen != b.gen {
						return a.gen < b.gen
					}
					return a.src < b.src
				})
				anchor := candidates[0]
				allowed := cfg.DepthTolerance + cfg.RelativeDepthTolerance*math.Abs(current.Depth[idx])
				var sumW float64
				var sum RGBA
				for _, c := range candidates {
					// The sorted frontmost surface wins collisions. Only
					// candidates on that surface may contribute; a rear
					// layer is never blended into the front layer.
					if c.object != anchor.object || c.gen != anchor.gen ||
						c.depth > anchor.depth+allowed {
						local.collision++
						continue
					}
					sumW += c.weight
					sum.R += c.color.R * c.weight
					sum.G += c.color.G * c.weight
					sum.B += c.color.B * c.weight
					sum.A += c.color.A * c.weight
				}
				if sumW <= 1e-15 {
					outColor[idx] = cur
					outHistory[idx] = cur
					outWeight[idx] = 0
					local.missing++
					continue
				}
				hist := RGBA{
					R: sum.R / sumW,
					G: sum.G / sumW,
					B: sum.B / sumW,
					A: sum.A / sumW,
				}
				lo, hi := sameObjectNeighborhood(current, x, y, idx, cfg.AlphaEpsilon)
				hist = clampColor(hist, lo, hi)
				outHistory[idx] = hist
				outWeight[idx] = sumW
				denom := sumW + cfg.CurrentWeight
				t := 1.0
				if denom > 0 {
					t = sumW / denom
				}
				outColor[idx] = RGBA{
					R: cur.R + (hist.R-cur.R)*t,
					G: cur.G + (hist.G-cur.G)*t,
					B: cur.B + (hist.B-cur.B)*t,
					A: cur.A + (hist.A-cur.A)*t,
				}
			}
		}
		perTile[ti] = local
	}

	if tileCount == 1 {
		processTile(0)
	} else {
		workers := cfg.Workers
		if workers > tileCount {
			workers = tileCount
		}
		if workers < 1 {
			workers = 1
		}
		jobs := make(chan int)
		var wg sync.WaitGroup
		wg.Add(workers)
		for worker := 0; worker < workers; worker++ {
			go func() {
				defer wg.Done()
				for ti := range jobs {
					processTile(ti)
				}
			}()
		}
		for ti := 0; ti < tileCount; ti++ {
			jobs <- ti
		}
		close(jobs)
		wg.Wait()
	}
	for _, local := range perTile {
		stats.PixelsWithoutHistory += local.missing
		stats.PixelsResolvedToZeroAlpha += local.alphaZero
		stats.DroppedByCollisionOrder += local.collision
	}

	outFrame := Frame{
		Width:       current.Width,
		Height:      current.Height,
		Camera:      current.Camera,
		Depth:       append([]float64(nil), current.Depth...),
		Color:       outColor,
		Normals:     append([]Vec3(nil), current.Normals...),
		ObjectIDs:   append([]ObjectID(nil), current.ObjectIDs...),
		Generations: append([]uint32(nil), current.Generations...),
	}
	return Result{
		Frame:         outFrame,
		HistoryColor:  outHistory,
		HistoryWeight: outWeight,
		Stats:         stats,
	}, nil
}

func prepareMotions(motions map[ObjectID]Mat4) map[ObjectID]motionInfo {
	out := make(map[ObjectID]motionInfo, len(motions))
	for id, matrix := range motions {
		affine, ok := Affine3FromMat4(matrix)
		if !ok {
			out[id] = motionInfo{}
			continue
		}
		det := affine.Determinant()
		if !isFinite(det) || math.Abs(det) <= 1e-15 {
			out[id] = motionInfo{}
			continue
		}
		if det < 0 {
			out[id] = motionInfo{negative: true}
			continue
		}
		normal, ok := affine.NormalMatrix()
		if !ok {
			out[id] = motionInfo{}
			continue
		}
		out[id] = motionInfo{affine: affine, normal: normal, valid: true}
	}
	return out
}

func identityMotion() motionInfo {
	return motionInfo{
		affine: Affine3{Linear: Identity3()},
		normal: Identity3(),
		valid:  true,
	}
}

func unprojectPixelWithInverse(cam Camera, invVP Mat4, x, y, width, height int, depth float64) (Vec3, bool) {
	ndcZ, ok := cam.ndcDepthAt(depth)
	if !ok {
		return Vec3{}, false
	}
	ndcX := (2.0*(float64(x)+0.5))/float64(width) - 1.0
	ndcY := 1.0 - (2.0*(float64(y)+0.5))/float64(height)
	p := invVP.MulVec(Vec4{X: ndcX, Y: ndcY, Z: ndcZ, W: 1})
	if !isFinite(p.W) || math.Abs(p.W) <= 1e-15 {
		return Vec3{}, false
	}
	world := Vec3{X: p.X / p.W, Y: p.Y / p.W, Z: p.Z / p.W}
	if !world.Finite() {
		return Vec3{}, false
	}
	view := cam.View.MulVec(Vec4{X: world.X, Y: world.Y, Z: world.Z, W: 1})
	if math.Abs(-view.Z-depth) > 1e-6*math.Max(1, math.Abs(depth)) {
		return Vec3{}, false
	}
	return world, true
}

func sameObjectNeighborhood(f Frame, x, y, center int, alphaEpsilon float64) (RGBA, RGBA) {
	cur := f.Color[center]
	lo, hi := cur, cur
	id := f.ObjectIDs[center]
	gen := f.Generations[center]
	for dy := -1; dy <= 1; dy++ {
		yy := y + dy
		if yy < 0 || yy >= f.Height {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			xx := x + dx
			if xx < 0 || xx >= f.Width {
				continue
			}
			idx := yy*f.Width + xx
			if f.ObjectIDs[idx] != id || f.Generations[idx] != gen {
				continue
			}
			c := f.Color[idx]
			if !c.Finite() {
				continue
			}
			if idx != center && !(c.A > alphaEpsilon) {
				continue
			}
			lo.R = math.Min(lo.R, c.R)
			lo.G = math.Min(lo.G, c.G)
			lo.B = math.Min(lo.B, c.B)
			lo.A = math.Min(lo.A, c.A)
			hi.R = math.Max(hi.R, c.R)
			hi.G = math.Max(hi.G, c.G)
			hi.B = math.Max(hi.B, c.B)
			hi.A = math.Max(hi.A, c.A)
		}
	}
	return lo, hi
}

func clampColor(c, lo, hi RGBA) RGBA {
	return RGBA{
		R: math.Min(math.Max(c.R, lo.R), hi.R),
		G: math.Min(math.Max(c.G, lo.G), hi.G),
		B: math.Min(math.Max(c.B, lo.B), hi.B),
		A: math.Min(math.Max(c.A, lo.A), hi.A),
	}
}
