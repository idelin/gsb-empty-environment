package radixkv

import "errors"

// Token is a prompt/generated token identifier.
type Token = int

// Common errors returned by cache and scheduler operations.
var (
	ErrInvalidConfig        = errors.New("radixkv: invalid configuration")
	ErrOutOfBlocks          = errors.New("radixkv: no free physical KV blocks")
	ErrPrefixMismatch       = errors.New("radixkv: prefix is not contained in radix tree")
	ErrKVCapacityExhausted  = errors.New("radixkv: KV capacity exhausted and no preemptible running request")
	ErrRequestNotFound      = errors.New("radixkv: request not found")
	ErrDuplicateRequestID   = errors.New("radixkv: duplicate request id")
	ErrRequestAlreadyLocked = errors.New("radixkv: request already holds a prefix lock")
	ErrRequestPreempted     = errors.New("radixkv: request was preempted to release KV blocks")
)

// BlockSlice references a contiguous range of token slots inside one physical block.
// Start and End are token offsets within blockSize. When an edge is split inside a
// physical block, the same BlockID can appear in two adjacent slices; the block is
// freed only after all tree references disappear.
type BlockSlice struct {
	BlockID BlockID `json:"block_id"`
	Start   int     `json:"start"`
	End     int     `json:"end"`
}

// Length returns the number of token slots covered by this slice.
func (s BlockSlice) Length() int {
	return s.End - s.Start
}

// Node is a radix-tree edge. Tokens/Key are the tokens stored on this edge.
// Blocks is the ordered physical block IDs covered by Slices.
type Node struct {
	ID         int             `json:"id"`
	Tokens     []Token         `json:"tokens"`
	Key        []Token         `json:"key"`
	Slices     []BlockSlice    `json:"slices"`
	Blocks     []BlockID       `json:"blocks"`
	Children   map[Token]*Node `json:"-"`
	Parent     *Node           `json:"-"`
	RefCount   int             `json:"ref_count"`
	LastAccess int64           `json:"last_access"`
	Depth      int             `json:"depth"`

	heapVersion uint64
	removed     bool
}

// RadixNode is an alias for callers using the longer name.
type RadixNode = Node

// IsLeaf reports whether the node has no child edges.
func (n *Node) IsLeaf() bool {
	return n != nil && len(n.Children) == 0
}

// Refs returns the number of request locks on this node path.
func (n *Node) Refs() int {
	if n == nil {
		return 0
	}
	return n.RefCount
}

// BlockIDs returns the block IDs covered by this edge.
func (n *Node) BlockIDs() []BlockID {
	if n == nil {
		return nil
	}
	return append([]BlockID(nil), n.Blocks...)
}

// TokenCount returns the edge length.
func (n *Node) TokenCount() int {
	if n == nil {
		return 0
	}
	return len(n.Tokens)
}

func commonPrefixLength(a, b []Token) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	i := 0
	for i < limit && a[i] == b[i] {
		i++
	}
	return i
}

func blockIDsFromSlices(slices []BlockSlice) []BlockID {
	ids := make([]BlockID, len(slices))
	for i, slice := range slices {
		ids[i] = slice.BlockID
	}
	return ids
}

func cloneTokens(tokens []Token) []Token {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]Token, len(tokens))
	copy(out, tokens)
	return out
}

func cloneBlockIDs(ids []BlockID) []BlockID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]BlockID, len(ids))
	copy(out, ids)
	return out
}

// localSlices builds block slices for a newly allocated edge whose local token
// offset starts at zero.
func localSlices(tokenCount int, ids []BlockID, blockSize int) []BlockSlice {
	slices := make([]BlockSlice, 0, len(ids))
	idIndex := 0
	for start := 0; start < tokenCount; start += blockSize {
		end := start + blockSize
		if end > tokenCount {
			end = tokenCount
		}
		slices = append(slices, BlockSlice{
			BlockID: ids[idIndex],
			Start:   0,
			End:     end - start,
		})
		idIndex++
	}
	return slices
}

func splitSlicesAt(slices []BlockSlice, at int) (left []BlockSlice, right []BlockSlice) {
	cut := at
	for _, slice := range slices {
		length := slice.Length()
		switch {
		case cut >= length:
			left = append(left, slice)
			cut -= length
		case cut > 0:
			middle := slice.Start + cut
			left = append(left, BlockSlice{BlockID: slice.BlockID, Start: slice.Start, End: middle})
			right = append(right, BlockSlice{BlockID: slice.BlockID, Start: middle, End: slice.End})
			cut = 0
		default:
			right = append(right, slice)
		}
	}
	return left, right
}
