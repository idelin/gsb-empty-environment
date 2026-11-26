package verifier

import (
	"crypto/ed25519"
	"fmt"
	"slices"
	"strings"

	root "formal.local/h26-32a6cdd198"
)

func VerifyBundle(u *root.Universe, bundle *root.CertificateBundle) error {
	if bundle == nil {
		return fmt.Errorf("certificate bundle is nil")
	}
	hashOK, err := bundle.VerifyHash()
	if err != nil {
		return err
	}
	if !hashOK {
		return fmt.Errorf("certificate bundle hash mismatch")
	}
	if err := validateSignatures(u); err != nil {
		return err
	}
	if bundle.RootID != u.RootDelegationID {
		return fmt.Errorf("bundle root %q does not match universe root %q", bundle.RootID, u.RootDelegationID)
	}
	byID := map[string]root.SignedDelegation{}
	for _, signed := range u.Delegations {
		byID[signed.Delegation.ID] = signed
	}
	children := map[string][]string{}
	for id, signed := range byID {
		for _, parent := range signed.Delegation.Parents {
			children[parent.DelegationID] = append(children[parent.DelegationID], id)
		}
	}
	reachable := map[string]struct{}{}
	queue := []string{bundle.RootID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := reachable[id]; ok {
			continue
		}
		reachable[id] = struct{}{}
		queue = append(queue, children[id]...)
	}
	if len(bundle.Certificates) != len(reachable) {
		return fmt.Errorf("certificate set does not match root-reachable delegations")
	}
	for id := range bundle.Certificates {
		if _, ok := reachable[id]; !ok {
			return fmt.Errorf("certificate for unreachable delegation %q", id)
		}
	}
	paths := shortestPaths(u, bundle)
	visited := map[string]struct{}{}
	var visit func(id string) error
	visit = func(id string) error {
		if _, ok := visited[id]; ok {
			return nil
		}
		_, ok := byID[id]
		if !ok {
			return fmt.Errorf("delegation %q is absent", id)
		}
		cert, ok := bundle.Certificates[id]
		if !ok {
			return fmt.Errorf("missing certificate for delegation %q", id)
		}
		hashOK, err := cert.VerifyHash()
		if err != nil {
			return fmt.Errorf("delegation %q hash: %w", id, err)
		}
		if !hashOK {
			return fmt.Errorf("delegation %q certificate hash mismatch", id)
		}
		if err := verifyCertificate(u, byID, bundle, cert, paths[id], reachable); err != nil {
			return fmt.Errorf("delegation %q: %w", id, err)
		}
		visited[id] = struct{}{}
		for _, childID := range children[id] {
			if _, ok := reachable[childID]; ok {
				if err := visit(childID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(bundle.RootID)
}

func verifyCertificate(
	u *root.Universe,
	byID map[string]root.SignedDelegation,
	bundle *root.CertificateBundle,
	cert *root.DelegationCertificate,
	statePaths map[int][]string,
	reachable map[string]struct{},
) error {
	d := byID[cert.DelegationID].Delegation
	if cert.Tenant != u.Tenant || cert.Action != u.Action ||
		!slices.Equal(cert.PathAlphabet, u.PathAlphabet) ||
		cert.QuorumK != d.QuorumK {
		return fmt.Errorf("certificate header does not independently match the signed delegation")
	}
	parents := make([]string, 0, len(d.Parents))
	parentSeen := map[string]struct{}{}
	for _, parent := range d.Parents {
		if _, ok := parentSeen[parent.DelegationID]; ok {
			return fmt.Errorf("duplicate parent reference")
		}
		parentSeen[parent.DelegationID] = struct{}{}
		parents = append(parents, parent.DelegationID)
	}
	slices.Sort(parents)
	if !slices.Equal(cert.ParentCertificateIDs, parents) {
		return fmt.Errorf("parent list mismatch")
	}
	wantSegments := timeSegments(collectReachableBoundaries(byID, reachable))
	if !sameSegments(cert.TimeSegments, wantSegments) {
		return fmt.Errorf("time segments do not match signed rule endpoints")
	}
	for _, state := range cert.PathStates {
		if len(state.Transition) != len(u.PathAlphabet) {
			return fmt.Errorf("path state transition is not defined for every alphabet symbol")
		}
		for _, next := range state.Transition {
			if next < 0 || next >= len(cert.PathStates) {
				return fmt.Errorf("path transition points outside certificate states")
			}
		}
	}
	if err := verifyPathMachine(cert, d.Rules); err != nil {
		return err
	}
	tagSets := validTagSets(u)
	if len(cert.Outputs) != len(cert.PathStates)*len(tagSets)*len(cert.TimeSegments) {
		return fmt.Errorf("output table is not complete")
	}
	seenOutputs := map[string]struct{}{}
	for _, output := range cert.Outputs {
		if output.PathStateID < 0 || output.PathStateID >= len(cert.PathStates) ||
			output.TimeSegmentIndex < 0 || output.TimeSegmentIndex >= len(cert.TimeSegments) {
			return fmt.Errorf("output references an unknown state or segment")
		}
		tags := output.TagSet
		if !slices.ContainsFunc(tagSets, func(valid []string) bool { return slices.Equal(valid, tags) }) {
			return fmt.Errorf("output tag set violates label constraints")
		}
		key := fmt.Sprintf("%d|%s|%d", output.PathStateID, strings.Join(tags, ","), output.TimeSegmentIndex)
		if _, ok := seenOutputs[key]; ok {
			return fmt.Errorf("duplicate certificate output")
		}
		seenOutputs[key] = struct{}{}
		path := statePaths[output.PathStateID]
		timestamp := cert.TimeSegments[output.TimeSegmentIndex].Start
		local, last := localDecision(byID[cert.DelegationID].Delegation, path, tags, timestamp)
		if local != output.LocalEffect || !sameRule(last, output.LastRule) {
			return fmt.Errorf("local effect or last matching rule differs from independent evaluation")
		}
		want := independentDecision(u, byID, bundle, cert.DelegationID, path, tags, timestamp)
		if want != output.Decision {
			return fmt.Errorf("independent decision %q differs from certificate decision %q", want, output.Decision)
		}
		if cert.DelegationID != u.RootDelegationID && local == root.EffectAllow {
			allowing := allowingParents(byID[cert.DelegationID].Delegation, bundle, path, tags, timestamp)
			if output.Achieved != len(allowing) || !slices.Equal(output.AllowingParents, allowing) {
				return fmt.Errorf("quorum attribution differs from independent evaluation")
			}
		}
	}
	return nil
}

func verifyPathMachine(cert *root.DelegationCertificate, rules []root.Rule) error {
	dfas := make([][][]int, len(rules))
	for i, rule := range rules {
		dfas[i] = buildRuleDFA(rule.Pattern, cert.PathAlphabet)
	}
	if len(cert.PathStates) == 0 {
		return fmt.Errorf("path machine has no states")
	}
	if !slices.Equal(cert.PathStates[0].RuleState, zeroRuleStates(len(rules))) {
		return fmt.Errorf("path machine start state differs from pattern start states")
	}
	expectedStates := map[string]struct{}{stateKey(cert.PathStates[0].RuleState): {}}
	queue := [][]int{cert.PathStates[0].RuleState}
	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		for symbolIndex := range cert.PathAlphabet {
			next := make([]int, len(rules))
			for ruleIndex := range rules {
				next[ruleIndex] = dfas[ruleIndex][state[ruleIndex]][symbolIndex]
			}
			key := stateKey(next)
			if _, ok := expectedStates[key]; !ok {
				expectedStates[key] = struct{}{}
				queue = append(queue, next)
			}
		}
	}
	if len(cert.PathStates) != len(expectedStates) {
		return fmt.Errorf("reachable path machine state set differs from reconstructed DFA")
	}
	for stateID, state := range cert.PathStates {
		if state.ID != stateID || len(state.RuleState) != len(rules) {
			return fmt.Errorf("path state identity or width is invalid")
		}
		if _, ok := expectedStates[stateKey(state.RuleState)]; !ok {
			return fmt.Errorf("path state is unreachable in reconstructed DFA")
		}
		for ruleIndex, ruleState := range state.RuleState {
			if ruleState < 0 || ruleState >= len(dfas[ruleIndex]) {
				return fmt.Errorf("rule automaton state outside reconstructed DFA")
			}
		}
		for symbolIndex := range cert.PathAlphabet {
			next := state.Transition[symbolIndex]
			want := make([]int, len(rules))
			for ruleIndex := range rules {
				want[ruleIndex] = dfas[ruleIndex][state.RuleState[ruleIndex]][symbolIndex]
			}
			if !slices.Equal(cert.PathStates[next].RuleState, want) {
				return fmt.Errorf("path transition differs from independently reconstructed DFA")
			}
		}
	}
	return nil
}

func zeroRuleStates(n int) []int {
	out := make([]int, n)
	return out
}

func buildRuleDFA(pattern string, alphabet []string) [][]int {
	tokens := strings.Split(pattern, "/")
	states := [][]int{nfaEpsilonClosure(tokens, []int{0})}
	index := map[string]int{stateKey(states[0]): 0}
	var transitions [][]int
	for queue := 0; queue < len(states); queue++ {
		state := states[queue]
		row := make([]int, len(alphabet))
		for symbolIndex, symbol := range alphabet {
			next := nfaEpsilonClosure(tokens, nfaTransition(tokens, state, symbol))
			key := stateKey(next)
			if id, ok := index[key]; ok {
				row[symbolIndex] = id
			} else {
				id = len(states)
				index[key] = id
				states = append(states, next)
				row[symbolIndex] = id
			}
		}
		transitions = append(transitions, row)
	}
	return transitions
}

func stateKey(state []int) string {
	parts := make([]string, len(state))
	for i, value := range state {
		parts[i] = fmt.Sprintf("%d", value)
	}
	return strings.Join(parts, ",")
}

func nfaEpsilonClosure(tokens []string, states []int) []int {
	reachable := append([]int(nil), states...)
	for _, state := range reachable {
		if state < len(tokens) && tokens[state] == "**" && !slices.Contains(reachable, state+1) {
			reachable = append(reachable, state+1)
		}
	}
	slices.Sort(reachable)
	return slices.Compact(reachable)
}

func nfaTransition(tokens []string, states []int, symbol string) []int {
	var next []int
	for _, state := range states {
		if state >= len(tokens) {
			continue
		}
		token := tokens[state]
		if token == "**" {
			next = append(next, state)
			if state+1 < len(tokens) && tokens[state+1] == symbol {
				next = append(next, state+2)
			} else if state+1 == len(tokens) {
				next = append(next, state+1)
			}
		} else if token == "*" || token == symbol {
			next = append(next, state+1)
		}
	}
	slices.Sort(next)
	return slices.Compact(next)
}

func allowingParents(d root.Delegation, bundle *root.CertificateBundle, path, tags []string, timestamp int64) []string {
	var allowing []string
	for _, parent := range d.Parents {
		if parentDecision(bundle, parent.DelegationID, path, tags, timestamp) == root.EffectAllow {
			allowing = append(allowing, parent.DelegationID)
		}
	}
	slices.Sort(allowing)
	return allowing
}

func collectReachableBoundaries(byID map[string]root.SignedDelegation, reachable map[string]struct{}) []int64 {
	var boundaries []int64
	for id := range reachable {
		signed := byID[id]
		d := signed.Delegation
		for _, rule := range d.Rules {
			boundaries = append(boundaries, rule.Start, rule.End)
		}
	}
	slices.Sort(boundaries)
	return slices.Compact(boundaries)
}

func timeSegments(boundaries []int64) []root.TimeSegment {
	if len(boundaries) == 0 {
		return []root.TimeSegment{{Start: -1 << 63}}
	}
	firstEnd := boundaries[0]
	segments := []root.TimeSegment{{Start: -1 << 63, End: &firstEnd}}
	segments = append(segments, root.TimeSegment{Start: boundaries[0]})
	for i := 1; i < len(boundaries); i++ {
		end := boundaries[i]
		segments[len(segments)-1].End = &end
		segments = append(segments, root.TimeSegment{Start: boundaries[i]})
	}
	return segments
}

func sameSegments(a, b []root.TimeSegment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Start != b[i].Start {
			return false
		}
		if (a[i].End == nil) != (b[i].End == nil) {
			return false
		}
		if a[i].End != nil && *a[i].End != *b[i].End {
			return false
		}
	}
	return true
}

func sameRule(a, b *root.RuleRef) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.DelegationID == b.DelegationID && a.RuleIndex == b.RuleIndex && a.Effect == b.Effect
}

func independentDecision(
	u *root.Universe,
	byID map[string]root.SignedDelegation,
	bundle *root.CertificateBundle,
	id string,
	path, tags []string,
	timestamp int64,
) root.Effect {
	d := byID[id].Delegation
	local, _ := localDecision(d, path, tags, timestamp)
	if local == root.EffectDeny {
		return root.EffectDeny
	}
	if id == u.RootDelegationID {
		return root.EffectAllow
	}
	achieved := 0
	for _, parent := range d.Parents {
		parentID := parent.DelegationID
		if parentDecision(bundle, parentID, path, tags, timestamp) == root.EffectAllow {
			achieved++
		}
	}
	if achieved >= d.QuorumK {
		return root.EffectAllow
	}
	return root.EffectDeny
}

func parentDecision(bundle *root.CertificateBundle, id string, path, tags []string, timestamp int64) root.Effect {
	cert := bundle.Certificates[id]
	if cert == nil {
		return root.EffectDeny
	}
	state := 0
	for _, symbol := range path {
		index := slices.Index(cert.PathAlphabet, symbol)
		if index < 0 || state >= len(cert.PathStates) {
			return root.EffectDeny
		}
		state = cert.PathStates[state].Transition[index]
	}
	segment := slices.IndexFunc(cert.TimeSegments, func(segment root.TimeSegment) bool {
		return segment.Start == timestamp
	})
	if segment < 0 {
		return root.EffectDeny
	}
	for _, output := range cert.Outputs {
		if output.PathStateID == state && output.TimeSegmentIndex == segment && slices.Equal(output.TagSet, tags) {
			return output.Decision
		}
	}
	return root.EffectDeny
}

func localDecision(d root.Delegation, path, tags []string, timestamp int64) (root.Effect, *root.RuleRef) {
	last := -1
	for i, rule := range d.Rules {
		if ruleActive(rule, timestamp) && predicateMatches(rule.Predicate, tags) && patternMatches(rule.Pattern, path) {
			last = i
		}
	}
	if last < 0 {
		return root.EffectDeny, nil
	}
	rule := d.Rules[last]
	return rule.Effect, &root.RuleRef{DelegationID: d.ID, RuleIndex: last, Effect: rule.Effect}
}

func patternMatches(pattern string, path []string) bool {
	tokens := strings.Split(pattern, "/")
	var match func(p, t int) bool
	match = func(p, t int) bool {
		if p == len(tokens) {
			return t == len(path)
		}
		token := tokens[p]
		if token == "**" {
			for skip := 0; t+skip <= len(path); skip++ {
				if match(p+1, t+skip) {
					return true
				}
			}
			return false
		}
		if t == len(path) {
			return false
		}
		if token == "*" || token == path[t] {
			return match(p+1, t+1)
		}
		return false
	}
	return match(0, 0)
}

func shortestPaths(u *root.Universe, bundle *root.CertificateBundle) map[string]map[int][]string {
	out := map[string]map[int][]string{}
	for id, cert := range bundle.Certificates {
		paths := map[int][]string{0: {}}
		queue := []int{0}
		for len(queue) > 0 {
			state := queue[0]
			queue = queue[1:]
			for symbolIndex, symbol := range u.PathAlphabet {
				next := cert.PathStates[state].Transition[symbolIndex]
				if _, ok := paths[next]; ok {
					continue
				}
				paths[next] = append(append([]string(nil), paths[state]...), symbol)
				queue = append(queue, next)
			}
		}
		out[id] = paths
	}
	return out
}

func ruleActive(rule root.Rule, timestamp int64) bool {
	return rule.Start <= timestamp && timestamp < rule.End
}

func predicateMatches(predicate root.TagPredicate, tags []string) bool {
	for _, required := range predicate.RequireAll {
		if !slices.Contains(tags, required) {
			return false
		}
	}
	for _, forbidden := range predicate.ForbidAll {
		if slices.Contains(tags, forbidden) {
			return false
		}
	}
	return true
}

func validTagSets(u *root.Universe) [][]string {
	var out [][]string
	for mask := 0; mask < 1<<len(u.Labels); mask++ {
		var tags []string
		for i, label := range u.Labels {
			if mask&(1<<i) != 0 {
				tags = append(tags, label)
			}
		}
		if validTags(tags, u.LabelRules) {
			out = append(out, tags)
		}
	}
	slices.SortFunc(out, compareSlices)
	return out
}

func validTags(tags []string, rules root.LabelConstraints) bool {
	for _, required := range rules.Required {
		if !slices.Contains(tags, required) {
			return false
		}
	}
	for _, pair := range rules.MutexPairs {
		if slices.Contains(tags, pair[0]) && slices.Contains(tags, pair[1]) {
			return false
		}
	}
	for _, implication := range rules.Implications {
		if slices.Contains(tags, implication.IfLabel) && !slices.Contains(tags, implication.ThenLabel) {
			return false
		}
	}
	return true
}

func compareSlices(a, b []string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return len(a) - len(b)
}

func validateSignatures(u *root.Universe) error {
	for _, signed := range u.Delegations {
		key := u.KeyRing[signed.Delegation.SignerKey]
		encoded, err := root.CanonicalDelegationJSON(signed.Delegation)
		if err != nil {
			return err
		}
		if len(key) == 0 || !ed25519.Verify(key, encoded, signed.Signature) {
			return fmt.Errorf("delegation %q invalid signature", signed.Delegation.ID)
		}
	}
	return nil
}
