package netsec

import "sort"

// holder 记录单个块的一个对象持有者及其共享组。
type holder struct {
	ref        ObjectRef
	shareGroup string
}

// Analyze 对目录引用图、对象区段表和分配位图做一致性分析。
//
// 分析过程不修改任何输入。返回的 Report 中每条发现都保留从根目录出发的
// 最短引用路径（BFS 保证），使取证操作员能够直接复核证据链。
func Analyze(graph *ReferenceGraph, table *SegmentTable, bitmap AllocationBitmap) (*Report, error) {
	if graph == nil || table == nil || bitmap == nil {
		return nil, ErrNilInput
	}

	objects := make(map[uint64]Object, len(table.Objects))
	for _, obj := range table.Objects {
		if _, exists := objects[obj.Ref.ID]; exists {
			return nil, ErrDuplicateObject
		}
		for _, extent := range obj.Extents {
			if extent.Length == 0 {
				return nil, ErrInvalidExtent
			}
		}
		objects[obj.Ref.ID] = obj
	}

	// 邻接表按目录项名称、目标对象号排序，使 BFS 结果确定。
	adjacency := make(map[uint64][]DirEntry, len(graph.Edges))
	for parentID, entries := range graph.Edges {
		sorted := append([]DirEntry(nil), entries...)
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].Name != sorted[j].Name {
				return sorted[i].Name < sorted[j].Name
			}
			return sorted[i].Target.ID < sorted[j].Target.ID
		})
		adjacency[parentID] = sorted
	}

	// staleEntry 只在引用可从根目录到达时报告：不可达子树内部的悬空项
	// 会随其所属对象一起被 KindUnreachableObject 覆盖，避免重复计数。
	var findings []Finding
	staleEntry := func(parentPath []PathNode, entry DirEntry) {
		path := appendPath(parentPath, entry)
		findings = append(findings, Finding{
			Kind:      KindStaleEntry,
			Object:    entry.Target,
			HasObject: true,
			EntryName: entry.Name,
			Path:      path,
			Detail: "目录项 " + PathString(path) + " 指向对象号 " + itoa(entry.Target.ID) +
				" 的旧代次 " + itoa(entry.Target.Generation) + "，该对象号已被代次复用",
		})
	}

	// BFS：以根对象为起点，只沿代次匹配的目录项扩展。
	shortest := make(map[uint64][]PathNode)
	type queueItem struct {
		ref  ObjectRef
		path []PathNode
	}
	queue := make([]queueItem, 0)
	if rootObj, ok := objects[graph.Root.ID]; ok {
		if rootObj.Ref.Generation != graph.Root.Generation {
			staleEntry(nil, DirEntry{Name: "/", Target: graph.Root, IsDir: true})
		} else {
			shortest[graph.Root.ID] = nil
			queue = append(queue, queueItem{ref: graph.Root, path: nil})
		}
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		for _, entry := range adjacency[item.ref.ID] {
			target, exists := objects[entry.Target.ID]
			if !exists || target.Ref.Generation != entry.Target.Generation {
				staleEntry(item.path, entry)
				continue
			}
			if _, visited := shortest[entry.Target.ID]; visited {
				continue // BFS 先到达者即最短路径，硬链接的后续路径不再展开
			}
			path := appendPath(item.path, entry)
			shortest[entry.Target.ID] = path
			queue = append(queue, queueItem{ref: entry.Target, path: path})
		}
	}

	// blockOwners 独立于可达性，对区段表中的全部对象逐块记账。
	// 一个对象内部若因区段重叠多次列出同一块，也只保留一个持有者。
	blockOwners := make(map[uint64][]holder)
	blockSeen := make(map[uint64]map[uint64]bool)
	for _, obj := range table.Objects {
		for _, extent := range obj.Extents {
			for _, block := range extent.Blocks() {
				if blockSeen[block] == nil {
					blockSeen[block] = make(map[uint64]bool)
				}
				if blockSeen[block][obj.Ref.ID] {
					continue
				}
				blockSeen[block][obj.Ref.ID] = true
				blockOwners[block] = append(blockOwners[block], holder{
					ref:        obj.Ref,
					shareGroup: obj.ShareGroup,
				})
			}
		}
	}

	// 1) 对象存在但目录不可达：区段表中有、BFS 最短路径集合中没有。
	for _, obj := range table.Objects {
		if _, reachable := shortest[obj.Ref.ID]; reachable {
			continue
		}
		findings = append(findings, Finding{
			Kind:       KindUnreachableObject,
			Object:     obj.Ref,
			HasObject:  true,
			ShareGroup: obj.ShareGroup,
			Detail:     "对象 " + obj.Ref.String() + " 存在于区段表但无任何从根目录可达的当前代次引用",
		})
	}

	// 2) 逐块判定非法重叠、可达但未分配。块号排序保证输出确定。
	blockNumbers := make([]uint64, 0, len(blockOwners))
	for block := range blockOwners {
		blockNumbers = append(blockNumbers, block)
	}
	sort.Slice(blockNumbers, func(i, j int) bool { return blockNumbers[i] < blockNumbers[j] })

	for _, block := range blockNumbers {
		holders := blockOwners[block]
		if len(holders) > 1 && !legalSharing(holders) {
			sort.SliceStable(holders, func(i, j int) bool {
				return lessRef(holders[i].ref, holders[j].ref)
			})
			owners := make([]ObjectRef, len(holders))
			for i, holder := range holders {
				owners[i] = holder.ref
			}
			first := holders[0]
			findings = append(findings, Finding{
				Kind:       KindIllegalOverlap,
				Block:      block,
				HasBlock:   true,
				Object:     first.ref,
				HasObject:  true,
				ShareGroup: first.shareGroup,
				Owners:     owners,
				Path:       shortest[first.ref.ID],
				Detail:     overlapDetail(block, holders),
			})
		}

		allocated := block < bitmap.TotalBlocks() && bitmap.IsAllocated(block)
		if allocated {
			continue
		}
		// 未分配块只要被任一可达对象引用即报告；选取最短路径最小的可达持有者。
		var reachableHolder *holder
		var reachablePath []PathNode
		for i := range holders {
			path, ok := shortest[holders[i].ref.ID]
			if !ok {
				continue
			}
			if reachableHolder == nil || lessPath(path, reachablePath) ||
				(equalPath(path, reachablePath) && lessRef(holders[i].ref, reachableHolder.ref)) {
				holderCopy := holders[i]
				reachableHolder = &holderCopy
				reachablePath = path
			}
		}
		if reachableHolder == nil {
			continue
		}
		findings = append(findings, Finding{
			Kind:       KindReachableUnallocated,
			Block:      block,
			HasBlock:   true,
			Object:     reachableHolder.ref,
			HasObject:  true,
			ShareGroup: reachableHolder.shareGroup,
			Path:       reachablePath,
			Detail: "块 " + itoa(block) + " 可由对象 " + reachableHolder.ref.String() +
				" 经路径 " + PathString(reachablePath) + " 到达，但分配位图未标记为已分配",
		})
	}

	// 3) 已分配但无对象引用：扫描整个位图，凡未出现在区段表中的已分配块。
	total := bitmap.TotalBlocks()
	for block := uint64(0); block < total; block++ {
		if !bitmap.IsAllocated(block) {
			continue
		}
		if _, referenced := blockOwners[block]; referenced {
			continue
		}
		findings = append(findings, Finding{
			Kind:     KindAllocatedUnreferenced,
			Block:    block,
			HasBlock: true,
			Detail:   "块 " + itoa(block) + " 在分配位图中为已分配，但区段表内无任何对象引用",
		})
	}

	sortFindings(findings)

	reachable := make(map[uint64]ObjectRef, len(shortest))
	for id, path := range shortest {
		if len(path) == 0 {
			reachable[id] = graph.Root
			continue
		}
		reachable[id] = path[len(path)-1].Object
	}
	return &Report{ReachableObjects: reachable, Findings: findings}, nil
}

// legalSharing 判断多个持有者共享同一块是否合法：
// 共享组必须非空，且全部持有者属于同一个共享组。
func legalSharing(holders []holder) bool {
	group := holders[0].shareGroup
	if group == "" {
		return false
	}
	for _, holder := range holders[1:] {
		if holder.shareGroup != group {
			return false
		}
	}
	return true
}

func appendPath(parent []PathNode, entry DirEntry) []PathNode {
	path := make([]PathNode, 0, len(parent)+1)
	path = append(path, parent...)
	path = append(path, PathNode{Name: entry.Name, Object: entry.Target})
	return path
}

func lessRef(a, b ObjectRef) bool {
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Generation < b.Generation
}

func lessPath(a, b []PathNode) bool {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i].Name != b[i].Name {
			return a[i].Name < b[i].Name
		}
		if !equalRef(a[i].Object, b[i].Object) {
			return lessRef(a[i].Object, b[i].Object)
		}
	}
	return len(a) < len(b)
}

func equalPath(a, b []PathNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || !equalRef(a[i].Object, b[i].Object) {
			return false
		}
	}
	return true
}

func equalRef(a, b ObjectRef) bool {
	return a.ID == b.ID && a.Generation == b.Generation
}

func overlapDetail(block uint64, holders []holder) string {
	detail := "块 " + itoa(block) + " 被 " + itoa(uint64(len(holders))) + " 个对象非法共享："
	for i, holder := range holders {
		if i > 0 {
			detail += "，"
		}
		group := holder.shareGroup
		if group == "" {
			group = "私有"
		}
		detail += holder.ref.String() + "[" + group + "]"
	}
	return detail
}

var findingRank = map[FindingKind]int{
	KindStaleEntry:            1,
	KindUnreachableObject:     2,
	KindReachableUnallocated:  3,
	KindAllocatedUnreferenced: 4,
	KindIllegalOverlap:        5,
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Kind != b.Kind {
			return findingRank[a.Kind] < findingRank[b.Kind]
		}
		if a.HasBlock != b.HasBlock {
			return a.HasBlock
		}
		if a.HasBlock && a.Block != b.Block {
			return a.Block < b.Block
		}
		if a.HasObject != b.HasObject {
			return a.HasObject
		}
		if a.HasObject && !equalRef(a.Object, b.Object) {
			return lessRef(a.Object, b.Object)
		}
		return a.EntryName < b.EntryName
	})
}
