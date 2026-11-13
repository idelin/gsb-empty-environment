package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// DeterministicReader is a cloneable pseudo-random source for reproducible
// audits. It is not suitable for production key generation.
type DeterministicReader struct {
	mu    sync.Mutex
	state uint64
}

// NewDeterministicReader returns a reader with a SplitMix64 state.
func NewDeterministicReader(seed uint64) *DeterministicReader {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &DeterministicReader{state: seed}
}

func (r *DeterministicReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range p {
		r.state += 0x9e3779b97f4a7c15
		z := r.state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		p[i] = byte(z >> 24)
	}
	return len(p), nil
}

// CloneReader returns an independent reader at the same position.
func (r *DeterministicReader) CloneReader() io.Reader {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &DeterministicReader{state: r.state}
}

type readerCloner interface {
	CloneReader() io.Reader
}

func cloneReader(r io.Reader) (io.Reader, error) {
	if r == nil {
		return nil, nil
	}
	if cloner, ok := r.(readerCloner); ok {
		return cloner.CloneReader(), nil
	}
	switch v := r.(type) {
	case *bytes.Reader:
		copy := *v
		return &copy, nil
	case *strings.Reader:
		copy := *v
		return &copy, nil
	}
	if r == rand.Reader {
		return r, nil
	}
	return nil, fmt.Errorf("%w: random source does not support cloning", ErrInvalidConfig)
}

func copyChallenge(rec *challengeRecord) *challengeRecord {
	out := *rec
	return &out
}

func copyAccount(acc *accountState) *accountState {
	out := *acc
	if acc.pending != nil {
		id := *acc.pending
		out.pending = &id
	}
	if acc.inFlight != nil {
		id := *acc.inFlight
		out.inFlight = &id
	}
	out.active = make(map[ChallengeID]struct{}, len(acc.active))
	for id := range acc.active {
		out.active[id] = struct{}{}
	}
	out.creationTimes = append([]MonoTime(nil), acc.creationTimes...)
	return &out
}

func (s *Server) cloneForAudit() (*Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, err := cloneReader(s.cfg.Rand)
	if err != nil {
		return nil, err
	}
	cfg := s.cfg
	cfg.ServiceKey = append([]byte(nil), s.cfg.ServiceKey...)
	cfg.Clock = cloneClock(s.cfg.Clock)
	cfg.Rand = r

	out := &Server{
		cfg:          cfg,
		challenges:   make(map[ChallengeID]*challengeRecord, len(s.challenges)),
		challengeIDs: make(map[ChallengeID]struct{}, len(s.challengeIDs)),
		pairs:        make(map[challengePair]struct{}, len(s.pairs)),
		accounts:     make(map[AccountID]*accountState, len(s.accounts)),
		devices:      make(map[DeviceID]*deviceRecord, len(s.devices)),
		transactions: make(map[TransactionID]*LoginTransaction, len(s.transactions)),
		sessions:     make(map[SessionID]*Session, len(s.sessions)),
	}
	for id, rec := range s.challenges {
		out.challenges[id] = copyChallenge(rec)
	}
	for id := range s.challengeIDs {
		out.challengeIDs[id] = struct{}{}
	}
	for pair := range s.pairs {
		out.pairs[pair] = struct{}{}
	}
	for id, acc := range s.accounts {
		out.accounts[id] = copyAccount(acc)
	}
	for id, dev := range s.devices {
		record := *dev
		record.device.PublicKey = append(ed25519.PublicKey(nil), dev.device.PublicKey...)
		out.devices[id] = &record
	}
	for id, tx := range s.transactions {
		copy := *tx
		out.transactions[id] = &copy
	}
	for id, session := range s.sessions {
		copy := *session
		out.sessions[id] = &copy
	}
	return out, nil
}

// BrowserRole identifies one of the two audited browsers.
type BrowserRole string

const (
	BrowserUser     BrowserRole = "user"
	BrowserAttacker BrowserRole = "attacker"
	BrowserServer   BrowserRole = "server"
)

// TraceStep is one successful operation in an audit trace.
type TraceStep struct {
	Actor       BrowserRole
	Action      string
	ChallengeID string
	Outcome     string
	Detail      string
}

// AuditConfig configures a two-browser interleaving search. The user browser
// owns a registered Ed25519 device; the attacker browser controls no
// registered key.
type AuditConfig struct {
	AccountID AccountID
	DeviceID  DeviceID

	// DevicePrivateKey is optional. If empty a key is generated and registered.
	DevicePrivateKey ed25519.PrivateKey

	UserBrowser     BrowserSessionID
	AttackerBrowser BrowserSessionID
	UserDigest      RequestDigest
	AttackerDigest  RequestDigest
}

// Auditor searches all bounded interleavings of the user's and attacker's
// browser actions using the real state machine implementation.
type Auditor struct {
	Server *Server

	AccountID       AccountID
	DeviceID        DeviceID
	UserBrowser     BrowserSessionID
	AttackerBrowser BrowserSessionID
	UserDigest      RequestDigest
	AttackerDigest  RequestDigest

	UserTransaction     TransactionID
	AttackerTransaction TransactionID

	userKey ed25519.PrivateKey
}

// NewAuditor registers the user device and opens one login transaction per
// browser. The returned Auditor does not run any searches until Search is
// called.
func NewAuditor(s *Server, cfg AuditConfig) (*Auditor, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: server is required", ErrInvalidConfig)
	}
	if normalize(string(cfg.AccountID)) == "" {
		return nil, fmt.Errorf("%w: account id is required", ErrInvalidConfig)
	}
	if cfg.UserBrowser == "" {
		cfg.UserBrowser = "user-browser"
	}
	if cfg.AttackerBrowser == "" {
		cfg.AttackerBrowser = "attacker-browser"
	}
	if cfg.UserBrowser == cfg.AttackerBrowser {
		return nil, fmt.Errorf("%w: browsers must be distinct", ErrInvalidConfig)
	}
	if cfg.UserDigest == (RequestDigest{}) && cfg.AttackerDigest == (RequestDigest{}) {
		cfg.UserDigest = DigestRequest([]byte("user-login-request"))
		cfg.AttackerDigest = DigestRequest([]byte("attacker-login-request"))
	}
	if cfg.DeviceID == "" {
		raw := make([]byte, 8)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, err
		}
		cfg.DeviceID = DeviceID("dev_" + hex.EncodeToString(raw))
	}

	key := cfg.DevicePrivateKey
	if len(key) == 0 {
		_, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		key = generated
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, ErrInvalidDeviceKey
	}
	public := key.Public().(ed25519.PublicKey)
	if err := s.RegisterDevice(cfg.DeviceID, cfg.AccountID, public); err != nil {
		return nil, err
	}
	userTx, err := s.BeginLogin(cfg.AccountID, cfg.UserBrowser)
	if err != nil {
		return nil, err
	}
	attackerTx, err := s.BeginLogin(cfg.AccountID, cfg.AttackerBrowser)
	if err != nil {
		return nil, err
	}
	return &Auditor{
		Server:              s,
		AccountID:           cfg.AccountID,
		DeviceID:            cfg.DeviceID,
		UserBrowser:         cfg.UserBrowser,
		AttackerBrowser:     cfg.AttackerBrowser,
		UserDigest:          cfg.UserDigest,
		AttackerDigest:      cfg.AttackerDigest,
		UserTransaction:     userTx.ID,
		AttackerTransaction: attackerTx.ID,
		userKey:             key,
	}, nil
}

// SearchOptions bounds the deterministic breadth-first search.
type SearchOptions struct {
	MaxDepth int
	MaxNodes int

	// IncludeMisTaps lets the simulated user also tap challenges for the
	// attacker browser. Such an approval is still checked against the digest
	// actually approved, so it is reported as legitimate for that digest.
	IncludeMisTaps bool
	// IncludeDenials adds denial actions and lets the search exercise locks.
	IncludeDenials bool
	// IncludeAccountEvents adds password change, risk escalation, and device
	// revocation actions.
	IncludeAccountEvents bool
	// IncludeSendFailures adds a failed push action, which consumes an hourly
	// allowance without creating Pending.
	IncludeSendFailures bool
}

// SearchResult describes either the shortest attack found or the bounded
// exhaustive result.
type SearchResult struct {
	Found        bool
	Trace        []TraceStep
	Session      *Session
	Explored     int
	Complete     bool
	ActionCounts map[string]int
}

type auditApproval struct {
	challenge Challenge
	response  DeviceResponse
	byUser    bool
}

type auditNode struct {
	server    *Server
	steps     []TraceStep
	approvals map[ChallengeID]auditApproval
	depth     int
}

type auditAction struct {
	label string
	kind  string
	actor BrowserRole
	apply func(*Server, map[ChallengeID]auditApproval) (TraceStep, error)
}

func copyApprovals(in map[ChallengeID]auditApproval) map[ChallengeID]auditApproval {
	out := make(map[ChallengeID]auditApproval, len(in))
	for id, approval := range in {
		out[id] = approval
	}
	return out
}

func shortDigest(d RequestDigest) string { return d.String()[:8] }

func (a *Auditor) canCreate(s *Server) bool {
	status := s.AccountStatus(a.AccountID)
	now := s.cfg.Clock.Now()
	if status.PendingChallenge != nil || status.InFlightChallenge != nil {
		return false
	}
	if status.LockedUntil.After(now) || status.CooldownUntil.After(now) {
		return false
	}
	if status.HourlyCreations >= s.cfg.HourlyCreationLimit {
		return false
	}
	dev, ok := s.GetDevice(a.DeviceID)
	return ok && !dev.Revoked && dev.AccountID == a.AccountID
}

func (a *Auditor) createAction(browser BrowserSessionID, digest RequestDigest) auditAction {
	name := "user"
	role := BrowserUser
	if browser == a.AttackerBrowser {
		name = "attacker"
		role = BrowserAttacker
	}
	owner := "own"
	if (role == BrowserUser && digest == a.AttackerDigest) || (role == BrowserAttacker && digest == a.UserDigest) {
		owner = "foreign"
	}
	label := fmt.Sprintf("create-%s-%s-digest", name, owner)
	kind := label
	req := ChallengeRequest{
		AccountID:        a.AccountID,
		BrowserSessionID: browser,
		DeviceID:         a.DeviceID,
		RequestDigest:    digest,
		Risk:             RiskLow,
	}
	return auditAction{
		label: label,
		kind:  kind,
		actor: role,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			ch, err := s.CreateChallenge(req)
			if err != nil {
				return TraceStep{}, err
			}
			return TraceStep{
				Actor:       role,
				Action:      label,
				ChallengeID: ch.ID.String(),
				Outcome:     string(ch.State),
				Detail:      "digest=" + shortDigest(digest),
			}, nil
		},
	}
}

func (a *Auditor) failingSendAction() auditAction {
	req := ChallengeRequest{
		AccountID:        a.AccountID,
		BrowserSessionID: a.AttackerBrowser,
		DeviceID:         a.DeviceID,
		RequestDigest:    a.AttackerDigest,
		Risk:             RiskLow,
	}
	return auditAction{
		label: "create-attacker-push-fails",
		kind:  "create-push-failed",
		actor: BrowserAttacker,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			ch, err := s.CreateChallenge(req, func(Challenge) error {
				return fmt.Errorf("injected push failure")
			})
			if err == nil {
				return TraceStep{}, fmt.Errorf("injected push failure unexpectedly succeeded")
			}
			step := TraceStep{
				Actor:   BrowserAttacker,
				Action:  "create-attacker-push-fails",
				Outcome: string(ChallengeSendFailed),
				Detail:  "send failure counted; no pending challenge",
			}
			if ch != nil {
				step.ChallengeID = ch.ID.String()
			}
			return step, nil
		},
	}
}

func (a *Auditor) cancelAction(ch Challenge) auditAction {
	role := BrowserUser
	if ch.BrowserSessionID == a.AttackerBrowser {
		role = BrowserAttacker
	}
	id := ch.ID
	label := fmt.Sprintf("cancel-%s", ch.BrowserSessionID)
	return auditAction{
		label: label,
		kind:  "cancel",
		actor: role,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			if err := s.CancelChallenge(a.AccountID, id); err != nil {
				return TraceStep{}, err
			}
			return TraceStep{
				Actor:       role,
				Action:      "cancel",
				ChallengeID: id.String(),
				Outcome:     string(ChallengeCancelled),
				Detail:      "explicit cancellation starts cooldown",
			}, nil
		},
	}
}

func (a *Auditor) approveAction(ch Challenge) auditAction {
	role := BrowserUser
	if ch.BrowserSessionID == a.AttackerBrowser {
		role = BrowserAttacker
	}
	label := fmt.Sprintf("user-device-approve-%s", ch.BrowserSessionID)
	snapshot := ch
	return auditAction{
		label: label,
		kind:  "approve",
		actor: role,
		apply: func(s *Server, approvals map[ChallengeID]auditApproval) (TraceStep, error) {
			resp, err := SignApproval(a.userKey, snapshot)
			if err != nil {
				return TraceStep{}, err
			}
			updated, err := s.Respond(resp)
			if err != nil {
				return TraceStep{}, err
			}
			approvals[snapshot.ID] = auditApproval{challenge: snapshot, response: resp, byUser: true}
			return TraceStep{
				Actor:       role,
				Action:      "user-device-approve",
				ChallengeID: snapshot.ID.String(),
				Outcome:     string(updated.State),
				Detail:      "digest=" + shortDigest(snapshot.RequestDigest),
			}, nil
		},
	}
}

func (a *Auditor) replayAction(approval auditApproval) auditAction {
	response := approval.response
	id := approval.challenge.ID
	return auditAction{
		label: "replay-device-response-" + id.String(),
		kind:  "replay-approval",
		actor: BrowserServer,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			updated, err := s.Respond(response)
			if err != nil {
				return TraceStep{
					Actor:       BrowserServer,
					Action:      "replay-device-response",
					ChallengeID: id.String(),
					Outcome:     "rejected",
					Detail:      "late or duplicate device response",
				}, nil
			}
			return TraceStep{
				Actor:       BrowserServer,
				Action:      "replay-device-response",
				ChallengeID: id.String(),
				Outcome:     string(updated.State),
				Detail:      "duplicate device response was accepted",
			}, nil
		},
	}
}

func (a *Auditor) denyAction(ch Challenge) auditAction {
	role := BrowserUser
	if ch.BrowserSessionID == a.AttackerBrowser {
		role = BrowserAttacker
	}
	snapshot := ch
	return auditAction{
		label: fmt.Sprintf("user-device-deny-%s", ch.BrowserSessionID),
		kind:  "deny",
		actor: role,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			resp, err := SignDenial(a.userKey, snapshot)
			if err != nil {
				return TraceStep{}, err
			}
			updated, err := s.Respond(resp)
			if err != nil {
				return TraceStep{}, err
			}
			return TraceStep{
				Actor:       role,
				Action:      "user-device-deny",
				ChallengeID: snapshot.ID.String(),
				Outcome:     string(updated.State),
			}, nil
		},
	}
}

func (a *Auditor) consumeAction(txID TransactionID, role BrowserRole, ch Challenge) auditAction {
	id := ch.ID
	code := ch.DisplayCode
	label := fmt.Sprintf("consume-%s", role)
	return auditAction{
		label: label,
		kind:  "consume-" + string(role),
		actor: role,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			session, err := s.ConsumeApproval(txID, id, code)
			if err != nil {
				return TraceStep{}, err
			}
			return TraceStep{
				Actor:       role,
				Action:      "consume-approval",
				ChallengeID: id.String(),
				Outcome:     string(ChallengeCompleted),
				Detail:      "session=" + string(session.ID),
			}, nil
		},
	}
}

func (a *Auditor) advanceAction(d time.Duration) auditAction {
	label := fmt.Sprintf("advance-%s", d)
	return auditAction{
		label: label,
		kind:  "advance-time",
		actor: BrowserServer,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			clock, ok := s.cfg.Clock.(*ManualClock)
			if !ok {
				return TraceStep{}, ErrSearchRequiresManualClock
			}
			clock.Advance(d)
			expired := s.ExpireDue()
			return TraceStep{
				Actor:   BrowserServer,
				Action:  "advance-time",
				Outcome: fmt.Sprintf("expired=%d", expired),
				Detail:  d.String(),
			}, nil
		},
	}
}

func (a *Auditor) passwordAction() auditAction {
	return auditAction{
		label: "server-change-password",
		kind:  "change-password",
		actor: BrowserServer,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			if err := s.ChangePassword(a.AccountID); err != nil {
				return TraceStep{}, err
			}
			return TraceStep{Actor: BrowserServer, Action: "change-password", Outcome: "invalidated"}, nil
		},
	}
}

func (a *Auditor) riskAction() auditAction {
	return auditAction{
		label: "server-escalate-risk",
		kind:  "escalate-risk",
		actor: BrowserServer,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			if err := s.EscalateRisk(a.AccountID, RiskCritical); err != nil {
				return TraceStep{}, err
			}
			return TraceStep{Actor: BrowserServer, Action: "escalate-risk", Outcome: "invalidated"}, nil
		},
	}
}

func (a *Auditor) revokeAction() auditAction {
	return auditAction{
		label: "server-revoke-device",
		kind:  "revoke-device",
		actor: BrowserServer,
		apply: func(s *Server, _ map[ChallengeID]auditApproval) (TraceStep, error) {
			if err := s.RevokeDevice(a.DeviceID); err != nil {
				return TraceStep{}, err
			}
			return TraceStep{Actor: BrowserServer, Action: "revoke-device", Outcome: "invalidated"}, nil
		},
	}
}

func (a *Auditor) actions(node *auditNode, opts SearchOptions) []auditAction {
	s := node.server
	actions := make([]auditAction, 0, 12)

	if a.canCreate(s) {
		actions = append(actions,
			a.createAction(a.UserBrowser, a.UserDigest),
			a.createAction(a.UserBrowser, a.AttackerDigest),
			a.createAction(a.AttackerBrowser, a.AttackerDigest),
			a.createAction(a.AttackerBrowser, a.UserDigest),
		)
		if opts.IncludeSendFailures {
			actions = append(actions, a.failingSendAction())
		}
	}

	challenges := s.ChallengesForAccount(a.AccountID)
	approved := make([]Challenge, 0, 2)
	now := s.cfg.Clock.Now()
	advanceSeen := make(map[time.Duration]struct{})
	advance := func(d time.Duration) {
		if d <= 0 {
			return
		}
		if _, ok := advanceSeen[d]; ok {
			return
		}
		advanceSeen[d] = struct{}{}
		actions = append(actions, a.advanceAction(d))
	}

	for _, ch := range challenges {
		switch ch.State {
		case ChallengePending:
			if ch.BrowserSessionID == a.UserBrowser && ch.RequestDigest == a.UserDigest {
				actions = append(actions, a.approveAction(ch))
				if opts.IncludeDenials {
					actions = append(actions, a.denyAction(ch))
				}
			} else if opts.IncludeMisTaps {
				actions = append(actions, a.approveAction(ch))
				if opts.IncludeDenials {
					actions = append(actions, a.denyAction(ch))
				}
			}
			actions = append(actions, a.cancelAction(ch))
			advance(ch.ExpiresAt.Sub(now) + 1)
		case ChallengeApproved:
			approved = append(approved, ch)
			actions = append(actions, a.cancelAction(ch))
			advance(ch.ExpiresAt.Sub(now) + 1)
		}
	}
	for _, approval := range node.approvals {
		actions = append(actions, a.replayAction(approval))
	}

	status := s.AccountStatus(a.AccountID)
	if status.CooldownUntil.After(now) {
		advance(status.CooldownUntil.Sub(now) + 1)
	}
	if status.LockedUntil.After(now) {
		advance(status.LockedUntil.Sub(now) + 1)
	}

	userTx, userOK := s.GetTransaction(a.UserTransaction)
	attackerTx, attackerOK := s.GetTransaction(a.AttackerTransaction)
	for _, ch := range approved {
		if userOK && userTx.State == TransactionOpen {
			actions = append(actions, a.consumeAction(a.UserTransaction, BrowserUser, ch))
		}
		if attackerOK && attackerTx.State == TransactionOpen {
			actions = append(actions, a.consumeAction(a.AttackerTransaction, BrowserAttacker, ch))
		}
	}

	if opts.IncludeAccountEvents {
		actions = append(actions, a.passwordAction(), a.riskAction())
		if dev, ok := s.GetDevice(a.DeviceID); ok && !dev.Revoked {
			actions = append(actions, a.revokeAction())
		}
	}

	sort.SliceStable(actions, func(i, j int) bool { return actions[i].label < actions[j].label })
	return actions
}

func (a *Auditor) attack(node *auditNode) *SearchResult {
	tx, ok := node.server.GetTransaction(a.AttackerTransaction)
	if !ok || tx.State != TransactionConsumed {
		return nil
	}
	session, ok := node.server.GetSession(tx.SessionID)
	if !ok {
		trace := append([]TraceStep(nil), node.steps...)
		return &SearchResult{
			Found:    true,
			Trace:    trace,
			Explored: 0,
			Complete: false,
		}
	}
	approval, approved := node.approvals[session.ChallengeID]
	legitimate := approved &&
		approval.byUser &&
		approval.challenge.BrowserSessionID == a.AttackerBrowser &&
		approval.challenge.RequestDigest == session.RequestDigest &&
		session.BrowserSessionID == a.AttackerBrowser &&
		session.AccountID == a.AccountID
	if legitimate {
		return nil
	}
	trace := append([]TraceStep(nil), node.steps...)
	copy := *session
	return &SearchResult{Found: true, Trace: trace, Session: &copy}
}

func (s *Server) auditFingerprint(approvals map[ChallengeID]auditApproval) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Clock.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d;", now)

	ids := make([]string, 0, len(s.challenges))
	for id := range s.challenges {
		ids = append(ids, id.String())
	}
	sort.Strings(ids)
	for _, text := range ids {
		id, _ := ParseChallengeID(text)
		ch := s.challenges[id].challenge
		fmt.Fprintf(&b, "c:%s:%s:%s:%s:%s:%s:%s:%d:%d:%d;",
			ch.ID, ch.State, ch.AccountID, ch.BrowserSessionID, ch.DeviceID,
			ch.RequestDigest, ch.DisplayCode, ch.Risk, ch.CreatedAt, ch.ExpiresAt)
	}

	accounts := make([]string, 0, len(s.accounts))
	for id := range s.accounts {
		accounts = append(accounts, string(id))
	}
	sort.Strings(accounts)
	for _, id := range accounts {
		acc := s.accounts[AccountID(id)]
		fmt.Fprintf(&b, "a:%s:%d:%d:%d:%d:%d:%d;", id, len(acc.creationTimes), acc.consecutiveDenials, acc.cooldownUntil, acc.lockedUntil, acc.passwordVersion, acc.risk)
		for _, created := range acc.creationTimes {
			fmt.Fprintf(&b, "u:%d;", created)
		}
		if acc.pending != nil {
			fmt.Fprintf(&b, "p:%s;", *acc.pending)
		}
		if acc.inFlight != nil {
			fmt.Fprintf(&b, "f:%s;", *acc.inFlight)
		}
	}

	devices := make([]string, 0, len(s.devices))
	for id := range s.devices {
		devices = append(devices, string(id))
	}
	sort.Strings(devices)
	for _, id := range devices {
		dev := s.devices[DeviceID(id)].device
		fmt.Fprintf(&b, "d:%s:%s:%t;", id, dev.AccountID, dev.Revoked)
	}

	txIDs := make([]string, 0, len(s.transactions))
	for id := range s.transactions {
		txIDs = append(txIDs, string(id))
	}
	sort.Strings(txIDs)
	for _, id := range txIDs {
		tx := s.transactions[TransactionID(id)]
		fmt.Fprintf(&b, "t:%s:%s:%s:%d:%d;", id, tx.State, tx.SessionID, tx.CreatedAt, tx.ExpiresAt)
	}

	approvalIDs := make([]string, 0, len(approvals))
	for id := range approvals {
		approvalIDs = append(approvalIDs, id.String())
	}
	sort.Strings(approvalIDs)
	for _, id := range approvalIDs {
		fmt.Fprintf(&b, "r:%s:%t;", id, approvals[mustParseChallengeID(id)].byUser)
	}
	return b.String()
}

func mustParseChallengeID(text string) ChallengeID {
	id, _ := ParseChallengeID(text)
	return id
}

// Search performs a deterministic breadth-first search over interleavings of
// the two browsers. It reports the shortest trace in which the attacker's
// transaction consumes a session without the user having approved the exact
// challenge/digest/browser binding that was consumed.
func (a *Auditor) Search(opts SearchOptions) (SearchResult, error) {
	if _, ok := a.Server.cfg.Clock.(*ManualClock); !ok {
		return SearchResult{}, ErrSearchRequiresManualClock
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 6
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 100000
	}

	root, err := a.Server.cloneForAudit()
	if err != nil {
		return SearchResult{}, err
	}
	queue := []*auditNode{{server: root, approvals: make(map[ChallengeID]auditApproval)}}
	seen := make(map[string]struct{})
	seen[a.Server.auditFingerprint(map[ChallengeID]auditApproval{})] = struct{}{}
	actionCounts := make(map[string]int)
	explored := 0
	generated := 1
	truncated := false

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		explored++

		if attack := a.attack(node); attack != nil {
			attack.Explored = explored
			attack.ActionCounts = actionCounts
			return *attack, nil
		}
		if node.depth >= opts.MaxDepth {
			truncated = true
			continue
		}

		for _, action := range a.actions(node, opts) {
			actionCounts[action.kind]++
			if generated >= opts.MaxNodes {
				truncated = true
				queue = nil
				break
			}
			childServer, err := node.server.cloneForAudit()
			if err != nil {
				return SearchResult{}, err
			}
			approvals := copyApprovals(node.approvals)
			step, err := action.apply(childServer, approvals)
			if err != nil {
				continue
			}
			child := &auditNode{
				server:    childServer,
				steps:     append(append([]TraceStep(nil), node.steps...), step),
				approvals: approvals,
				depth:     node.depth + 1,
			}
			fingerprint := childServer.auditFingerprint(approvals)
			if _, ok := seen[fingerprint]; ok {
				continue
			}
			seen[fingerprint] = struct{}{}
			queue = append(queue, child)
			generated++
		}
	}

	return SearchResult{
		Found:        false,
		Explored:     explored,
		Complete:     !truncated && len(queue) == 0,
		ActionCounts: actionCounts,
	}, nil
}

// SearchTwoBrowser is a convenience wrapper that constructs an Auditor and
// runs Search.
func SearchTwoBrowser(s *Server, cfg AuditConfig, opts SearchOptions) (SearchResult, error) {
	auditor, err := NewAuditor(s, cfg)
	if err != nil {
		return SearchResult{}, err
	}
	return auditor.Search(opts)
}
