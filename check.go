package netsec

import (
	"encoding/hex"
	"fmt"
	"sort"
)

type checker struct {
	order map[string]int

	authCaps map[string]map[string]struct{}
	clueCaps map[string]map[string]struct{}
	seenPeer map[string]struct{}

	authedSelection   string
	selectionSeen     bool
	authedSelectionOk bool

	authedConfirm map[string]*Confirmation
	confSeen      map[string]bool

	phase     int // 0 capabilities, 1 selection, 2 confirmation
	sessionID string

	findings []Finding
}

// Check verifies a negotiation transcript against the downgrade rules.
//
// The selection must lie in the intersection of both peers' authenticated
// capabilities and must be the highest-preference member of that
// intersection. Unauthenticated entries are retained as clues but never
// count as proof. Confirmations must authenticate the same algorithm and
// bind to the digest of the preceding raw transcript bytes, which detects
// deletion, reordering and cross-session splicing.
func Check(t *Transcript) (*Report, error) {
	if t == nil {
		return nil, fmt.Errorf("netsec: nil transcript")
	}

	rank := make(map[string]int, len(t.PreferenceOrder))
	for _, alg := range t.PreferenceOrder {
		if alg == "" {
			return nil, fmt.Errorf("netsec: preference order contains empty algorithm")
		}
		if _, dup := rank[alg]; dup {
			return nil, fmt.Errorf("netsec: preference order lists %q more than once", alg)
		}
		rank[alg] = len(rank)
	}

	c := &checker{
		order:         rank,
		authCaps:      map[string]map[string]struct{}{},
		clueCaps:      map[string]map[string]struct{}{},
		seenPeer:      map[string]struct{}{},
		authedConfirm: map[string]*Confirmation{},
		confSeen:      map[string]bool{},
	}

	for i := range t.Envelopes {
		c.process(i, t.Envelopes[i], PrefixDigest(t.Envelopes, i))
	}

	c.finish()

	report := &Report{
		SessionID:                   c.sessionID,
		TranscriptHash:              hex.EncodeToString(TranscriptDigest(t.Envelopes)),
		AuthenticatedCapabilities:   stringSets(c.authCaps),
		UnauthenticatedCapabilities: stringSets(c.clueCaps),
		AuthenticatedSelection:      c.authedSelection,
		Intersection:                c.intersection(),
		Findings:                    c.findings,
	}
	report.Secure = !c.hasError()
	return report, nil
}

func (c *checker) process(index int, env Envelope, prefixHash []byte) {
	msg, err := DecodeMessage(env.Raw)
	if err != nil {
		c.add(index, FindingMalformedMessage, SeverityError, "", err.Error())
		return
	}

	c.seenPeer[msg.Sender] = struct{}{}

	if c.sessionID == "" {
		c.sessionID = msg.SessionID
	} else if msg.SessionID != c.sessionID {
		c.add(index, FindingSessionMismatch, SeverityError, msg.Sender,
			fmt.Sprintf("session id %q does not match transcript session %q", msg.SessionID, c.sessionID))
	}

	if !env.Auth.Authenticated {
		c.handleUnauthenticated(index, msg)
		return
	}
	if env.Auth.Signer != msg.Sender {
		c.add(index, FindingSignerMismatch, SeverityError, msg.Sender,
			fmt.Sprintf("authenticated signer %q does not match sender %q", env.Auth.Signer, msg.Sender))
		return
	}

	switch msg.Type {
	case TypeCapabilities:
		c.checkPhase(index, 0, msg)
		c.addAuthenticatedCaps(msg.Sender, msg.Capabilities)
	case TypeSelection:
		c.checkPhase(index, 1, msg)
		c.selectionSeen = true
		if c.authedSelectionOk {
			c.add(index, FindingDuplicateMessage, SeverityError, msg.Sender,
				"multiple authenticated selections")
			return
		}
		c.authedSelection = msg.Selection
		c.authedSelectionOk = true
	case TypeConfirmation:
		c.checkPhase(index, 2, msg)
		c.confSeen[msg.Sender] = true
		c.checkConfirmation(index, msg, prefixHash)
		c.authedConfirm[msg.Sender] = msg.Confirmation
	}
}

func (c *checker) checkPhase(index int, want int, msg *Message) {
	if want < c.phase {
		c.add(index, FindingOrderViolation, SeverityError, msg.Sender,
			fmt.Sprintf("%s message appears after the negotiation phase advanced", msg.Type))
		return
	}
	// Only authenticated messages advance the phase; a malformed or
	// unauthenticated entry must never legitimize later ordering.
	c.phase = want
}

func messagePhase(typ string) (int, bool) {
	switch typ {
	case TypeCapabilities:
		return 0, true
	case TypeSelection:
		return 1, true
	case TypeConfirmation:
		return 2, true
	default:
		return 0, false
	}
}

func (c *checker) checkConfirmation(index int, msg *Message, prefixHash []byte) {
	want := hex.EncodeToString(prefixHash)
	if msg.Confirmation.TranscriptHash != want {
		c.add(index, FindingConfirmationHashMismatch, SeverityError, msg.Sender,
			fmt.Sprintf("confirmation transcript hash %q does not match digest %q of preceding messages",
				msg.Confirmation.TranscriptHash, want))
	}
}

func (c *checker) handleUnauthenticated(index int, msg *Message) {
	c.add(index, FindingUnauthenticated, SeverityError, msg.Sender,
		fmt.Sprintf("%s message lacks valid authentication", msg.Type))
	if want, ok := messagePhase(msg.Type); ok {
		// Check but do not advance the phase.
		if want < c.phase {
			c.add(index, FindingOrderViolation, SeverityError, msg.Sender,
				fmt.Sprintf("unauthenticated %s message appears after the negotiation phase advanced", msg.Type))
		}
	}
	switch msg.Type {
	case TypeCapabilities:
		c.addClueCaps(msg.Sender, msg.Capabilities)
		c.add(index, FindingUnauthenticatedCapabilities, SeverityClue, msg.Sender,
			fmt.Sprintf("unauthenticated capability claim %v is a clue, not proof of support", msg.Capabilities))
	case TypeSelection:
		c.selectionSeen = true
		c.add(index, FindingUnauthenticatedSelection, SeverityClue, msg.Sender,
			fmt.Sprintf("unauthenticated selection %q cannot be trusted", msg.Selection))
	case TypeConfirmation:
		c.confSeen[msg.Sender] = true
		c.add(index, FindingUnauthenticatedConfirmation, SeverityError, msg.Sender,
			"confirmation failed authentication")
	}
}

func (c *checker) addAuthenticatedCaps(peer string, algs []string) {
	set, ok := c.authCaps[peer]
	if !ok {
		set = map[string]struct{}{}
		c.authCaps[peer] = set
	}
	for _, alg := range algs {
		set[alg] = struct{}{}
	}
}

func (c *checker) addClueCaps(peer string, algs []string) {
	set, ok := c.clueCaps[peer]
	if !ok {
		set = map[string]struct{}{}
		c.clueCaps[peer] = set
	}
	for _, alg := range algs {
		set[alg] = struct{}{}
	}
}

func (c *checker) finish() {
	peers := c.knownPeers()

	for _, peer := range peers {
		if len(c.authCaps[peer]) == 0 {
			c.add(-1, FindingMissingCapabilities, SeverityError, peer,
				"peer sent no authenticated capabilities")
		}
	}

	if !c.selectionSeen {
		c.add(-1, FindingMissingSelection, SeverityError, "",
			"transcript contains no selection message")
	} else if !c.authedSelectionOk {
		c.add(-1, FindingMissingSelection, SeverityError, "",
			"no authenticated selection message")
	}

	if c.authedSelectionOk && len(c.authCaps) >= 2 {
		for _, peer := range peers {
			set, ok := c.authCaps[peer]
			if !ok {
				continue
			}
			if _, ok := set[c.authedSelection]; !ok {
				c.add(-1, FindingSelectionNotInCapabilities, SeverityError, peer,
					fmt.Sprintf("selected algorithm %q is not in peer's authenticated capabilities", c.authedSelection))
			}
		}
		c.checkPreference()
	}

	for _, peer := range peers {
		if !c.confSeen[peer] {
			c.add(-1, FindingMissingConfirmation, SeverityError, peer,
				"peer sent no confirmation")
			continue
		}
		conf, ok := c.authedConfirm[peer]
		if !ok {
			continue
		}
		if c.authedSelectionOk && conf.Algorithm != c.authedSelection {
			c.add(-1, FindingConfirmationAlgorithmMismatch, SeverityError, peer,
				fmt.Sprintf("confirmation binds %q but selection is %q", conf.Algorithm, c.authedSelection))
		}
	}
}

func (c *checker) checkPreference() {
	rank, ok := c.order[c.authedSelection]
	if !ok {
		c.add(-1, FindingAlgorithmNotRanked, SeverityError, "",
			fmt.Sprintf("selected algorithm %q is absent from the preference order", c.authedSelection))
		return
	}
	for alg, algRank := range c.order {
		if algRank >= rank {
			continue
		}
		if c.inAllAuthCaps(alg) {
			c.add(-1, FindingPreferenceViolation, SeverityError, "",
				fmt.Sprintf("higher-preference algorithm %q belongs to the authenticated intersection but %q was selected",
					alg, c.authedSelection))
		}
	}
}

func (c *checker) inAllAuthCaps(alg string) bool {
	for _, set := range c.authCaps {
		if _, ok := set[alg]; !ok {
			return false
		}
	}
	return true
}

func (c *checker) intersection() []string {
	if len(c.authCaps) < 2 {
		return nil
	}
	common := map[string]struct{}{}
	first := true
	for _, set := range c.authCaps {
		if first {
			for alg := range set {
				common[alg] = struct{}{}
			}
			first = false
			continue
		}
		for alg := range common {
			if _, ok := set[alg]; !ok {
				delete(common, alg)
			}
		}
	}

	var out []string
	for alg := range common {
		out = append(out, alg)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, iok := c.order[out[i]]
		rj, jok := c.order[out[j]]
		switch {
		case iok && jok:
			return ri < rj
		case iok:
			return true
		case jok:
			return false
		default:
			return out[i] < out[j]
		}
	})
	return out
}

func (c *checker) knownPeers() []string {
	out := make([]string, 0, len(c.seenPeer))
	for peer := range c.seenPeer {
		out = append(out, peer)
	}
	sort.Strings(out)
	return out
}

func (c *checker) hasError() bool {
	for _, f := range c.findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (c *checker) add(index int, code string, sev Severity, peer, detail string) {
	c.findings = append(c.findings, Finding{
		Index: index, Code: code, Severity: sev, Peer: peer, Detail: detail,
	})
}

func stringSets(sets map[string]map[string]struct{}) map[string][]string {
	if len(sets) == 0 {
		return nil
	}
	out := make(map[string][]string, len(sets))
	for peer, set := range sets {
		algs := make([]string, 0, len(set))
		for alg := range set {
			algs = append(algs, alg)
		}
		sort.Strings(algs)
		out[peer] = algs
	}
	return out
}
