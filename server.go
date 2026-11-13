package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
)

// Host is the storage side of the protocol.
//
// Initialize supplies the client-generated genesis buckets.  Path writes are
// staged one bucket at a time and then committed atomically by CommitPath;
// nodes are in leaf-to-root order.
type Host interface {
	TreeID() string
	Initialize(buckets []WireBucket) (RootReceipt, error)
	Root() (RootReceipt, error)
	ReadBucket(tx uint64, node int) (WireBucket, error)
	StageBucket(tx uint64, bucket WireBucket) error
	CommitPath(tx uint64, leaf int, nodes []int) (RootReceipt, error)
}

// ServerFaults injects the malicious behaviors exercised by the tests.
// Pointer fields distinguish "not set" from node 0 / transaction 0.
type ServerFaults struct {
	FailInitialize bool
	FailRoot       bool
	FailCommit     bool
	FailReadNode   *int
	FailStageNode  *int

	// DropWriteNode removes one staged bucket from the commit.  If
	// DropWriteReturnsReceipt is true, the server nevertheless commits the
	// remaining buckets, advances its transaction, and returns a valid
	// signed receipt for the resulting (forked) tree.
	DropWriteNode           *int
	DropWriteReturnsReceipt bool

	// ReplayReceiptTx makes CommitPath return an old valid receipt after
	// applying the new path.
	ReplayReceiptTx *uint64
}

// AccessRecord records paths visible to the server.  Read records have Kind
// "read" and one Node; path commits have Kind "commit", a leaf, and Nodes in
// leaf-to-root order.
type AccessRecord struct {
	Kind  string
	Tx    uint64
	Node  int
	Leaf  int
	Nodes []int
}

// CommitRecord describes one accepted path write.
type CommitRecord struct {
	Tx    uint64
	Leaf  int
	Nodes []int
	Root  [32]byte
}

type serverSnapshot struct {
	txn     uint64
	root    [32]byte
	buckets map[int]WireBucket
	receipt RootReceipt
}

// MemoryServer is an in-memory malicious storage host.  It can roll back to
// any committed transaction, delete buckets, drop a path write, or replay an
// old signed receipt.
type MemoryServer struct {
	mu sync.Mutex

	treeID string
	geom   treeGeometry
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey

	initialized bool
	txn         uint64
	root        [32]byte
	buckets     map[int]WireBucket
	pending     map[uint64]map[int]WireBucket

	history  map[uint64]*serverSnapshot
	receipts map[uint64]RootReceipt
	access   []AccessRecord
	commits  []CommitRecord
	faults   ServerFaults
}

// NewMemoryServer creates an uninitialized server with a fresh ed25519
// signing key.
func NewMemoryServer(treeID string, height int) (*MemoryServer, error) {
	if height < 2 || height > 10 {
		return nil, fmt.Errorf("%w: height %d outside [2,10]", ErrInvalidConfig, height)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return NewMemoryServerWithKey(treeID, height, priv)
}

// NewMemoryServerWithKey creates an uninitialized server with a caller
// supplied signing key.
func NewMemoryServerWithKey(treeID string, height int, priv ed25519.PrivateKey) (*MemoryServer, error) {
	if treeID == "" {
		return nil, fmt.Errorf("%w: empty tree ID", ErrInvalidConfig)
	}
	if height < 2 || height > 10 {
		return nil, fmt.Errorf("%w: height %d outside [2,10]", ErrInvalidConfig, height)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: bad ed25519 private key", ErrInvalidConfig)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: bad ed25519 public key", ErrInvalidConfig)
	}
	g := newTreeGeometry(height)
	return &MemoryServer{
		treeID:   treeID,
		geom:     g,
		priv:     append(ed25519.PrivateKey(nil), priv...),
		pub:      append(ed25519.PublicKey(nil), pub...),
		buckets:  make(map[int]WireBucket, g.nodes),
		pending:  make(map[uint64]map[int]WireBucket),
		history:  make(map[uint64]*serverSnapshot),
		receipts: make(map[uint64]RootReceipt),
	}, nil
}

// TreeID implements Host.
func (s *MemoryServer) TreeID() string { return s.treeID }

// PublicKey returns the root-receipt verification key.
func (s *MemoryServer) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), s.pub...)
}

// SetFaults installs malicious-server behavior.  Passing the zero value
// clears all faults.
func (s *MemoryServer) SetFaults(f ServerFaults) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = f
}

// Initialize implements Host.  The client supplies every genesis bucket so
// the server never needs the client's encryption key.
func (s *MemoryServer) Initialize(buckets []WireBucket) (RootReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.FailInitialize {
		return RootReceipt{}, errInjectedServer
	}
	if s.initialized {
		return RootReceipt{}, ErrAlreadyInitialized
	}
	if len(buckets) != s.geom.nodes {
		return RootReceipt{}, fmt.Errorf("%w: got %d genesis buckets, want %d", ErrInvalidConfig, len(buckets), s.geom.nodes)
	}
	next := make(map[int]WireBucket, len(buckets))
	for _, bucket := range buckets {
		if bucket.Node < 0 || bucket.Node >= s.geom.nodes {
			return RootReceipt{}, fmt.Errorf("%w: genesis node %d out of range", ErrInvalidConfig, bucket.Node)
		}
		if bucket.Txn != 0 || bucket.Generation != 0 {
			return RootReceipt{}, fmt.Errorf("%w: genesis bucket has non-zero generation/transaction", ErrInvalidConfig)
		}
		if _, exists := next[bucket.Node]; exists {
			return RootReceipt{}, fmt.Errorf("%w: duplicate genesis node %d", ErrInvalidConfig, bucket.Node)
		}
		next[bucket.Node] = bucket.clone()
	}
	if len(next) != s.geom.nodes {
		return RootReceipt{}, fmt.Errorf("%w: incomplete genesis tree", ErrInvalidConfig)
	}
	s.buckets = next
	s.initialized = true
	s.txn = 0
	s.root = s.recomputeRootLocked()
	receipt, err := s.signLocked(s.txn, s.root)
	if err != nil {
		return RootReceipt{}, err
	}
	s.receipts[0] = receipt
	s.history[0] = s.snapshotLocked(receipt)
	return receipt.clone(), nil
}

// Root returns a freshly signed receipt for the current tree.
func (s *MemoryServer) Root() (RootReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.FailRoot {
		return RootReceipt{}, errInjectedServer
	}
	if !s.initialized {
		return RootReceipt{}, ErrNotInitialized
	}
	return s.signLocked(s.txn, s.root)
}

// ReadBucket implements Host.
func (s *MemoryServer) ReadBucket(tx uint64, node int) (WireBucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return WireBucket{}, ErrNotInitialized
	}
	if s.faults.FailReadNode != nil && *s.faults.FailReadNode == node {
		return WireBucket{}, errInjectedServer
	}
	if node < 0 || node >= s.geom.nodes {
		return WireBucket{}, fmt.Errorf("%w: node %d out of range", ErrInvalidConfig, node)
	}
	bucket, ok := s.buckets[node]
	if !ok {
		return WireBucket{}, fmt.Errorf("%w: node %d", ErrMissingBucket, node)
	}
	s.access = append(s.access, AccessRecord{Kind: "read", Tx: tx, Node: node})
	return bucket.clone(), nil
}

// StageBucket implements Host.  Staged writes do not become visible until
// CommitPath.
func (s *MemoryServer) StageBucket(tx uint64, bucket WireBucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return ErrNotInitialized
	}
	if s.faults.FailStageNode != nil && *s.faults.FailStageNode == bucket.Node {
		return errInjectedServer
	}
	if tx != s.txn+1 {
		return fmt.Errorf("%w: stage tx %d, server tx %d", ErrUnexpectedTransaction, tx, s.txn)
	}
	if bucket.Txn != tx {
		return fmt.Errorf("%w: bucket txn %d does not match stage tx %d", ErrIntegrity, bucket.Txn, tx)
	}
	if bucket.Node < 0 || bucket.Node >= s.geom.nodes {
		return fmt.Errorf("%w: node %d out of range", ErrInvalidConfig, bucket.Node)
	}
	live, ok := s.buckets[bucket.Node]
	if !ok {
		return fmt.Errorf("%w: node %d", ErrMissingBucket, bucket.Node)
	}
	if bucket.Generation != live.Generation+1 {
		return fmt.Errorf("%w: node %d generation %d, live %d", ErrGeneration, bucket.Node, bucket.Generation, live.Generation)
	}
	if s.pending[tx] == nil {
		s.pending[tx] = make(map[int]WireBucket)
	}
	if existing, ok := s.pending[tx][bucket.Node]; ok {
		if !existing.equal(bucket) {
			return fmt.Errorf("%w: conflicting staged bucket for node %d", ErrIntegrity, bucket.Node)
		}
		return nil
	}
	s.pending[tx][bucket.Node] = bucket.clone()
	return nil
}

// CommitPath implements Host.  The path must be exactly the pre-drawn leaf's
// path in leaf-to-root order.  All staged buckets are applied atomically and
// the resulting root is signed.
func (s *MemoryServer) CommitPath(tx uint64, leaf int, nodes []int) (RootReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return RootReceipt{}, ErrNotInitialized
	}
	if s.faults.FailCommit {
		return RootReceipt{}, errInjectedServer
	}
	if tx != s.txn+1 {
		return RootReceipt{}, fmt.Errorf("%w: commit tx %d, server tx %d", ErrUnexpectedTransaction, tx, s.txn)
	}
	expected, err := s.geom.pathLeafToRoot(leaf)
	if err != nil {
		return RootReceipt{}, err
	}
	if !equalIntSlices(expected, nodes) {
		return RootReceipt{}, fmt.Errorf("%w: commit path is not the pre-drawn leaf path", ErrIncompletePathWrite)
	}
	staged := s.pending[tx]
	if len(staged) == 0 {
		return RootReceipt{}, ErrIncompletePathWrite
	}

	dropNode := -1
	if s.faults.DropWriteNode != nil {
		dropNode = *s.faults.DropWriteNode
	}
	if dropNode >= 0 {
		if _, ok := staged[dropNode]; !ok {
			return RootReceipt{}, ErrIncompletePathWrite
		}
		if !s.faults.DropWriteReturnsReceipt {
			return RootReceipt{}, ErrDroppedWrite
		}
	}

	toApply := make(map[int]WireBucket, len(nodes))
	for _, node := range nodes {
		bucket, ok := staged[node]
		if !ok {
			return RootReceipt{}, fmt.Errorf("%w: no staged bucket for node %d", ErrIncompletePathWrite, node)
		}
		live, ok := s.buckets[node]
		if !ok {
			return RootReceipt{}, fmt.Errorf("%w: node %d", ErrMissingBucket, node)
		}
		if bucket.Node != node || bucket.Txn != tx || bucket.Generation != live.Generation+1 {
			return RootReceipt{}, fmt.Errorf("%w: inconsistent staged bucket for node %d", ErrIntegrity, node)
		}
		if node != dropNode {
			toApply[node] = bucket
		}
	}
	for node, bucket := range toApply {
		s.buckets[node] = bucket.clone()
	}
	s.txn = tx
	s.root = s.recomputeRootLocked()
	receipt, err := s.signLocked(s.txn, s.root)
	if err != nil {
		return RootReceipt{}, err
	}
	s.receipts[tx] = receipt
	s.history[tx] = s.snapshotLocked(receipt)
	s.commits = append(s.commits, CommitRecord{
		Tx:    tx,
		Leaf:  leaf,
		Nodes: append([]int(nil), nodes...),
		Root:  s.root,
	})
	s.access = append(s.access, AccessRecord{
		Kind:  "commit",
		Tx:    tx,
		Leaf:  leaf,
		Nodes: append([]int(nil), nodes...),
	})
	delete(s.pending, tx)

	if s.faults.ReplayReceiptTx != nil {
		old, ok := s.receipts[*s.faults.ReplayReceiptTx]
		if !ok {
			return RootReceipt{}, fmt.Errorf("%w: no receipt for tx %d", ErrReceipt, *s.faults.ReplayReceiptTx)
		}
		return old.clone(), nil
	}
	return receipt.clone(), nil
}

// RollbackTo restores the complete tree as it existed after transaction tx.
// Transaction 0 is the genesis tree.
func (s *MemoryServer) RollbackTo(tx uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.history[tx]
	if !ok {
		return fmt.Errorf("%w: no historical tree at tx %d", ErrRollback, tx)
	}
	s.buckets = cloneBucketMap(snap.buckets)
	s.txn = snap.txn
	s.root = snap.root
	s.pending = make(map[uint64]map[int]WireBucket)
	return nil
}

// DeleteBucket removes one live bucket.  The server transaction number is
// unchanged, so the signed root necessarily changes.
func (s *MemoryServer) DeleteBucket(node int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return ErrNotInitialized
	}
	if _, ok := s.buckets[node]; !ok {
		return fmt.Errorf("%w: node %d", ErrMissingBucket, node)
	}
	delete(s.buckets, node)
	s.root = s.recomputeRootLocked()
	delete(s.receipts, s.txn)
	return nil
}

// SetBucketRaw installs a server-visible bucket without client involvement
// and recomputes the (now inconsistent) root.  Tests use it to inject
// authenticated but semantically invalid buckets.
func (s *MemoryServer) SetBucketRaw(node int, bucket WireBucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return ErrNotInitialized
	}
	if node < 0 || node >= s.geom.nodes || bucket.Node != node {
		return fmt.Errorf("%w: bad node", ErrInvalidConfig)
	}
	s.buckets[node] = bucket.clone()
	s.root = s.recomputeRootLocked()
	return nil
}

// Bucket returns a copy of a live bucket.
func (s *MemoryServer) Bucket(node int) (WireBucket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.buckets[node]
	return bucket.clone(), ok
}

// Receipt returns the historical receipt for tx, if any.
func (s *MemoryServer) Receipt(tx uint64) (RootReceipt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[tx]
	return receipt.clone(), ok
}

// AccessLog returns a copy of every read and accepted path commit.
func (s *MemoryServer) AccessLog() []AccessRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AccessRecord, len(s.access))
	for i, record := range s.access {
		out[i] = record
		out[i].Nodes = append([]int(nil), record.Nodes...)
	}
	return out
}

// CommitLog returns a copy of every accepted path commit.
func (s *MemoryServer) CommitLog() []CommitRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CommitRecord, len(s.commits))
	for i, record := range s.commits {
		out[i] = record
		out[i].Nodes = append([]int(nil), record.Nodes...)
	}
	return out
}

// ServerTxn reports the server's current monotonic transaction number.
func (s *MemoryServer) ServerTxn() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txn
}

// HistoryTransactions lists the transaction numbers available to
// RollbackTo.
func (s *MemoryServer) HistoryTransactions() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint64, 0, len(s.history))
	for tx := range s.history {
		out = append(out, tx)
	}
	sortUint64(out)
	return out
}

func (s *MemoryServer) signLocked(tx uint64, root [32]byte) (RootReceipt, error) {
	receipt := RootReceipt{TreeID: s.treeID, Transaction: tx, Root: root}
	return SignRootReceipt(s.priv, receipt)
}

func (s *MemoryServer) recomputeRootLocked() [32]byte {
	var root [32]byte
	for node := 0; node < s.geom.nodes; node++ {
		if bucket, ok := s.buckets[node]; ok {
			xorRoot(&root, BucketDigest(s.treeID, bucket))
		}
	}
	return root
}

func (s *MemoryServer) snapshotLocked(receipt RootReceipt) *serverSnapshot {
	return &serverSnapshot{
		txn:     s.txn,
		root:    s.root,
		buckets: cloneBucketMap(s.buckets),
		receipt: receipt.clone(),
	}
}

func cloneBucketMap(in map[int]WireBucket) map[int]WireBucket {
	out := make(map[int]WireBucket, len(in))
	for node, bucket := range in {
		out[node] = bucket.clone()
	}
	return out
}

func equalIntSlices(a, b []int) bool {
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

func sortUint64(values []uint64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

var errInjectedServer = errors.New("netsec: injected server failure")
