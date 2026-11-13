package netsec

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Client is a path-ORAM client.  All operations are serialized.
type Client struct {
	mu sync.Mutex

	cfg    Config
	geom   treeGeometry
	host   Host
	store  StateStore
	verify func(RootReceipt) error
}

// New is a short alias for NewClient.
func New(cfg Config, host Host, store StateStore) (*Client, error) {
	return NewClient(cfg, host, store)
}

// NewClient validates the configuration, binds the client to one state store
// and storage host, initializes an empty tree if necessary, and recovers any
// journaled transaction.  A server whose root does not equal the committed
// client root is rejected as a rollback or fork.
func NewClient(cfg Config, host Host, store StateStore) (*Client, error) {
	cfg, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	if host == nil {
		return nil, fmt.Errorf("%w: nil host", ErrInvalidConfig)
	}
	if host.TreeID() != cfg.TreeID {
		return nil, fmt.Errorf("%w: host tree ID %q != config tree ID %q", ErrInvalidConfig, host.TreeID(), cfg.TreeID)
	}
	if store == nil {
		store = NewMemoryStore()
	}
	c := &Client{
		cfg:   cfg,
		geom:  newTreeGeometry(cfg.Height),
		host:  host,
		store: store,
	}
	if cfg.VerifyReceipt != nil {
		c.verify = cfg.VerifyReceipt
	} else {
		pub := append(ed25519.PublicKey(nil), cfg.RootPublicKey...)
		c.verify = func(receipt RootReceipt) error {
			return VerifyRootReceipt(pub, receipt)
		}
	}

	state, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("netsec: load state: %w", err)
	}
	if state == nil {
		if err := c.initializeLocked(); err != nil {
			return nil, err
		}
		return c, nil
	}
	if err := c.validateCommittedState(state); err != nil {
		return nil, err
	}
	if state.Intent != nil || state.Prepared != nil {
		if _, err := c.recoverLocked(state); err != nil {
			return nil, err
		}
		state, err = store.Load()
		if err != nil {
			return nil, fmt.Errorf("netsec: reload state: %w", err)
		}
	}
	if err := c.verifyServerAtLocked(state); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) initializeLocked() error {
	buckets := make([]WireBucket, c.geom.nodes)
	var expectedRoot [32]byte
	for node := 0; node < c.geom.nodes; node++ {
		bucket, err := sealBucket(c.cfg, node, 0, 0, nil)
		if err != nil {
			return err
		}
		buckets[node] = bucket
		xorRoot(&expectedRoot, BucketDigest(c.cfg.TreeID, bucket))
	}
	receipt, err := c.host.Initialize(buckets)
	if err != nil {
		return fmt.Errorf("netsec: initialize host: %w", err)
	}
	if err := c.checkReceipt(receipt, 0, expectedRoot); err != nil {
		return err
	}
	state := &PersistentState{
		Positions: make(map[BlockID]int),
		Root:      receipt.Root,
		Txn:       0,
	}
	if err := c.store.Commit(state); err != nil {
		return fmt.Errorf("netsec: persist genesis state: %w", err)
	}
	return nil
}

// Read performs one ORAM access and returns the latest committed version of
// id.  The returned Block is a copy.
func (c *Client) Read(id BlockID) (Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessLocked(OpRead, id, nil)
}

// Write stores data under id and returns the newly created block.  The
// version is one greater than the latest version observed by the client.
func (c *Client) Write(id BlockID, data []byte) (Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(data) != c.cfg.BlockDataSize {
		return Block{}, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidData, len(data), c.cfg.BlockDataSize)
	}
	return c.accessLocked(OpWrite, id, data)
}

// ReadBlock is an alias for Read.
func (c *Client) ReadBlock(id BlockID) (Block, error) { return c.Read(id) }

// WriteBlock is an alias for Write.
func (c *Client) WriteBlock(id BlockID, data []byte) (Block, error) { return c.Write(id, data) }

func (c *Client) accessLocked(op Operation, id BlockID, data []byte) (Block, error) {
	state, err := c.loadReadyLocked()
	if err != nil {
		return Block{}, err
	}
	if op == OpRead {
		if _, ok := state.Positions[id]; !ok {
			return Block{}, fmt.Errorf("%w: %d", ErrBlockNotFound, id)
		}
	}
	if state.Txn == ^uint64(0) {
		return Block{}, ErrTransactionOverflow
	}
	newLeaf, err := c.drawLeafLocked()
	if err != nil {
		return Block{}, err
	}
	oldLeaf := newLeaf
	if leaf, ok := state.Positions[id]; ok {
		oldLeaf = leaf
	}
	intent := &Intent{
		Tx:        state.Txn + 1,
		Op:        op,
		BlockID:   id,
		OldLeaf:   oldLeaf,
		NewLeaf:   newLeaf,
		WriteData: append([]byte(nil), data...),
	}
	journal := state.Clone()
	journal.Intent = intent.clone()
	journal.Prepared = nil
	if err := c.store.Commit(journal); err != nil {
		return Block{}, fmt.Errorf("netsec: persist transaction intent: %w", err)
	}

	result, err := c.executeIntentLocked(state, intent)
	if err != nil {
		if isAbortable(err) {
			abort := state.Clone()
			abort.Intent = nil
			abort.Prepared = nil
			if commitErr := c.store.Commit(abort); commitErr != nil {
				return Block{}, errors.Join(err, fmt.Errorf("netsec: abort intent: %w", commitErr))
			}
		}
		return Block{}, err
	}
	return result, nil
}

// Recover replays or completes the journaled transaction, if any, and then
// verifies that the server root matches the committed client root.  Replaying
// an old intent never draws a new leaf.
func (c *Client) Recover() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.store.Load()
	if err != nil {
		return fmt.Errorf("netsec: load state: %w", err)
	}
	if state == nil {
		return ErrNotInitialized
	}
	if state.Intent != nil || state.Prepared != nil {
		state, err = c.recoverLocked(state)
		if err != nil {
			return err
		}
	}
	return c.verifyServerAtLocked(state)
}

// Snapshot returns a deep copy of the persisted state.  Positions, Stash,
// Root, and Txn are committed values; Intent/Prepared are journal entries.
func (c *Client) Snapshot() (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.store.Load()
	if err != nil {
		return Snapshot{}, err
	}
	if state == nil {
		return Snapshot{}, ErrNotInitialized
	}
	return Snapshot{
		Positions:   clonePositions(state.Positions),
		Stash:       cloneBlocks(state.Stash),
		Root:        state.Root,
		Txn:         state.Txn,
		Intent:      state.Intent.clone(),
		Prepared:    state.Prepared.clone(),
		Pending:     state.Intent != nil || state.Prepared != nil,
		StashBlocks: len(state.Stash),
	}, nil
}

func (c *Client) loadReadyLocked() (*PersistentState, error) {
	state, err := c.store.Load()
	if err != nil {
		return nil, fmt.Errorf("netsec: load state: %w", err)
	}
	if state == nil {
		return nil, ErrNotInitialized
	}
	if state.Intent != nil || state.Prepared != nil {
		state, err = c.recoverLocked(state)
		if err != nil {
			return nil, err
		}
	}
	if err := c.verifyServerAtLocked(state); err != nil {
		return nil, err
	}
	return state, nil
}

func (c *Client) verifyServerAtLocked(state *PersistentState) error {
	receipt, err := c.host.Root()
	if err != nil {
		return fmt.Errorf("netsec: fetch server root: %w", err)
	}
	if err := c.checkReceiptSignature(receipt); err != nil {
		return err
	}
	switch {
	case receipt.Transaction < state.Txn:
		return fmt.Errorf("%w: server tx %d, committed tx %d", ErrRollback, receipt.Transaction, state.Txn)
	case receipt.Transaction > state.Txn:
		return fmt.Errorf("%w: server tx %d, committed tx %d", ErrUnexpectedTransaction, receipt.Transaction, state.Txn)
	case receipt.Root != state.Root:
		return fmt.Errorf("%w: tx %d", ErrServerStateMismatch, state.Txn)
	}
	return nil
}

func (c *Client) drawLeafLocked() (int, error) {
	if c.cfg.SelectLeaf != nil {
		leaf, err := c.cfg.SelectLeaf(c.geom.leaves)
		if err != nil {
			return 0, err
		}
		if leaf < 0 || leaf >= c.geom.leaves {
			return 0, fmt.Errorf("%w: selected leaf %d", ErrInvalidConfig, leaf)
		}
		return leaf, nil
	}
	var buf [8]byte
	if _, err := io.ReadFull(c.cfg.Rand, buf[:]); err != nil {
		return 0, fmt.Errorf("netsec: read host randomness: %w", err)
	}
	return int(binary.BigEndian.Uint64(buf[:]) % uint64(c.geom.leaves)), nil
}

func (c *Client) executeIntentLocked(state *PersistentState, intent *Intent) (Block, error) {
	tx := intent.Tx
	pathWrite, err := c.geom.pathLeafToRoot(intent.OldLeaf)
	if err != nil {
		return Block{}, err
	}
	pathRead, err := c.geom.pathRootToLeaf(intent.OldLeaf)
	if err != nil {
		return Block{}, err
	}

	oldBuckets := make(map[int]WireBucket, len(pathRead))
	oldDigests := make(map[int][32]byte, len(pathRead))
	pathBlocks := make([]Block, 0)
	for _, node := range pathRead {
		bucket, err := c.host.ReadBucket(tx, node)
		if err != nil {
			return Block{}, fmt.Errorf("netsec: read bucket %d: %w", node, err)
		}
		if bucket.Node != node {
			return Block{}, fmt.Errorf("%w: requested node %d, got %d", ErrIntegrity, node, bucket.Node)
		}
		if bucket.Txn > state.Txn {
			return Block{}, fmt.Errorf("%w: bucket %d from future transaction %d", ErrIntegrity, node, bucket.Txn)
		}
		blocks, err := openBucket(c.cfg, bucket)
		if err != nil {
			return Block{}, err
		}
		oldBuckets[node] = bucket
		oldDigests[node] = BucketDigest(c.cfg.TreeID, bucket)
		pathBlocks = append(pathBlocks, blocks...)
	}

	// The path snapshot must belong to the client's committed tree.  A
	// server that changed the tree while serving the path is rejected before
	// anything is written back.
	if err := c.verifyServerAtLocked(state); err != nil {
		return Block{}, err
	}

	stash := stashMap(state.Stash)
	if err := mergePathBlocks(stash, pathBlocks); err != nil {
		return Block{}, err
	}
	if len(stash) > c.cfg.StashCapacity {
		return Block{}, fmt.Errorf("%w: %d blocks, limit %d", ErrStashOverflow, len(stash), c.cfg.StashCapacity)
	}

	var result Block
	switch intent.Op {
	case OpRead:
		block, ok := stash[intent.BlockID]
		if !ok {
			return Block{}, fmt.Errorf("%w: %d absent from path and stash", ErrBlockNotFound, intent.BlockID)
		}
		result = block.clone()
	case OpWrite:
		baseVersion := uint64(0)
		if existing, ok := stash[intent.BlockID]; ok {
			baseVersion = existing.Version
		}
		if baseVersion == ^uint64(0) {
			return Block{}, ErrTransactionOverflow
		}
		if len(intent.WriteData) != c.cfg.BlockDataSize {
			return Block{}, fmt.Errorf("%w: journaled data length %d", ErrInvalidData, len(intent.WriteData))
		}
		block := Block{
			ID:      intent.BlockID,
			Version: baseVersion + 1,
			Data:    append([]byte(nil), intent.WriteData...),
		}
		block.MAC = blockMAC(c.cfg.MACKey, c.cfg.TreeID, block.ID, block.Version, block.Data)
		stash[block.ID] = block
		result = block.clone()
	default:
		return Block{}, fmt.Errorf("%w: unknown operation", ErrInvalidConfig)
	}
	if len(stash) > c.cfg.StashCapacity {
		return Block{}, fmt.Errorf("%w: %d blocks after %d, limit %d", ErrStashOverflow, len(stash), intent.Op, c.cfg.StashCapacity)
	}

	nextPositions := clonePositions(state.Positions)
	nextPositions[intent.BlockID] = intent.NewLeaf

	plan, err := c.evictLocked(stash, nextPositions, pathWrite)
	if err != nil {
		return Block{}, err
	}
	if len(stash) > c.cfg.StashCapacity {
		return Block{}, fmt.Errorf("%w: %d blocks after eviction, limit %d", ErrStashOverflow, len(stash), c.cfg.StashCapacity)
	}

	newBuckets := make([]WireBucket, 0, len(pathWrite))
	expectedRoot := state.Root
	for _, node := range pathWrite {
		old, ok := oldBuckets[node]
		if !ok {
			return Block{}, fmt.Errorf("%w: missing old bucket %d", ErrIntegrity, node)
		}
		bucket, err := sealBucket(c.cfg, node, old.Generation+1, tx, plan[node])
		if err != nil {
			return Block{}, err
		}
		newBuckets = append(newBuckets, bucket)
		xorRoot(&expectedRoot, oldDigests[node])
		xorRoot(&expectedRoot, BucketDigest(c.cfg.TreeID, bucket))
	}

	prepared := &Prepared{
		Tx:            tx,
		PathLeaf:      intent.OldLeaf,
		PathNodes:     append([]int(nil), pathWrite...),
		Buckets:       make([]WireBucket, len(newBuckets)),
		NewRoot:       expectedRoot,
		NextPositions: nextPositions,
		NextStash:     stashSlice(stash),
	}
	for i := range newBuckets {
		prepared.Buckets[i] = newBuckets[i].clone()
	}
	journal := state.Clone()
	journal.Intent = intent.clone()
	journal.Prepared = prepared
	if err := c.store.Commit(journal); err != nil {
		return Block{}, fmt.Errorf("netsec: persist prepared transaction: %w", err)
	}

	for _, bucket := range newBuckets {
		if err := c.host.StageBucket(tx, bucket); err != nil {
			return Block{}, fmt.Errorf("netsec: stage bucket %d: %w", bucket.Node, err)
		}
	}
	receipt, err := c.host.CommitPath(tx, intent.OldLeaf, pathWrite)
	if err != nil {
		return Block{}, fmt.Errorf("netsec: commit path: %w", err)
	}
	if err := c.checkReceipt(receipt, tx, expectedRoot); err != nil {
		return Block{}, err
	}
	committed := &PersistentState{
		Positions: nextPositions,
		Stash:     stashSlice(stash),
		Root:      expectedRoot,
		Txn:       tx,
	}
	if err := c.store.Commit(committed); err != nil {
		return Block{}, fmt.Errorf("netsec: commit client state: %w", err)
	}
	return result, nil
}

func (c *Client) evictLocked(stash map[BlockID]Block, positions map[BlockID]int, pathLeafToRoot []int) (map[int][]Block, error) {
	plan := make(map[int][]Block, len(pathLeafToRoot))
	ids := make([]BlockID, 0, len(stash))
	for id := range stash {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, node := range pathLeafToRoot {
		for _, id := range ids {
			block, ok := stash[id]
			if !ok {
				continue
			}
			leaf, ok := positions[id]
			if !ok || leaf < 0 || leaf >= c.geom.leaves {
				return nil, fmt.Errorf("%w: block %d has no valid position", ErrIntegrity, id)
			}
			leafNode, err := c.geom.leafNode(leaf)
			if err != nil {
				return nil, err
			}
			if len(plan[node]) >= c.cfg.BucketCapacity || !c.geom.isAncestor(node, leafNode) {
				continue
			}
			plan[node] = append(plan[node], block.clone())
			delete(stash, id)
		}
	}
	return plan, nil
}

func (c *Client) recoverLocked(state *PersistentState) (*PersistentState, error) {
	if state.Prepared != nil {
		return c.recoverPreparedLocked(state)
	}
	if state.Intent != nil {
		return c.recoverIntentLocked(state)
	}
	return state, nil
}

func (c *Client) recoverIntentLocked(state *PersistentState) (*PersistentState, error) {
	if err := c.verifyServerAtLocked(state); err != nil {
		return nil, err
	}
	if _, err := c.executeIntentLocked(state, state.Intent); err != nil {
		if isAbortable(err) {
			abort := state.Clone()
			abort.Intent = nil
			abort.Prepared = nil
			if commitErr := c.store.Commit(abort); commitErr != nil {
				return nil, errors.Join(err, fmt.Errorf("netsec: abort intent: %w", commitErr))
			}
		}
		return nil, err
	}
	return c.store.Load()
}

func (c *Client) recoverPreparedLocked(state *PersistentState) (*PersistentState, error) {
	prepared := state.Prepared
	if prepared.Tx != state.Txn+1 {
		return nil, fmt.Errorf("%w: prepared tx %d, committed tx %d", ErrIntegrity, prepared.Tx, state.Txn)
	}
	receipt, err := c.host.Root()
	if err != nil {
		return nil, fmt.Errorf("netsec: fetch server root during recovery: %w", err)
	}
	if err := c.checkReceiptSignature(receipt); err != nil {
		return nil, err
	}
	switch {
	case receipt.Transaction == prepared.Tx:
		if receipt.Root != prepared.NewRoot {
			return nil, fmt.Errorf("%w: server committed tx %d with forked root", ErrServerStateMismatch, prepared.Tx)
		}
		return c.commitPreparedLocked(prepared)
	case receipt.Transaction == state.Txn:
		if receipt.Root != state.Root {
			return nil, fmt.Errorf("%w: tx %d", ErrServerStateMismatch, state.Txn)
		}
		for _, bucket := range prepared.Buckets {
			if err := c.host.StageBucket(prepared.Tx, bucket); err != nil {
				return nil, fmt.Errorf("netsec: restage bucket %d: %w", bucket.Node, err)
			}
		}
		committedReceipt, err := c.host.CommitPath(prepared.Tx, prepared.PathLeaf, prepared.PathNodes)
		if err != nil {
			return nil, fmt.Errorf("netsec: replay path commit: %w", err)
		}
		if err := c.checkReceipt(committedReceipt, prepared.Tx, prepared.NewRoot); err != nil {
			return nil, err
		}
		return c.commitPreparedLocked(prepared)
	case receipt.Transaction < state.Txn:
		return nil, fmt.Errorf("%w: server tx %d, committed tx %d", ErrRollback, receipt.Transaction, state.Txn)
	default:
		return nil, fmt.Errorf("%w: server tx %d, prepared tx %d", ErrUnexpectedTransaction, receipt.Transaction, prepared.Tx)
	}
}

func (c *Client) commitPreparedLocked(prepared *Prepared) (*PersistentState, error) {
	committed := &PersistentState{
		Positions: clonePositions(prepared.NextPositions),
		Stash:     cloneBlocks(prepared.NextStash),
		Root:      prepared.NewRoot,
		Txn:       prepared.Tx,
	}
	if err := c.store.Commit(committed); err != nil {
		return nil, fmt.Errorf("netsec: persist recovered transaction: %w", err)
	}
	return committed, nil
}

func (c *Client) checkReceipt(receipt RootReceipt, tx uint64, root [32]byte) error {
	if err := c.checkReceiptSignature(receipt); err != nil {
		return err
	}
	switch {
	case receipt.Transaction < tx:
		return fmt.Errorf("%w: replayed receipt tx %d, expected %d", ErrReceipt, receipt.Transaction, tx)
	case receipt.Transaction > tx:
		return fmt.Errorf("%w: future receipt tx %d, expected %d", ErrUnexpectedTransaction, receipt.Transaction, tx)
	case receipt.Root != root:
		return fmt.Errorf("%w: receipt root at tx %d does not match client root", ErrServerStateMismatch, tx)
	}
	return nil
}

func (c *Client) checkReceiptSignature(receipt RootReceipt) error {
	if receipt.TreeID != c.cfg.TreeID {
		return fmt.Errorf("%w: receipt tree ID %q", ErrReceipt, receipt.TreeID)
	}
	if err := c.verify(receipt); err != nil {
		return fmt.Errorf("%w: %v", ErrReceipt, err)
	}
	return nil
}

func (c *Client) validateCommittedState(state *PersistentState) error {
	if state.Positions == nil {
		state.Positions = make(map[BlockID]int)
	}
	if len(state.Stash) > c.cfg.StashCapacity {
		return fmt.Errorf("%w: committed stash has %d blocks, limit %d", ErrStashOverflow, len(state.Stash), c.cfg.StashCapacity)
	}
	for id, leaf := range state.Positions {
		if leaf < 0 || leaf >= c.geom.leaves {
			return fmt.Errorf("%w: block %d position %d", ErrIntegrity, id, leaf)
		}
	}
	seen := make(map[BlockID]struct{}, len(state.Stash))
	for _, block := range state.Stash {
		if _, ok := seen[block.ID]; ok {
			return fmt.Errorf("%w: duplicate block %d in committed stash", ErrIntegrity, block.ID)
		}
		seen[block.ID] = struct{}{}
		if len(block.Data) != c.cfg.BlockDataSize || !block.validMAC(c.cfg.MACKey, c.cfg.TreeID) {
			return fmt.Errorf("%w: invalid committed stash block %d", ErrIntegrity, block.ID)
		}
	}
	return nil
}

func isAbortable(err error) bool {
	return errors.Is(err, ErrStashOverflow) ||
		errors.Is(err, ErrVersionConflict) ||
		errors.Is(err, ErrBlockNotFound) ||
		errors.Is(err, ErrInvalidData) ||
		errors.Is(err, ErrTransactionOverflow)
}
