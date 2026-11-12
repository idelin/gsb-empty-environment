package taskpkg

import (
	"fmt"
	"math"
)

type instance struct {
	id           uint64
	mesh         *Mesh
	transform    Affine
	inverse      Affine
	normalMatrix Mat3
	detSign      int
	worldBounds  AABB
}

// InstanceInfo is a read-only snapshot of a scene instance.
type InstanceInfo struct {
	ID          uint64
	Mesh        *Mesh
	Transform   Affine
	WorldBounds AABB
}

// Scene contains shared triangle meshes instanced by invertible affine
// transforms. Its two-level acceleration structure consists of one world-space
// TLAS over instances and one shared local-space BLAS per Mesh.
type Scene struct {
	instances []*instance
	byID      map[uint64]int
	nextID    uint64
	tree      bvh
}

func NewScene() *Scene {
	return &Scene{
		byID:   make(map[uint64]int),
		nextID: 1,
	}
}

// AddInstance adds a shared mesh with an automatically assigned instance ID.
func (s *Scene) AddInstance(mesh *Mesh, transform Affine) (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("nil scene")
	}
	id := s.nextID
	for {
		if _, exists := s.byID[id]; !exists {
			break
		}
		id++
	}
	if err := s.AddInstanceWithID(id, mesh, transform); err != nil {
		return 0, err
	}
	if id >= s.nextID {
		s.nextID = id + 1
	}
	return id, nil
}

// AddInstanceWithID adds a shared mesh with an explicit stable ID. Instance IDs
// must be unique and are part of the deterministic same-distance ordering.
func (s *Scene) AddInstanceWithID(id uint64, mesh *Mesh, transform Affine) error {
	if s == nil {
		return fmt.Errorf("nil scene")
	}
	if mesh == nil {
		return fmt.Errorf("instance %d has a nil mesh", id)
	}
	if _, exists := s.byID[id]; exists {
		return fmt.Errorf("instance ID %d already exists", id)
	}
	in, err := makeInstance(id, mesh, transform)
	if err != nil {
		return err
	}
	s.byID[id] = len(s.instances)
	s.instances = append(s.instances, in)
	s.rebuildTLAS()
	return nil
}

func makeInstance(id uint64, mesh *Mesh, transform Affine) (*instance, error) {
	if !transform.IsFinite() {
		return nil, fmt.Errorf("instance %d transform is not finite", id)
	}
	inverse, ok := transform.Inverse()
	if !ok {
		return nil, fmt.Errorf("instance %d transform is not invertible", id)
	}
	detSign := transform.DeterminantSign()
	if detSign == 0 {
		return nil, fmt.Errorf("instance %d transform is singular", id)
	}
	normalMatrix := inverse.Linear.Transpose()
	worldBounds := transformAABB(mesh.Bounds(), transform)
	if !worldBounds.IsEmpty() &&
		(!worldBounds.Min.IsFinite() || !worldBounds.Max.IsFinite()) {
		return nil, fmt.Errorf("instance %d world bounds are not finite", id)
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if !isFinite(normalMatrix[i][j]) {
				return nil, fmt.Errorf("instance %d normal transform is not finite", id)
			}
		}
	}
	return &instance{
		id:           id,
		mesh:         mesh,
		transform:    transform,
		inverse:      inverse,
		normalMatrix: normalMatrix,
		detSign:      detSign,
		worldBounds:  worldBounds,
	}, nil
}

// UpdateInstance changes an instance transform and refits only the world-space
// TLAS. The shared mesh BLAS is not rebuilt. The transform must remain
// invertible.
func (s *Scene) UpdateInstance(id uint64, transform Affine) error {
	if s == nil {
		return fmt.Errorf("nil scene")
	}
	index, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("instance ID %d does not exist", id)
	}
	old := s.instances[index]
	updated, err := makeInstance(id, old.mesh, transform)
	if err != nil {
		return err
	}
	s.instances[index] = updated
	refitBVH(&s.tree, func(prim uint32) AABB {
		return s.instances[prim].worldBounds
	})
	return nil
}

// SetInstanceTransform is an alias for UpdateInstance.
func (s *Scene) SetInstanceTransform(id uint64, transform Affine) error {
	return s.UpdateInstance(id, transform)
}

func (s *Scene) InstanceTransform(id uint64) (Affine, bool) {
	if s == nil {
		return Affine{}, false
	}
	index, ok := s.byID[id]
	if !ok {
		return Affine{}, false
	}
	return s.instances[index].transform, true
}

func (s *Scene) InstanceMesh(id uint64) (*Mesh, bool) {
	if s == nil {
		return nil, false
	}
	index, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	return s.instances[index].mesh, true
}

func (s *Scene) InstanceWorldBounds(id uint64) (AABB, bool) {
	if s == nil {
		return EmptyAABB(), false
	}
	index, ok := s.byID[id]
	if !ok {
		return EmptyAABB(), false
	}
	return s.instances[index].worldBounds, true
}

func (s *Scene) InstanceInfo(id uint64) (InstanceInfo, bool) {
	if s == nil {
		return InstanceInfo{}, false
	}
	index, ok := s.byID[id]
	if !ok {
		return InstanceInfo{}, false
	}
	in := s.instances[index]
	return InstanceInfo{
		ID:          in.id,
		Mesh:        in.mesh,
		Transform:   in.transform,
		WorldBounds: in.worldBounds,
	}, true
}

func (s *Scene) InstanceCount() int {
	if s == nil {
		return 0
	}
	return len(s.instances)
}

// Rebuild reconstructs the world-space TLAS from current instance bounds.
// Transform-only updates use refit instead and never touch mesh BLASes.
func (s *Scene) Rebuild() {
	if s == nil {
		return
	}
	s.rebuildTLAS()
}

func (s *Scene) rebuildTLAS() {
	validInstances := make([]uint32, 0, len(s.instances))
	bounds := make([]AABB, 0, len(s.instances))
	for i, in := range s.instances {
		if in.worldBounds.IsEmpty() {
			continue
		}
		validInstances = append(validInstances, uint32(i))
		bounds = append(bounds, in.worldBounds)
	}
	s.tree = buildBVH(bounds, 2)
	for i, prim := range s.tree.prims {
		s.tree.prims[i] = validInstances[prim]
	}
}

// Pick returns the nearest hit using default options.
func (s *Scene) Pick(ray Ray) (Hit, bool) {
	return s.PickWithOptions(ray, PickOptions{})
}

// PickWithOptions returns the nearest hit. Candidates are ranked by world ray
// parameter, then by (InstanceID, TriangleID) for equal world distances.
func (s *Scene) PickWithOptions(ray Ray, options PickOptions) (Hit, bool) {
	if s == nil || len(s.tree.nodes) == 0 {
		return Hit{}, false
	}
	options = options.normalized()
	if !validRay(ray) {
		return Hit{}, false
	}
	invDirection := invDir(ray.Direction)
	best := Hit{}
	found := false

	var stack []int32
	stack = append(stack, 0)
	for len(stack) != 0 {
		nodeIndex := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := &s.tree.nodes[nodeIndex]

		limit := options.MaxT
		if found {
			limit = math.Min(limit, best.T+options.tieTolerance(best.T))
		}
		tEnter, tExit, ok := node.bounds.IntersectRay(ray.Origin, invDirection, options.TMin, limit)
		if !ok || tExit < options.TMin || tEnter > limit {
			continue
		}

		if node.isLeaf() {
			start := int(node.start)
			end := start + int(node.count)
			for k := start; k < end; k++ {
				instanceIndex := s.tree.prims[k]
				candidate, hit := s.intersectInstance(ray, s.instances[instanceIndex], options, found, best)
				if hit && (!found || hitBetter(candidate, best, options.tieTolerance)) {
					best = candidate
					found = true
				}
			}
			continue
		}
		stack = append(stack, node.right, node.left)
	}
	return best, found
}

// PickAll returns every hit in world-parameter order. Equal parameters are
// ordered by (InstanceID, TriangleID).
func (s *Scene) PickAll(ray Ray, options PickOptions) []Hit {
	if s == nil || len(s.tree.nodes) == 0 {
		return nil
	}
	options = options.normalized()
	if !validRay(ray) {
		return nil
	}
	invDirection := invDir(ray.Direction)
	hits := make([]Hit, 0)

	var stack []int32
	stack = append(stack, 0)
	for len(stack) != 0 {
		nodeIndex := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := &s.tree.nodes[nodeIndex]
		_, _, ok := node.bounds.IntersectRay(ray.Origin, invDirection, options.TMin, options.MaxT)
		if !ok {
			continue
		}
		if node.isLeaf() {
			start := int(node.start)
			end := start + int(node.count)
			for k := start; k < end; k++ {
				instanceIndex := s.tree.prims[k]
				instanceHits := s.intersectInstanceAll(ray, s.instances[instanceIndex], options)
				hits = append(hits, instanceHits...)
			}
			continue
		}
		stack = append(stack, node.right, node.left)
	}
	sortHits(hits)
	return hits
}

func validRay(ray Ray) bool {
	return ray.Origin.IsFinite() &&
		ray.Direction.IsFinite() &&
		(ray.Direction.X != 0 || ray.Direction.Y != 0 || ray.Direction.Z != 0)
}

func (s *Scene) intersectInstance(ray Ray, in *instance, options PickOptions, haveBest bool, best Hit) (Hit, bool) {
	if in == nil || in.mesh == nil || len(in.mesh.tree.nodes) == 0 {
		return Hit{}, false
	}
	localRay := Ray{
		Origin:    in.inverse.TransformPoint(ray.Origin),
		Direction: in.inverse.TransformVector(ray.Direction),
	}
	if !validRay(localRay) {
		return Hit{}, false
	}
	invLocalDirection := invDir(localRay.Direction)
	limit := options.MaxT
	if haveBest {
		limit = math.Min(limit, best.T+options.tieTolerance(best.T))
	}

	var stack []int32
	stack = append(stack, 0)
	var instanceBest Hit
	found := false
	for len(stack) != 0 {
		nodeIndex := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := &in.mesh.tree.nodes[nodeIndex]
		tEnter, tExit, ok := node.bounds.IntersectRay(localRay.Origin, invLocalDirection, options.TMin, limit)
		if !ok || tExit < options.TMin || tEnter > limit {
			continue
		}
		if node.isLeaf() {
			start := int(node.start)
			end := start + int(node.count)
			for k := start; k < end; k++ {
				triangleID := in.mesh.tree.prims[k]
				triangle := in.mesh.triangles[triangleID]
				localT, u, v, front, ok := intersectTriangleLocal(triangle, localRay)
				if !ok {
					continue
				}
				if options.CullBackfaces && !front {
					continue
				}
				if options.CullFrontfaces && front {
					continue
				}
				hit, ok := worldHit(ray, localRay, in, triangle, triangleID, localT, u, v, front)
				if !ok || hit.T < options.TMin || hit.T > options.MaxT {
					continue
				}
				if found && !hitBetter(hit, instanceBest, options.tieTolerance) {
					continue
				}
				if haveBest && found && !hitBetter(hit, best, options.tieTolerance) {
					continue
				}
				instanceBest = hit
				found = true
				if hit.T < limit {
					limit = hit.T + options.tieTolerance(hit.T)
				}
			}
			continue
		}
		stack = append(stack, node.right, node.left)
	}
	if !found {
		return Hit{}, false
	}
	return instanceBest, true
}

func (s *Scene) intersectInstanceAll(ray Ray, in *instance, options PickOptions) []Hit {
	if in == nil || in.mesh == nil || len(in.mesh.tree.nodes) == 0 {
		return nil
	}
	localRay := Ray{
		Origin:    in.inverse.TransformPoint(ray.Origin),
		Direction: in.inverse.TransformVector(ray.Direction),
	}
	if !validRay(localRay) {
		return nil
	}
	invLocalDirection := invDir(localRay.Direction)
	hits := make([]Hit, 0)

	var stack []int32
	stack = append(stack, 0)
	for len(stack) != 0 {
		nodeIndex := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := &in.mesh.tree.nodes[nodeIndex]
		_, _, ok := node.bounds.IntersectRay(localRay.Origin, invLocalDirection, options.TMin, options.MaxT)
		if !ok {
			continue
		}
		if node.isLeaf() {
			start := int(node.start)
			end := start + int(node.count)
			for k := start; k < end; k++ {
				triangleID := in.mesh.tree.prims[k]
				triangle := in.mesh.triangles[triangleID]
				localT, u, v, front, ok := intersectTriangleLocal(triangle, localRay)
				if !ok {
					continue
				}
				if options.CullBackfaces && !front {
					continue
				}
				if options.CullFrontfaces && front {
					continue
				}
				hit, ok := worldHit(ray, localRay, in, triangle, triangleID, localT, u, v, front)
				if !ok || hit.T < options.TMin || hit.T > options.MaxT {
					continue
				}
				hits = append(hits, hit)
			}
			continue
		}
		stack = append(stack, node.right, node.left)
	}
	return hits
}

// worldHit converts a local hit to a world point, projects it back onto the
// original world ray to recover the world parameter, and transforms the face
// normal with the inverse transpose.
func worldHit(ray, localRay Ray, in *instance, triangle Triangle, triangleID uint32, localT, u, v float64, front bool) (Hit, bool) {
	localPoint := localRay.PointAt(localT)
	worldPoint := in.transform.TransformPoint(localPoint)
	worldT := ray.parameterOf(worldPoint)
	if !isFinite(worldT) || !worldPoint.IsFinite() {
		return Hit{}, false
	}
	worldNormal := in.normalMatrix.MulVec(triangle.FaceNormal()).Normalize()
	if !worldNormal.IsFinite() {
		return Hit{}, false
	}
	return Hit{
		InstanceID:  in.id,
		TriangleID:  triangleID,
		T:           worldT,
		Point:       ray.PointAt(worldT),
		Normal:      worldNormal,
		Barycentric: [3]float64{1 - u - v, u, v},
		FrontFace:   front,
	}, true
}
