package radixkv

import "fmt"

// BlockID is an opaque physical KV block identifier.
type BlockID int

type blockState uint8

const (
	blockFree blockState = iota
	blockTree
	blockRunning
)

type blockPool struct {
	total     int
	blockSize int
	state     []blockState
	treeRefs  []int
	owner     []string
	freeStack []BlockID
}

func newBlockPool(totalBlocks, blockSize int) *blockPool {
	p := &blockPool{
		total:     totalBlocks,
		blockSize: blockSize,
		state:     make([]blockState, totalBlocks),
		treeRefs:  make([]int, totalBlocks),
		owner:     make([]string, totalBlocks),
	}
	// Push in reverse so the first pop yields block 0, making tests deterministic.
	for id := totalBlocks - 1; id >= 0; id-- {
		p.freeStack = append(p.freeStack, BlockID(id))
	}
	return p
}

func (p *blockPool) checkID(id BlockID) {
	if int(id) < 0 || int(id) >= p.total {
		panic(fmt.Sprintf("radixkv: block id %d out of range [0,%d)", id, p.total))
	}
}

func (p *blockPool) popFree() (BlockID, bool) {
	if len(p.freeStack) == 0 {
		return -1, false
	}
	id := p.freeStack[len(p.freeStack)-1]
	p.freeStack = p.freeStack[:len(p.freeStack)-1]
	if p.state[id] != blockFree {
		panic(fmt.Sprintf("radixkv: free stack contains non-free block %d", id))
	}
	return id, true
}

func (p *blockPool) allocateTree() (BlockID, error) {
	id, ok := p.popFree()
	if !ok {
		return -1, ErrOutOfBlocks
	}
	p.state[id] = blockTree
	p.treeRefs[id] = 1
	p.owner[id] = "radix-tree"
	return id, nil
}

func (p *blockPool) addTreeRef(id BlockID) {
	p.checkID(id)
	if p.state[id] != blockTree || p.treeRefs[id] <= 0 {
		panic(fmt.Sprintf("radixkv: adding tree reference to non-tree block %d", id))
	}
	p.treeRefs[id]++
}

func (p *blockPool) releaseTreeRef(id BlockID) {
	p.checkID(id)
	if p.state[id] != blockTree || p.treeRefs[id] <= 0 {
		panic(fmt.Sprintf("radixkv: releasing invalid tree reference for block %d", id))
	}
	p.treeRefs[id]--
	if p.treeRefs[id] == 0 {
		p.state[id] = blockFree
		p.owner[id] = ""
		p.freeStack = append(p.freeStack, id)
	}
}

func (p *blockPool) allocateRunning(owner string) (BlockID, error) {
	id, ok := p.popFree()
	if !ok {
		return -1, ErrOutOfBlocks
	}
	p.state[id] = blockRunning
	p.treeRefs[id] = 0
	p.owner[id] = owner
	return id, nil
}

func (p *blockPool) releaseRunning(id BlockID, owner string) error {
	p.checkID(id)
	if p.state[id] != blockRunning {
		return fmt.Errorf("radixkv: block %d is not a running block", id)
	}
	if owner != "" && p.owner[id] != owner {
		return fmt.Errorf("radixkv: block %d owner is %q, not %q", id, p.owner[id], owner)
	}
	p.state[id] = blockFree
	p.owner[id] = ""
	p.freeStack = append(p.freeStack, id)
	return nil
}

func (p *blockPool) checkRunningOwner(id BlockID, owner string) error {
	p.checkID(id)
	if p.state[id] != blockRunning {
		return fmt.Errorf("radixkv: block %d is not marked running", id)
	}
	if p.owner[id] != owner {
		return fmt.Errorf("radixkv: running block %d owner is %q, not request %q", id, p.owner[id], owner)
	}
	return nil
}

func (p *blockPool) freeCount() int {
	return len(p.freeStack)
}

func (p *blockPool) treeCount() int {
	count := 0
	for id := 0; id < p.total; id++ {
		if p.state[id] == blockTree {
			count++
		}
	}
	return count
}

func (p *blockPool) runningCount() int {
	count := 0
	for id := 0; id < p.total; id++ {
		if p.state[id] == blockRunning {
			count++
		}
	}
	return count
}

func (p *blockPool) runningIDs() []BlockID {
	ids := make([]BlockID, 0)
	for id := 0; id < p.total; id++ {
		if p.state[id] == blockRunning {
			ids = append(ids, BlockID(id))
		}
	}
	return ids
}

func (p *blockPool) treeIDs() []BlockID {
	ids := make([]BlockID, 0)
	for id := 0; id < p.total; id++ {
		if p.state[id] == blockTree {
			ids = append(ids, BlockID(id))
		}
	}
	return ids
}
