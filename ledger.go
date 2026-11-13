package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

var (
	// ErrInvalidConfig is returned when the fixed committee or chain is invalid.
	ErrInvalidConfig = errors.New("netsec: invalid ledger config")
	// ErrUnknownMember is returned for a share from a member outside the fixed list.
	ErrUnknownMember = errors.New("netsec: unknown member")
	// ErrChainMismatch is returned for a share signed for another chain.
	ErrChainMismatch = errors.New("netsec: chain ID mismatch")
	// ErrInvalidShare is returned for a structurally invalid or badly signed share.
	ErrInvalidShare = errors.New("netsec: invalid share")
	// ErrWrongParent is returned for a prepare whose parent cannot be the latest commit.
	ErrWrongParent = errors.New("netsec: prepare parent does not match latest committed block")
	// ErrRoundRegression is returned when an accepted event would decrease the
	// member's round at a height.
	ErrRoundRegression = errors.New("netsec: round regression")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("netsec: ledger is closed")
)

// Config configures a ledger. The member list and chain ID are immutable.
type Config struct {
	ChainID string
	Members []Member
	// GenesisDigest is the parent of height 1 (and height 0). A zero digest is
	// a valid default.
	GenesisDigest Digest
}

// Status is the disposition of one submitted share.
type Status string

const (
	// StatusPublished means the share, and possibly newly unblocked pending
	// shares, were accepted and are visible in Result.Published.
	StatusPublished Status = "published"
	// StatusPending means the share is authentic but a prerequisite is missing.
	StatusPending Status = "pending"
	// StatusDuplicate means the exact signed share was already seen.
	StatusDuplicate Status = "duplicate"
	// StatusRejected means the share is authentic but violates the protocol.
	StatusRejected Status = "rejected"
	// StatusDoubleSigned means two conflicting shares were found and the member
	// was frozen.
	StatusDoubleSigned Status = "double_signed"
	// StatusFrozen means the member was already frozen when the share arrived.
	StatusFrozen Status = "frozen"
)

// Result describes what happened to one submitted share.
type Result struct {
	EventID string
	Share   Share
	Status  Status
	// Published lists newly accepted shares in publication order. For a
	// non-conflicting submission it contains the submitted share, plus any
	// previously pending shares that became publishable.
	Published []Share
	// Evidence is set when the member is, or becomes, frozen.
	Evidence *DoubleSignEvidence
	// Err explains StatusRejected. Pending and double-sign outcomes are normal
	// protocol outcomes and do not set Err.
	Err error
}

// SignedIndexEntry is one published (height, phase) index entry.
type SignedIndexEntry struct {
	Height       uint64
	Phase        Phase
	BlockDigest  Digest
	ParentDigest Digest
	Round        uint64
}

type signedEntry struct {
	BlockDigest  Digest `json:"block_digest"`
	ParentDigest Digest `json:"parent_digest"`
	Round        uint64 `json:"round"`
	EventID      string `json:"event_id"`
}

// memberState is persisted per member.
type memberState struct {
	Version  int    `json:"version"`
	MemberID string `json:"member_id"`

	Frozen *DoubleSignEvidence `json:"frozen,omitempty"`
	Seen   map[string]string   `json:"seen,omitempty"`

	// Claims contains every authentic share seen for a (height, phase),
	// indexed by block digest. It is what makes conflict detection survive a
	// rejected or still-pending first share.
	Claims map[string]map[string]Share `json:"claims,omitempty"`
	// Index is the first published share for each (height, phase).
	Index map[string]signedEntry `json:"index,omitempty"`
	// Accepted and Pending are the complete ledger state.
	Accepted map[string]Share `json:"accepted,omitempty"`
	Pending  []Share          `json:"pending,omitempty"`
	// MaxRound is the highest published round at a height. It enforces the
	// no-round-regression rule across both phases.
	MaxRound map[string]uint64 `json:"max_round,omitempty"`
	// Committed maps an accepted commit height to its block digest.
	Committed map[string]Digest `json:"committed,omitempty"`
}

func newMemberState(memberID string) *memberState {
	return &memberState{
		Version:   1,
		MemberID:  memberID,
		Seen:      make(map[string]string),
		Claims:    make(map[string]map[string]Share),
		Index:     make(map[string]signedEntry),
		Accepted:  make(map[string]Share),
		MaxRound:  make(map[string]uint64),
		Committed: make(map[string]Digest),
	}
}

func (st *memberState) init() {
	if st.Version == 0 {
		st.Version = 1
	}
	if st.Seen == nil {
		st.Seen = make(map[string]string)
	}
	if st.Claims == nil {
		st.Claims = make(map[string]map[string]Share)
	}
	if st.Index == nil {
		st.Index = make(map[string]signedEntry)
	}
	if st.Accepted == nil {
		st.Accepted = make(map[string]Share)
	}
	if st.MaxRound == nil {
		st.MaxRound = make(map[string]uint64)
	}
	if st.Committed == nil {
		st.Committed = make(map[string]Digest)
	}
}

// Ledger is an importable, concurrency-safe anti-double-sign ledger.
type Ledger struct {
	mu     sync.Mutex
	cfg    Config
	member map[string]Member
	states map[string]*memberState
	dir    string
	closed bool
}

// NewLedger creates an in-memory ledger.
func NewLedger(cfg Config) (*Ledger, error) {
	normalized, members, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	l := &Ledger{
		cfg:    normalized,
		member: members,
		states: make(map[string]*memberState, len(normalized.Members)),
	}
	for _, m := range normalized.Members {
		l.states[m.ID] = newMemberState(m.ID)
	}
	return l, nil
}

func normalizeConfig(cfg Config) (Config, map[string]Member, error) {
	if cfg.ChainID == "" {
		return Config{}, nil, fmt.Errorf("%w: empty chain ID", ErrInvalidConfig)
	}
	if len(cfg.Members) == 0 {
		return Config{}, nil, fmt.Errorf("%w: empty member list", ErrInvalidConfig)
	}
	out := Config{
		ChainID:       cfg.ChainID,
		Members:       make([]Member, 0, len(cfg.Members)),
		GenesisDigest: cfg.GenesisDigest,
	}
	members := make(map[string]Member, len(cfg.Members))
	keys := make(map[string]string, len(cfg.Members))
	for _, raw := range cfg.Members {
		if raw.ID == "" {
			return Config{}, nil, fmt.Errorf("%w: empty member ID", ErrInvalidConfig)
		}
		if len(raw.PublicKey) != 32 {
			return Config{}, nil, fmt.Errorf("%w: member %q public key must be 32 bytes", ErrInvalidConfig, raw.ID)
		}
		if _, exists := members[raw.ID]; exists {
			return Config{}, nil, fmt.Errorf("%w: duplicate member ID %q", ErrInvalidConfig, raw.ID)
		}
		keyID := string(raw.PublicKey)
		if other, exists := keys[keyID]; exists {
			return Config{}, nil, fmt.Errorf("%w: members %q and %q share a public key", ErrInvalidConfig, other, raw.ID)
		}
		m := raw.clone()
		members[m.ID] = m
		keys[keyID] = m.ID
		out.Members = append(out.Members, m)
	}
	return out, members, nil
}

// ChainID returns the immutable chain ID.
func (l *Ledger) ChainID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cfg.ChainID
}

// Members returns a defensive copy of the fixed member list.
func (l *Ledger) Members() []Member {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Member, len(l.cfg.Members))
	for i, m := range l.cfg.Members {
		out[i] = m.clone()
	}
	return out
}

// Submit validates and processes one share.
func (l *Ledger) Submit(share Share) (Result, error) {
	results, err := l.SubmitBatch([]Share{share})
	if err != nil {
		return Result{}, err
	}
	return results[0], nil
}

// SubmitBatch validates, journals, and processes a batch.
//
// Every authentic share is appended to its member's write-ahead log and
// fsynced before any share in the batch is processed. A crash after that
// point can therefore never lose evidence; recovery replays the log. Invalid
// (badly signed or structurally invalid) shares are rejected without being
// journaled.
func (l *Ledger) SubmitBatch(shares []Share) ([]Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}

	results := make([]Result, len(shares))
	journaled := make(map[string]bool)
	var entries []journalEntry

	for i, raw := range shares {
		s := raw.clone()
		id := s.EventID()
		if err := l.validateShare(s); err != nil {
			results[i] = Result{EventID: id, Share: s, Status: StatusRejected, Err: err}
			continue
		}
		st := l.states[s.MemberID]
		if _, seen := st.Seen[id]; seen {
			continue
		}
		if st.Frozen != nil {
			continue
		}
		if !journaled[id] {
			journaled[id] = true
			entries = append(entries, journalEntry{MemberID: s.MemberID, Share: s})
		}
	}
	if err := l.journal(entries); err != nil {
		return nil, err
	}

	touched := make(map[string]bool)
	for i, raw := range shares {
		if results[i].Status == StatusRejected && results[i].Err != nil {
			continue
		}
		s := raw.clone()
		st := l.states[s.MemberID]
		results[i] = l.ingest(st, s)
		touched[s.MemberID] = true
	}
	if err := l.persistMembers(touched); err != nil {
		return results, err
	}
	return results, nil
}

func (l *Ledger) validateShare(s Share) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidShare, err)
	}
	if s.ChainID != l.cfg.ChainID {
		return fmt.Errorf("%w: got %q want %q", ErrChainMismatch, s.ChainID, l.cfg.ChainID)
	}
	m, ok := l.member[s.MemberID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownMember, s.MemberID)
	}
	if err := s.Verify(m.PublicKey); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidShare, err)
	}
	return nil
}

type disposition uint8

const (
	dispositionAccept disposition = iota
	dispositionPending
	dispositionReject
)

func (l *Ledger) ingest(st *memberState, s Share) Result {
	s = s.clone()
	id := s.EventID()
	res := Result{EventID: id, Share: s}

	if _, seen := st.Seen[id]; seen {
		res.Status = StatusDuplicate
		if st.Frozen != nil {
			res.Evidence = cloneEvidence(st.Frozen)
		}
		return res
	}
	if st.Frozen != nil {
		st.Seen[id] = "frozen"
		res.Status = StatusFrozen
		res.Evidence = cloneEvidence(st.Frozen)
		return res
	}

	if evidence := st.recordClaim(s); evidence != nil {
		st.Frozen = cloneEvidence(evidence)
		st.Seen[id] = "double_signed"
		res.Status = StatusDoubleSigned
		res.Evidence = cloneEvidence(evidence)
		return res
	}

	disp, reason := l.evaluate(st, s)
	switch disp {
	case dispositionAccept:
		published := []Share{l.accept(st, s)}
		published = append(published, l.resolvePending(st)...)
		res.Status = StatusPublished
		res.Published = published
	case dispositionPending:
		st.Pending = append(st.Pending, s.clone())
		st.Seen[id] = "pending"
		res.Status = StatusPending
	case dispositionReject:
		st.Seen[id] = "rejected"
		res.Status = StatusRejected
		res.Err = reason
	}
	return res
}

// recordClaim stores every authentic share and returns minimal evidence if
// this share names a different block for the same (height, phase). This check
// is deliberately independent of whether either share currently has its
// prerequisites: two Ed25519 signatures over different blocks at the same
// height and phase are actual double signing, even if one arrived early.
func (st *memberState) recordClaim(s Share) *DoubleSignEvidence {
	key := phaseKey(s.Height, s.Phase)
	claims := st.Claims[key]
	if claims == nil {
		claims = make(map[string]Share)
		st.Claims[key] = claims
	}
	blockKey := s.BlockDigest.String()

	var other *Share
	var otherID string
	for candidateBlock, candidate := range claims {
		if candidateBlock == blockKey {
			continue
		}
		candidateID := candidate.EventID()
		if other == nil || candidateID < otherID {
			c := candidate
			other = &c
			otherID = candidateID
		}
	}
	if other != nil {
		evidence := orderEvidence(other.clone(), s.clone())
		return &evidence
	}
	if _, exists := claims[blockKey]; !exists {
		claims[blockKey] = s.clone()
	}
	return nil
}

func (l *Ledger) evaluate(st *memberState, s Share) (disposition, error) {
	hKey := heightKey(s.Height)
	if maxRound, ok := st.MaxRound[hKey]; ok && s.Round < maxRound {
		return dispositionReject, fmt.Errorf("%w: height %d round %d is below published round %d",
			ErrRoundRegression, s.Height, s.Round, maxRound)
	}

	switch s.Phase {
	case Prepare:
		ok, waiting, err := l.prepareParentStatus(st, s)
		if err != nil {
			return dispositionReject, err
		}
		if waiting {
			return dispositionPending, nil
		}
		if !ok {
			// Defensive; prepareParentStatus returns an error with !ok.
			return dispositionReject, ErrWrongParent
		}
	case Commit:
		if !hasMatchingPrepare(st, s) {
			// The prepare may simply arrive later (or be waiting on its own
			// parent), so hold the commit rather than discard it.
			return dispositionPending, nil
		}
	default:
		return dispositionReject, fmt.Errorf("%w: invalid phase %q", ErrInvalidShare, s.Phase)
	}
	return dispositionAccept, nil
}

func (l *Ledger) prepareParentStatus(st *memberState, s Share) (ok, waiting bool, err error) {
	var latestHeight uint64
	var latestDigest Digest
	found := false
	for hKey, digest := range st.Committed {
		h, parseErr := strconv.ParseUint(hKey, 10, 64)
		if parseErr != nil {
			return false, false, fmt.Errorf("netsec: corrupt committed height %q", hKey)
		}
		if h < s.Height && (!found || h > latestHeight) {
			found = true
			latestHeight = h
			latestDigest = digest
		}
	}

	if !found {
		if s.Height <= 1 {
			if s.ParentDigest == l.cfg.GenesisDigest {
				return true, false, nil
			}
			return false, false, fmt.Errorf("%w: height %d must extend genesis", ErrWrongParent, s.Height)
		}
		// A lower commit may still be in flight. Intermediates may be absent,
		// so this can become valid as soon as any lower commit arrives.
		return false, true, nil
	}
	if s.ParentDigest == latestDigest {
		return true, false, nil
	}
	if latestHeight == s.Height-1 {
		return false, false, fmt.Errorf("%w: height %d parent %s, latest commit at %d is %s",
			ErrWrongParent, s.Height, s.ParentDigest, latestHeight, latestDigest)
	}
	// A commit in the gap [latestHeight+1, height-1] could still arrive and
	// become the true latest commit.
	return false, true, nil
}

func hasMatchingPrepare(st *memberState, commit Share) bool {
	for _, accepted := range st.Accepted {
		if accepted.Phase != Prepare {
			continue
		}
		if accepted.Height == commit.Height &&
			accepted.BlockDigest == commit.BlockDigest &&
			accepted.Round <= commit.Round {
			return true
		}
	}
	return false
}

func (l *Ledger) accept(st *memberState, s Share) Share {
	id := s.EventID()
	stored := s.clone()
	st.Accepted[id] = stored
	st.Seen[id] = "accepted"

	key := phaseKey(s.Height, s.Phase)
	if _, exists := st.Index[key]; !exists {
		st.Index[key] = signedEntry{
			BlockDigest:  s.BlockDigest,
			ParentDigest: s.ParentDigest,
			Round:        s.Round,
			EventID:      id,
		}
	}
	if s.Phase == Commit {
		st.Committed[heightKey(s.Height)] = s.BlockDigest
	}
	hKey := heightKey(s.Height)
	if maxRound, ok := st.MaxRound[hKey]; !ok || s.Round > maxRound {
		st.MaxRound[hKey] = s.Round
	}
	return stored.clone()
}

func (l *Ledger) resolvePending(st *memberState) []Share {
	var published []Share
	for {
		if len(st.Pending) == 0 || st.Frozen != nil {
			return published
		}
		progress := false
		remaining := make([]Share, 0, len(st.Pending))
		for _, pending := range st.Pending {
			disp, reason := l.evaluate(st, pending)
			switch disp {
			case dispositionAccept:
				published = append(published, l.accept(st, pending))
				progress = true
			case dispositionReject:
				st.Seen[pending.EventID()] = "rejected"
				_ = reason
				progress = true
			default:
				remaining = append(remaining, pending)
			}
		}
		st.Pending = remaining
		if !progress {
			return published
		}
	}
}

// FrozenEvidence returns a copy of a member's immutable evidence.
func (l *Ledger) FrozenEvidence(memberID string) (DoubleSignEvidence, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.states[memberID]
	if !ok || st.Frozen == nil {
		return DoubleSignEvidence{}, false
	}
	return *cloneEvidence(st.Frozen), true
}

// Evidence returns a copy of a member's frozen evidence, if any.
func (l *Ledger) Evidence(memberID string) (*DoubleSignEvidence, bool) {
	e, ok := l.FrozenEvidence(memberID)
	if !ok {
		return nil, false
	}
	return &e, true
}

// IsFrozen reports whether a member has been frozen.
func (l *Ledger) IsFrozen(memberID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.states[memberID]
	return ok && st.Frozen != nil
}

// Pending returns a member's currently stashed shares in arrival order.
func (l *Ledger) Pending(memberID string) ([]Share, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.states[memberID]
	if !ok {
		return nil, false
	}
	out := make([]Share, len(st.Pending))
	for i, s := range st.Pending {
		out[i] = s.clone()
	}
	return out, true
}

// SignedIndex returns the published (height, phase) index for a member.
func (l *Ledger) SignedIndex(memberID string) ([]SignedIndexEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.states[memberID]
	if !ok {
		return nil, false
	}
	out := make([]SignedIndexEntry, 0, len(st.Index))
	for key, entry := range st.Index {
		height, phase, err := parsePhaseKey(key)
		if err != nil {
			continue
		}
		out = append(out, SignedIndexEntry{
			Height:       height,
			Phase:        phase,
			BlockDigest:  entry.BlockDigest,
			ParentDigest: entry.ParentDigest,
			Round:        entry.Round,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Height != out[j].Height {
			return out[i].Height < out[j].Height
		}
		return out[i].Phase < out[j].Phase
	})
	return out, true
}

// VerifyEvidence verifies evidence against this ledger's fixed member list.
func (l *Ledger) VerifyEvidence(e DoubleSignEvidence) error {
	return VerifyEvidence(l.Members(), e)
}

// Close releases resources. It is safe to call more than once.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.dir == "" {
		return nil
	}
	all := make(map[string]bool, len(l.states))
	for id := range l.states {
		all[id] = true
	}
	return l.persistMembers(all)
}

func (s Share) clone() Share {
	out := s
	out.Signature = append([]byte(nil), s.Signature...)
	return out
}

func cloneEvidence(e *DoubleSignEvidence) *DoubleSignEvidence {
	if e == nil {
		return nil
	}
	return &DoubleSignEvidence{ShareA: e.ShareA.clone(), ShareB: e.ShareB.clone()}
}

func phaseKey(height uint64, phase Phase) string {
	return heightKey(height) + "/" + string(phase)
}

func heightKey(height uint64) string {
	return strconv.FormatUint(height, 10)
}

func parsePhaseKey(key string) (uint64, Phase, error) {
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			h, err := strconv.ParseUint(key[:i], 10, 64)
			if err != nil {
				return 0, "", err
			}
			p := Phase(key[i+1:])
			if !p.Valid() {
				return 0, "", fmt.Errorf("netsec: invalid phase %q", p)
			}
			return h, p, nil
		}
	}
	return 0, "", fmt.Errorf("netsec: invalid phase key %q", key)
}
