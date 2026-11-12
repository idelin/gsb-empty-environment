package netsec

import "testing"

func ref(id, generation uint64) ObjectRef {
	return ObjectRef{ID: id, Generation: generation}
}

func mustAllocate(t *testing.T, device *MemoryDevice, blocks ...uint64) {
	t.Helper()
	for _, block := range blocks {
		if err := device.Allocate(block, []byte{byte(block)}); err != nil {
			t.Fatalf("allocate block %d: %v", block, err)
		}
	}
}

func findingForBlock(findings []Finding, block uint64) *Finding {
	for i := range findings {
		if findings[i].HasBlock && findings[i].Block == block {
			return &findings[i]
		}
	}
	return nil
}

func accountForBlock(accounts []BlockAccount, block uint64) *BlockAccount {
	for i := range accounts {
		if accounts[i].Block == block {
			return &accounts[i]
		}
	}
	return nil
}

func pathNames(path []PathNode) []string {
	names := make([]string, len(path))
	for i, node := range path {
		names[i] = node.Name
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestHardlinkNotDoubleCounted 验证硬链接的多个目录项不会把同一对象
// 的数据块计算成两份对象引用。
func TestHardlinkNotDoubleCounted(t *testing.T) {
	device := NewMemoryDevice(8, 4)
	mustAllocate(t, device, 0, 1, 2, 3)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}},
		{Ref: ref(2, 1), Extents: []Extent{{1, 1}}},
		{Ref: ref(3, 1), Extents: []Extent{{2, 2}}},
	}}
	graph := &ReferenceGraph{
		Root: ref(1, 1),
		Edges: map[uint64][]DirEntry{
			1: {{Name: "dir", Target: ref(2, 1), IsDir: true}},
			2: {
				{Name: "link-a", Target: ref(3, 1)},
				{Name: "link-b", Target: ref(3, 1)},
			},
		},
	}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("干净夹具不应有发现，实际: %+v", report.Findings)
	}
	if len(report.ReachableObjects) != 3 {
		t.Fatalf("可达对象应为 3 个（硬链接不重复），实际: %d", len(report.ReachableObjects))
	}

	accounts, err := VerifyBlockReferences(device, table)
	if err != nil {
		t.Fatalf("VerifyBlockReferences: %v", err)
	}
	for _, block := range []uint64{2, 3} {
		account := accountForBlock(accounts, block)
		if account == nil || account.DistinctOwners != 1 {
			t.Fatalf("块 %d 应只有 1 个持有者，实际: %+v", block, account)
		}
		count, err := device.RefCount(block)
		if err != nil || count != 1 {
			t.Fatalf("块 %d 引用计数应为 1（硬链接不算两份），实际 count=%d err=%v", block, count, err)
		}
	}
}

// TestLegalSharing 验证同一受信共享组内的区段共享合法。
func TestLegalSharing(t *testing.T) {
	device := NewMemoryDevice(8, 4)
	mustAllocate(t, device, 0, 1, 2, 3)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}},
		{Ref: ref(4, 1), ShareGroup: "snap", Extents: []Extent{{2, 1}, {3, 1}}},
		{Ref: ref(5, 1), ShareGroup: "snap", Extents: []Extent{{1, 1}, {3, 1}}},
	}}
	graph := &ReferenceGraph{
		Root: ref(1, 1),
		Edges: map[uint64][]DirEntry{
			1: {
				{Name: "clone-a", Target: ref(4, 1)},
				{Name: "clone-b", Target: ref(5, 1)},
			},
		},
	}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if overlaps := report.ByKind(KindIllegalOverlap); len(overlaps) != 0 {
		t.Fatalf("同组共享不应报告非法重叠: %+v", overlaps)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("合法共享夹具不应有其它发现: %+v", report.Findings)
	}

	accounts, err := VerifyBlockReferences(device, table)
	if err != nil {
		t.Fatalf("VerifyBlockReferences: %v", err)
	}
	account := accountForBlock(accounts, 3)
	if account == nil || account.DistinctOwners != 2 || !account.LegalSharing {
		t.Fatalf("块 3 应为 2 个同组持有者的合法共享，实际: %+v", account)
	}
}

// TestIllegalOverlap 验证私有对象与跨组共享都被识别为非法重叠，
// 且发现保留持有者清单与最短路径。
func TestIllegalOverlap(t *testing.T) {
	device := NewMemoryDevice(8, 4)
	mustAllocate(t, device, 0, 1, 2, 3, 4)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}},
		{Ref: ref(6, 1), Extents: []Extent{{2, 1}}},
		{Ref: ref(7, 1), ShareGroup: "g1", Extents: []Extent{{2, 1}}},
		{Ref: ref(8, 1), ShareGroup: "g1", Extents: []Extent{{3, 1}}},
		{Ref: ref(9, 1), ShareGroup: "g2", Extents: []Extent{{3, 1}}},
	}}
	graph := &ReferenceGraph{
		Root: ref(1, 1),
		Edges: map[uint64][]DirEntry{
			1: {
				{Name: "p", Target: ref(6, 1)},
				{Name: "q", Target: ref(7, 1)},
				{Name: "r", Target: ref(8, 1)},
				{Name: "s", Target: ref(9, 1)},
			},
		},
	}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	overlaps := report.ByKind(KindIllegalOverlap)
	if len(overlaps) != 2 {
		t.Fatalf("应报告 2 处非法重叠，实际: %+v", overlaps)
	}

	block2 := findingForBlock(overlaps, 2)
	if block2 == nil || len(block2.Owners) != 2 {
		t.Fatalf("块 2 非法重叠持有者应为 2 个: %+v", block2)
	}
	if !equalRef(block2.Owners[0], ref(6, 1)) || !equalRef(block2.Owners[1], ref(7, 1)) {
		t.Fatalf("块 2 持有者排序错误: %+v", block2.Owners)
	}
	if len(block2.Path) != 1 || block2.Path[0].Name != "p" {
		t.Fatalf("块 2 应保留首个持有者的最短路径，实际: %+v", block2.Path)
	}

	block3 := findingForBlock(overlaps, 3)
	if block3 == nil || len(block3.Owners) != 2 {
		t.Fatalf("块 3 非法重叠持有者应为 2 个: %+v", block3)
	}

	accounts, err := VerifyBlockReferences(device, table)
	if err != nil {
		t.Fatalf("VerifyBlockReferences: %v", err)
	}
	for _, block := range []uint64{2, 3} {
		account := accountForBlock(accounts, block)
		if account == nil || account.LegalSharing {
			t.Fatalf("块 %d 独立重放也应判定为非法共享: %+v", block, account)
		}
	}
}

// TestGenerationReuse 验证对象号代次复用：旧代次目录项判悬空，
// 新代次对象仍可达，已删除对象号的残留项同样判悬空。
func TestGenerationReuse(t *testing.T) {
	device := NewMemoryDevice(8, 4)
	mustAllocate(t, device, 0, 1, 2)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}},
		{Ref: ref(10, 2), Extents: []Extent{{1, 1}}},
		{Ref: ref(11, 1), Extents: []Extent{{2, 1}}},
	}}
	graph := &ReferenceGraph{
		Root: ref(1, 1),
		Edges: map[uint64][]DirEntry{
			1: {
				{Name: "old", Target: ref(10, 1)},
				{Name: "new", Target: ref(10, 2)},
				{Name: "gone", Target: ref(12, 1)},
			},
		},
	}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	stale := report.ByKind(KindStaleEntry)
	if len(stale) != 2 {
		t.Fatalf("应报告 2 条悬空代次引用，实际: %+v", stale)
	}
	names := map[string]bool{}
	for _, finding := range stale {
		names[finding.EntryName] = true
		if len(finding.Path) == 0 {
			t.Fatalf("悬空引用必须保留路径: %+v", finding)
		}
	}
	if !names["old"] || !names["gone"] {
		t.Fatalf("悬空目录项名称不匹配: %v", names)
	}

	unreachable := report.ByKind(KindUnreachableObject)
	if len(unreachable) != 1 || !equalRef(unreachable[0].Object, ref(11, 1)) {
		t.Fatalf("对象 11.1 应不可达，实际: %+v", unreachable)
	}
	current, ok := report.ReachableObjects[10]
	if !ok || !equalRef(current, ref(10, 2)) {
		t.Fatalf("对象号 10 的当前代次 10.2 应可达，实际: %+v", report.ReachableObjects)
	}
}

// TestReachableUnallocated 验证可达对象引用未分配块被报告，
// 而不可达对象持有的未分配块不会误报为可达问题。
func TestReachableUnallocated(t *testing.T) {
	device := NewMemoryDevice(8, 4)
	mustAllocate(t, device, 0)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}},
		{Ref: ref(13, 1), Extents: []Extent{{4, 1}}},
		{Ref: ref(14, 1), Extents: []Extent{{5, 1}}},
	}}
	graph := &ReferenceGraph{
		Root: ref(1, 1),
		Edges: map[uint64][]DirEntry{
			1: {{Name: "live", Target: ref(13, 1)}},
		},
	}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	unallocated := report.ByKind(KindReachableUnallocated)
	if len(unallocated) != 1 {
		t.Fatalf("应只有 1 条可达未分配发现，实际: %+v", unallocated)
	}
	if unallocated[0].Block != 4 || !equalRef(unallocated[0].Object, ref(13, 1)) {
		t.Fatalf("发现应指向块 4/对象 13.1: %+v", unallocated[0])
	}
	if !equalStrings(pathNames(unallocated[0].Path), []string{"live"}) {
		t.Fatalf("最短路径应为 [live]，实际: %v", pathNames(unallocated[0].Path))
	}

	unreachable := report.ByKind(KindUnreachableObject)
	if len(unreachable) != 1 || !equalRef(unreachable[0].Object, ref(14, 1)) {
		t.Fatalf("对象 14.1 应不可达: %+v", unreachable)
	}
}

// TestAllocatedUnreferenced 验证位图已分配但无对象引用的块被报告。
func TestAllocatedUnreferenced(t *testing.T) {
	device := NewMemoryDevice(8, 4)
	mustAllocate(t, device, 0, 6)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}},
	}}
	graph := &ReferenceGraph{Root: ref(1, 1)}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	leaks := report.ByKind(KindAllocatedUnreferenced)
	if len(leaks) != 1 || leaks[0].Block != 6 || leaks[0].HasObject {
		t.Fatalf("块 6 应报告为已分配无引用，实际: %+v", leaks)
	}
}

// TestShortestPath 验证 BFS 保留最短引用路径，根对象路径为空。
func TestShortestPath(t *testing.T) {
	device := NewMemoryDevice(8, 4)
	mustAllocate(t, device, 0, 1, 2, 3)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}},
		{Ref: ref(2, 1), Extents: []Extent{{1, 1}}},
		{Ref: ref(3, 1), Extents: []Extent{{2, 1}}},
		{Ref: ref(4, 1), Extents: []Extent{{3, 1}}},
	}}
	graph := &ReferenceGraph{
		Root: ref(1, 1),
		Edges: map[uint64][]DirEntry{
			1: {
				{Name: "deep", Target: ref(2, 1), IsDir: true},
				{Name: "shortcut", Target: ref(4, 1)},
			},
			2: {{Name: "child", Target: ref(3, 1), IsDir: true}},
			3: {{Name: "long-way", Target: ref(4, 1)}},
		},
	}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("夹具应一致: %+v", report.Findings)
	}
	if _, rootReachable := report.ReachableObjects[1]; !rootReachable {
		t.Fatalf("根对象 1.1 应在可达集合中")
	}
	if got := PathString(nil); got != "/" {
		t.Fatalf("根路径应渲染为 /，实际: %s", got)
	}

	// 直接用可达集合拿不到路径，通过一个未分配块触发带路径的发现来验证。
	device.Free(3)
	report, err = Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	unallocated := report.ByKind(KindReachableUnallocated)
	if len(unallocated) != 1 {
		t.Fatalf("应报告块 3 可达未分配: %+v", unallocated)
	}
	if !equalStrings(pathNames(unallocated[0].Path), []string{"shortcut"}) {
		t.Fatalf("块 3 应取一跳最短路径 shortcut，实际: %v", pathNames(unallocated[0].Path))
	}
}

// TestBlockwiseCrossVerification 在混合夹具上逐块交叉核对
// Analyze 与独立重放 VerifyBlockReferences 的结论完全一致。
func TestBlockwiseCrossVerification(t *testing.T) {
	const totalBlocks uint64 = 12
	device := NewMemoryDevice(totalBlocks, 4)
	// 块 0..4、7、9 已分配；块 5、6、8、10、11 未分配。
	mustAllocate(t, device, 0, 1, 2, 3, 4, 7, 9)

	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}}}, // 根
		{Ref: ref(2, 1), ShareGroup: "g", Extents: []Extent{{1, 1}, {4, 1}}},
		{Ref: ref(3, 1), ShareGroup: "g", Extents: []Extent{{2, 1}, {4, 1}}}, // 块 4 合法共享
		{Ref: ref(4, 1), Extents: []Extent{{5, 1}}},                          // 可达但块 5 未分配
		{Ref: ref(5, 1), Extents: []Extent{{6, 1}, {7, 1}}},                  // 不可达，块 6 未分配、块 7 已分配
		{Ref: ref(6, 1), Extents: []Extent{{1, 1}}},                          // 与同组对象撞块 1，非法
	}}
	graph := &ReferenceGraph{
		Root: ref(1, 1),
		Edges: map[uint64][]DirEntry{
			1: {
				{Name: "a", Target: ref(2, 1)},
				{Name: "b", Target: ref(3, 1)},
				{Name: "c", Target: ref(4, 1)},
				{Name: "x", Target: ref(6, 1)},
			},
		},
	}

	report, err := Analyze(graph, table, device)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	accounts, err := VerifyBlockReferences(device, table)
	if err != nil {
		t.Fatalf("VerifyBlockReferences: %v", err)
	}

	illegalBlocks := map[uint64]bool{}
	for _, finding := range report.ByKind(KindIllegalOverlap) {
		illegalBlocks[finding.Block] = true
	}
	unallocatedBlocks := map[uint64]bool{}
	for _, finding := range report.ByKind(KindReachableUnallocated) {
		unallocatedBlocks[finding.Block] = true
	}

	referenced := map[uint64]bool{}
	for _, account := range accounts {
		referenced[account.Block] = true
		// 交叉点 1：非法重叠判定必须逐块一致。
		if want := account.DistinctOwners > 1 && !account.LegalSharing; want != illegalBlocks[account.Block] {
			t.Fatalf("块 %d 非法重叠判定不一致: account=%v report=%v", account.Block, want, illegalBlocks[account.Block])
		}
		// 交叉点 2：可达未分配判定必须与位图状态和报告一致。
		if !account.Allocated {
			anyReachable := false
			for _, owner := range account.Owners {
				if _, ok := report.ReachableObjects[owner.ID]; ok {
					anyReachable = true
				}
			}
			if anyReachable != unallocatedBlocks[account.Block] {
				t.Fatalf("块 %d 可达未分配判定不一致: account reachable=%v report=%v",
					account.Block, anyReachable, unallocatedBlocks[account.Block])
			}
		}
		// 交叉点 3：重放引用数等于设备块层计数，且硬链接不影响该数。
		count, err := device.RefCount(account.Block)
		if err != nil || count != account.DistinctOwners {
			t.Fatalf("块 %d 引用计数不一致: device=%d account=%d err=%v",
				account.Block, count, account.DistinctOwners, err)
		}
	}

	// 交叉点 4：每个已分配且未被引用的块都必须出现在报告中。
	leakBlocks := map[uint64]bool{}
	for _, finding := range report.ByKind(KindAllocatedUnreferenced) {
		leakBlocks[finding.Block] = true
	}
	for block := uint64(0); block < totalBlocks; block++ {
		if device.IsAllocated(block) && !referenced[block] && !leakBlocks[block] {
			t.Fatalf("已分配无引用块 %d 未被报告", block)
		}
		if referenced[block] && leakBlocks[block] {
			t.Fatalf("被引用块 %d 不应出现在无引用发现中", block)
		}
	}

	// 夹具预期：非法块 1；可达未分配块 5；无引用块 3、9；不可达对象 5.1。
	if len(illegalBlocks) != 1 || !illegalBlocks[1] {
		t.Fatalf("非法重叠集合异常: %v", illegalBlocks)
	}
	if len(unallocatedBlocks) != 1 || !unallocatedBlocks[5] {
		t.Fatalf("可达未分配集合异常: %v", unallocatedBlocks)
	}
	if len(leakBlocks) != 2 || !leakBlocks[3] || !leakBlocks[9] {
		t.Fatalf("已分配无引用集合异常: %v", leakBlocks)
	}
	unreachable := report.ByKind(KindUnreachableObject)
	if len(unreachable) != 1 || !equalRef(unreachable[0].Object, ref(5, 1)) {
		t.Fatalf("不可达对象集合异常: %+v", unreachable)
	}
}

// TestAnalyzeValidation 验证输入错误被明确拒绝。
func TestAnalyzeValidation(t *testing.T) {
	graph := &ReferenceGraph{Root: ref(1, 1)}
	table := &SegmentTable{Objects: []Object{{Ref: ref(1, 1), Extents: []Extent{{0, 1}}}}}
	bitmap := Bitmap{true}

	if _, err := Analyze(nil, table, bitmap); err != ErrNilInput {
		t.Fatalf("空图应返回 ErrNilInput，实际: %v", err)
	}
	if _, err := Analyze(graph, nil, bitmap); err != ErrNilInput {
		t.Fatalf("空区段表应返回 ErrNilInput，实际: %v", err)
	}
	if _, err := Analyze(graph, table, nil); err != ErrNilInput {
		t.Fatalf("空位图应返回 ErrNilInput，实际: %v", err)
	}

	dup := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1)},
		{Ref: ref(1, 2)},
	}}
	if _, err := Analyze(graph, dup, bitmap); err != ErrDuplicateObject {
		t.Fatalf("重复对象号应返回 ErrDuplicateObject，实际: %v", err)
	}

	badExtent := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 0}}},
	}}
	if _, err := Analyze(graph, badExtent, bitmap); err != ErrInvalidExtent {
		t.Fatalf("零长度区段应返回 ErrInvalidExtent，实际: %v", err)
	}
}
