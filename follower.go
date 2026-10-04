package snapcatchup

import (
	"encoding/json"
	"fmt"
)

const (
	keyInstalled       = "installed/meta"
	keyInstalledData   = "installed/data"
	keyInstalling      = "installing"
	stagingPrefix      = "staging/"
	keyStagingMeta     = "staging/meta"
	stagingChunkPrefix = "staging/chunks/"
)

// Follower receives snapshot chunks, installs them atomically once the whole
// snapshot is consistent, and afterwards replays only the log entries that
// came after the snapshot boundary.
type Follower struct {
	store        Store
	sm           *StateMachine
	installedGen uint64
	lastIndex    uint64
	lastTerm     uint64
	stagingGen   uint64
	stagingMeta  SnapshotMeta
	received     map[int]uint32 // chunk seq -> checksum, for the staging generation
	hasStaging   bool
}

func NewFollower(store Store) *Follower {
	return &Follower{store: store, sm: NewStateMachine(), received: make(map[int]uint32)}
}

func (f *Follower) StateMachine() *StateMachine { return f.sm }
func (f *Follower) LastIndex() uint64           { return f.lastIndex }
func (f *Follower) InstalledGeneration() uint64 { return f.installedGen }

// ReceiveChunk stages one snapshot chunk. It reports true when the chunk
// completed a full, consistent snapshot that was then installed. Stale,
// duplicated or corrupt chunks never touch local state.
func (f *Follower) ReceiveChunk(c Chunk) (bool, error) {
	// Stale chunk: already installed, or older than the generation being staged.
	if c.Meta.Generation <= f.installedGen || (f.hasStaging && c.Meta.Generation < f.stagingGen) {
		return false, nil
	}
	if !c.valid() {
		return false, fmt.Errorf("follower: chunk %d checksum mismatch", c.Seq)
	}
	if !f.hasStaging || c.Meta.Generation > f.stagingGen {
		// A newer generation supersedes any partial transfer.
		if err := f.resetStaging(c.Meta); err != nil {
			return false, err
		}
	}
	if c.Meta != f.stagingMeta {
		return false, fmt.Errorf("follower: inconsistent meta for generation %d", c.Meta.Generation)
	}
	if c.Seq < 0 || c.Seq >= c.Meta.NumChunks {
		return false, fmt.Errorf("follower: chunk seq %d out of range", c.Seq)
	}
	if sum, ok := f.received[c.Seq]; ok {
		if sum != c.Checksum {
			return false, fmt.Errorf("follower: conflicting chunk %d for generation %d", c.Seq, c.Meta.Generation)
		}
		// Duplicate chunk (e.g. a retransmitted last chunk): idempotent no-op.
		return false, nil
	}
	key := fmt.Sprintf("%s%06d", stagingChunkPrefix, c.Seq)
	if err := f.store.Put(key, c.Data); err != nil {
		return false, err
	}
	f.received[c.Seq] = c.Checksum
	if len(f.received) == f.stagingMeta.NumChunks {
		if err := f.install(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// Replay applies only log entries after the snapshot boundary. Entries at or
// below LastIndex are already reflected in the installed snapshot and are
// skipped.
func (f *Follower) Replay(entries []Entry) {
	for _, e := range entries {
		if e.Index <= f.lastIndex {
			continue
		}
		f.sm.Apply(e.Cmd)
		f.lastIndex = e.Index
		f.lastTerm = e.Term
	}
}

// Recover restores follower state from the durable store after a restart.
// A committed install is loaded as-is; an install interrupted between the
// begin and commit boundaries is rolled forward from the staged chunks when
// they are still complete and consistent, otherwise the partial transfer is
// discarded so a fresh one can start.
func (f *Follower) Recover() error {
	f.sm = NewStateMachine()
	f.received = make(map[int]uint32)
	f.hasStaging = false

	installed, err := f.store.Get(keyInstalled)
	if err != nil {
		return err
	}
	if installed != nil {
		var meta SnapshotMeta
		if err := json.Unmarshal(installed, &meta); err != nil {
			return err
		}
		data, err := f.store.Get(keyInstalledData)
		if err != nil {
			return err
		}
		sm, err := DecodeStateMachine(data)
		if err != nil {
			return err
		}
		f.sm = sm
		f.installedGen = meta.Generation
		f.lastIndex = meta.LastIndex
		f.lastTerm = meta.LastTerm
	}

	marker, err := f.store.Get(keyInstalling)
	if err != nil {
		return err
	}
	if marker != nil {
		var meta SnapshotMeta
		if err := json.Unmarshal(marker, &meta); err != nil {
			return err
		}
		if meta.Generation > f.installedGen && f.stagingIntact(meta) {
			// Roll the interrupted install forward.
			f.stagingMeta = meta
			f.stagingGen = meta.Generation
			return f.install()
		}
		// Staging is incomplete or corrupt: roll the interrupted install back.
		if err := f.store.Delete(keyInstalling); err != nil {
			return err
		}
	}
	// Any leftover staging belongs to an unfinished transfer; drop it.
	if err := f.store.DeletePrefix(stagingPrefix); err != nil {
		return err
	}
	f.stagingGen = f.installedGen
	return nil
}

func (f *Follower) resetStaging(meta SnapshotMeta) error {
	if err := f.store.DeletePrefix(stagingPrefix); err != nil {
		return err
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := f.store.Put(keyStagingMeta, b); err != nil {
		return err
	}
	f.stagingGen = meta.Generation
	f.stagingMeta = meta
	f.received = make(map[int]uint32)
	f.hasStaging = true
	return nil
}

// install replaces local state with the staged snapshot. The "installing"
// marker is the install boundary: it is written before any state change and
// removed only after the installed meta and payload are durable, so a crash
// in between is rolled forward by Recover.
func (f *Follower) install() error {
	data, err := f.assemble()
	if err != nil {
		return err
	}
	marker, err := json.Marshal(f.stagingMeta)
	if err != nil {
		return err
	}
	if err := f.store.Put(keyInstalling, marker); err != nil { // begin boundary
		return err
	}
	sm, err := DecodeStateMachine(data)
	if err != nil {
		return err
	}
	if err := f.store.Put(keyInstalledData, data); err != nil {
		return err
	}
	if err := f.store.Put(keyInstalled, marker); err != nil { // commit boundary
		return err
	}
	if err := f.store.Delete(keyInstalling); err != nil {
		return err
	}
	if err := f.store.DeletePrefix(stagingPrefix); err != nil {
		return err
	}
	f.sm = sm
	f.installedGen = f.stagingMeta.Generation
	f.lastIndex = f.stagingMeta.LastIndex
	f.lastTerm = f.stagingMeta.LastTerm
	f.stagingGen = f.installedGen
	f.hasStaging = false
	f.received = make(map[int]uint32)
	return nil
}

// assemble concatenates the staged chunks and verifies the whole-snapshot
// checksum. State is replaced only when every chunk is present and the
// combined payload matches the meta checksum.
func (f *Follower) assemble() ([]byte, error) {
	var data []byte
	for seq := 0; seq < f.stagingMeta.NumChunks; seq++ {
		key := fmt.Sprintf("%s%06d", stagingChunkPrefix, seq)
		b, err := f.store.Get(key)
		if err != nil {
			return nil, err
		}
		if b == nil {
			return nil, fmt.Errorf("follower: missing chunk %d", seq)
		}
		data = append(data, b...)
	}
	if checksum(data) != f.stagingMeta.Checksum {
		return nil, fmt.Errorf("follower: snapshot checksum mismatch")
	}
	return data, nil
}

func (f *Follower) stagingIntact(meta SnapshotMeta) bool {
	saved := f.stagingMeta
	f.stagingMeta = meta
	_, err := f.assemble()
	f.stagingMeta = saved
	return err == nil
}
