package gin

import "sort"

// Posting stores the sorted TIDs for one key. Small postings use a compact
// sorted slice; once the configured threshold is exceeded the posting is
// represented by an ordered search tree.
type Posting struct {
	list      []TID
	tree      *PostingTree
	threshold int
}

// PostingList is a descriptive alias for a per-key posting.
type PostingList = Posting

func newPosting(threshold int) *Posting {
	return &Posting{threshold: threshold}
}

// Len returns the number of TIDs in the posting.
func (p *Posting) Len() int {
	if p == nil {
		return 0
	}
	if p.tree != nil {
		return p.tree.Len()
	}
	return len(p.list)
}

// IsTree reports whether this posting has been upgraded to a posting tree.
func (p *Posting) IsTree() bool {
	return p != nil && p.tree != nil
}

// TIDs returns the posting contents in ascending TID order.
func (p *Posting) TIDs() []TID {
	if p == nil {
		return nil
	}
	if p.tree != nil {
		return p.tree.TIDs()
	}
	return append([]TID(nil), p.list...)
}

// Contains reports whether tid is in the posting.
func (p *Posting) Contains(tid TID) bool {
	if p == nil {
		return false
	}
	if p.tree != nil {
		return p.tree.Contains(tid)
	}
	index := sort.Search(len(p.list), func(i int) bool { return p.list[i] >= tid })
	return index < len(p.list) && p.list[index] == tid
}

// ForEach visits TIDs in ascending order until fn returns false.
func (p *Posting) ForEach(fn func(TID) bool) {
	if p == nil {
		return
	}
	if p.tree != nil {
		p.tree.ForEach(fn)
		return
	}
	for _, tid := range p.list {
		if !fn(tid) {
			return
		}
	}
}

func (p *Posting) setTIDs(tids []TID) {
	tids = uniqueSortedTIDs(tids)
	p.list = nil
	p.tree = nil
	if len(tids) == 0 {
		return
	}
	if len(tids) > p.threshold {
		p.tree = NewPostingTree(tids)
		return
	}
	p.list = tids
}

// PostingTree is an ordered AVL tree of unique TIDs. Its in-order traversal is
// ascending, which preserves the ordering contract of a posting list.
type PostingTree struct {
	root *postingNode
	size int
}

type postingNode struct {
	tid    TID
	left   *postingNode
	right  *postingNode
	height int
}

// NewPostingTree builds a balanced posting tree from tids. Duplicate values are
// removed.
func NewPostingTree(tids []TID) *PostingTree {
	sorted := uniqueSortedTIDs(tids)
	tree := &PostingTree{}
	tree.root = buildPostingNode(sorted)
	tree.size = len(sorted)
	return tree
}

func buildPostingNode(tids []TID) *postingNode {
	if len(tids) == 0 {
		return nil
	}
	middle := len(tids) / 2
	node := &postingNode{
		tid:    tids[middle],
		left:   buildPostingNode(tids[:middle]),
		right:  buildPostingNode(tids[middle+1:]),
		height: 1,
	}
	node.height = 1 + maxInt(nodeHeight(node.left), nodeHeight(node.right))
	return node
}

// Len returns the number of unique TIDs.
func (t *PostingTree) Len() int {
	if t == nil {
		return 0
	}
	return t.size
}

// Height returns the tree height. An empty tree has height zero.
func (t *PostingTree) Height() int {
	if t == nil {
		return 0
	}
	return nodeHeight(t.root)
}

// Contains reports whether tid is present.
func (t *PostingTree) Contains(tid TID) bool {
	if t == nil {
		return false
	}
	node := t.root
	for node != nil {
		switch {
		case tid < node.tid:
			node = node.left
		case tid > node.tid:
			node = node.right
		default:
			return true
		}
	}
	return false
}

// Insert adds tid to the tree.
func (t *PostingTree) Insert(tid TID) {
	if t == nil {
		return
	}
	var inserted bool
	t.root, inserted = insertPostingNode(t.root, tid)
	if inserted {
		t.size++
	}
}

func insertPostingNode(node *postingNode, tid TID) (*postingNode, bool) {
	if node == nil {
		return &postingNode{tid: tid, height: 1}, true
	}
	var inserted bool
	switch {
	case tid < node.tid:
		node.left, inserted = insertPostingNode(node.left, tid)
	case tid > node.tid:
		node.right, inserted = insertPostingNode(node.right, tid)
	default:
		return node, false
	}
	node.height = 1 + maxInt(nodeHeight(node.left), nodeHeight(node.right))
	return rebalancePosting(node), inserted
}

// Delete removes tid from the tree.
func (t *PostingTree) Delete(tid TID) {
	if t == nil {
		return
	}
	var deleted bool
	t.root, deleted = deletePostingNode(t.root, tid)
	if deleted {
		t.size--
	}
}

func deletePostingNode(node *postingNode, tid TID) (*postingNode, bool) {
	if node == nil {
		return nil, false
	}
	var deleted bool
	switch {
	case tid < node.tid:
		node.left, deleted = deletePostingNode(node.left, tid)
	case tid > node.tid:
		node.right, deleted = deletePostingNode(node.right, tid)
	default:
		deleted = true
		switch {
		case node.left == nil:
			return node.right, true
		case node.right == nil:
			return node.left, true
		default:
			successor := leftmostPostingNode(node.right)
			node.tid = successor.tid
			node.right, _ = deletePostingNode(node.right, successor.tid)
		}
	}
	if node == nil {
		return nil, deleted
	}
	node.height = 1 + maxInt(nodeHeight(node.left), nodeHeight(node.right))
	return rebalancePosting(node), deleted
}

// TIDs returns the tree contents in ascending order.
func (t *PostingTree) TIDs() []TID {
	if t == nil {
		return nil
	}
	result := make([]TID, 0, t.size)
	t.ForEach(func(tid TID) bool {
		result = append(result, tid)
		return true
	})
	return result
}

// ForEach visits TIDs in ascending order until fn returns false.
func (t *PostingTree) ForEach(fn func(TID) bool) {
	if t == nil {
		return
	}
	continuing := true
	var walk func(*postingNode)
	walk = func(node *postingNode) {
		if node == nil || !continuing {
			return
		}
		walk(node.left)
		if !continuing {
			return
		}
		if !fn(node.tid) {
			continuing = false
			return
		}
		walk(node.right)
	}
	walk(t.root)
}

func rebalancePosting(node *postingNode) *postingNode {
	balance := nodeHeight(node.left) - nodeHeight(node.right)
	if balance > 1 {
		if nodeHeight(node.left.left) < nodeHeight(node.left.right) {
			node.left = rotatePostingLeft(node.left)
		}
		return rotatePostingRight(node)
	}
	if balance < -1 {
		if nodeHeight(node.right.right) < nodeHeight(node.right.left) {
			node.right = rotatePostingRight(node.right)
		}
		return rotatePostingLeft(node)
	}
	return node
}

func rotatePostingLeft(node *postingNode) *postingNode {
	right := node.right
	node.right = right.left
	right.left = node
	node.height = 1 + maxInt(nodeHeight(node.left), nodeHeight(node.right))
	right.height = 1 + maxInt(nodeHeight(right.left), nodeHeight(right.right))
	return right
}

func rotatePostingRight(node *postingNode) *postingNode {
	left := node.left
	node.left = left.right
	left.right = node
	node.height = 1 + maxInt(nodeHeight(node.left), nodeHeight(node.right))
	left.height = 1 + maxInt(nodeHeight(left.left), nodeHeight(left.right))
	return left
}

func leftmostPostingNode(node *postingNode) *postingNode {
	for node != nil && node.left != nil {
		node = node.left
	}
	return node
}

func nodeHeight(node *postingNode) int {
	if node == nil {
		return 0
	}
	return node.height
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func uniqueSortedTIDs(tids []TID) []TID {
	if len(tids) == 0 {
		return nil
	}
	result := append([]TID(nil), tids...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}
