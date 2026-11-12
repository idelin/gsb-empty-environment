package netsec

import (
	"errors"
	"fmt"
	"net"
	"sync"
)

// Policy controls whether an unencrypted open connection may be selected.
// The zero value never selects an open BSS: a failed or unverifiable OWE
// transition results in ActionBlock rather than a silent downgrade.
type Policy struct {
	// AllowOpen is a blanket opt-in for selecting open BSSs in every case.
	AllowOpen bool

	// AllowOpenFallback explicitly permits selecting an open BSS when an OWE
	// transition pair is incomplete, inconsistent, or the OWE association was
	// rejected. The resulting Decision has Fallback set and a fallback reason.
	AllowOpenFallback bool

	// AllowOpenWithoutOWE permits selecting a plain open BSS that makes no OWE
	// transition claim at all.
	AllowOpenWithoutOWE bool
}

// SelectionPolicy is an alias for Policy.
type SelectionPolicy = Policy

func (p Policy) allowsOpenFallback() bool {
	return p.AllowOpen || p.AllowOpenFallback
}

func (p Policy) allowsPlainOpen() bool {
	return p.AllowOpen || p.AllowOpenWithoutOWE
}

// Action is the result of a selection.
type Action uint8

const (
	// ActionBlock means no BSS may be used.
	ActionBlock Action = iota
	// ActionConnectOWE means select an OWE BSS. The BSS is either part of a
	// verified transition pair or a directly usable OWE scan result.
	ActionConnectOWE
	// ActionConnectOpen means select an open BSS. This is only returned when
	// the policy explicitly allows it.
	ActionConnectOpen
)

// Aliases for callers that prefer verb-named actions.
const (
	ActionUpgradeOWE   = ActionConnectOWE
	ActionFallbackOpen = ActionConnectOpen
)

func (a Action) String() string {
	switch a {
	case ActionConnectOWE:
		return "connect-owe"
	case ActionConnectOpen:
		return "connect-open"
	default:
		return "block"
	}
}

// Reason explains a Decision. It is deliberately explicit so a caller cannot
// mistake an allowed open fallback for a verified OWE connection.
type Reason string

const (
	ReasonVerifiedOWEPair        Reason = "verified-owe-transition-pair"
	ReasonDirectOWE              Reason = "owe"
	ReasonOWERejected            Reason = "owe-association-rejected"
	ReasonOneWayTransition       Reason = "one-way-transition"
	ReasonBSSIDMismatch          Reason = "transition-bssid-mismatch"
	ReasonSSIDMismatch           Reason = "transition-ssid-mismatch"
	ReasonTransitionTargetAbsent Reason = "transition-target-absent"
	ReasonMalformedTransition    Reason = "malformed-transition"
	ReasonConflictingScan        Reason = "conflicting-scan-results"
	ReasonInvalidScan            Reason = "invalid-scan-result"
	ReasonOpenFallback           Reason = "explicit-open-fallback"
	ReasonPlainOpen              Reason = "open-without-owe"
	ReasonOpenDenied             Reason = "open-denied-by-policy"
	ReasonNoCandidate            Reason = "no-candidate"
)

// Pair is a verified open/OWE transition pair. The two BSS entries point at
// each other and their BSSID and SSID declarations agree.
type Pair struct {
	Open BSS
	OWE  BSS
}

// OWEPair is an alias for Pair.
type OWEPair = Pair

// Decision is the scan selection result.
//
// BSS is non-nil only when Action requests a connection. Pair is set when a
// verified transition pair exists; for an allowed open fallback it identifies
// the pair that could not be used. Fallback is true only for an open selection
// made because OWE was unavailable, inconsistent, or rejected.
type Decision struct {
	Action   Action
	BSS      *BSS
	Pair     *Pair
	Reason   Reason
	Detail   string
	Fallback bool
}

// Selection is an alias for Decision.
type Selection = Decision

func (d Decision) String() string {
	if d.BSS == nil {
		return fmt.Sprintf("action=%s reason=%s detail=%q", d.Action, d.Reason, d.Detail)
	}
	return fmt.Sprintf("action=%s bssid=%s ssid=%q reason=%s fallback=%t detail=%q",
		d.Action, d.BSS.BSSID, d.BSS.SSID, d.Reason, d.Fallback, d.Detail)
}

// Select chooses a BSS from scan results according to policy. It never silently
// downgrades: a transition failure blocks by default, and an open selection is
// reported as an explicit fallback.
func Select(results []BSS, policy Policy) Decision {
	return SelectWithRejected(results, policy, nil)
}

// SelectBSS is an alias for Select.
func SelectBSS(results []BSS, policy Policy) Decision {
	return Select(results, policy)
}

// SelectWithRejected behaves like Select but also treats the supplied OWE BSS
// BSSIDs as rejected association attempts during this roaming decision.
func SelectWithRejected(results []BSS, policy Policy, rejected ...net.HardwareAddr) Decision {
	rejectedSet := make(map[string]struct{}, len(rejected))
	for _, bssid := range rejected {
		if validateBSSID(bssid) == nil {
			rejectedSet[bssidKey(bssid)] = struct{}{}
		}
	}
	return decide(results, policy, rejectedSet)
}

// SelectAfterOWERejection is an alias for SelectWithRejected.
func SelectAfterOWERejection(results []BSS, policy Policy, rejected ...net.HardwareAddr) Decision {
	return SelectWithRejected(results, policy, rejected...)
}

// Selector retains OWE rejection state across the scan and association phases
// of a roaming decision. It is safe for concurrent use.
type Selector struct {
	mu       sync.RWMutex
	policy   Policy
	rejected map[string]struct{}
}

// NewSelector creates a selector with the supplied policy.
func NewSelector(policy Policy) *Selector {
	return &Selector{
		policy:   policy,
		rejected: make(map[string]struct{}),
	}
}

// New is an alias for NewSelector.
func New(policy Policy) *Selector {
	return NewSelector(policy)
}

// RejectOWE records that an OWE association to bssid failed or was rejected.
// The open counterpart must not be used silently afterwards.
func (s *Selector) RejectOWE(bssid net.HardwareAddr) error {
	if s == nil {
		return errors.New("nil selector")
	}
	if err := validateBSSID(bssid); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejected == nil {
		s.rejected = make(map[string]struct{})
	}
	s.rejected[bssidKey(bssid)] = struct{}{}
	return nil
}

// MarkOWEREjected is an alias for RejectOWE.
func (s *Selector) MarkOWEREjected(bssid net.HardwareAddr) error {
	return s.RejectOWE(bssid)
}

// ClearRejections forgets all recorded OWE failures.
func (s *Selector) ClearRejections() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = make(map[string]struct{})
}

// Reset clears both recorded OWE failures and resets the policy.
func (s *Selector) Reset(policy Policy) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = policy
	s.rejected = make(map[string]struct{})
}

// SetPolicy changes the policy used by subsequent selections.
func (s *Selector) SetPolicy(policy Policy) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = policy
}

// Policy returns the selector's current policy.
func (s *Selector) Policy() Policy {
	if s == nil {
		return Policy{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policy
}

// Select chooses a BSS using the selector's policy and rejection history.
func (s *Selector) Select(results []BSS) Decision {
	if s == nil {
		return blocked(ReasonNoCandidate, "nil selector")
	}
	s.mu.RLock()
	policy := s.policy
	rejected := make(map[string]struct{}, len(s.rejected))
	for key := range s.rejected {
		rejected[key] = struct{}{}
	}
	s.mu.RUnlock()
	return decide(results, policy, rejected)
}

type normalizedBSS struct {
	bss               BSS
	index             int
	transition        *TransitionMode
	transitionClaimed bool
	transitionErr     error
	err               error
	conflicted        bool
	duplicate         bool
}

type pairCandidate struct {
	open normalizedBSS
	owe  normalizedBSS
	pair Pair
}

func decide(results []BSS, policy Policy, rejected map[string]struct{}) Decision {
	entries := normalizeScan(results)
	if len(entries) == 0 {
		return blocked(ReasonNoCandidate, "scan contains no BSS")
	}

	valid := make([]*normalizedBSS, 0, len(entries))
	var invalidDetail string
	for _, entry := range entries {
		switch {
		case entry.err != nil:
			if invalidDetail == "" {
				invalidDetail = fmt.Sprintf("BSS %s: %v", entry.bss.BSSID, entry.err)
			}
		case entry.conflicted:
			if invalidDetail == "" {
				invalidDetail = fmt.Sprintf("BSS %s has conflicting scan entries", entry.bss.BSSID)
			}
		case entry.duplicate:
			continue
		default:
			valid = append(valid, entry)
		}
	}

	pairs := findPairs(valid)
	if len(pairs) > 0 {
		var selected *pairCandidate
		for i := range pairs {
			candidate := &pairs[i]
			if _, wasRejected := rejected[bssidKey(candidate.owe.bss.BSSID)]; wasRejected {
				continue
			}
			if selected == nil || betterPair(candidate, selected) {
				selected = candidate
			}
		}
		if selected != nil {
			selectedBSS := selected.pair.OWE
			return Decision{
				Action: ActionConnectOWE,
				BSS:    &selectedBSS,
				Pair:   pairPointer(selected.pair),
				Reason: ReasonVerifiedOWEPair,
				Detail: fmt.Sprintf("verified pair %s <-> %s", selected.pair.Open.BSSID, selected.pair.OWE.BSSID),
			}
		}

		// Every verified pair had its OWE side rejected. Never silently use the
		// open BSS: require an explicit policy opt-in.
		failed := bestPairByOpen(pairs)
		if policy.allowsOpenFallback() {
			openBSS := failed.pair.Open
			return Decision{
				Action:   ActionConnectOpen,
				BSS:      &openBSS,
				Pair:     pairPointer(failed.pair),
				Reason:   ReasonOpenFallback,
				Detail:   "OWE association was rejected for every verified pair",
				Fallback: true,
			}
		}
		return Decision{
			Action: ActionBlock,
			Pair:   pairPointer(failed.pair),
			Reason: ReasonOWERejected,
			Detail: "OWE association was rejected; open fallback is not allowed by policy",
		}
	}

	// A standalone OWE BSS with a known SSID is at least as secure as a
	// transition upgrade and does not require an open counterpart.
	if owe := bestStandaloneOWE(valid, rejected); owe != nil {
		oweBSS := cloneBSS(*owe)
		return Decision{
			Action: ActionConnectOWE,
			BSS:    &oweBSS,
			Reason: ReasonDirectOWE,
			Detail: "OWE BSS has no verified transition counterpart",
		}
	}

	var opens []*normalizedBSS
	for _, entry := range valid {
		if entry.bss.Security == SecurityOpen {
			opens = append(opens, entry)
		}
	}

	if rejectedOpen := bestRejectedOpen(valid, rejected); rejectedOpen != nil {
		if policy.allowsOpenFallback() {
			openBSS := cloneBSS(rejectedOpen.bss)
			return Decision{
				Action:   ActionConnectOpen,
				BSS:      &openBSS,
				Reason:   ReasonOpenFallback,
				Detail:   "the OWE counterpart was rejected; open fallback is explicitly allowed",
				Fallback: true,
			}
		}
		return blocked(ReasonOWERejected, "the OWE counterpart was rejected; open fallback is not allowed by policy")
	}

	reason, detail := transitionFailureReason(entries, valid)
	if reason != "" {
		if policy.allowsOpenFallback() {
			if openBSS := bestOpen(opens); openBSS != nil {
				selected := cloneBSS(openBSS.bss)
				return Decision{
					Action:   ActionConnectOpen,
					BSS:      &selected,
					Reason:   ReasonOpenFallback,
					Detail:   detail + "; open fallback is explicitly allowed",
					Fallback: true,
				}
			}
		}
		return blocked(reason, detail)
	}

	if len(opens) > 0 {
		if policy.allowsPlainOpen() {
			selected := cloneBSS(bestOpen(opens).bss)
			return Decision{
				Action: ActionConnectOpen,
				BSS:    &selected,
				Reason: ReasonPlainOpen,
				Detail: "open BSS makes no OWE transition claim; policy explicitly allows it",
			}
		}
		return blocked(ReasonOpenDenied, "open BSS makes no OWE transition claim; open selection is not allowed by policy")
	}

	if invalidDetail != "" {
		return blocked(ReasonInvalidScan, invalidDetail)
	}
	return blocked(ReasonConflictingScan, "no usable BSS remained after validation")
}

func normalizeScan(results []BSS) []*normalizedBSS {
	entries := make([]*normalizedBSS, 0, len(results))
	for index := range results {
		bss := cloneBSS(results[index])
		entry := &normalizedBSS{
			bss:   bss,
			index: index,
		}
		entries = append(entries, entry)

		if err := validateBSSID(bss.BSSID); err != nil {
			entry.err = err
			continue
		}
		if len([]byte(bss.SSID)) > 32 {
			entry.err = errors.New("SSID exceeds 32 octets")
			continue
		}

		inferred := SecurityUnknown
		if len(bss.IEs) > 0 {
			inferred = InferSecurity(bss.IEs)
		}
		if bss.Security == SecurityUnknown {
			if inferred != SecurityUnknown {
				bss.Security = inferred
				entry.bss.Security = inferred
			} else {
				entry.err = errors.New("security mode is unknown")
				continue
			}
		}
		if bss.Security != SecurityOpen && bss.Security != SecurityOWE {
			entry.err = fmt.Errorf("unsupported security mode %s", bss.Security)
			continue
		}

		if bss.Transition != nil {
			transition := *bss.Transition
			transition.BSSID = cloneBSSID(bss.Transition.BSSID)
			entry.transitionClaimed = true
			if err := validateTransitionTarget(transition); err != nil {
				entry.transitionErr = err
				continue
			}
			entry.transition = &transition
			continue
		}

		if len(bss.IEs) == 0 {
			continue
		}
		transition, err := ParseTransitionIEs(bss.IEs)
		switch {
		case err == nil:
			entry.transitionClaimed = true
			entry.transition = &transition
		case errors.Is(err, ErrNoTransitionElement):
			// A plain open BSS or a non-transition OWE BSS.
		default:
			entry.transitionClaimed = true
			entry.transitionErr = err
		}
	}

	markConflicts(entries)
	return entries
}

func markConflicts(entries []*normalizedBSS) {
	groups := make(map[string][]*normalizedBSS)
	for _, entry := range entries {
		if entry.err != nil {
			continue
		}
		groups[bssidKey(entry.bss.BSSID)] = append(groups[bssidKey(entry.bss.BSSID)], entry)
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		first := group[0]
		conflict := false
		for _, other := range group[1:] {
			if !sameScanEntry(first, other) {
				conflict = true
				break
			}
		}
		if conflict {
			for _, entry := range group {
				entry.conflicted = true
			}
			continue
		}
		for _, other := range group[1:] {
			other.duplicate = true
			if other.bss.RSSI > first.bss.RSSI {
				first.bss.RSSI = other.bss.RSSI
			}
		}
	}
}

func sameScanEntry(a, b *normalizedBSS) bool {
	if a.bss.SSID != b.bss.SSID || a.bss.Security != b.bss.Security {
		return false
	}
	if a.transitionClaimed != b.transitionClaimed {
		return false
	}
	if (a.transitionErr == nil) != (b.transitionErr == nil) {
		return false
	}
	if a.transition == nil || b.transition == nil {
		return a.transition == nil && b.transition == nil
	}
	return a.transition.equal(*b.transition)
}

func findPairs(entries []*normalizedBSS) []pairCandidate {
	var pairs []pairCandidate
	for _, open := range entries {
		if open.bss.Security != SecurityOpen || open.transition == nil {
			continue
		}
		for _, owe := range entries {
			if owe.bss.Security != SecurityOWE || owe.transition == nil {
				continue
			}
			if sameBSSID(open.bss.BSSID, owe.bss.BSSID) {
				continue
			}
			if !verifiedTransitionPair(open, owe) {
				continue
			}
			pairs = append(pairs, pairCandidate{
				open: *open,
				owe:  *owe,
				pair: makePair(open, owe),
			})
		}
	}
	return pairs
}

func verifiedTransitionPair(open, owe *normalizedBSS) bool {
	if open.transition == nil || owe.transition == nil {
		return false
	}
	if !sameBSSID(open.transition.BSSID, owe.bss.BSSID) {
		return false
	}
	if !sameBSSID(owe.transition.BSSID, open.bss.BSSID) {
		return false
	}
	if open.bss.SSID == "" || open.transition.SSID == "" {
		return false
	}
	if owe.bss.SSID != "" && owe.bss.SSID != open.transition.SSID {
		return false
	}
	if owe.transition.SSID != open.bss.SSID {
		return false
	}
	return true
}

func makePair(open, owe *normalizedBSS) Pair {
	openBSS := cloneBSS(open.bss)
	oweBSS := cloneBSS(owe.bss)
	if oweBSS.SSID == "" {
		oweBSS.SSID = open.transition.SSID
	}
	return Pair{Open: openBSS, OWE: oweBSS}
}

func pairPointer(pair Pair) *Pair {
	clone := pair
	clone.Open = cloneBSS(pair.Open)
	clone.OWE = cloneBSS(pair.OWE)
	return &clone
}

func betterPair(candidate, current *pairCandidate) bool {
	if candidate.owe.bss.RSSI != current.owe.bss.RSSI {
		return candidate.owe.bss.RSSI > current.owe.bss.RSSI
	}
	if candidate.open.bss.RSSI != current.open.bss.RSSI {
		return candidate.open.bss.RSSI > current.open.bss.RSSI
	}
	return candidate.open.index < current.open.index
}

func bestPairByOpen(pairs []pairCandidate) *pairCandidate {
	if len(pairs) == 0 {
		return nil
	}
	best := &pairs[0]
	for i := 1; i < len(pairs); i++ {
		candidate := &pairs[i]
		if candidate.pair.Open.RSSI > best.pair.Open.RSSI {
			best = candidate
			continue
		}
		if candidate.pair.Open.RSSI == best.pair.Open.RSSI && candidate.open.index < best.open.index {
			best = candidate
		}
	}
	return best
}

func bestStandaloneOWE(entries []*normalizedBSS, rejected map[string]struct{}) *BSS {
	// Once any BSS claims OWE transition mode, a direct OWE selection would
	// bypass the bidirectional transition check. Only a scan with no transition
	// claims at all may select OWE directly.
	for _, entry := range entries {
		if entry.transitionClaimed || entry.transitionErr != nil {
			return nil
		}
	}

	var best *normalizedBSS
	for _, entry := range entries {
		if entry.bss.Security != SecurityOWE || entry.bss.SSID == "" {
			continue
		}
		if _, wasRejected := rejected[bssidKey(entry.bss.BSSID)]; wasRejected {
			continue
		}
		if best == nil || entry.bss.RSSI > best.bss.RSSI ||
			(entry.bss.RSSI == best.bss.RSSI && entry.index < best.index) {
			best = entry
		}
	}
	if best == nil {
		return nil
	}
	selected := cloneBSS(best.bss)
	return &selected
}

func bestOpen(entries []*normalizedBSS) *normalizedBSS {
	var best *normalizedBSS
	for _, entry := range entries {
		if entry.bss.Security != SecurityOpen {
			continue
		}
		if best == nil || entry.bss.RSSI > best.bss.RSSI ||
			(entry.bss.RSSI == best.bss.RSSI && entry.index < best.index) {
			best = entry
		}
	}
	return best
}

func bestRejectedOpen(entries []*normalizedBSS, rejected map[string]struct{}) *normalizedBSS {
	var best *normalizedBSS
	for _, entry := range entries {
		if entry.bss.Security != SecurityOpen || entry.transition == nil {
			continue
		}
		if _, wasRejected := rejected[bssidKey(entry.transition.BSSID)]; !wasRejected {
			continue
		}
		if best == nil || entry.bss.RSSI > best.bss.RSSI ||
			(entry.bss.RSSI == best.bss.RSSI && entry.index < best.index) {
			best = entry
		}
	}
	return best
}

func transitionFailureReason(all, valid []*normalizedBSS) (Reason, string) {
	for _, entry := range all {
		if entry.transitionErr == nil {
			continue
		}
		if errors.Is(entry.transitionErr, ErrMalformedTransitionElement) ||
			errors.Is(entry.transitionErr, ErrMalformedIEs) ||
			errors.Is(entry.transitionErr, ErrAmbiguousTransitionElement) {
			return ReasonMalformedTransition, fmt.Sprintf("BSS %s: %v", entry.bss.BSSID, entry.transitionErr)
		}
	}

	byBSSID := make(map[string]*normalizedBSS, len(valid))
	for _, entry := range valid {
		byBSSID[bssidKey(entry.bss.BSSID)] = entry
	}

	for _, open := range valid {
		if open.bss.Security != SecurityOpen || open.transition == nil {
			continue
		}
		target := byBSSID[bssidKey(open.transition.BSSID)]
		if target == nil {
			return ReasonTransitionTargetAbsent, fmt.Sprintf("open BSS %s references absent BSSID %s", open.bss.BSSID, open.transition.BSSID)
		}
		if target.bss.Security != SecurityOWE {
			return ReasonBSSIDMismatch, fmt.Sprintf("open BSS %s references non-OWE BSS %s", open.bss.BSSID, open.transition.BSSID)
		}
		if target.bss.SSID != "" && target.bss.SSID != open.transition.SSID {
			return ReasonSSIDMismatch, fmt.Sprintf("open BSS %s declares OWE SSID %q but BSS %s advertises %q",
				open.bss.BSSID, open.transition.SSID, target.bss.BSSID, target.bss.SSID)
		}
		if target.transition == nil {
			return ReasonOneWayTransition, fmt.Sprintf("open BSS %s references OWE BSS %s but the OWE BSS does not reference it back",
				open.bss.BSSID, target.bss.BSSID)
		}
		if !sameBSSID(target.transition.BSSID, open.bss.BSSID) {
			return ReasonBSSIDMismatch, fmt.Sprintf("open BSS %s references %s but the OWE BSS references %s",
				open.bss.BSSID, target.bss.BSSID, target.transition.BSSID)
		}
		if target.transition.SSID != open.bss.SSID {
			return ReasonSSIDMismatch, fmt.Sprintf("OWE BSS %s references open SSID %q but the open BSS advertises %q",
				target.bss.BSSID, target.transition.SSID, open.bss.SSID)
		}
	}

	for _, owe := range valid {
		if owe.bss.Security != SecurityOWE || owe.transition == nil {
			continue
		}
		target := byBSSID[bssidKey(owe.transition.BSSID)]
		if target == nil {
			return ReasonTransitionTargetAbsent, fmt.Sprintf("OWE BSS %s references absent BSSID %s", owe.bss.BSSID, owe.transition.BSSID)
		}
		if target.bss.Security != SecurityOpen {
			return ReasonBSSIDMismatch, fmt.Sprintf("OWE BSS %s references non-open BSS %s", owe.bss.BSSID, owe.transition.BSSID)
		}
		if target.transition == nil {
			return ReasonOneWayTransition, fmt.Sprintf("OWE BSS %s references open BSS %s but the open BSS does not reference it back",
				owe.bss.BSSID, target.bss.BSSID)
		}
		if target.transition.SSID != owe.bss.SSID && owe.bss.SSID != "" {
			return ReasonSSIDMismatch, fmt.Sprintf("open BSS %s declares OWE SSID %q but the OWE BSS advertises %q",
				target.bss.BSSID, target.transition.SSID, owe.bss.SSID)
		}
	}

	return "", ""
}

func blocked(reason Reason, detail string) Decision {
	return Decision{
		Action: ActionBlock,
		Reason: reason,
		Detail: detail,
	}
}
