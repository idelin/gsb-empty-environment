package lfs

// Persisted snapshot record inside a checkpoint.
type snapshotState struct {
	Name    string
	Mapping map[NATKey]NATEntry
}

// Per-segment persisted bookkeeping.
type segState struct {
	ID          int
	Active      bool
	Hot         bool
	Summary     []Summary
	WriteTimeNs []int64
}

// cpState is the complete serializable checkpoint payload.
type cpState struct {
	Version   uint64
	LastSeq   uint64
	NextSeq   uint64
	NextVer   uint64
	NAT       map[NATKey]NATEntry
	Snapshots []snapshotState
	Segments  []segState
	FreeSegs  []int
	Reserve   []int
	HotCur    int
	ColdCur   int
}

// SaveCheckpoint durably persists all filesystem metadata into the next
// alternating CP slot. Each slot carries head and tail records whose CRCs and
// version numbers both protect the same state blob.
func (fs *FS) SaveCheckpoint() (version uint64, slot int, err error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.saveCheckpointLocked()
}

func (fs *FS) saveCheckpointLocked() (uint64, int, error) {
	state := cpState{
		Version: fs.cpVersion + 1,
		LastSeq: fs.nextSeq - 1,
		NextSeq: fs.nextSeq,
		NextVer: fs.nextVer,
		NAT:     make(map[NATKey]NATEntry, len(fs.nat)),
		HotCur:  fs.hotCur,
		ColdCur: fs.coldCur,
	}
	for key, entry := range fs.nat {
		state.NAT[key] = entry
	}
	names := make([]string, 0, len(fs.snaps))
	for name := range fs.snaps {
		names = append(names, name)
	}
	// Stable snapshot order for reproducible encoding.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	for _, name := range names {
		snap := fs.snaps[name]
		state.Snapshots = append(state.Snapshots, snapshotState{
			Name:    name,
			Mapping: snap.Mapping,
		})
	}
	state.Segments = make([]segState, len(fs.segments))
	for id, seg := range fs.segments {
		state.Segments[id] = segState{
			ID:          seg.ID,
			Active:      seg.Active,
			Hot:         seg.Hot,
			Summary:     append([]Summary(nil), seg.Summary...),
			WriteTimeNs: append([]int64(nil), seg.WriteTimeNs...),
		}
	}
	state.FreeSegs = append([]int(nil), fs.freeSegs...)
	state.Reserve = append([]int(nil), fs.reserve...)

	raw, err := writeCPSlot(state.Version, state)
	if err != nil {
		return 0, 0, err
	}
	slot := fs.cpSlot
	fs.dev.writeCPSlot(slot, raw)

	fs.cpVersion = state.Version
	fs.cpLastSeq = state.LastSeq
	fs.cpSlot = 1 - fs.cpSlot // alternate CP0/CP1
	return state.Version, slot, nil
}

// CheckpointVersion reports the newest persisted checkpoint version (0 if none).
func (fs *FS) CheckpointVersion() uint64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.cpVersion
}

// loadBestCheckpoint reads both CP slots and returns the highest-version slot
// that passes head/tail CRC validation. A torn newer slot transparently falls
// back to the older valid slot.
func loadBestCheckpoint(dev *Device) (cpState, int, error) {
	type parsed struct {
		slot       int
		version    uint64
		stateFrame []byte
	}
	var valid []parsed
	for slot := 0; slot < 2; slot++ {
		version, frame, err := readCPSlot(dev.readCPRaw(slot))
		if err != nil {
			continue
		}
		valid = append(valid, parsed{slot: slot, version: version, stateFrame: frame})
	}
	if len(valid) == 0 {
		return cpState{}, 0, ErrNoCheckpoint
	}
	best := valid[0]
	for _, p := range valid[1:] {
		if p.version > best.version {
			best = p
		}
	}
	var state cpState
	if err := decodeFrame(best.stateFrame, &state); err != nil {
		return cpState{}, 0, ErrCorruptCheckpoint
	}
	return state, best.slot, nil
}

// RecoveryReport describes a roll-forward recovery.
type RecoveryReport struct {
	CPVersion      uint64
	CPSlot         int
	BaseSeq        uint64 // highest sequence covered by the checkpoint
	ReplayedNodes  int
	ReplayedFsyncs int
	StopSeq        uint64 // next expected sequence when replay stopped
	StopReason     string
	DroppedTail    int // node blocks beyond the stop point (never resurrected)
}

// Recover rebuilds a filesystem from a device after a crash: it selects the
// newest fully valid CP slot and rolls forward only the continuous node chain
// whose fsync markers and checksums are intact.
func Recover(dev *Device, clock Clock) (*FS, RecoveryReport, error) {
	if clock == nil {
		clock = WallClock
	}
	state, bestSlot, err := loadBestCheckpoint(dev)
	if err != nil {
		return nil, RecoveryReport{}, err
	}

	fs := &FS{
		dev:       dev,
		blocks:    dev.BlocksPerSeg,
		clock:     clock,
		segments:  make([]*Segment, dev.SegNumber()),
		hotCur:    state.HotCur,
		coldCur:   state.ColdCur,
		nextSeq:   state.NextSeq,
		nextVer:   state.NextVer,
		cpSlot:    int(state.Version % 2),
		cpVersion: state.Version,
		cpLastSeq: state.LastSeq,
		nat:       map[NATKey]NATEntry{},
		snaps:     map[string]*Snapshot{},
		lastSync:  map[NodeID]uint64{},
	}
	fs.freeSegs = append(fs.freeSegs, state.FreeSegs...)
	fs.reserve = append(fs.reserve, state.Reserve...)
	for id, ss := range state.Segments {
		fs.segments[id] = &Segment{
			ID:          ss.ID,
			Active:      ss.Active,
			Hot:         ss.Hot,
			Summary:     append([]Summary(nil), ss.Summary...),
			WriteTimeNs: append([]int64(nil), ss.WriteTimeNs...),
		}
	}
	for key, entry := range state.NAT {
		fs.nat[key] = entry
	}
	for _, ss := range state.Snapshots {
		fs.snaps[ss.Name] = &Snapshot{Name: ss.Name, Mapping: ss.Mapping}
	}

	report := RecoveryReport{
		CPVersion:  state.Version,
		CPSlot:     bestSlot,
		BaseSeq:    state.LastSeq,
		StopSeq:    state.LastSeq + 1,
		StopReason: StopComplete,
	}

	// Index every node block above the checkpoint baseline by sequence number.
	type located struct {
		addr PhysAddr
		node Node
		bad  bool // frame present but checksum/torn
	}
	bySeq := map[uint64]located{}
	badSeqs := map[uint64]bool{}
	maxSeq := state.LastSeq
	for seg := 0; seg < dev.SegNumber(); seg++ {
		for slot := 0; slot < dev.BlocksPerSeg; slot++ {
			addr := PhysAddr{seg, slot}
			raw := dev.rawNode(addr)
			if raw == nil {
				continue
			}
			node, derr := dev.ReadNode(addr)
			if derr != nil {
				// Torn frame: best-effort decode the payload to learn its sequence
				// so replay can stop at the exact checksum-fault position.
				if seq, ok := peekSeq(raw); ok && seq > state.LastSeq {
					badSeqs[seq] = true
					if seq > maxSeq {
						maxSeq = seq
					}
				}
				continue
			}
			if node.Seq > state.LastSeq {
				bySeq[node.Seq] = located{addr: addr, node: node}
				if node.Seq > maxSeq {
					maxSeq = node.Seq
				}
			}
		}
	}
	// Torn frames must participate in ordering so replay stops at their exact
	// sequence rather than misclassifying them as a gap.
	for seq := range badSeqs {
		bySeq[seq] = located{bad: true}
	}

	// Replay the strictly continuous sequence chain. Tentative data updates are
	// only committed to the NAT when an fsync marker is reached; anything after a
	// gap/checksum error/non-fsync tail is discarded and never resurrected.
	tentative := map[NATKey]NATEntry{}
	for seq := state.LastSeq + 1; seq <= maxSeq; seq++ {
		loc, ok := bySeq[seq]
		if !ok {
			report.StopSeq = seq
			report.StopReason = StopSequenceGap
			break
		}
		if loc.bad {
			report.StopSeq = seq
			report.StopReason = StopChecksum
			break
		}
		node := loc.node
		if node.Seq != seq {
			report.StopSeq = seq
			report.StopReason = StopSequenceGap
			break
		}
		if node.Kind == KindFsync {
			if !node.Fsync {
				report.StopSeq = seq
				report.StopReason = StopChecksum
				break
			}
			for key, entry := range tentative {
				fs.nat[key] = entry
			}
			tentative = map[NATKey]NATEntry{}
			fs.lastSync[node.Ino] = seq
			report.ReplayedFsyncs++
			report.ReplayedNodes++
			report.StopSeq = seq + 1
			continue
		}
		tentative[NATKey{node.Ino, node.BlkIdx}] = NATEntry{
			Addr: loc.addr, Ver: node.Ver, Hot: node.Hot,
		}
		report.ReplayedNodes++
		report.StopSeq = seq + 1
	}

	// Uncommitted tentative blocks are non-fsync tail writes: drop them.
	if len(tentative) > 0 && report.StopReason == StopComplete {
		report.StopReason = StopNonFsyncTail
	}
	report.DroppedTail = len(tentative)

	// Sweep every occupied block that is unreachable after replay: overwritten
	// old versions, non-fsync orphan tails and torn frames. They must never be
	// resurrected, and removing them keeps block conservation exact.
	live := fs.liveAddrs()
	for seg := 0; seg < dev.SegNumber(); seg++ {
		for slot := 0; slot < dev.BlocksPerSeg; slot++ {
			addr := PhysAddr{seg, slot}
			if dev.BlockOccupied(addr) && !live[addr] {
				dev.WipeBlock(addr)
			}
		}
	}

	// Reconcile segment allocation metadata against actual frames.
	for id, seg := range fs.segments {
		used := fs.usedSlots(seg)
		if used == 0 && !seg.Active {
			continue
		}
		if used == 0 && seg.Active {
			seg.Active = false
			if fs.hotCur == id {
				fs.hotCur = -1
			}
			if fs.coldCur == id {
				fs.coldCur = -1
			}
		}
	}
	return fs, report, nil
}

// peekSeq best-effort extracts the sequence number from a raw frame, ignoring
// checksum integrity, so a torn record can still be located by its header.
func peekSeq(raw []byte) (uint64, bool) {
	return nodeFrameSeq(raw)
}
