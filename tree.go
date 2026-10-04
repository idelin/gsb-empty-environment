package antientropy

// Config controls the digest tree split rule.
type Config struct {
	// MaxLeafItems is the leaf capacity: a node covering more than this
	// many records is split in half, unless MaxDepth is reached.
	MaxLeafItems int
	// MaxDepth bounds the tree; at this depth a node is always a leaf.
	MaxDepth int
}

// DefaultConfig is a reasonable default split rule.
var DefaultConfig = Config{MaxLeafItems: 4, MaxDepth: 32}

func (c Config) withDefaults() Config {
	if c.MaxLeafItems <= 0 {
		c.MaxLeafItems = DefaultConfig.MaxLeafItems
	}
	if c.MaxDepth <= 0 {
		c.MaxDepth = DefaultConfig.MaxDepth
	}
	return c
}

// node is one interval [lo, hi) of the fixed key space. The tree shape is a
// pure function of the key set and the split rule — never of insertion
// order — because placement hashes and split points are fixed.
type node struct {
	lo, hi   uint64
	depth    int
	leaf     bool
	digest   uint64
	keys     []string // leaf only
	children [2]*node // internal only
}

// digestTree is a fixed-range summary tree over a store snapshot.
type digestTree struct {
	root  *node
	store *Store
	cfg   Config
}

// buildDigestTree constructs the summary tree for a store. Records are
// bucketed by placement hash; a bucket exceeding MaxLeafItems splits its
// range in half recursively. Digests aggregate by XOR, so the resulting
// tree (shape and every digest) is identical regardless of the order in
// which records were inserted into the store.
func buildDigestTree(s *Store, cfg Config) *digestTree {
	cfg = cfg.withDefaults()
	t := &digestTree{store: s, cfg: cfg}
	t.root = t.build(0, ^uint64(0), 0, s.keysInRange(0, ^uint64(0)))
	return t
}

func (t *digestTree) build(lo, hi uint64, depth int, keys []string) *node {
	n := &node{lo: lo, hi: hi, depth: depth}
	if len(keys) <= t.cfg.MaxLeafItems || depth >= t.cfg.MaxDepth {
		n.leaf = true
		n.keys = keys
		for _, k := range keys {
			r := t.store.records[k]
			n.digest ^= r.digest()
		}
		return n
	}
	mid := lo + (hi-lo)/2 + 1 // upper half starts here; [lo,mid) [mid,hi]
	var leftKeys, rightKeys []string
	for _, k := range keys {
		if KeyHash(k) < mid {
			leftKeys = append(leftKeys, k)
		} else {
			rightKeys = append(rightKeys, k)
		}
	}
	n.children[0] = t.build(lo, mid-1, depth+1, leftKeys)
	n.children[1] = t.build(mid, hi, depth+1, rightKeys)
	n.digest = combineDigests(n.children[0].digest, n.children[1].digest)
	return n
}
