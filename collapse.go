package meshsimp

import "container/heap"

// collapse merges v into u at the edge's optimal QEM position.
//
// Face, quadric and attribute updates are local. Afterwards the edge and
// adjacency tables are rebuilt from all surviving faces, which makes the
// topology tables impossible to drift from the live triangles. The edges
// whose neighborhood the collapse touches are then re-evaluated; every other
// rebuilt edge keeps its record and heap version.
func (s *simplifier) collapse(u, v int, entry *heapEntry) {
	mesh := s.mesh
	uvKey := edgeKey(u, v)
	target := s.edges[uvKey].target

	dying := make(map[int]struct{}, 2)
	for _, fi := range s.edges[uvKey].faces {
		dying[fi] = struct{}{}
	}

	// Remove dying faces; reroute every other face of v onto u.
	for fi := range s.vtoF[v] {
		if _, dead := dying[fi]; dead {
			s.aliveF[fi] = false
			s.faceCount--
			for _, vi := range mesh.Faces[fi] {
				delete(s.vtoF[vi], fi)
			}
			continue
		}
		f := &mesh.Faces[fi]
		for k := range f {
			if f[k] == v {
				f[k] = u
			}
		}
		delete(s.vtoF[v], fi)
		s.vtoF[u][fi] = struct{}{}
	}

	// Quadrics add; attributes interpolate by the target's segment position.
	wU, wV := interpolationWeights(mesh.Positions[u], mesh.Positions[v], target)
	s.quad[u].add(s.quad[v])
	mesh.Positions[u] = target
	if len(mesh.Normals) > v {
		mesh.Normals[u] = blendVec3(mesh.Normals, u, v, wU, wV, mesh.NormalizeAttributes)
	}
	if len(mesh.Colors) > v {
		mesh.Colors[u] = blendVec3(mesh.Colors, u, v, wU, wV, mesh.NormalizeAttributes)
	}
	if len(mesh.UVs) > v {
		mesh.UVs[u] = blendUV(mesh.UVs, u, v, wU, wV)
	}

	s.aliveV[v] = false
	s.collapsed++

	// Capture every edge whose legality the collapse could change before the
	// rebuild rewrites adjacency: the endpoint star edges plus every other edge
	// of every face in either endpoint's star (link-condition neighbors).
	changed := make(map[uint64]struct{}, len(s.vtoE[u])+len(s.vtoE[v]))
	for key := range s.vtoE[u] {
		changed[key] = struct{}{}
	}
	for key := range s.vtoE[v] {
		changed[key] = struct{}{}
	}
	starFaces := make(map[int]struct{})
	for fi := range s.vtoF[u] {
		starFaces[fi] = struct{}{}
	}
	for fi := range s.vtoF[v] {
		starFaces[fi] = struct{}{}
	}
	for fi := range starFaces {
		for _, key := range faceEdges(mesh.Faces[fi]) {
			changed[key] = struct{}{}
		}
	}

	s.rebuildTopology()

	// Re-evaluate every edge whose neighborhood the collapse touched; other
	// edges keep their record, version and existing heap entry.
	s.refreshEdges(changed)
}

// refreshEdges re-evaluates the given edge keys and pushes fresh heap entries
// for the legal survivors. Edges that disappeared in the rebuild are dropped.
func (s *simplifier) refreshEdges(keys map[uint64]struct{}) {
	for key := range keys {
		rec, ok := s.edges[key]
		if !ok || len(rec.faces) == 0 {
			delete(s.edges, key)
			a, b := keyEnds(key)
			delete(s.vtoE[a], key)
			delete(s.vtoE[b], key)
			continue
		}
		s.evaluate(key, rec)
		heap.Push(s.heap, &heapEntry{key: key, cost: rec.cost, version: rec.version})
	}
}

// rebuildTopology reconstructs vtoF, vtoE and edges from the surviving faces,
// reusing surviving edge records so edges away from the collapse keep their
// version and remain valid heap candidates.
func (s *simplifier) rebuildTopology() {
	mesh := s.mesh
	old := s.edges

	for i := range s.vtoE {
		s.vtoE[i] = make(map[uint64]struct{})
	}
	for i := range s.vtoF {
		s.vtoF[i] = make(map[int]struct{})
	}

	next := make(map[uint64]*edgeRec, len(old))
	for fi, alive := range s.aliveF {
		if !alive {
			continue
		}
		f := mesh.Faces[fi]
		s.vtoF[f[0]][fi] = struct{}{}
		s.vtoF[f[1]][fi] = struct{}{}
		s.vtoF[f[2]][fi] = struct{}{}
		for _, key := range faceEdges(f) {
			rec, ok := next[key]
			if !ok {
				if prev, existed := old[key]; existed {
					rec = prev
					rec.faces = rec.faces[:0]
				} else {
					rec = &edgeRec{}
				}
				next[key] = rec
				a, b := keyEnds(key)
				s.vtoE[a][key] = struct{}{}
				s.vtoE[b][key] = struct{}{}
			}
			rec.faces = append(rec.faces, fi)
		}
	}
	s.edges = next
}

func faceEdges(f [3]int) [3]uint64 {
	return [3]uint64{
		edgeKey(f[0], f[1]),
		edgeKey(f[1], f[2]),
		edgeKey(f[2], f[0]),
	}
}
