package netsec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StageHook is invoked after a stage has become durable.  A non-nil error is
// treated like an injected crash: stages before Commit are rolled back, while
// a durable Commit remains for recovery to complete.
type StageHook func(ctx context.Context, stage Stage, txID TxID) error

// Options configures a Host.
type Options struct {
	Root       string
	HMACKey    []byte
	Authorizer Authorizer
	Applier    PatchApplier
	Store      ObjectStore

	// NoAutoRecover leaves recovered-but-unpublished transactions for an
	// explicit Recover call.  The default is to recover during Open.
	NoAutoRecover bool

	OnStage StageHook
	Now     func() time.Time
}

// WriteRequest is an online authorized operation.  OldMetadataDigest is the
// exact CAS precondition observed by the caller; a zero value means "observe
// the current digest while holding the object lock".  Patch is the serialized
// metadata patch interpreted by the configured PatchApplier.  A nil Data
// slice means metadata-only operation; an empty non-nil slice is a zero-length
// data write.
type WriteRequest struct {
	TxID                 TxID
	PrincipalID          PrincipalID
	CredentialGeneration uint64
	Object               ObjectRef
	Operation            Operation
	Params               []byte
	OldMetadataDigest    Digest
	Patch                []byte
	Data                 []byte
}

// Receipt is returned only after Commit is durable and publication has
// completed.
type Receipt struct {
	TxID                TxID       `json:"txID"`
	Identity            TxIdentity `json:"identity"`
	AuthReceiptDigest   Digest     `json:"authReceiptDigest"`
	ChainSeq            uint64     `json:"chainSeq"`
	ChainDigest         Digest     `json:"chainDigest"`
	Published           bool       `json:"published"`
	OldMetadataDigest   Digest     `json:"oldMetadataDigest"`
	NewMetadataDigest   Digest     `json:"newMetadataDigest"`
	AuthorizationPolicy Digest     `json:"authorizationPolicy"`
}

// RecoveryReport describes how a log prefix was reconciled with object
// storage.  A transaction appears in exactly one of Completed, RolledBack or
// Conflicts.
type RecoveryReport struct {
	CheckpointFound   bool     `json:"checkpointFound"`
	CheckpointSeq     uint64   `json:"checkpointSeq"`
	TrustedThroughSeq uint64   `json:"trustedThroughSeq"`
	TornTail          bool     `json:"tornTail"`
	Isolated          bool     `json:"isolated"`
	Completed         []TxID   `json:"completed"`
	RolledBack        []TxID   `json:"rolledBack"`
	Conflicts         []TxID   `json:"conflicts"`
	Errors            []string `json:"errors"`
}

// Host is an importable file-host transaction engine.  All online writes are
// serialized through one mutex; recovery never calls the authorizer.
type Host struct {
	root           string
	logPath        string
	checkpointPath string
	key            []byte
	authorizer     Authorizer
	applier        PatchApplier
	store          ObjectStore
	j              *journal
	onStage        StageHook
	now            func() time.Time

	mu          sync.Mutex
	closed      bool
	txIDs       map[TxID]struct{}
	cp          Checkpoint
	cpFound     bool
	cpIssue     error
	report      RecoveryReport
	healthy     bool
	healthErr   error
	recoveryErr error
}

// Open is NewHost with an explicit root directory.
func Open(root string, opts Options) (*Host, error) {
	opts.Root = root
	return NewHost(opts)
}

func NewHost(opts Options) (*Host, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("netsec: root directory is required")
	}
	if len(opts.HMACKey) == 0 {
		return nil, ErrHMACKey
	}
	if err := os.MkdirAll(opts.Root, 0o700); err != nil {
		return nil, err
	}
	h := &Host{
		root:           opts.Root,
		logPath:        filepath.Join(opts.Root, "auth.log"),
		checkpointPath: filepath.Join(opts.Root, "checkpoint"),
		key:            append([]byte(nil), opts.HMACKey...),
		authorizer:     opts.Authorizer,
		applier:        opts.Applier,
		store:          opts.Store,
		onStage:        opts.OnStage,
		now:            opts.Now,
		txIDs:          make(map[TxID]struct{}),
	}
	if h.applier == nil {
		h.applier = ReplacePatch{}
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.store == nil {
		store, err := NewFileStore(filepath.Join(opts.Root, "objects"), filepath.Join(opts.Root, "staging"))
		if err != nil {
			return nil, err
		}
		h.store = store
	}
	j, err := openJournalRaw(h.logPath, h.key)
	if err != nil {
		return nil, err
	}
	h.j = j
	for _, rec := range j.trustedRecords() {
		h.txIDs[rec.Identity.TxID] = struct{}{}
	}
	cp, found, cpErr := loadCheckpoint(h.checkpointPath, h.key)
	h.cp, h.cpFound, h.cpIssue = cp, found, cpErr
	if !opts.NoAutoRecover {
		_, recErr := h.recoverLocked(context.Background())
		if recErr != nil {
			return h, recErr
		}
		if _, err := j.health(); err != nil {
			return h, err
		}
	} else {
		h.refreshHealth()
	}
	return h, nil
}

func (h *Host) refreshHealth() {
	ok, err := h.j.health()
	h.healthy, h.healthErr = ok, err
}

// Close releases the log file.
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	return h.j.close()
}

func (h *Host) LogPath() string        { return h.logPath }
func (h *Host) CheckpointPath() string { return h.checkpointPath }

// Healthy reports whether the chain is complete and authenticated.  A torn
// tail is repaired at open; a complete bad record or fork is not.
func (h *Host) Healthy() (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.j.health()
}

func (h *Host) Report() RecoveryReport {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneReport(h.report)
}

func cloneReport(in RecoveryReport) RecoveryReport {
	out := in
	out.Completed = append([]TxID(nil), in.Completed...)
	out.RolledBack = append([]TxID(nil), in.RolledBack...)
	out.Conflicts = append([]TxID(nil), in.Conflicts...)
	out.Errors = append([]string(nil), in.Errors...)
	return out
}

// Execute performs one online authorized transaction:
//
//	auth -> Prepare -> durable data -> DataStable -> staged metadata CAS ->
//	Commit -> publish data -> publish metadata.
//
// A transaction without a durable complete Commit never publishes data or
// metadata.
func (h *Host) Execute(ctx context.Context, req WriteRequest) (Receipt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return Receipt{}, ErrClosed
	}
	if _, err := h.j.health(); err != nil {
		return Receipt{}, err
	}
	if h.recoveryErr != nil {
		return Receipt{}, h.recoveryErr
	}
	if err := validateWriteRequest(req); err != nil {
		return Receipt{}, err
	}
	if _, exists := h.txIDs[req.TxID]; exists {
		return Receipt{}, fmt.Errorf("%w: %q", ErrTxIDExists, req.TxID)
	}
	if h.authorizer == nil {
		return Receipt{}, ErrNoAuthorizer
	}
	current, err := h.store.CurrentGeneration(ctx, req.Object.ID)
	if err != nil {
		return Receipt{}, err
	}
	if current != req.Object.Generation {
		return Receipt{}, fmt.Errorf("%w: object %q current=%d requested=%d", ErrObjectGenerationChanged, req.Object.ID, current, req.Object.Generation)
	}
	snapshot, err := h.store.Read(ctx, req.Object)
	if err != nil {
		return Receipt{}, err
	}
	oldDigest := req.OldMetadataDigest
	if oldDigest.IsZero() {
		oldDigest = snapshot.MetadataDigest
	}
	if snapshot.MetadataDigest != oldDigest {
		return Receipt{}, fmt.Errorf("%w: expected %s, observed %s", ErrMetadataConflict, oldDigest, snapshot.MetadataDigest)
	}
	newMetadata, err := h.applier.Apply(snapshot.Metadata, req.Patch)
	if err != nil {
		return Receipt{}, err
	}
	dataDigest := ZeroDigest
	if req.Data != nil {
		dataDigest = DigestBytes(req.Data)
	}
	identity := TxIdentity{
		TxID:                 req.TxID,
		PrincipalID:          req.PrincipalID,
		CredentialGeneration: req.CredentialGeneration,
		PolicyDigest:         ZeroDigest,
		ObjectID:             req.Object.ID,
		ObjectGeneration:     req.Object.Generation,
		Operation:            req.Operation,
		ParamsDigest:         DigestBytes(req.Params),
		PatchDigest:          DigestBytes(req.Patch),
		OldMetadataDigest:    oldDigest,
		NewMetadataDigest:    DigestBytes(newMetadata),
		DataDigest:           dataDigest,
	}
	auth, err := h.authorizer.Authorize(ctx, identity)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	if auth.HasCredentialGeneration && auth.CredentialGeneration != req.CredentialGeneration {
		return Receipt{}, fmt.Errorf("%w: %v", ErrUnauthorized, GenerationMismatchError(req.PrincipalID, req.CredentialGeneration, auth.CredentialGeneration))
	}
	if !auth.Allowed {
		return Receipt{}, fmt.Errorf("%w: principal %q operation %q", ErrUnauthorized, req.PrincipalID, req.Operation)
	}
	identity.PolicyDigest = auth.PolicyDigest
	receiptDigest, err := ComputeAuthReceiptDigest(identity, auth.PolicyDigest, true)
	if err != nil {
		return Receipt{}, err
	}
	base := Record{
		Identity:          identity,
		AuthReceiptDigest: receiptDigest,
		Patch:             append([]byte(nil), req.Patch...),
	}

	h.txIDs[req.TxID] = struct{}{}
	prepare := base
	prepare.Stage = StagePrepare
	_, _, err = h.j.append(prepare)
	if err != nil {
		delete(h.txIDs, req.TxID)
		return Receipt{}, err
	}
	if err := h.callStage(ctx, StagePrepare, req.TxID); err != nil {
		return Receipt{}, h.abortBeforeCommit(ctx, req.TxID, err)
	}

	if req.Data != nil {
		if _, err := h.store.StageData(ctx, req.TxID, req.Object, req.Data, dataDigest); err != nil {
			return Receipt{}, h.abortBeforeCommit(ctx, req.TxID, err)
		}
	}

	dataStable := base
	dataStable.Stage = StageDataStable
	_, _, err = h.j.append(dataStable)
	if err != nil {
		return Receipt{}, h.abortBeforeCommit(ctx, req.TxID, err)
	}
	if err := h.callStage(ctx, StageDataStable, req.TxID); err != nil {
		return Receipt{}, h.abortBeforeCommit(ctx, req.TxID, err)
	}

	stagedMeta, err := h.store.StageMetadata(ctx, req.TxID, req.Object, oldDigest, identity.NewMetadataDigest, newMetadata)
	if err != nil {
		return Receipt{}, h.abortBeforeCommit(ctx, req.TxID, err)
	}
	_ = stagedMeta

	commit := base
	commit.Stage = StageCommit
	committed, commitDigest, err := h.j.append(commit)
	if err != nil {
		return Receipt{}, h.abortBeforeCommit(ctx, req.TxID, err)
	}
	if err := h.callStage(ctx, StageCommit, req.TxID); err != nil {
		return Receipt{}, errors.Join(ErrCommittedPending, err)
	}
	if dataDigest != ZeroDigest {
		if err := h.store.PublishData(ctx, req.TxID, req.Object, dataDigest); err != nil {
			return Receipt{}, fmt.Errorf("%w: publish data: %v", ErrCommittedPending, err)
		}
	}
	if err := h.store.PublishMetadata(ctx, req.TxID, req.Object, oldDigest, identity.NewMetadataDigest, newMetadata); err != nil {
		return Receipt{}, fmt.Errorf("%w: publish metadata: %v", ErrCommittedPending, err)
	}
	if err := h.callStage(ctx, StagePublished, req.TxID); err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrCommittedPending, err)
	}
	return Receipt{
		TxID:                req.TxID,
		Identity:            identity,
		AuthReceiptDigest:   receiptDigest,
		ChainSeq:            committed.Seq,
		ChainDigest:         commitDigest,
		Published:           true,
		OldMetadataDigest:   oldDigest,
		NewMetadataDigest:   identity.NewMetadataDigest,
		AuthorizationPolicy: auth.PolicyDigest,
	}, nil
}

func validateWriteRequest(req WriteRequest) error {
	if req.TxID == "" {
		return fmt.Errorf("%w: empty txID", ErrInvalidRequest)
	}
	if req.PrincipalID == "" {
		return fmt.Errorf("%w: empty principalID", ErrInvalidRequest)
	}
	if req.Object.ID == "" {
		return fmt.Errorf("%w: empty objectID", ErrInvalidRequest)
	}
	if req.Operation == "" {
		return fmt.Errorf("%w: empty operation", ErrInvalidRequest)
	}
	return nil
}

func (h *Host) callStage(ctx context.Context, stage Stage, txID TxID) error {
	if h.onStage == nil {
		return nil
	}
	return h.onStage(ctx, stage, txID)
}

func (h *Host) abortBeforeCommit(ctx context.Context, txID TxID, cause error) error {
	if err := h.store.Rollback(ctx, txID); err != nil {
		return errors.Join(cause, fmt.Errorf("netsec: rollback staging: %w", err))
	}
	return cause
}

// Recover reconciles the authenticated log suffix with object storage.  It
// never calls the authorizer and never resolves a principal by name.
func (h *Host) Recover(ctx context.Context) (RecoveryReport, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return cloneReport(h.report), ErrClosed
	}
	return h.recoverLocked(ctx)
}

// Checkpoint records a host-authenticated trusted prefix after ensuring all
// currently logged transactions are completed or rolled back.
func (h *Host) Checkpoint(ctx context.Context) (Checkpoint, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return Checkpoint{}, ErrClosed
	}
	if _, err := h.j.health(); err != nil {
		return Checkpoint{}, err
	}
	if _, err := h.recoverLocked(ctx); err != nil {
		return Checkpoint{}, err
	}
	seq, head := h.j.position()
	cp, err := appendCheckpoint(h.checkpointPath, h.key, Checkpoint{
		Version:   1,
		Seq:       seq,
		Head:      head,
		CreatedAt: h.now().UTC(),
	})
	if err != nil {
		return Checkpoint{}, err
	}
	h.cp, h.cpFound, h.cpIssue = cp, true, nil
	return cp, nil
}

// CreateObject administratively seeds or advances an object generation.
// Ordinary authorized writes still go through Execute.
func (h *Host) CreateObject(ctx context.Context, ref ObjectRef, metadata, data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrClosed
	}
	creator, ok := h.store.(ObjectCreator)
	if !ok {
		return fmt.Errorf("netsec: object store does not support CreateObject")
	}
	return creator.CreateObject(ctx, ref, metadata, data)
}

func (h *Host) ReadObject(ctx context.Context, ref ObjectRef) (ObjectSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ObjectSnapshot{}, ErrClosed
	}
	return h.store.Read(ctx, ref)
}

func (h *Host) ReadObjectByID(ctx context.Context, id ObjectID) (ObjectSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ObjectSnapshot{}, ErrClosed
	}
	gen, err := h.store.CurrentGeneration(ctx, id)
	if err != nil {
		return ObjectSnapshot{}, err
	}
	return h.store.Read(ctx, ObjectRef{ID: id, Generation: gen})
}
