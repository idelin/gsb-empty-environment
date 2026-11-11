package controlflow

import "encoding/binary"

const (
	defaultMaxInstructions = 1 << 20
	defaultMaxPaths        = 256
)

type workItem struct {
	addr  uint64
	from  Edge
	path  []uint64
	root  uint64
	entry bool
}

// Recover expands the control-flow graph from the trusted entry addresses.
// It never mutates decoder state and always returns a Report, even when some
// addresses fail to decode; setup errors are returned alongside a nil report.
func (e *Engine) Recover(entries []uint64) (*Report, error) {
	if e.Decoder == nil {
		return nil, ErrNoDecoder
	}
	if len(entries) == 0 {
		return nil, ErrNoEntry
	}

	budget := e.MaxInstructions
	if budget <= 0 {
		budget = defaultMaxInstructions
	}
	pathCap := e.MaxPathsPerNode
	if pathCap <= 0 {
		pathCap = defaultMaxPaths
	}

	report := &Report{BudgetLimit: budget}
	nodes := make(map[uint64]*Node)
	edgeSeen := make(map[Edge]struct{})
	backSeen := make(map[Edge]struct{})
	unmappedSeen := make(map[Edge]struct{})
	unknownSeen := make(map[uint64]struct{})

	inRange := func(addr uint64) bool {
		if e.Size == 0 || addr < e.Base {
			return false
		}
		end := e.Base + e.Size
		return end > e.Base && addr < end
	}

	addEdge := func(edge Edge, back bool) {
		if _, ok := edgeSeen[edge]; !ok {
			edgeSeen[edge] = struct{}{}
			report.Edges = append(report.Edges, edge)
		}
		if back {
			if _, ok := backSeen[edge]; !ok {
				backSeen[edge] = struct{}{}
				report.BackEdges = append(report.BackEdges, edge)
			}
		}
	}

	addUnmapped := func(edge Edge) {
		if _, ok := unmappedSeen[edge]; ok {
			return
		}
		unmappedSeen[edge] = struct{}{}
		report.UnmappedTargets = append(report.UnmappedTargets, edge)
	}

	queue := make([]workItem, 0, len(entries))
	seenEntry := make(map[uint64]struct{}, len(entries))
	for _, entry := range entries {
		if _, dup := seenEntry[entry]; dup {
			continue
		}
		seenEntry[entry] = struct{}{}
		queue = append(queue, workItem{addr: entry, root: entry, entry: true})
	}

	for head := 0; head < len(queue); head++ {
		item := queue[head]
		fullPath := make([]uint64, 0, len(item.path)+1)
		fullPath = append(fullPath, item.path...)
		fullPath = append(fullPath, item.addr)

		node, known := nodes[item.addr]
		if !known {
			if !inRange(item.addr) {
				if !item.entry {
					addUnmapped(item.from)
				}
				continue
			}
			if report.Decoded >= budget {
				report.BudgetExceeded = true
				break
			}
			report.Decoded++

			result := e.Decoder.Decode(item.addr)
			node = &Node{Address: item.addr, Length: result.Length, Terminates: result.Terminates}
			if result.Length > 0 {
				node.End = item.addr + uint64(result.Length)
			} else {
				node.End = item.addr
				if result.Err == nil {
					result.Err = ErrZeroLength
				}
			}
			if result.Err != nil {
				node.Err = result.Err
			}

			nodes[item.addr] = node
			report.Nodes = append(report.Nodes, node)

			mergeProvenance(node, item, fullPath, pathCap)
			if !item.entry {
				addEdge(item.from, pathContains(item.path, item.addr))
			}

			if node.IsError() {
				report.Errors = append(report.Errors, node)
				continue
			}

			report.recordConflicts(node)

			if result.UnknownIndirect {
				if _, ok := unknownSeen[item.addr]; !ok {
					unknownSeen[item.addr] = struct{}{}
					report.UnknownIndirect = append(report.UnknownIndirect, UnknownIndirectSite{
						At:   item.addr,
						Path: append([]uint64(nil), fullPath...),
					})
				}
			}

			successors := make([]Target, 0, len(result.Successors)+1)
			successorSeen := make(map[Target]struct{}, len(result.Successors)+1)
			for _, successor := range result.Successors {
				if _, dup := successorSeen[successor]; dup {
					continue
				}
				successorSeen[successor] = struct{}{}
				successors = append(successors, successor)
			}
			if !result.Terminates {
				fallthroughTarget := Target{Address: node.End, Kind: EdgeFallthrough}
				if _, dup := successorSeen[fallthroughTarget]; !dup {
					successors = append(successors, fallthroughTarget)
				}
			}

			for _, successor := range successors {
				edge := Edge{From: item.addr, To: successor.Address, Kind: successor.Kind}
				if !inRange(successor.Address) {
					addUnmapped(edge)
					continue
				}
				queue = append(queue, workItem{
					addr: successor.Address,
					from: edge,
					path: fullPath,
					root: item.root,
				})
			}
			continue
		}

		mergeProvenance(node, item, fullPath, pathCap)
		if !item.entry {
			addEdge(item.from, pathContains(item.path, item.addr))
		}
	}

	return report, nil
}

func mergeProvenance(node *Node, item workItem, fullPath []uint64, pathCap int) {
	if !containsUint64(node.EntryRoots, item.root) {
		node.EntryRoots = append(node.EntryRoots, item.root)
	}
	if !item.entry && !containsEdge(node.Incoming, item.from) {
		node.Incoming = append(node.Incoming, item.from)
	}
	if len(node.Paths) >= pathCap {
		node.PathsTruncated = true
		return
	}
	key := pathKey(fullPath)
	for _, existing := range node.Paths {
		if pathKey(existing) == key {
			return
		}
	}
	node.Paths = append(node.Paths, append([]uint64(nil), fullPath...))
}

func (r *Report) recordConflicts(fresh *Node) {
	freshInterval := Interval{Start: fresh.Address, End: fresh.End}
	for _, earlier := range r.Nodes {
		if earlier == fresh {
			continue
		}
		earlierInterval := Interval{Start: earlier.Address, End: earlier.End}
		overlapStart := max(freshInterval.Start, earlierInterval.Start)
		overlapEnd := min(freshInterval.End, earlierInterval.End)
		if overlapStart >= overlapEnd {
			continue
		}
		if freshInterval == earlierInterval {
			continue
		}
		conflict := Conflict{
			First:  earlierInterval,
			Second: freshInterval,
			Shared: [2]uint64{overlapStart, overlapEnd},
		}
		r.Conflicts = append(r.Conflicts, conflict)
		earlier.Conflicts = append(earlier.Conflicts, conflict)
		fresh.Conflicts = append(fresh.Conflicts, conflict)
	}
}

func pathContains(path []uint64, addr uint64) bool {
	return containsUint64(path, addr)
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsEdge(edges []Edge, target Edge) bool {
	for _, edge := range edges {
		if edge == target {
			return true
		}
	}
	return false
}

func pathKey(path []uint64) string {
	buffer := make([]byte, 0, len(path)*8)
	for _, addr := range path {
		buffer = binary.BigEndian.AppendUint64(buffer, addr)
	}
	return string(buffer)
}
