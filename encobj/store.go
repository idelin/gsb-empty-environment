package encobj

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Operation is one idempotent commit. OpID identifies the operation; the
// content digest of Set/Del is committed alongside it. Replaying an OpID
// with identical content returns the original version; identical OpID with
// different content is ErrConflict.
type Operation struct {
	OpID string
	Set  map[string][]byte
	Del  []string
}

func opDigest(op Operation) [32]byte {
	var e encoder
	names := make([]string, 0, len(op.Set))
	for n := range op.Set {
		names = append(names, n)
	}
	sort.Strings(names)
	e.u64(uint64(len(names)))
	for _, n := range names {
		e.str(n)
		e.bytes(op.Set[n])
	}
	dels := append([]string(nil), op.Del...)
	sort.Strings(dels)
	e.u64(uint64(len(dels)))
	for _, n := range dels {
		e.str(n)
	}
	return sha256.Sum256(e.buf)
}

type opRec struct {
	version uint64
	digest  [32]byte
}

type objState struct {
	mu       sync.Mutex
	id       string
	cur      *currentRecord // nil: does not exist (or deleted)
	m        *manifest
	pinFiles map[string]int // files pinned by live snapshots or staged candidates
}

// pin marks files as referenced so gcObjectLocked cannot reclaim them.
// Callers hold o.mu or take it via pinLocked's wrapper below.
func (o *objState) pin(files ...string) {
	o.mu.Lock()
	for _, f := range files {
		o.pinFiles[f]++
	}
	o.mu.Unlock()
}

func (o *objState) unpin(files ...string) {
	o.mu.Lock()
	for _, f := range files {
		o.pinFiles[f]--
		if o.pinFiles[f] <= 0 {
			delete(o.pinFiles, f)
		}
	}
	o.mu.Unlock()
}

// Store is a single-writer encrypted object store rooted at one directory.
type Store struct {
	st     Storage
	keys   *keyRegistry
	alloc  *nonceAlloc
	unlock func()

	maxNonce uint64

	mu      sync.Mutex
	objs    map[string]*objState
	closed  bool
	crashed bool

	// opsMu guards the idempotency index and in-flight op reservations.
	opsMu   sync.Mutex
	ops     map[string]opRec
	pending map[string]chan struct{}

	// Test hooks (nil in production use).
	hookPersist func(point string) error
	hookEncrypt func(keyID string, nonce [12]byte)
}

type Options struct {
	// Keys are the caller-supplied key ID -> key material mappings,
	// registered before recovery so persisted data can be verified. Keys
	// are never written to the directory or logs.
	Keys map[string][]byte
	// ActiveKey is the key ID used for new commits; must be in Keys.
	ActiveKey string
	// NonceReservationBlock is the high-water reservation interval
	// (default 1024). Smaller values cost one fsync per block.
	NonceReservationBlock uint64
	// MaxNonce is the exclusive counter limit (default 1<<62). Encryption
	// is refused with ErrNonceExhausted once reached.
	MaxNonce uint64
}

// Open takes the writer lock on the directory and recovers persisted state.
func Open(st Storage, opts *Options) (*Store, error) {
	if opts == nil {
		opts = &Options{}
	}
	maxNonce := opts.MaxNonce
	if maxNonce == 0 {
		maxNonce = 1 << 62
	}
	unlock, err := st.Lock()
	if err != nil {
		return nil, err
	}
	s := &Store{
		st:       st,
		keys:     newKeyRegistry(),
		unlock:   unlock,
		maxNonce: maxNonce,
		objs:     map[string]*objState{},
		ops:      map[string]opRec{},
		pending:  map[string]chan struct{}{},
	}
	for id, k := range opts.Keys {
		if err := s.keys.RegisterKey(id, k); err != nil {
			unlock()
			return nil, err
		}
	}
	if opts.ActiveKey != "" {
		if err := s.keys.SetActiveKey(opts.ActiveKey); err != nil {
			unlock()
			return nil, err
		}
	}
	alloc, err := openNonceAlloc(st, opts.NonceReservationBlock, maxNonce)
	if err != nil {
		unlock()
		return nil, err
	}
	alloc.hook = func() error { return s.persistHook("nonce.reserve") }
	s.alloc = alloc
	if err := s.recover(); err != nil {
		unlock()
		return nil, err
	}
	return s, nil
}

func (s *Store) persistHook(point string) error {
	if s.hookPersist != nil {
		if err := s.hookPersist(point); err != nil {
			return err
		}
	}
	s.mu.Lock()
	crashed := s.crashed
	s.mu.Unlock()
	if crashed {
		return ErrCrashed
	}
	return nil
}

func (s *Store) checkLive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrCrashed
	}
	if s.closed {
		return fmt.Errorf("encobj: store closed")
	}
	return nil
}

// Close releases the writer lock.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.unlock()
	return nil
}

func (s *Store) RegisterKey(keyID string, key []byte) error {
	return s.keys.RegisterKey(keyID, key)
}

func (s *Store) SetActiveKey(keyID string) error {
	return s.keys.SetActiveKey(keyID)
}

// RetireKey retires a non-active key once no snapshot references it.
func (s *Store) RetireKey(keyID string) error {
	return s.keys.Retire(keyID)
}

func (s *Store) obj(id string) *objState {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objs[id]
	if !ok {
		o = &objState{id: id, pinFiles: map[string]int{}}
		s.objs[id] = o
	}
	return o
}

// Version returns the committed version of objID (0 if absent).
func (s *Store) Version(objID string) (uint64, error) {
	if err := s.checkLive(); err != nil {
		return 0, err
	}
	o := s.obj(objID)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cur == nil {
		return 0, ErrObjectNotFound
	}
	return o.cur.Version, nil
}

// Commit applies op atomically and returns the committed version.
//
// Protocol (each step durable before the next begins):
//  1. reserve nonces (high-water appended to nonce.log and fsynced)
//  2. write new/changed field ciphertexts (tmp + fsync + rename + dirsync)
//  3. write the new manifest ciphertext the same way
//  4. atomically replace obj-<id>/current  <-- commit point
//  5. append the idempotency record to ops.log and fsync
//  6. update in-memory index, reclaim unreferenced files
//
// A crash before step 4 leaves only the old version visible; a crash after
// leaves only the new one. Orphan files from torn commits are reclaimed on
// recovery. The idempotency record also lives inside the manifest, so a
// crash between 4 and 5 is repaired during recovery.
func (s *Store) Commit(objID string, op Operation) (uint64, error) {
	if err := s.checkLive(); err != nil {
		return 0, err
	}
	if op.OpID == "" {
		return 0, fmt.Errorf("encobj: empty op ID")
	}
	digest := opDigest(op)
	done, rec, err := s.reserveOp(op.OpID, digest)
	if err != nil {
		return 0, err
	}
	if rec != nil {
		return rec.version, nil // idempotent replay
	}
	defer done()

	o := s.obj(objID)
	o.mu.Lock()
	defer o.mu.Unlock()

	keyID, ke, err := s.keys.active()
	if err != nil {
		return 0, err
	}

	var version uint64 = 1
	var nextFID uint64
	old := map[string]fieldRef{}
	if o.cur != nil {
		version = o.cur.Version + 1
		nextFID = o.m.NextFID
		for _, f := range o.m.Fields {
			old[f.Name] = f
		}
	}

	// Determine the new field set.
	del := map[string]bool{}
	for _, n := range op.Del {
		del[n] = true
	}
	type pending struct {
		name    string
		value   []byte
		ref     fieldRef
		encrypt bool
	}
	var news []pending
	final := map[string]fieldRef{}
	for n, f := range old {
		if del[n] {
			continue
		}
		if _, ok := op.Set[n]; !ok {
			final[n] = f // unchanged: share old ciphertext
		}
	}
	names := make([]string, 0, len(op.Set))
	for n := range op.Set {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, existed := old[n]
		if !existed {
			f = fieldRef{Name: n, FieldID: nextFID, Revision: 0}
			nextFID++
		}
		f.Revision++
		f.KeyID = keyID
		news = append(news, pending{name: n, value: op.Set[n], ref: f, encrypt: true})
	}

	// Step 1+2: encrypt and write changed fields.
	for i := range news {
		p := &news[i]
		nonce, err := s.alloc.Next()
		if err != nil {
			return 0, err
		}
		ad := fieldAD(objID, p.ref.FieldID, p.ref.Revision, keyID)
		body := sealFile(fieldMagic, keyID, nonce[:], p.value, ad, ke.aead)
		if s.hookEncrypt != nil {
			s.hookEncrypt(keyID, nonce)
		}
		p.ref.File = fieldFileName(p.ref.FieldID, p.ref.Revision, body)
		p.ref.Digest = sha256.Sum256(body)
		if err := s.persistHook("field.write"); err != nil {
			return 0, err
		}
		if err := s.st.WriteFileAtomic(objPrefix(objID)+p.ref.File, body); err != nil {
			return 0, err
		}
		final[p.name] = p.ref
	}

	// Step 3: manifest.
	m := &manifest{
		Version:  version,
		KeyID:    keyID,
		OpID:     op.OpID,
		OpDigest: digest,
		NextFID:  nextFID,
	}
	for _, n := range sortedKeys(final) {
		m.Fields = append(m.Fields, final[n])
	}
	mnonce, err := s.alloc.Next()
	if err != nil {
		return 0, err
	}
	mbody := sealFile(manifestMagic, keyID, mnonce[:], m.encode(), manifestAD(objID, version, keyID), ke.aead)
	if s.hookEncrypt != nil {
		s.hookEncrypt(keyID, mnonce)
	}
	mfile := manifestFileName(version, mbody)
	if err := s.persistHook("manifest.write"); err != nil {
		return 0, err
	}
	if err := s.st.WriteFileAtomic(objPrefix(objID)+mfile, mbody); err != nil {
		return 0, err
	}

	// Step 4: commit point.
	cur := &currentRecord{Version: version, ManifestFile: mfile, ManifestDigest: sha256.Sum256(mbody)}
	if err := s.persistHook("current.write"); err != nil {
		return 0, err
	}
	if err := s.st.WriteFileAtomic(objPrefix(objID)+"current", encodeCurrent(cur)); err != nil {
		return 0, err
	}

	// Step 5: idempotency index.
	if err := s.persistHook("ops.append"); err != nil {
		return 0, err
	}
	if err := s.st.AppendFile("ops.log", encodeOpRec(version, op.OpID, digest)); err != nil {
		return 0, err
	}

	// Step 6: in-memory index + reclamation.
	s.opsMu.Lock()
	s.ops[op.OpID] = opRec{version: version, digest: digest}
	s.opsMu.Unlock()
	oldFiles := filesOf(o.cur, o.m)
	o.cur, o.m = cur, m
	s.gcObjectLocked(o, oldFiles)
	return version, nil
}

// reserveOp resolves idempotency before any work happens. It returns a
// non-nil opRec for a completed replay, or a done func that publishes the
// result and wakes waiters. A concurrent commit reusing the same op ID
// waits for the first to finish, then replays or conflicts.
func (s *Store) reserveOp(opID string, digest [32]byte) (func(), *opRec, error) {
	s.opsMu.Lock()
	for {
		if rec, ok := s.ops[opID]; ok {
			s.opsMu.Unlock()
			if rec.digest == digest {
				return nil, &rec, nil
			}
			return nil, nil, ErrConflict
		}
		if ch, ok := s.pending[opID]; ok {
			s.opsMu.Unlock()
			<-ch
			s.opsMu.Lock()
			continue
		}
		ch := make(chan struct{})
		s.pending[opID] = ch
		s.opsMu.Unlock()
		return func() {
			s.opsMu.Lock()
			delete(s.pending, opID)
			close(ch)
			s.opsMu.Unlock()
		}, nil, nil
	}
}

func sortedKeys(m map[string]fieldRef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func filesOf(cur *currentRecord, m *manifest) []string {
	if cur == nil || m == nil {
		return nil
	}
	out := []string{cur.ManifestFile}
	for _, f := range m.Fields {
		out = append(out, f.File)
	}
	return out
}

// gcObjectLocked removes on-disk files of the object that are neither
// referenced by the current manifest nor pinned by a live snapshot —
// superseded ciphertexts, torn-commit orphans and injected strangers.
// Allocated nonces are never reclaimed. Best effort: leftovers are retried
// on the next commit and on recovery.
func (s *Store) gcObjectLocked(o *objState, oldFiles []string) {
	keep := map[string]bool{}
	if o.cur != nil {
		keep[o.cur.ManifestFile] = true
		for _, f := range o.m.Fields {
			keep[f.File] = true
		}
	}
	for f := range o.pinFiles {
		keep[f] = true
	}
	candidates := map[string]bool{}
	for _, f := range oldFiles {
		candidates[f] = true
	}
	if names, err := s.st.ReadFileNames(); err == nil {
		prefix := objPrefix(o.id)
		for _, n := range names {
			if strings.HasPrefix(n, prefix) {
				_, file, ok := splitObjFile(n)
				// ".tmp-" staging files belong to in-flight atomic
				// writes; recovery sweeps them, runtime GC must not.
				if ok && file != "current" && !strings.Contains(file, ".tmp-") {
					candidates[file] = true
				}
			}
		}
	}
	var victims []string
	for f := range candidates {
		if !keep[f] {
			victims = append(victims, f)
		}
	}
	if len(victims) == 0 {
		return
	}
	if err := s.persistHook("gc"); err != nil {
		return
	}
	for _, f := range victims {
		s.st.RemoveFile(objPrefix(o.id) + f)
	}
	s.st.SyncDir()
}

// Delete removes the object. Files pinned by live snapshots survive until
// the last snapshot closes; the object becomes invisible immediately.
func (s *Store) Delete(objID string) error {
	if err := s.checkLive(); err != nil {
		return err
	}
	o := s.obj(objID)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cur == nil {
		return ErrObjectNotFound
	}
	if err := s.persistHook("delete.current"); err != nil {
		return err
	}
	if err := s.st.RemoveFile(objPrefix(objID) + "current"); err != nil {
		return err
	}
	if err := s.st.SyncDir(); err != nil {
		return err
	}
	oldFiles := filesOf(o.cur, o.m)
	o.cur, o.m = nil, nil
	s.gcObjectLocked(o, oldFiles)
	return nil
}
