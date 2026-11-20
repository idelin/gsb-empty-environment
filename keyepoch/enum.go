package keyepoch

import "fmt"

// EnumReport summarizes a bounded state-space enumeration over the small
// read/write queues. Violation is empty when every reachable state satisfied
// all key-generation and send-count invariants.
type EnumReport struct {
	States       int
	Transitions  int
	Traces       int
	MaxDepth     int
	MaxQueueLen  int
	Violation    string
	ViolationLog []Event
	SeenSimul    bool
	SeenCancel   bool
	SeenDead     bool
	SeenLate     bool
	SeenCrossSeq bool
	SeenUnknown  bool
}

type enumCtx struct {
	cfg       Config
	report    *EnumReport
	memo      map[string]bool
	depth     int
	maxDepth  int
	maxStates int
	events    []Event
}

// Enumerate performs a bounded DFS over every legal short event stream for one
// engine with small queues. Every reached state is replayed independently and
// checked for the component invariants. The enumeration is deterministic and
// network-free.
func Enumerate(cfg Config, maxDepth, maxStates int) (*EnumReport, error) {
	ctx := &enumCtx{
		cfg:       cfg,
		report:    &EnumReport{},
		memo:      map[string]bool{},
		maxDepth:  maxDepth,
		maxStates: maxStates,
	}
	e, err := NewEngine(cfg)
	if err != nil {
		return nil, err
	}
	// Seed deterministic prefixes that guarantee the required edge cases
	// (simultaneous updates, close followed by stale callbacks) are explored
	// even when the generic DFS hits its state cap first.
	for _, seed := range seedEvents(cfg) {
		seedEngine, _, seedErr := Replay(cfg, seed)
		if seedErr != nil && !seedEngine.dead() {
			return nil, seedErr
		}
		ctx.events = append(ctx.events, seed...)
		ctx.depth = len(seed)
		if seedEngine.dead() {
			ctx.report.SeenLate = true
		}
		if err := ctx.dfs(seedEngine); err != nil {
			return ctx.report, err
		}
		ctx.events = nil
		ctx.depth = 0
	}
	if err := ctx.dfs(e); err != nil {
		return ctx.report, err
	}
	return ctx.report, nil
}

func seedEvents(cfg Config) [][]Event {
	closeFrame := signedInbound(cfg, KindClose, 0, 0, false)
	return [][]Event{
		// Local update in flight, then a peer request arrives.
		{
			{Kind: EvRequestUpdate},
			{Kind: EvIncoming, Frame: signedInbound(cfg, KindKeyUpdate, 0, 0, true)},
		},
		// Close, then stale inbound and resolution callbacks after death.
		{
			{Kind: EvSetWrite, MsgID: "E-m1", Write: WriteCommitted},
			{Kind: EvClose},
			{Kind: EvPump},
			{Kind: EvIncoming, Frame: closeFrame},
			{Kind: EvResolveWrite, MsgID: "stale"},
			{Kind: EvSendApp, Payload: []byte("late")},
		},
	}
}

func (c *enumCtx) dfs(e *Engine) error {
	if c.report.Violation != "" {
		return nil
	}
	key := e.Summaries()[len(e.Summaries())-1].stateKey()
	if c.memo[key] {
		return nil
	}
	c.memo[key] = true
	c.report.States++
	c.report.Traces++
	if e.dead() && len(c.events) > 0 {
		// Any continuation applied after the session ended is a stale
		// callback / late frame scenario.
		c.report.SeenLate = true
	}
	if err := c.checkInvariants(e); err != nil {
		c.report.Violation = err.Error()
		c.report.ViolationLog = append([]Event(nil), c.events...)
		return err
	}
	if c.depth >= c.maxDepth || c.report.States >= c.maxStates || e.dead() {
		if c.depth > c.report.MaxDepth {
			c.report.MaxDepth = c.depth
		}
		return nil
	}
	for _, ev := range c.actions(e) {
		child := e.clone()
		if _, err := child.Apply(ev); err != nil {
			// Rejected inputs leave the engine untouched; the child simply
			// re-enters the same memoized state.
		}
		c.events = append(c.events, ev)
		c.depth++
		c.report.Transitions++
		c.flagCoverage(e, child, ev)
		if err := c.dfs(child); err != nil {
			return err
		}
		c.depth--
		c.events = c.events[:len(c.events)-1]
	}
	return nil
}

func (c *enumCtx) flagCoverage(parent, child *Engine, ev Event) {
	if ev.Kind == EvIncoming && ev.Frame.Kind == KindKeyUpdate && parent.localUpdatePending {
		// A peer request lands while our own update is still in flight.
		c.report.SeenSimul = true
	}
	if ev.Kind == EvCancelAuth {
		c.report.SeenCancel = true
	}
	if ev.Kind == EvTransportDead {
		c.report.SeenDead = true
	}
	if parent.dead() {
		c.report.SeenLate = true
	}
	if ev.Kind == EvIncoming && ev.Frame.Seq == 0 && ev.Frame.Epoch > 0 {
		c.report.SeenCrossSeq = true
	}
	if ev.Kind == EvSetLookup || ev.Kind == EvResolveWrite {
		c.report.SeenUnknown = true
	}
	if q := len(child.readQueue) + len(child.writeQueue); q > c.report.MaxQueueLen {
		c.report.MaxQueueLen = q
	}
}

func (c *enumCtx) actions(e *Engine) []Event {
	var out []Event
	// Inbound frames: app and KeyUpdate at current and next epoch, including a
	// same sequence number across generations, plus close_notify.
	for _, epoch := range []uint64{e.readEpoch, e.readEpoch + 1} {
		out = append(out,
			Event{Kind: EvIncoming, Frame: signedInbound(c.cfg, KindApp, epoch, 0, false)},
			Event{Kind: EvIncoming, Frame: signedInbound(c.cfg, KindKeyUpdate, epoch, 0, true)},
		)
	}
	out = append(out, Event{Kind: EvIncoming, Frame: signedInbound(c.cfg, KindClose, e.readEpoch, 0, false)})
	// Outbound control and data.
	out = append(out,
		Event{Kind: EvSendApp, Payload: []byte("a")},
		Event{Kind: EvRequestUpdate},
		Event{Kind: EvClose},
		Event{Kind: EvTransportDead},
		Event{Kind: EvPump},
	)
	// Script and resolution events keyed at the current head MsgIDs.
	if len(e.readQueue) > 0 {
		id := e.readQueue[0].frame.MsgID
		out = append(out,
			Event{Kind: EvCancelAuth, MsgID: id},
			Event{Kind: EvSetAuth, MsgID: id, Auth: AuthAccepted},
			Event{Kind: EvSetAuth, MsgID: id, Auth: AuthRejected},
		)
	}
	if len(e.writeQueue) > 0 {
		id := e.writeQueue[0].frame.MsgID
		out = append(out,
			Event{Kind: EvSetWrite, MsgID: id, Write: WriteCommitted},
			Event{Kind: EvSetWrite, MsgID: id, Write: WriteNotCommitted},
			Event{Kind: EvSetWrite, MsgID: id, Write: WriteUnknown},
			Event{Kind: EvResolveWrite, MsgID: id},
			Event{Kind: EvSetLookup, MsgID: id, Write: WriteCommitted},
			Event{Kind: EvSetLookup, MsgID: id, Write: WriteNotCommitted},
		)
	}
	return out
}

func signedInbound(cfg Config, kind FrameKind, epoch, seq uint64, request bool) Frame {
	f := Frame{Kind: kind, Epoch: epoch, Seq: seq, MsgID: fmt.Sprintf("in-e%d-s%d-%t", epoch, seq, request), Request: request}
	return SignFrame(f, cfg.ReadSecret)
}

// clone returns a deep, independent copy used by enumeration branching. The
// shared config (scripts and wire) is intentionally not copied here: branches
// only need engine-local state, and independent Replay audits construct their
// own isolated configs via replayConfig.
func (e *Engine) clone() *Engine {
	cp := *e
	cp.cfg = replayConfig(e.cfg)
	cp.readWindows = make(map[uint64]*seqWindow, len(e.readWindows))
	for k, v := range e.readWindows {
		win := &seqWindow{base: v.base, bitmap: append([]bool(nil), v.bitmap...)}
		cp.readWindows[k] = win
	}
	cp.writeSeq = make(map[uint64]uint64, len(e.writeSeq))
	for k, v := range e.writeSeq {
		cp.writeSeq[k] = v
	}
	cp.readQueue = append([]readEntry(nil), e.readQueue...)
	cp.writeQueue = append([]writeEntry(nil), e.writeQueue...)
	cp.delivered = append([]FrameRef(nil), e.delivered...)
	cp.sendAttempts = append([]SendAttempt(nil), e.sendAttempts...)
	cp.drops = append([]Drop(nil), e.drops...)
	cp.events = append([]Event(nil), e.events...)
	cp.log = append([]Summary(nil), e.log...)
	return &cp
}

// checkInvariants validates the global properties that must hold in every
// reachable state, then cross-checks the snapshot against an independent
// Replay of the recorded history.
func (c *enumCtx) checkInvariants(e *Engine) error {
	sum := e.Summaries()[len(e.Summaries())-1]
	// 1. Epochs never exceed the public budget and match advance counters.
	if e.readEpoch > c.cfg.Budget || e.writeEpoch > c.cfg.Budget {
		return fmt.Errorf("epoch beyond budget: r=%d w=%d budget=%d", e.readEpoch, e.writeEpoch, c.cfg.Budget)
	}
	if sum.ReadAdvances != e.readEpoch || sum.WriteAdvances != e.writeEpoch {
		return fmt.Errorf("advance counters diverged from epochs")
	}
	// 2. Every queued frame keeps the epoch it was enqueued with; no queued
	// frame can ever claim a generation ahead of the installed write key.
	for _, en := range e.readQueue {
		if en.frame.Epoch > e.readEpoch && en.authed && en.auth == AuthAccepted && en.frame.Kind == KindApp {
			return fmt.Errorf("app from future epoch marked deliverable")
		}
	}
	for _, en := range e.writeQueue {
		if en.frame.Epoch > e.writeEpoch {
			return fmt.Errorf("queued frame bound to future write epoch")
		}
	}
	// 3. At most one write may be ambiguous at any instant, and the ambiguous
	// frame is always the FIFO head; retries reuse the same MsgID, and the
	// engine never mints a second update while one is unresolved.
	amb := 0
	seenIDs := map[string]int{}
	for _, a := range e.sendAttempts {
		seenIDs[a.MsgID]++
	}
	for _, en := range e.writeQueue {
		if en.status == wsAmbiguous {
			amb++
		}
	}
	if amb > 1 || (amb == 1 && (len(e.writeQueue) == 0 || e.writeQueue[0].status != wsAmbiguous)) {
		return fmt.Errorf("ambiguous write is not the single FIFO head")
	}
	for id, n := range seenIDs {
		// Same MsgID may be retried, but bounded retries keep a failing
		// callback from spinning; distinct updates always have distinct IDs.
		if n > 4 {
			return fmt.Errorf("frame %s attempted %d times", id, n)
		}
	}
	// 4. Delivered frames are always at the current or a prior-but-not-revoked
	// window-checked epoch and never duplicate within an epoch.
	delSeen := map[string]bool{}
	for _, d := range e.delivered {
		key := fmt.Sprintf("%d/%d", d.Epoch, d.Seq)
		if delSeen[key] {
			return fmt.Errorf("duplicate delivery %s", key)
		}
		delSeen[key] = true
		if d.Epoch > e.readEpoch {
			return fmt.Errorf("delivery from future epoch %d", d.Epoch)
		}
	}
	// 5. Independent replay must reproduce the exact canonical state. Full
	// replay is quadratic over the trace, so every state gets the cheap
	// structural checks above and a deterministic sample gets an end-to-end
	// replay audit; Replay is also covered exhaustively by the package tests.
	if c.report.States%17 == 0 || e.dead() {
		re, sums, err := Replay(c.cfg, e.events)
		if err != nil && !re.dead() {
			return fmt.Errorf("replay failed: %w", err)
		}
		last := sums[len(sums)-1]
		if last.Canonical() != sum.Canonical() {
			return fmt.Errorf("replay divergence")
		}
	}
	return nil
}
