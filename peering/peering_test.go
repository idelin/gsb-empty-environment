package peering

import "testing"

func ev(epoch Epoch, version uint64) EVersion {
	return EVersion{Epoch: epoch, Version: version}
}

func le(epoch Epoch, version uint64, object ObjectID, op OpType) LogEntry {
	return LogEntry{Version: ev(epoch, version), Object: object, Op: op}
}

func replica(id OSDID, alive bool, log PGLog, objects map[ObjectID]EVersion) OSDState {
	return OSDState{ID: id, Alive: alive, Log: log, Objects: objects}
}

func assertVersion(t *testing.T, label string, got, want EVersion) {
	t.Helper()
	if got.Compare(want) != 0 {
		t.Fatalf("%s = %s, want %s", label, got, want)
	}
}

func assertIDs(t *testing.T, label string, got, want []OSDID) {
	t.Helper()
	if !setsEqual(got, want) || len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// TestBuildPastIntervalsAndWitness covers interval merging, the
// active+min_size maybe_went_rw rule and the witness lookup.
func TestBuildPastIntervalsAndWitness(t *testing.T) {
	epochs := []OSDMapEpoch{
		{Epoch: 1, Up: []OSDID{1, 2}, Acting: []OSDID{1, 2}, MinSize: 2, Active: true},
		{Epoch: 2, Up: []OSDID{1, 2}, Acting: []OSDID{1, 2}, MinSize: 2, Active: true},
		{Epoch: 3, Up: []OSDID{1, 2, 3}, Acting: []OSDID{1, 2, 3}, MinSize: 2, Active: false},
		{Epoch: 4, Up: []OSDID{1}, Acting: []OSDID{1}, MinSize: 2, Active: true},
	}

	intervals := BuildPastIntervals(epochs)
	if len(intervals) != 3 {
		t.Fatalf("expected 3 intervals, got %d: %+v", len(intervals), intervals)
	}
	if intervals[0].First != 1 || intervals[0].Last != 2 || !intervals[0].MaybeWentRW {
		t.Fatalf("interval A not merged/writable: %+v", intervals[0])
	}
	if intervals[1].MaybeWentRW {
		t.Fatalf("inactive interval must not be maybe_went_rw: %+v", intervals[1])
	}
	if intervals[2].MaybeWentRW {
		t.Fatalf("under-min_size interval must not be maybe_went_rw: %+v", intervals[2])
	}

	if missing := FindUnwitnessedInterval(intervals, []OSDID{3}); missing == nil ||
		missing.First != 1 {
		t.Fatalf("expected interval A to be unwitnessed when only OSD 3 probed")
	}
	if missing := FindUnwitnessedInterval(intervals, []OSDID{1}); missing != nil {
		t.Fatalf("OSD 1 witnesses interval A, expected no gap: %+v", missing)
	}
}

// TestHistoryGapBlocksIncomplete: acceptance case 1. A writable historical
// interval whose members all went missing must force Incomplete and never
// allow activation.
func TestHistoryGapBlocksIncomplete(t *testing.T) {
	history := []OSDMapEpoch{
		{Epoch: 1, Up: []OSDID{1, 2}, Acting: []OSDID{1, 2}, MinSize: 2, Active: true},
		{Epoch: 2, Up: []OSDID{1, 2}, Acting: []OSDID{1, 2}, MinSize: 2, Active: true},
		{Epoch: 3, Up: []OSDID{3, 4}, Acting: []OSDID{3, 4}, MinSize: 2, Active: true},
	}

	baseLog := PGLog{
		Entries:          []LogEntry{le(1, 1, "objA", OpWrite)},
		Tail:             ev(1, 1),
		LastUpdate:       ev(1, 1),
		LastComplete:     ev(1, 1),
		LastEpochStarted: 1,
	}
	pg := NewPG("pg.0", 2, []OSDID{1, 2}, history,
		replica(1, true, baseLog, map[ObjectID]EVersion{"objA": ev(1, 1)}),
		replica(2, true, baseLog, map[ObjectID]EVersion{"objA": ev(1, 1)}),
	)

	result := pg.Peer()
	if result.State != StateIncomplete {
		t.Fatalf("expected Incomplete, got %s", result.State)
	}
	if result.Phase != PhaseHistoryGate || result.BlockedReason != ReasonHistoryGap {
		t.Fatalf("expected history-gate block, phase=%s reason=%q",
			result.Phase, result.BlockedReason)
	}
	if result.Unwitnessed == nil || !setsEqual(result.Unwitnessed.Acting, []OSDID{3, 4}) {
		t.Fatalf("expected unwitnessed interval on {3,4}, got %+v", result.Unwitnessed)
	}
	if result.CanWrite || result.Recovery != nil {
		t.Fatalf("Incomplete PG must not be writable or build a recovery plan")
	}
}

// TestDivergentLogRollback: acceptance case 2. The isolated old primary has
// the highest last_update but a stale last_epoch_started; its post-partition
// entries are divergent and truncated instead of being trusted.
func TestDivergentLogRollback(t *testing.T) {
	history := []OSDMapEpoch{
		{Epoch: 1, Up: []OSDID{1, 2}, Acting: []OSDID{1, 2}, MinSize: 2, Active: true},
	}

	isolatedLog := PGLog{
		Entries: []LogEntry{
			le(10, 1, "objA", OpWrite),
			le(10, 2, "objB", OpWrite),
			le(11, 3, "objC", OpWrite), // written while isolated, minority only
			le(11, 4, "objA", OpWrite), // uncommitted overwrite
		},
		Tail:             ev(10, 1),
		LastUpdate:       ev(11, 4),
		LastComplete:     ev(11, 4),
		LastEpochStarted: 10,
	}
	survivorLog := PGLog{
		Entries: []LogEntry{
			le(10, 1, "objA", OpWrite),
			le(10, 2, "objB", OpWrite),
		},
		Tail:             ev(10, 1),
		LastUpdate:       ev(10, 2),
		LastComplete:     ev(10, 2),
		LastEpochStarted: 11,
	}
	committed := map[ObjectID]EVersion{"objA": ev(10, 1), "objB": ev(10, 2)}

	pg := NewPG("pg.1", 2, []OSDID{1, 2}, history,
		replica(1, true, isolatedLog, committed),
		replica(2, true, survivorLog, committed),
	)
	result := pg.Peer()

	if result.State != StateActive || !result.CanWrite {
		t.Fatalf("expected active+writable, state=%s reason=%q",
			result.State, result.BlockedReason)
	}
	if result.Merge.AuthOSD != 2 {
		t.Fatalf("auth log must be OSD 2 (LES 11), got OSD %d despite lower last_update",
			result.Merge.AuthOSD)
	}

	if len(result.Merge.Divergent) != 2 {
		t.Fatalf("expected 2 divergent priors, got %+v", result.Merge.Divergent)
	}
	for _, divergent := range result.Merge.Divergent {
		if divergent.OSD != 1 || divergent.Kind != DivergentAfterLES {
			t.Fatalf("unexpected divergent entry: %+v", divergent)
		}
	}

	rollback := result.Merge.RolledBack[1]
	if rollback == nil {
		t.Fatalf("expected rollback record for OSD 1")
	}
	assertVersion(t, "rolled-back head", rollback.Head, ev(10, 2))

	assertVersion(t, "objA authoritative version", result.Merge.AuthLatest["objA"].Version, ev(10, 1))
	assertVersion(t, "objB authoritative version", result.Merge.AuthLatest["objB"].Version, ev(10, 2))
	if _, present := result.Merge.AuthLatest["objC"]; present {
		t.Fatalf("isolated objC must not survive into the authoritative log")
	}
}

// TestDivergentClassification covers same-version forks and entries unknown to
// the authoritative log, beyond the after-LES case.
func TestDivergentClassification(t *testing.T) {
	// Exercise the classifier directly so the three failure modes are
	// deterministic regardless of which log wins authoritative selection.
	authByVersion := map[EVersion]LogEntry{
		ev(10, 1): le(10, 1, "objA", OpWrite),
	}
	peer := OSDState{Log: PGLog{LastEpochStarted: 10}}

	fork := classifyDivergent(le(10, 1, "objX", OpWrite), peer, authByVersion)
	if fork != DivergentFork {
		t.Fatalf("expected divergent fork for same-version different-object, got %q", fork)
	}
	notAuth := classifyDivergent(le(10, 2, "objY", OpWrite), peer, authByVersion)
	if notAuth != DivergentNotAuth {
		t.Fatalf("expected not-in-auth for an unknown version, got %q", notAuth)
	}
	afterLES := classifyDivergent(le(11, 1, "objZ", OpWrite), peer, authByVersion)
	if afterLES != DivergentAfterLES {
		t.Fatalf("expected after-LES divergence, got %q", afterLES)
	}
	matching := classifyDivergent(le(10, 1, "objA", OpWrite), peer, authByVersion)
	if matching != "" {
		t.Fatalf("identical entry must be retained, got %q", matching)
	}
}

// TestPgTempAndMissing: acceptance case 3. A backfill target triggers pg_temp
// over the complete replicas while exact missing sets and missing_locations
// are computed per member.
func TestPgTempAndMissing(t *testing.T) {
	history := []OSDMapEpoch{
		{Epoch: 1, Up: []OSDID{1, 2}, Acting: []OSDID{1, 2}, MinSize: 2, Active: true},
	}

	authLog := PGLog{
		Entries: []LogEntry{
			le(10, 1, "objA", OpWrite),
			le(10, 2, "objB", OpWrite),
		},
		Tail:             ev(10, 1),
		LastUpdate:       ev(10, 2),
		LastComplete:     ev(10, 2),
		LastEpochStarted: 11,
	}
	behindLog := PGLog{
		Entries:          []LogEntry{le(10, 1, "objA", OpWrite)},
		Tail:             ev(10, 1),
		LastUpdate:       ev(10, 1),
		LastComplete:     ev(10, 1),
		LastEpochStarted: 11,
	}

	pg := NewPG("pg.2", 2, []OSDID{1, 2, 3}, history,
		replica(1, true, authLog,
			map[ObjectID]EVersion{"objA": ev(10, 1), "objB": ev(10, 2)}),
		replica(2, true, behindLog,
			map[ObjectID]EVersion{"objA": ev(10, 1)}),
		OSDState{ID: 3, Alive: true, NeedsBackfill: true},
	)
	result := pg.Peer()

	if result.State != StateActive || !result.CanWrite || !result.Degraded {
		t.Fatalf("expected active+degraded+writable, state=%s degraded=%v reason=%q",
			result.State, result.Degraded, result.BlockedReason)
	}

	recovery := result.Recovery
	assertIDs(t, "up", recovery.Up, []OSDID{1, 2, 3})
	if !recovery.UsingTemp {
		t.Fatalf("expected pg_temp to be installed for the backfill member")
	}
	assertIDs(t, "pg_temp", recovery.PgTemp, []OSDID{1, 2})
	assertIDs(t, "acting", recovery.Acting, []OSDID{1, 2})
	assertIDs(t, "backfill", recovery.Backfill, []OSDID{3})
	assertIDs(t, "active replicas", recovery.ActiveReplicas, []OSDID{1, 2})

	if len(recovery.Missing[1]) != 0 {
		t.Fatalf("OSD 1 should have nothing missing, got %+v", recovery.Missing[1])
	}
	osd2Missing := recovery.Missing[2]
	if len(osd2Missing) != 1 || osd2Missing[0].Object != "objB" {
		t.Fatalf("OSD 2 should miss only objB, got %+v", osd2Missing)
	}
	assertVersion(t, "OSD 2 needed version", osd2Missing[0].Need, ev(10, 2))

	osd3Missing := recovery.Missing[3]
	if len(osd3Missing) != 2 {
		t.Fatalf("backfill OSD 3 should miss both objects, got %+v", osd3Missing)
	}

	assertIDs(t, "objA locations", recovery.MissingLocations["objA"], []OSDID{1, 2})
	assertIDs(t, "objB locations", recovery.MissingLocations["objB"], []OSDID{1})
	if len(recovery.Unfound) != 0 {
		t.Fatalf("all authoritative objects are recoverable, unexpected unfound: %v",
			recovery.Unfound)
	}
}

// TestUnfoundAndMinSizeGate: acceptance case 4. An object whose latest version
// exists only on a dead OSD is marked unfound and individually blocked, while
// the PG still activates when min_size holds; dropping below min_size forces
// Incomplete.
func TestUnfoundAndMinSizeGate(t *testing.T) {
	history := []OSDMapEpoch{
		{Epoch: 1, Up: []OSDID{1, 2, 3}, Acting: []OSDID{1, 2, 3}, MinSize: 2, Active: true},
	}

	authLog := PGLog{
		Entries: []LogEntry{
			le(10, 1, "objA", OpWrite),
			le(10, 2, "objLost", OpWrite),
		},
		Tail:             ev(10, 1),
		LastUpdate:       ev(10, 2),
		LastComplete:     ev(10, 2),
		LastEpochStarted: 11,
	}
	objects := map[ObjectID]EVersion{"objA": ev(10, 1)}

	// Sub-case A: two of three replicas survive (>= min_size 2). The PG is
	// active+degraded, but objLost is unfound and individually blocked.
	pg := NewPG("pg.3", 2, []OSDID{1, 2, 3}, history,
		replica(1, true, authLog, objects),
		replica(2, true, authLog, objects),
		replica(3, false, authLog, map[ObjectID]EVersion{
			"objA":    ev(10, 1),
			"objLost": ev(10, 2),
		}),
	)
	result := pg.Peer()

	if result.State != StateActive || !result.CanWrite {
		t.Fatalf("expected active+writable with min_size satisfied, state=%s reason=%q",
			result.State, result.BlockedReason)
	}
	if !result.Degraded {
		t.Fatalf("expected degraded because OSD 3 is down and an object is unfound")
	}
	if len(result.Unfound) != 1 || result.Unfound[0] != "objLost" {
		t.Fatalf("expected objLost to be the sole unfound object, got %v", result.Unfound)
	}
	if result.ObjectIOAllowed("objLost") {
		t.Fatalf("unfound object must reject IO")
	}
	if !result.ObjectIOAllowed("objA") {
		t.Fatalf("recoverable object must allow IO")
	}
	if _, present := result.Recovery.MissingLocations["objLost"]; present {
		t.Fatalf("unfound object must not have recovery locations")
	}

	// Sub-case B: only one replica survives (< min_size). Peering must stop
	// Incomplete on the min_size gate even though the log is intact.
	pgMinority := NewPG("pg.3b", 2, []OSDID{1, 2, 3}, history,
		replica(1, true, authLog, objects),
		replica(2, false, authLog, objects),
		replica(3, false, authLog, objects),
	)
	minority := pgMinority.Peer()
	if minority.State != StateIncomplete {
		t.Fatalf("expected Incomplete below min_size, got %s", minority.State)
	}
	if minority.Phase != PhaseMinSizeGate || minority.BlockedReason != ReasonMinSize {
		t.Fatalf("expected min_size gate block, phase=%s reason=%q",
			minority.Phase, minority.BlockedReason)
	}
	if minority.CanWrite || minority.ObjectIOAllowed("objA") {
		t.Fatalf("Incomplete PG must reject all writes and IO")
	}
}

// TestNoReachableReplica ensures a PG with no answering OSD stays Incomplete.
func TestNoReachableReplica(t *testing.T) {
	history := []OSDMapEpoch{
		// Not active, so the interval is not maybe_went_rw: this isolates the
		// no-reachable-replica path from the history-witness gate.
		{Epoch: 1, Up: []OSDID{1, 2}, Acting: []OSDID{1, 2}, MinSize: 1, Active: false},
	}
	log := PGLog{LastEpochStarted: 1}
	pg := NewPG("pg.4", 1, []OSDID{1, 2}, history,
		replica(1, false, log, nil),
		replica(2, false, log, nil),
	)
	result := pg.Peer()
	if result.State != StateIncomplete || result.BlockedReason != ReasonNoReplica {
		t.Fatalf("expected no-replica Incomplete, state=%s reason=%q",
			result.State, result.BlockedReason)
	}
}
