package keyepoch

import (
	"errors"
	"fmt"
	"strings"
)

// Engine status. The engine is never concurrently driven: every transition is
// the deterministic result of one Event applied in order.
const (
	statusRunning = "running"
	statusClosing = "closing"
	statusClosed  = "closed"
	statusFailed  = "failed"
)

// DefaultBudget is the maximum key generation (epoch index) reachable in
// either direction when no explicit budget is configured.
const DefaultBudget uint64 = 4

// DefaultWindow is the per-epoch acceptable sequence-number window.
const DefaultWindow uint64 = 64

// Config configures an Engine. ReadSecret verifies inbound frames, WriteSecret
// protects outbound frames. Any of Auth/Write/Lookup may be nil, in which case
// scripted adapters over Wire and Scripts are used.
type Config struct {
	Role        string
	ReadSecret  []byte
	WriteSecret []byte
	Budget      uint64
	WindowSize  uint64
	Wire        *Wire
	Scripts     *Scripts
	Auth        Authenticator
	Write       Writer
	Lookup      CommittedLookup
}

// EventKind identifies one deterministic input to the engine.
type EventKind uint8

const (
	// EvPump re-evaluates both queues: pending authentications, ambiguous
	// writes and any frame that can now make progress.
	EvPump EventKind = iota + 1
	// EvIncoming enqueues an inbound frame on the read queue.
	EvIncoming
	// EvSendApp enqueues outbound application data, bound to the current
	// write epoch at enqueue time.
	EvSendApp
	// EvRequestUpdate enqueues a local KeyUpdateRequest.
	EvRequestUpdate
	// EvClose enqueues a close_notify at the current write epoch.
	EvClose
	// EvTransportDead models an observed transport disconnection.
	EvTransportDead
	// EvCancelAuth cancels a still-pending inbound authentication by MsgID.
	EvCancelAuth
	// EvResolveWrite re-checks an ambiguous write by message identity.
	EvResolveWrite
	// EvSetAuth scripts the answer of a later authentication.
	EvSetAuth
	// EvSetWrite scripts the answer of a later write attempt.
	EvSetWrite
	// EvSetLookup scripts the answer of a later ambiguity lookup.
	EvSetLookup
)

// String renders the canonical event-kind name.
func (k EventKind) String() string {
	switch k {
	case EvPump:
		return "pump"
	case EvIncoming:
		return "incoming"
	case EvSendApp:
		return "send-app"
	case EvRequestUpdate:
		return "request-update"
	case EvClose:
		return "close"
	case EvTransportDead:
		return "transport-dead"
	case EvCancelAuth:
		return "cancel-auth"
	case EvResolveWrite:
		return "resolve-write"
	case EvSetAuth:
		return "set-auth"
	case EvSetWrite:
		return "set-write"
	case EvSetLookup:
		return "set-lookup"
	default:
		return "illegal"
	}
}

// Event is one input. Only the fields relevant to Kind are read.
type Event struct {
	Kind    EventKind
	Frame   Frame
	MsgID   string
	Payload []byte
	Request bool
	Auth    AuthResult
	Write   WriteResult
}

// FrameRef is a value-stable pointer to a frame for summaries and replay.
type FrameRef struct {
	Kind    string
	Epoch   uint64
	Seq     uint64
	MsgID   string
	Request bool
}

func refOf(f Frame) FrameRef {
	return FrameRef{Kind: f.Kind.String(), Epoch: f.Epoch, Seq: f.Seq, MsgID: f.MsgID, Request: f.Request}
}

// SendAttempt records one transmission attempt: a repeated MsgID means the
// same frame was retried, never a second update.
type SendAttempt struct {
	MsgID   string
	Kind    string
	Epoch   uint64
	Seq     uint64
	Attempt int
	Result  string
}

// Drop records why a queued frame was discarded without delivery or commit.
type Drop struct {
	MsgID  string
	Kind   string
	Epoch  uint64
	Seq    uint64
	Reason string
}

// Summary is a complete, canonicalizable state snapshot for replay audits.
type Summary struct {
	Index              int
	Event              string
	Status             string
	ReadEpoch          uint64
	WriteEpoch         uint64
	ReadQueue          []FrameRef
	WriteQueue         []FrameRef
	PendingAuth        []string
	AmbiguousWrite     string
	ReplyPending       bool
	ReplyQueued        bool
	LocalUpdatePending bool
	Delivered          []FrameRef
	SendAttempts       []SendAttempt
	Drops              []Drop
	ReadAdvances       uint64
	WriteAdvances      uint64
	Budget             uint64
	Reason             string
}

type readEntry struct {
	frame    Frame
	authed   bool
	auth     AuthResult
	canceled bool
}

const (
	wsQueued    = "queued"
	wsAmbiguous = "ambiguous"
)

type writeEntry struct {
	frame    Frame
	attempts int
	status   string
}

type seqWindow struct {
	base   uint64
	bitmap []bool
}

func newSeqWindow(size uint64) *seqWindow {
	return &seqWindow{bitmap: make([]bool, size)}
}

// checkAndMark rejects duplicates and frames outside the sliding window and
// records accepted sequence numbers, advancing the base over a contiguous
// accepted prefix.
func (w *seqWindow) checkAndMark(seq uint64) bool {
	if seq < w.base || seq >= w.base+uint64(len(w.bitmap)) {
		return false
	}
	idx := int(seq - w.base)
	if w.bitmap[idx] {
		return false
	}
	w.bitmap[idx] = true
	for len(w.bitmap) > 0 && w.bitmap[0] {
		w.bitmap = w.bitmap[1:]
		w.bitmap = append(w.bitmap, false)
		w.base++
	}
	return true
}

// Engine is the key-generation coordinator for one TLS endpoint.
type Engine struct {
	cfg Config

	status string
	reason string

	readEpoch  uint64
	writeEpoch uint64

	readWindows map[uint64]*seqWindow
	readQueue   []readEntry
	writeQueue  []writeEntry

	writeSeq map[uint64]uint64

	replyPending       bool
	replyQueued        bool
	localUpdatePending bool

	delivered    []FrameRef
	sendAttempts []SendAttempt
	drops        []Drop

	readAdvances  uint64
	writeAdvances uint64

	idCounter int
	events    []Event
	log       []Summary
}

// NewEngine validates the configuration and returns a ready engine. Nil
// callbacks are replaced with deterministic scripted adapters.
func NewEngine(cfg Config) (*Engine, error) {
	if len(cfg.ReadSecret) == 0 || len(cfg.WriteSecret) == 0 {
		return nil, errors.New("keyepoch: ReadSecret and WriteSecret are required")
	}
	if cfg.Budget == 0 {
		cfg.Budget = DefaultBudget
	}
	if cfg.WindowSize == 0 {
		cfg.WindowSize = DefaultWindow
	}
	if cfg.Wire == nil {
		cfg.Wire = NewWire()
	}
	if cfg.Scripts == nil {
		cfg.Scripts = NewScripts()
	}
	if cfg.Auth == nil {
		cfg.Auth = &ScriptedAuthenticator{ReadSecret: cfg.ReadSecret, Scripts: cfg.Scripts}
	}
	if cfg.Write == nil {
		cfg.Write = &ScriptedWriter{Wire: cfg.Wire, Scripts: cfg.Scripts}
	}
	if cfg.Lookup == nil {
		cfg.Lookup = &ScriptedLookup{Wire: cfg.Wire, Scripts: cfg.Scripts}
	}
	e := &Engine{
		cfg:         cfg,
		status:      statusRunning,
		readWindows: map[uint64]*seqWindow{0: newSeqWindow(cfg.WindowSize)},
		writeSeq:    map[uint64]uint64{},
	}
	e.log = append(e.log, e.snapshot(0, "init"))
	return e, nil
}

func (e *Engine) newID() string {
	e.idCounter++
	return fmt.Sprintf("%s-m%d", e.cfg.Role, e.idCounter)
}

// nextWriteSeq allocates the per-epoch record sequence number. Sequence
// numbering restarts at zero for every key generation, so identical numbers in
// different epochs name different records.
func (e *Engine) nextWriteSeq() uint64 {
	seq := e.writeSeq[e.writeEpoch]
	e.writeSeq[e.writeEpoch] = seq + 1
	return seq
}

func (e *Engine) dead() bool {
	return e.status == statusClosed || e.status == statusFailed
}

// fail seals the session fatally. It is reserved for invariant violations
// (currently: exceeding the public update budget), and deliberately leaves no
// half-updated session: queues are purged and both directions stay at the
// already-committed epochs.
func (e *Engine) fail(reason string) {
	e.status = statusFailed
	e.reason = reason
	e.purgeQueues("session-failed")
}

func dropOf(f Frame, reason string) Drop {
	return Drop{MsgID: f.MsgID, Kind: f.Kind.String(), Epoch: f.Epoch, Seq: f.Seq, Reason: reason}
}

// Apply feeds one event and returns the post-event summary. An event that is
// invalid for the current state returns an error without changing the engine,
// except budget violations, which fail the session by design.
func (e *Engine) Apply(ev Event) (Summary, error) {
	e.events = append(e.events, ev)
	var err error
	switch ev.Kind {
	case EvPump:
		e.pump()
	case EvIncoming:
		err = e.onIncoming(ev.Frame)
	case EvSendApp:
		err = e.onSendApp(ev.Payload)
	case EvRequestUpdate:
		err = e.onRequestUpdate()
	case EvClose:
		e.onClose()
	case EvTransportDead:
		e.onTransportDead()
	case EvCancelAuth:
		e.onCancelAuth(ev.MsgID)
	case EvResolveWrite:
		e.onResolveWrite(ev.MsgID)
	case EvSetAuth:
		e.cfg.Scripts.Auth[ev.MsgID] = ev.Auth
	case EvSetWrite:
		e.cfg.Scripts.Write[ev.MsgID] = ev.Write
	case EvSetLookup:
		e.cfg.Scripts.Lookup[ev.MsgID] = ev.Write
	default:
		err = fmt.Errorf("keyepoch: unknown event kind %d", ev.Kind)
	}
	sum := e.snapshot(len(e.log), ev.Kind.String())
	e.log = append(e.log, sum)
	return sum, err
}

func (e *Engine) onIncoming(f Frame) error {
	if e.dead() {
		e.drops = append(e.drops, dropOf(f, "session-"+e.status))
		return nil
	}
	// The epoch binding is fixed when the frame enters the queue and never
	// changes while it waits for authentication.
	e.readQueue = append(e.readQueue, readEntry{frame: f, auth: AuthUnknown})
	return nil
}

func (e *Engine) onSendApp(payload []byte) error {
	if e.dead() || e.status == statusClosing {
		return errors.New("keyepoch: cannot enqueue app data after close")
	}
	f := Frame{Kind: KindApp, Epoch: e.writeEpoch, Seq: e.nextWriteSeq(), MsgID: e.newID(), Payload: append([]byte(nil), payload...)}
	f = SignFrame(f, e.cfg.WriteSecret)
	e.writeQueue = append(e.writeQueue, writeEntry{frame: f, status: wsQueued})
	return nil
}

func (e *Engine) onRequestUpdate() error {
	if e.dead() || e.status == statusClosing {
		return errors.New("keyepoch: cannot request update after close")
	}
	if e.localUpdatePending {
		return errors.New("keyepoch: local update already in flight")
	}
	if e.writeEpoch+1 > e.cfg.Budget {
		e.fail(fmt.Sprintf("write update budget %d exceeded", e.cfg.Budget))
		return errors.New(e.reason)
	}
	f := Frame{Kind: KindKeyUpdate, Epoch: e.writeEpoch, Seq: e.nextWriteSeq(), MsgID: e.newID(), Request: true}
	f = SignFrame(f, e.cfg.WriteSecret)
	e.writeQueue = append(e.writeQueue, writeEntry{frame: f, status: wsQueued})
	e.localUpdatePending = true
	return nil
}

func (e *Engine) onClose() {
	if e.dead() {
		return
	}
	if e.status == statusClosing {
		return
	}
	f := Frame{Kind: KindClose, Epoch: e.writeEpoch, Seq: e.nextWriteSeq(), MsgID: e.newID()}
	f = SignFrame(f, e.cfg.WriteSecret)
	e.writeQueue = append(e.writeQueue, writeEntry{frame: f, status: wsQueued})
	e.status = statusClosing
}

func (e *Engine) onTransportDead() {
	if e.dead() {
		return
	}
	e.status = statusFailed
	e.reason = "transport dead"
	e.purgeQueues("transport-dead")
}

func (e *Engine) purgeQueues(reason string) {
	for _, en := range e.readQueue {
		e.drops = append(e.drops, dropOf(en.frame, reason))
	}
	e.readQueue = nil
	for _, en := range e.writeQueue {
		e.drops = append(e.drops, dropOf(en.frame, reason))
	}
	e.writeQueue = nil
	e.replyPending = false
	e.replyQueued = false
	e.localUpdatePending = false
}

func (e *Engine) onCancelAuth(msgID string) {
	for i := range e.readQueue {
		if e.readQueue[i].frame.MsgID == msgID && !e.readQueue[i].authed {
			e.readQueue[i].canceled = true
			return
		}
	}
}

func (e *Engine) onResolveWrite(msgID string) {
	if e.dead() || len(e.writeQueue) == 0 {
		return
	}
	head := &e.writeQueue[0]
	if head.status != wsAmbiguous || head.frame.MsgID != msgID {
		return
	}
	switch e.cfg.Lookup.Lookup(head.frame) {
	case WriteCommitted:
		e.commitHead()
	case WriteNotCommitted:
		head.status = wsQueued
	default:
		// Still unknown: keep the queue frozen. Emitting another update
		// here would risk a duplicate KeyUpdate on the wire.
	}
}

func (e *Engine) pump() {
	if e.dead() {
		return
	}
	e.readPump()
	if !e.dead() {
		e.writePump()
	}
}

// readPump drains every currently decidable inbound frame. Frames whose
// authentication is unknown stay in place and block later frames on the same
// ordered queue, mirroring a record layer that cannot reorder records.
func (e *Engine) readPump() {
	for len(e.readQueue) > 0 && !e.dead() {
		en := &e.readQueue[0]
		if en.canceled {
			e.drops = append(e.drops, dropOf(en.frame, "auth-canceled"))
			e.readQueue = e.readQueue[1:]
			continue
		}
		if !en.authed {
			en.auth = e.cfg.Auth.Authenticate(en.frame)
			if en.auth == AuthUnknown {
				return
			}
			en.authed = true
		}
		f := en.frame
		if f.Epoch < e.readEpoch {
			// The key generation protecting this frame was already rotated
			// away: the frame is revoked and must never be delivered.
			e.drops = append(e.drops, dropOf(f, "epoch-revoked"))
			e.readQueue = e.readQueue[1:]
			continue
		}
		if f.Epoch > e.readEpoch {
			// Protected by a key generation we have not installed yet: the
			// advancing KeyUpdate is still queued ahead. Wait for it.
			return
		}
		switch f.Kind {
		case KindClose:
			if en.auth != AuthAccepted {
				e.drops = append(e.drops, dropOf(f, "close-auth-rejected"))
				e.readQueue = e.readQueue[1:]
				continue
			}
			e.drops = append(e.drops, dropOf(f, "peer-close"))
			e.readQueue = e.readQueue[1:]
			e.status = statusClosed
			e.reason = "peer close_notify"
			e.purgeQueues("peer-close")
			return
		case KindKeyUpdate:
			if en.auth != AuthAccepted {
				// Rejected under the old read key: the update advances
				// nothing and produces no reply.
				e.drops = append(e.drops, dropOf(f, "update-auth-rejected"))
				e.readQueue = e.readQueue[1:]
				continue
			}
			// Only an update authenticated under the old read key may
			// advance the read generation.
			if f.Request {
				e.replyPending = true
			}
			e.advanceRead()
			e.readQueue = e.readQueue[1:]
		case KindApp:
			if en.auth != AuthAccepted {
				e.drops = append(e.drops, dropOf(f, "app-auth-rejected"))
				e.readQueue = e.readQueue[1:]
				continue
			}
			win := e.readWindows[f.Epoch]
			if !win.checkAndMark(f.Seq) {
				e.drops = append(e.drops, dropOf(f, "seq-window"))
				e.readQueue = e.readQueue[1:]
				continue
			}
			// Final delivery re-checks every gate: epoch not revoked,
			// session not closed and sequence inside the window.
			e.delivered = append(e.delivered, refOf(f))
			e.readQueue = e.readQueue[1:]
		default:
			e.drops = append(e.drops, dropOf(f, "unknown-kind"))
			e.readQueue = e.readQueue[1:]
		}
	}
}

func (e *Engine) advanceRead() {
	e.readEpoch++
	e.readAdvances++
	if _, ok := e.readWindows[e.readEpoch]; !ok {
		e.readWindows[e.readEpoch] = newSeqWindow(e.cfg.WindowSize)
	}
}

// writePump first enqueues any owed reply, then transmits the strict FIFO
// head. An ambiguous write freezes the whole queue; an explicit non-commit
// allows one retry of the same frame within the same pump.
func (e *Engine) writePump() {
	if e.replyPending && !e.replyQueued {
		if e.writeEpoch+1 > e.cfg.Budget {
			e.fail(fmt.Sprintf("reply update budget %d exceeded", e.cfg.Budget))
			return
		}
		f := Frame{Kind: KindKeyUpdate, Epoch: e.writeEpoch, Seq: e.nextWriteSeq(), MsgID: e.newID(), Request: false}
		f = SignFrame(f, e.cfg.WriteSecret)
		e.writeQueue = append(e.writeQueue, writeEntry{frame: f, status: wsQueued})
		e.replyQueued = true
	}
	for len(e.writeQueue) > 0 && !e.dead() {
		head := &e.writeQueue[0]
		if head.status == wsAmbiguous {
			return
		}
		head.attempts++
		result := e.cfg.Write.Write(head.frame)
		e.sendAttempts = append(e.sendAttempts, SendAttempt{
			MsgID:   head.frame.MsgID,
			Kind:    head.frame.Kind.String(),
			Epoch:   head.frame.Epoch,
			Seq:     head.frame.Seq,
			Attempt: head.attempts,
			Result:  result.String(),
		})
		switch result {
		case WriteCommitted:
			e.commitHead()
		case WriteUnknown:
			head.status = wsAmbiguous
			return
		default:
			// Explicitly not committed: one retry of the identical frame is
			// allowed; stop after it so a persistently failing callback
			// cannot spin the pump.
			if head.attempts >= 2 {
				return
			}
		}
	}
}

// commitHead finalizes the committed head frame. Key material only changes
// here, after the wire commit, so the write generation never advances ahead of
// the actually transmitted update (including a requested reply).
func (e *Engine) commitHead() {
	head := e.writeQueue[0]
	f := head.frame
	e.writeQueue = e.writeQueue[1:]
	switch f.Kind {
	case KindKeyUpdate:
		e.writeEpoch++
		e.writeAdvances++
		if !f.Request {
			// A reply frame: the obligation is discharged exactly once and
			// only after the correctly-keyed frame committed.
			e.replyPending = false
			e.replyQueued = false
		} else {
			e.localUpdatePending = false
		}
	case KindClose:
		e.status = statusClosed
		e.reason = "local close_notify committed"
		e.purgeQueues("local-close")
	}
}

func refList(in []FrameRef) []FrameRef {
	if len(in) == 0 {
		return []FrameRef{}
	}
	return in
}

func (e *Engine) snapshot(index int, event string) Summary {
	rq := make([]FrameRef, 0, len(e.readQueue))
	pending := []string{}
	for _, en := range e.readQueue {
		rq = append(rq, refOf(en.frame))
		if !en.authed && !en.canceled {
			pending = append(pending, en.frame.MsgID)
		}
	}
	wq := make([]FrameRef, 0, len(e.writeQueue))
	ambiguous := ""
	for _, en := range e.writeQueue {
		wq = append(wq, refOf(en.frame))
		if en.status == wsAmbiguous && ambiguous == "" {
			ambiguous = en.frame.MsgID
		}
	}
	return Summary{
		Index:              index,
		Event:              event,
		Status:             e.status,
		ReadEpoch:          e.readEpoch,
		WriteEpoch:         e.writeEpoch,
		ReadQueue:          rq,
		WriteQueue:         wq,
		PendingAuth:        pending,
		AmbiguousWrite:     ambiguous,
		ReplyPending:       e.replyPending,
		ReplyQueued:        e.replyQueued,
		LocalUpdatePending: e.localUpdatePending,
		Delivered:          refList(append([]FrameRef{}, e.delivered...)),
		SendAttempts:       append([]SendAttempt{}, e.sendAttempts...),
		Drops:              append([]Drop{}, e.drops...),
		ReadAdvances:       e.readAdvances,
		WriteAdvances:      e.writeAdvances,
		Budget:             e.cfg.Budget,
		Reason:             e.reason,
	}
}

func renderRefs(rs []FrameRef) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s(e%d,s%d,%s,req=%t)", r.Kind, r.Epoch, r.Seq, r.MsgID, r.Request))
	}
	return strings.Join(parts, ";")
}

// Canonical renders the summary as deterministic text: identical event
// histories always produce byte-identical summaries, which is what an
// independent replay audit compares against.
func (s Summary) Canonical() string {
	attempts := make([]string, 0, len(s.SendAttempts))
	for _, a := range s.SendAttempts {
		attempts = append(attempts, fmt.Sprintf("%s/%s/e%d/s%d#%d=%s", a.MsgID, a.Kind, a.Epoch, a.Seq, a.Attempt, a.Result))
	}
	drops := make([]string, 0, len(s.Drops))
	for _, d := range s.Drops {
		drops = append(drops, fmt.Sprintf("%s/%s/e%d/s%d=%s", d.MsgID, d.Kind, d.Epoch, d.Seq, d.Reason))
	}
	lines := []string{
		fmt.Sprintf("index=%d event=%s status=%s reason=%q", s.Index, s.Event, s.Status, s.Reason),
		fmt.Sprintf("read_epoch=%d write_epoch=%d read_advances=%d write_advances=%d budget=%d",
			s.ReadEpoch, s.WriteEpoch, s.ReadAdvances, s.WriteAdvances, s.Budget),
		"read_queue=[" + renderRefs(s.ReadQueue) + "]",
		"write_queue=[" + renderRefs(s.WriteQueue) + "]",
		"pending_auth=[" + strings.Join(s.PendingAuth, ",") + "]",
		fmt.Sprintf("ambiguous_write=%s reply_pending=%t reply_queued=%t local_update_pending=%t",
			s.AmbiguousWrite, s.ReplyPending, s.ReplyQueued, s.LocalUpdatePending),
		"delivered=[" + renderRefs(s.Delivered) + "]",
		"send_attempts=[" + strings.Join(attempts, ",") + "]",
		"drops=[" + strings.Join(drops, ",") + "]",
	}
	return strings.Join(lines, "\n")
}

// stateKey is the replay-independent portion of a summary: event index and the
// triggering event name are excluded so equivalent states merge during
// enumeration and replay comparison.
func (s Summary) stateKey() string {
	s.Index = 0
	s.Event = ""
	return s.Canonical()
}

// Replay rebuilds an engine from scratch by re-applying the same events and
// returns every post-event summary. It is pure over the supplied config and
// performs no I/O, so a third party can independently verify a recorded
// history by replaying it and diffing Canonical summaries.
func Replay(cfg Config, events []Event) (*Engine, []Summary, error) {
	cfg = replayConfig(cfg)
	e, err := NewEngine(cfg)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Summary, 0, len(events)+1)
	out = append(out, e.Summaries()[0])
	for _, ev := range events {
		sum, applyErr := e.Apply(ev)
		// Replay models every input, including ones rejected by the current
		// state. Such rejections are part of the recorded behavior and never
		// mutate the engine, so the summaries still reproduce exactly.
		_ = applyErr
		out = append(out, sum)
	}
	return e, out, nil
}

// replayConfig gives each replay an isolated script set and wire so that
// independent re-runs (including parallel enumeration branches) cannot observe
// state left behind by another engine instance. Explicitly injected custom
// callbacks are left untouched.
func replayConfig(cfg Config) Config {
	if cfg.Scripts != nil {
		clone := NewScripts()
		for k, v := range cfg.Scripts.Auth {
			clone.Auth[k] = v
		}
		for k, v := range cfg.Scripts.Write {
			clone.Write[k] = v
		}
		for k, v := range cfg.Scripts.Lookup {
			clone.Lookup[k] = v
		}
		cfg.Scripts = clone
	}
	if cfg.Auth == nil && cfg.Write == nil && cfg.Lookup == nil {
		cfg.Wire = NewWire()
	}
	return cfg
}

// Summaries returns every snapshot recorded since construction (init first).
func (e *Engine) Summaries() []Summary {
	out := make([]Summary, len(e.log))
	copy(out, e.log)
	return out
}

// Events returns the applied event history.
func (e *Engine) Events() []Event {
	out := make([]Event, len(e.events))
	copy(out, e.events)
	return out
}

// ReadEpoch returns the currently installed read key generation.
func (e *Engine) ReadEpoch() uint64 { return e.readEpoch }

// WriteEpoch returns the currently installed write key generation.
func (e *Engine) WriteEpoch() uint64 { return e.writeEpoch }

// Status returns the engine status (running, closing, closed or failed).
func (e *Engine) Status() string { return e.status }

// FailureReason returns the fatal reason, empty while running.
func (e *Engine) FailureReason() string { return e.reason }

// Delivered returns a copy of all frames handed to the application layer.
func (e *Engine) Delivered() []FrameRef {
	out := make([]FrameRef, len(e.delivered))
	copy(out, e.delivered)
	return out
}

// SendAttempts returns a copy of every transmission attempt with its result.
func (e *Engine) SendAttempts() []SendAttempt {
	out := make([]SendAttempt, len(e.sendAttempts))
	copy(out, e.sendAttempts)
	return out
}

// Pump applies EvPump and returns the resulting summary.
func (e *Engine) Pump() (Summary, error) { return e.Apply(Event{Kind: EvPump}) }

// Incoming applies EvIncoming for a signed inbound frame.
func (e *Engine) Incoming(f Frame) (Summary, error) {
	return e.Apply(Event{Kind: EvIncoming, Frame: f})
}

// SendApp applies EvSendApp for application payload.
func (e *Engine) SendApp(payload []byte) (Summary, error) {
	return e.Apply(Event{Kind: EvSendApp, Payload: payload})
}

// RequestUpdate applies EvRequestUpdate.
func (e *Engine) RequestUpdate() (Summary, error) {
	return e.Apply(Event{Kind: EvRequestUpdate})
}

// Close applies EvClose.
func (e *Engine) Close() (Summary, error) { return e.Apply(Event{Kind: EvClose}) }

// TransportDead applies EvTransportDead.
func (e *Engine) TransportDead() (Summary, error) {
	return e.Apply(Event{Kind: EvTransportDead})
}

// CancelAuth applies EvCancelAuth for the given queued MsgID.
func (e *Engine) CancelAuth(msgID string) (Summary, error) {
	return e.Apply(Event{Kind: EvCancelAuth, MsgID: msgID})
}

// ResolveWrite applies EvResolveWrite for the ambiguous head MsgID.
func (e *Engine) ResolveWrite(msgID string) (Summary, error) {
	return e.Apply(Event{Kind: EvResolveWrite, MsgID: msgID})
}

// SetAuth scripts a later authentication answer for MsgID.
func (e *Engine) SetAuth(msgID string, r AuthResult) (Summary, error) {
	return e.Apply(Event{Kind: EvSetAuth, MsgID: msgID, Auth: r})
}

// SetWrite scripts a later write answer for MsgID.
func (e *Engine) SetWrite(msgID string, r WriteResult) (Summary, error) {
	return e.Apply(Event{Kind: EvSetWrite, MsgID: msgID, Write: r})
}

// SetLookup scripts a later ambiguity lookup answer for MsgID.
func (e *Engine) SetLookup(msgID string, r WriteResult) (Summary, error) {
	return e.Apply(Event{Kind: EvSetLookup, MsgID: msgID, Write: r})
}
