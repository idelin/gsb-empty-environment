package netsec

// Install outcomes reported by the reducer. The facade maps them onto the
// exported manifest errors.
const (
	installApplied = iota
	installRedundant
	installRejectedObsolete
	installRejectedConflict
)

type slotState struct {
	verdict CheckVerdict
	filled  bool
}

type genState struct {
	manifest Manifest
	// checkSet maps the manifest's complete check set for O(1) membership.
	checkSet  map[CheckID]struct{}
	slots     [][]slotState
	outstand  int
	terminal  bool
	status    Status
	reason    Reason
	positives []SlotRef
	failures  []SlotRef
}

// Stats counts events observed by the reducer. Counters are primarily useful
// in tests to prove that stale or duplicate events were ignored.
type Stats struct {
	ManifestsApplied   int
	ManifestsRedundant int
	ManifestsObsolete  int
	ManifestsConflict  int
	ReceiptsAccepted   int
	ReceiptsDuplicate  int
	ReceiptsStale      int
	ReceiptsUnknown    int
	TimeoutsApplied    int
	TimeoutsStale      int
	CancelsApplied     int
	CancelsStale       int
}

// machine is a single-goroutine, event-sourced reducer. All state transitions
// happen through apply* methods, which makes ordering tests deterministic.
type machine struct {
	current *genState
	stats   Stats
}

func newMachine() *machine {
	return &machine{}
}

func newGenState(manifest Manifest) *genState {
	checkSet := make(map[CheckID]struct{}, len(manifest.Checks))
	for _, check := range manifest.Checks {
		checkSet[check] = struct{}{}
	}
	slots := make([][]slotState, len(manifest.Chunks))
	for i := range slots {
		slots[i] = make([]slotState, len(manifest.Checks))
	}
	return &genState{
		manifest: manifest,
		checkSet: checkSet,
		slots:    slots,
		outstand: len(manifest.Chunks) * len(manifest.Checks),
		status:   StatusIncomplete,
		reason:   ReasonAwaitingReceipts,
	}
}

func (m *machine) generation() int {
	if m.current == nil {
		return -1
	}
	return m.current.manifest.Generation
}

func (m *machine) terminal() bool {
	return m.current != nil && m.current.terminal
}

// applyManifest installs a manifest generation. A newer generation resets all
// fragment state; the same generation is idempotent when identical.
func (m *machine) applyManifest(manifest Manifest) int {
	switch {
	case m.current == nil || manifest.Generation > m.current.manifest.Generation:
		m.current = newGenState(manifest)
		m.stats.ManifestsApplied++
		return installApplied
	case manifest.Generation == m.current.manifest.Generation:
		if m.current.manifest.equal(manifest) {
			m.stats.ManifestsRedundant++
			return installRedundant
		}
		m.stats.ManifestsConflict++
		return installRejectedConflict
	default:
		m.stats.ManifestsObsolete++
		return installRejectedObsolete
	}
}

// applyReceipt applies one detector receipt. It reports false when the receipt
// is stale, targets an unknown slot/check, or arrives after the terminal state.
func (m *machine) applyReceipt(receipt Receipt) bool {
	if m.current == nil {
		m.stats.ReceiptsUnknown++
		return false
	}
	state := m.current
	if receipt.Generation != state.manifest.Generation {
		m.stats.ReceiptsStale++
		return false
	}
	if state.terminal {
		// Replays for the current generation after completion are duplicates.
		m.stats.ReceiptsDuplicate++
		return false
	}
	if receipt.ChunkIndex >= len(state.slots) {
		m.stats.ReceiptsUnknown++
		return false
	}
	checkIndex := -1
	for i, check := range state.manifest.Checks {
		if check == receipt.Check {
			checkIndex = i
			break
		}
	}
	if checkIndex < 0 {
		m.stats.ReceiptsUnknown++
		return false
	}

	slot := &state.slots[receipt.ChunkIndex][checkIndex]
	if slot.filled {
		// Repeated receipt for an already settled slot: keep the first one.
		m.stats.ReceiptsDuplicate++
		return false
	}

	slot.filled = true
	slot.verdict = receipt.Verdict
	state.outstand--
	m.stats.ReceiptsAccepted++

	ref := SlotRef{ChunkIndex: receipt.ChunkIndex, Check: receipt.Check}
	switch receipt.Verdict {
	case VerdictPositive:
		state.positives = append(state.positives, ref)
		m.terminate(StatusBlocked, ReasonPositive)
	case VerdictFailed:
		state.failures = append(state.failures, ref)
		m.terminate(StatusIncomplete, ReasonCheckFailed)
	default:
		if state.outstand == 0 {
			// Every chunk completed every check with no positive: publish.
			m.terminate(StatusPass, ReasonAllClear)
		}
	}
	return true
}

// applyTimeout ends the generation as incomplete unless it already ended.
// Timeouts for a superseded generation are ignored.
func (m *machine) applyTimeout(generation int) bool {
	if m.current == nil || generation != m.current.manifest.Generation {
		m.stats.TimeoutsStale++
		return false
	}
	if m.current.terminal {
		m.stats.TimeoutsStale++
		return false
	}
	m.stats.TimeoutsApplied++
	m.terminate(StatusIncomplete, ReasonTimeout)
	return true
}

// applyCancel ends the generation as incomplete. Cancels for a superseded
// generation are ignored.
func (m *machine) applyCancel(generation int) bool {
	if m.current == nil || generation != m.current.manifest.Generation {
		m.stats.CancelsStale++
		return false
	}
	if m.current.terminal {
		m.stats.CancelsStale++
		return false
	}
	m.stats.CancelsApplied++
	m.terminate(StatusIncomplete, ReasonCancelled)
	return true
}

func (m *machine) terminate(status Status, reason Reason) {
	m.current.terminal = true
	m.current.status = status
	m.current.reason = reason
}

func (m *machine) missing() []SlotRef {
	if m.current == nil {
		return nil
	}
	state := m.current
	var missing []SlotRef
	for chunkIndex, chunkSlots := range state.slots {
		for checkIndex, slot := range chunkSlots {
			if !slot.filled {
				missing = append(missing, SlotRef{
					ChunkIndex: chunkIndex,
					Check:      state.manifest.Checks[checkIndex],
				})
			}
		}
	}
	return missing
}

// decision snapshots the current state. Non-terminal snapshots report
// Incomplete with the outstanding slots, as required while the object waits.
func (m *machine) decision() Decision {
	if m.current == nil {
		return Decision{
			Status:     StatusIncomplete,
			Reason:     ReasonAwaitingManifest,
			Generation: -1,
			Terminal:   false,
		}
	}
	state := m.current
	decision := Decision{
		Status:     state.status,
		Reason:     state.reason,
		Generation: state.manifest.Generation,
		Terminal:   state.terminal,
		Missing:    m.missing(),
	}
	if len(state.positives) > 0 {
		decision.Positives = append([]SlotRef(nil), state.positives...)
	}
	if len(state.failures) > 0 {
		decision.Failures = append([]SlotRef(nil), state.failures...)
	}
	return decision
}
