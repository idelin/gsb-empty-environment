package radixkv

import (
	"container/heap"
	"fmt"
	"sort"
	"sync"
)

// ConservationReport is the strict physical-block audit.
type ConservationReport struct {
	TotalBlocks   int      `json:"total_blocks"`
	TreeBlocks    int      `json:"tree_blocks"`
	FreeBlocks    int      `json:"free_blocks"`
	RunningBlocks int      `json:"running_blocks"`
	Sum           int      `json:"sum"`
	Conserved     bool     `json:"conserved"`
	Problems      []string `json:"problems,omitempty"`
}

// MatchResult describes a radix prefix match.
type MatchResult struct {
	// Node is the deepest completely consumed edge (root for a partial first edge).
	Node *Node
	// PartialNode is set when matching stops in the middle of an edge.
	PartialNode  *Node
	Matched      int
	CachedTokens int
	BlockIDs     []BlockID
}

// leafHeapEntry is a lazy heap entry. Stale entries are discarded on Pop.
type leafHeapEntry struct {
	lastAccess int64
	nodeID     int
	version    uint64
}

type leafHeap []leafHeapEntry

func (h leafHeap) Len() int { return len(h) }

func (h leafHeap) Less(i, j int) bool {
	if h[i].lastAccess != h[j].lastAccess {
		return h[i].lastAccess < h[j].lastAccess
	}
	if h[i].nodeID != h[j].nodeID {
		return h[i].nodeID < h[j].nodeID
	}
	return h[i].version < h[j].version
}

func (h leafHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *leafHeap) Push(x any) { *h = append(*h, x.(leafHeapEntry)) }

func (h *leafHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	*h = old[:n-1]
	return entry
}

// Cache is a block-level RadixAttention prefix cache and LRU leaf evictor.
type Cache struct {
	mu          sync.Mutex
	blockSize   int
	totalBlocks int
	pool        *blockPool

	root       *Node
	nodes      map[int]*Node
	nextNodeID int
	clock      int64
	leafHeap   leafHeap

	// locks maps a request ID to its current terminal locked node.
	locks map[string]*Node
}

// RadixCache is the descriptive alias for Cache.
type RadixCache = Cache

// RadixTree is an alias for callers that treat this component as the tree itself.
type RadixTree = Cache

// NewCache creates a cache backed by totalBlocks physical blocks of blockSize tokens.
func NewCache(totalBlocks, blockSize int) (*Cache, error) {
	if totalBlocks <= 0 {
		return nil, fmt.Errorf("%w: totalBlocks must be positive", ErrInvalidConfig)
	}
	if blockSize <= 0 {
		return nil, fmt.Errorf("%w: blockSize must be positive", ErrInvalidConfig)
	}
	c := &Cache{
		blockSize:   blockSize,
		totalBlocks: totalBlocks,
		pool:        newBlockPool(totalBlocks, blockSize),
		nodes:       make(map[int]*Node),
		locks:       make(map[string]*Node),
	}
	c.root = &Node{ID: 0, Children: make(map[Token]*Node), Depth: 0}
	c.nodes[0] = c.root
	c.nextNodeID = 1
	heap.Init(&c.leafHeap)
	return c, nil
}

// MustNewCache panics on invalid configuration and is intended for tests/setup.
func MustNewCache(totalBlocks, blockSize int) *Cache {
	c, err := NewCache(totalBlocks, blockSize)
	if err != nil {
		panic(err)
	}
	return c
}

// NewRadixCache is the convenient constructor expected by concise call sites.
func NewRadixCache(totalBlocks, blockSize int) *Cache {
	return MustNewCache(totalBlocks, blockSize)
}

func (c *Cache) tickLocked() int64 {
	c.clock++
	return c.clock
}

// TotalBlocks returns the physical pool capacity.
func (c *Cache) TotalBlocks() int { return c.totalBlocks }

// BlockSize returns tokens stored per physical block.
func (c *Cache) BlockSize() int { return c.blockSize }

// Root returns the radix root.
func (c *Cache) Root() *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.root
}

// NodeCount returns live tree nodes, including root.
func (c *Cache) NodeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.nodes)
}

// RequestLockCount returns the RefCount contribution tracked for one request.
func (c *Cache) RequestLockCount(requestID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.locks[requestID]; !ok {
		return 0
	}
	return 1
}

func (c *Cache) newNodeLocked(tokens []Token, slices []BlockSlice, parent *Node) *Node {
	id := c.nextNodeID
	c.nextNodeID++
	node := &Node{
		ID:         id,
		Tokens:     cloneTokens(tokens),
		Key:        nil,
		Slices:     append([]BlockSlice(nil), slices...),
		Blocks:     blockIDsFromSlices(slices),
		Children:   make(map[Token]*Node),
		Parent:     parent,
		LastAccess: c.tickLocked(),
		Depth:      parent.Depth + len(tokens),
	}
	node.Key = node.Tokens
	c.nodes[id] = node
	return node
}

func (c *Cache) enqueueIfEvictableLocked(node *Node) {
	if node == nil || node.ID == c.root.ID || node.removed {
		return
	}
	if node.RefCount != 0 || len(node.Children) != 0 {
		return
	}
	node.heapVersion++
	entry := leafHeapEntry{
		lastAccess: node.LastAccess,
		nodeID:     node.ID,
		version:    node.heapVersion,
	}
	heap.Push(&c.leafHeap, entry)
}

func (c *Cache) allocateTreeBlocksLocked(count int, protected map[int]bool) ([]BlockID, error) {
	ids := make([]BlockID, 0, count)
	for len(ids) < count {
		id, err := c.pool.allocateTree()
		if err == nil {
			ids = append(ids, id)
			continue
		}
		if c.evictLRUProtectedLocked(protected) == nil {
			for _, allocated := range ids {
				c.pool.releaseTreeRef(allocated)
			}
			return nil, ErrOutOfBlocks
		}
	}
	return ids, nil
}

func protectionSetFromNode(node *Node) map[int]bool {
	protected := make(map[int]bool)
	for current := node; current != nil; current = current.Parent {
		protected[current.ID] = true
	}
	return protected
}

// splitEdgeLocked atomically splits child at offset in [1,len(child.Tokens)).
// The original Node pointer remains the suffix node so outstanding lock terminal
// pointers stay valid; a new prefix node is inserted between parent and child.
func (c *Cache) splitEdgeLocked(child *Node, at int) *Node {
	if at <= 0 || at >= len(child.Tokens) {
		return nil
	}
	parent := child.Parent
	oldFirst := child.Tokens[0]

	leftTokens := cloneTokens(child.Tokens[:at])
	rightTokens := cloneTokens(child.Tokens[at:])
	leftSlices, rightSlices := splitSlicesAt(child.Slices, at)

	prefix := &Node{
		ID:         c.nextNodeID,
		Tokens:     leftTokens,
		Key:        leftTokens,
		Slices:     append([]BlockSlice(nil), leftSlices...),
		Blocks:     blockIDsFromSlices(leftSlices),
		Children:   make(map[Token]*Node),
		Parent:     parent,
		RefCount:   child.RefCount,
		LastAccess: child.LastAccess,
		Depth:      child.Depth - len(rightTokens),
	}
	c.nextNodeID++
	c.nodes[prefix.ID] = prefix

	// A block intersected by the cut is referenced by both adjacent slices.
	leftSet := make(map[BlockID]struct{}, len(leftSlices))
	for _, slice := range leftSlices {
		leftSet[slice.BlockID] = struct{}{}
	}
	rightSet := make(map[BlockID]struct{}, len(rightSlices))
	for _, slice := range rightSlices {
		rightSet[slice.BlockID] = struct{}{}
	}
	for id := range leftSet {
		if _, shared := rightSet[id]; shared {
			c.pool.addTreeRef(id)
		}
	}

	prefix.Children[rightTokens[0]] = child
	if parent != nil {
		if current, ok := parent.Children[oldFirst]; ok && current == child {
			parent.Children[leftTokens[0]] = prefix
			if leftTokens[0] != oldFirst {
				delete(parent.Children, oldFirst)
			}
		} else {
			parent.Children[leftTokens[0]] = prefix
		}
	}

	child.Tokens = rightTokens
	child.Key = rightTokens
	child.Slices = append([]BlockSlice(nil), rightSlices...)
	child.Blocks = blockIDsFromSlices(rightSlices)
	child.Parent = prefix
	child.Depth = prefix.Depth + len(rightTokens)

	return prefix
}

// Insert adds tokens to the tree and allocates physical blocks for new edges.
// If the pool is full, unlocked LRU leaves are evicted first.
func (c *Cache) Insert(tokens []Token) (*Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.insertLocked(tokens)
}

// InsertTokens is an alias for Insert.
func (c *Cache) InsertTokens(tokens []Token) (*Node, error) {
	return c.Insert(tokens)
}

func (c *Cache) insertLocked(tokens []Token) (*Node, error) {
	if len(tokens) == 0 {
		return c.root, nil
	}

	parent := c.root
	depth := 0
	for depth < len(tokens) {
		first := tokens[depth]
		child := parent.Children[first]
		if child == nil {
			suffix := cloneTokens(tokens[depth:])
			protected := protectionSetFromNode(parent)
			ids, err := c.allocateTreeBlocksLocked(
				(len(suffix)+c.blockSize-1)/c.blockSize, protected)
			if err != nil {
				return nil, err
			}
			slices := localSlices(len(suffix), ids, c.blockSize)
			node := c.newNodeLocked(suffix, slices, parent)
			parent.Children[first] = node
			c.enqueueIfEvictableLocked(node)
			return node, nil
		}

		common := commonPrefixLength(tokens[depth:], child.Tokens)
		if common < len(child.Tokens) {
			suffix := cloneTokens(tokens[depth+common:])
			var ids []BlockID
			if len(suffix) > 0 {
				var err error
				protected := protectionSetFromNode(child)
				ids, err = c.allocateTreeBlocksLocked(
					(len(suffix)+c.blockSize-1)/c.blockSize, protected)
				if err != nil {
					return nil, err
				}
			}

			prefix := c.splitEdgeLocked(child, common)
			depth += common
			if len(suffix) == 0 {
				return prefix, nil
			}

			slices := localSlices(len(suffix), ids, c.blockSize)
			node := c.newNodeLocked(suffix, slices, prefix)
			prefix.Children[suffix[0]] = node
			c.enqueueIfEvictableLocked(node)
			return node, nil
		}

		depth += common
		parent = child
	}
	return parent, nil
}

// EnsurePrefixNode splits edges as needed so that tokens identify one exact node.
// It returns ErrPrefixMismatch if tokens diverge from the tree.
func (c *Cache) EnsurePrefixNode(tokens []Token) (*Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensurePrefixNodeLocked(tokens)
}

func (c *Cache) ensurePrefixNodeLocked(tokens []Token) (*Node, error) {
	if len(tokens) == 0 {
		return c.root, nil
	}
	parent := c.root
	depth := 0
	for depth < len(tokens) {
		child := parent.Children[tokens[depth]]
		if child == nil {
			return nil, fmt.Errorf("%w: missing edge at token %d", ErrPrefixMismatch, depth)
		}
		common := commonPrefixLength(tokens[depth:], child.Tokens)
		if depth+common == len(tokens) {
			if common < len(child.Tokens) {
				prefix := c.splitEdgeLocked(child, common)
				return prefix, nil
			}
			return child, nil
		}
		if common < len(child.Tokens) {
			return nil, fmt.Errorf("%w: divergence at token %d", ErrPrefixMismatch, depth+common)
		}
		depth += common
		parent = child
	}
	return parent, nil
}

// MatchPrefix returns exact radix match length and the block-aligned cache hit.
func (c *Cache) MatchPrefix(tokens []Token) MatchResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := MatchResult{Node: c.root, Matched: 0}
	if len(tokens) == 0 {
		result.CachedTokens = 0
		return result
	}

	parent := c.root
	depth := 0
	for depth < len(tokens) {
		child := parent.Children[tokens[depth]]
		if child == nil {
			break
		}
		common := commonPrefixLength(tokens[depth:], child.Tokens)
		matched := depth + common
		result.Matched = matched

		ts := c.tickLocked()
		child.LastAccess = ts

		if common < len(child.Tokens) {
			result.PartialNode = child
			break
		}

		result.Node = child
		depth = matched
		parent = child
	}

	result.CachedTokens = (result.Matched / c.blockSize) * c.blockSize
	if result.CachedTokens > 0 {
		node, err := c.ensurePrefixNodeLocked(tokens[:result.CachedTokens])
		if err == nil {
			result.Node = node
			result.BlockIDs = c.blockIDsForNodeLocked(node)
		}
	}
	return result
}

func (c *Cache) blockIDsForNodeLocked(node *Node) []BlockID {
	path := make([]*Node, 0, 8)
	for current := node; current != nil && current.ID != c.root.ID; current = current.Parent {
		path = append(path, current)
	}
	ids := make([]BlockID, 0)
	for i := len(path) - 1; i >= 0; i-- {
		ids = append(ids, path[i].Blocks...)
	}
	return ids
}

// BlockIDsForNode returns all physical block IDs along root-to-node path.
func (c *Cache) BlockIDsForNode(node *Node) []BlockID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.blockIDsForNodeLocked(node)
}

func (c *Cache) lockPathLocked(node *Node) {
	timestamp := c.tickLocked()
	for current := node; current != nil; current = current.Parent {
		current.RefCount++
		current.LastAccess = timestamp
	}
}

func (c *Cache) unlockPathLocked(node *Node) {
	for current := node; current != nil; current = current.Parent {
		if current.RefCount <= 0 {
			panic(fmt.Sprintf("radixkv: negative refCount on node %d", current.ID))
		}
		current.RefCount--
		if current.RefCount == 0 && current.ID != c.root.ID && len(current.Children) == 0 {
			c.enqueueIfEvictableLocked(current)
		}
	}
}

// LockNode records requestID as locking node and all its ancestors.
// Moving an existing request lock first releases its old path.
func (c *Cache) LockNode(requestID string, node *Node) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if requestID == "" {
		return fmt.Errorf("%w: empty request id", ErrInvalidConfig)
	}
	if node == nil {
		node = c.root
	}
	if old, ok := c.locks[requestID]; ok && old != node {
		c.unlockPathLocked(old)
	}
	c.locks[requestID] = node
	c.lockPathLocked(node)
	return nil
}

// LockPrefix ensures tokens name an exact prefix node and locks its path.
func (c *Cache) LockPrefix(requestID string, tokens []Token) (*Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	node, err := c.ensurePrefixNodeLocked(tokens)
	if err != nil {
		return nil, err
	}
	if old, ok := c.locks[requestID]; ok && old != node {
		c.unlockPathLocked(old)
	}
	c.locks[requestID] = node
	c.lockPathLocked(node)
	return node, nil
}

// UnlockRequest releases exactly the path currently owned by requestID.
func (c *Cache) UnlockRequest(requestID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	node, ok := c.locks[requestID]
	if !ok {
		return false
	}
	c.unlockPathLocked(node)
	delete(c.locks, requestID)
	return true
}

// EvictLRU removes and returns the least-recently-used eligible leaf.
// Only RefCount==0 leaves are eligible. It returns nil when no leaf can be evicted.
func (c *Cache) EvictLRU() *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evictLRULocked()
}

func (c *Cache) evictLRULocked() *Node {
	return c.evictLRUProtectedLocked(nil)
}

func (c *Cache) evictLRUProtectedLocked(protected map[int]bool) *Node {
	skipped := make([]leafHeapEntry, 0)
	for c.leafHeap.Len() > 0 {
		entry := heap.Pop(&c.leafHeap).(leafHeapEntry)
		node := c.nodes[entry.nodeID]
		if node == nil || node.removed {
			continue
		}
		if node.RefCount != 0 || len(node.Children) != 0 {
			continue
		}
		if node.LastAccess != entry.lastAccess || node.heapVersion != entry.version {
			continue
		}
		if protected != nil && protected[node.ID] {
			skipped = append(skipped, entry)
			continue
		}

		parent := node.Parent
		if parent != nil {
			key := node.Tokens[0]
			if parent.Children[key] != node {
				for candidateKey, candidate := range parent.Children {
					if candidate == node {
						key = candidateKey
						break
					}
				}
			}
			delete(parent.Children, key)
		}

		for _, slice := range node.Slices {
			c.pool.releaseTreeRef(slice.BlockID)
		}

		node.removed = true
		node.Children = make(map[Token]*Node)
		node.Parent = nil
		delete(c.nodes, node.ID)

		if parent != nil && parent.ID != c.root.ID && len(parent.Children) == 0 && parent.RefCount == 0 {
			c.enqueueIfEvictableLocked(parent)
		}
		for _, skippedEntry := range skipped {
			heap.Push(&c.leafHeap, skippedEntry)
		}
		return node
	}
	for _, skippedEntry := range skipped {
		heap.Push(&c.leafHeap, skippedEntry)
	}
	return nil
}

// FreeBlockCount returns currently free physical blocks.
func (c *Cache) FreeBlockCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pool.freeCount()
}

// TreeBlockCount returns unique physical blocks currently owned by the tree.
func (c *Cache) TreeBlockCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pool.treeCount()
}

// RunningBlockCount returns blocks allocated to unattached running request tables.
func (c *Cache) RunningBlockCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pool.runningCount()
}

// AllocateRunningBlock reserves one free block for a running request.
func (c *Cache) AllocateRunningBlock(owner string) (BlockID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pool.allocateRunning(owner)
}

// FreeRunningBlock returns a request-owned block to the free pool.
func (c *Cache) FreeRunningBlock(id BlockID, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pool.releaseRunning(id, owner)
}

// Conservation audits free/tree/running ownership and physical capacity.
func (c *Cache) Conservation() ConservationReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conservationLocked()
}

func (c *Cache) conservationLocked() ConservationReport {
	report := ConservationReport{
		TotalBlocks:   c.totalBlocks,
		TreeBlocks:    c.pool.treeCount(),
		FreeBlocks:    c.pool.freeCount(),
		RunningBlocks: c.pool.runningCount(),
	}
	report.Sum = report.TreeBlocks + report.FreeBlocks + report.RunningBlocks
	report.Conserved = report.Sum == report.TotalBlocks
	if !report.Conserved {
		report.Problems = append(report.Problems,
			fmt.Sprintf("block sum %d != totalBlocks %d", report.Sum, report.TotalBlocks))
	}

	seenTree := make(map[BlockID]int)
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		for _, id := range node.Blocks {
			seenTree[id]++
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(c.root)

	for id := 0; id < c.totalBlocks; id++ {
		blockID := BlockID(id)
		if c.pool.state[id] == blockTree {
			if c.pool.treeRefs[id] <= 0 {
				report.Problems = append(report.Problems, fmt.Sprintf("tree block %d has non-positive refs", id))
			}
			if _, ok := seenTree[blockID]; !ok {
				report.Problems = append(report.Problems, fmt.Sprintf("pool marks %d tree but tree walk misses it", id))
			}
		}
		if count, ok := seenTree[blockID]; ok {
			if c.pool.state[id] != blockTree {
				report.Problems = append(report.Problems, fmt.Sprintf("tree references block %d but pool state is %d", id, c.pool.state[id]))
			} else if count > c.pool.treeRefs[id] {
				report.Problems = append(report.Problems,
					fmt.Sprintf("block %d tree slice multiplicity %d exceeds pool refs %d", id, count, c.pool.treeRefs[id]))
			}
		}
	}
	if len(report.Problems) > 0 {
		report.Conserved = false
	}
	return report
}

// LRUNodeIDs returns eligible leaf IDs in current heap order for diagnostics.
func (c *Cache) LRUNodeIDs() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := append([]leafHeapEntry(nil), c.leafHeap...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].lastAccess != entries[j].lastAccess {
			return entries[i].lastAccess < entries[j].lastAccess
		}
		return entries[i].nodeID < entries[j].nodeID
	})
	ids := make([]int, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.nodeID)
	}
	return ids
}
