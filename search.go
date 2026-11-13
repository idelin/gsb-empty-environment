package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// TransitionFunc is the callback shape used for both the audited
// implementation and the reference model.
type TransitionFunc func(*Session, HopRequest) (*Session, error)

// RoleSpec describes one target role available to the search.
type RoleSpec struct {
	RoleID                string
	FixedLabels           map[string]string
	OverrideKeys          []string
	TargetTransitiveKeys  []string
	FixedTransitiveKeys   []string
	AllowedTransitiveKeys []string
}

// Role is a compatibility alias for RoleSpec.
type Role = RoleSpec

// Scenario describes a finite search space.  Values and Keys can be supplied
// explicitly to make the search deterministic for a particular policy.
type Scenario struct {
	InitialLabels     map[string]string
	InitialTransitive []string
	Roles             []RoleSpec
	Keys              []string
	Values            []string
	Policy            any
	MaxDepth          int
	MaxStates         int
	MaxCandidates     int
}

// Witness is a shortest path on which the audited implementation accepts a
// hop that the secure model rejects, and the resource policy moves from deny
// to allow.
type Witness struct {
	Requests        []HopRequest
	AuditedStates   []*Session
	ModelError      error
	RejectedAt      int
	InitialDecision Decision
	FinalDecision   Decision
	InitialLabels   map[string]string
	FinalLabels     map[string]string
}

func (w *Witness) Depth() int {
	if w == nil {
		return 0
	}
	return len(w.Requests)
}

func (w *Witness) String() string {
	if w == nil {
		return "<nil witness>"
	}
	return fmt.Sprintf("label escalation in %d hop(s), rejected at hop %d: %v; policy %s -> %s",
		len(w.Requests), w.RejectedAt, w.ModelError, w.InitialDecision, w.FinalDecision)
}

// SearchConfig controls FindEscalation.  Only fields not supplied through the
// variadic arguments need to be set.
type SearchConfig struct {
	MaxDepth      int
	MaxStates     int
	MaxCandidates int
	Policy        any
	Audited       TransitionFunc
}

// FindEscalation searches up to MaxDepth (at most four) hops for the shortest
// witness where:
//   - the audited transition accepts the path,
//   - the secure reference model rejects at least one hop on the path, and
//   - the policy is deny on the initial labels and allow on the final labels.
//
// start may be a *Session, Session, map[string]string, *HopResult, or nil.
// candidates may be []HopRequest, []*HopRequest, a Scenario, or a
// CandidateProvider.  Variadic arguments may contain a Policy/PolicyFunc,
// TransitionFunc, AuditConfig, Flaw, SearchConfig, and/or an int max depth.
func FindEscalation(start any, candidates any, args ...any) (*Witness, error) {
	cfg := SearchConfig{
		MaxDepth:      MaxDepth,
		MaxStates:     200000,
		MaxCandidates: 20000,
	}
	var scenario *Scenario

	root, scen, err := sessionFrom(start)
	if err != nil {
		return nil, err
	}
	if scen != nil {
		scenario = scen
		if scen.Policy != nil {
			cfg.Policy = scen.Policy
		}
		if scen.MaxDepth > 0 {
			cfg.MaxDepth = scen.MaxDepth
		}
		if scen.MaxStates > 0 {
			cfg.MaxStates = scen.MaxStates
		}
		if scen.MaxCandidates > 0 {
			cfg.MaxCandidates = scen.MaxCandidates
		}
	}

	if s, ok := asScenario(candidates); ok {
		scenario = &s
		if s.Policy != nil {
			cfg.Policy = s.Policy
		}
		if s.MaxDepth > 0 {
			cfg.MaxDepth = s.MaxDepth
		}
		if s.MaxStates > 0 {
			cfg.MaxStates = s.MaxStates
		}
		if s.MaxCandidates > 0 {
			cfg.MaxCandidates = s.MaxCandidates
		}
	}

	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
		case SearchConfig:
			if v.MaxDepth > 0 {
				cfg.MaxDepth = v.MaxDepth
			}
			if v.MaxStates > 0 {
				cfg.MaxStates = v.MaxStates
			}
			if v.MaxCandidates > 0 {
				cfg.MaxCandidates = v.MaxCandidates
			}
			if v.Policy != nil {
				cfg.Policy = v.Policy
			}
			if v.Audited != nil {
				cfg.Audited = v.Audited
			}
		case *SearchConfig:
			if v != nil {
				if v.MaxDepth > 0 {
					cfg.MaxDepth = v.MaxDepth
				}
				if v.MaxStates > 0 {
					cfg.MaxStates = v.MaxStates
				}
				if v.MaxCandidates > 0 {
					cfg.MaxCandidates = v.MaxCandidates
				}
				if v.Policy != nil {
					cfg.Policy = v.Policy
				}
				if v.Audited != nil {
					cfg.Audited = v.Audited
				}
			}
		case PolicyFunc:
			cfg.Policy = v
		case func(map[string]string) bool:
			cfg.Policy = PolicyFunc(v)
		case func(LabelView) bool:
			cfg.Policy = ViewPolicyFunc(v)
		case func(map[string]string) Decision:
			cfg.Policy = PolicyFunc(func(labels map[string]string) bool { return v(labels) == Allow })
		case func(map[string]string) (bool, error):
			cfg.Policy = PolicyFunc(func(labels map[string]string) bool {
				ok, err := v(labels)
				return err == nil && ok
			})
		case Policy:
			cfg.Policy = v
		case TransitionFunc:
			cfg.Audited = v
		case func(*Session, HopRequest) (*Session, error):
			cfg.Audited = TransitionFunc(v)
		case func(HopRequest) (*HopResult, error):
			cfg.Audited = hopResultTransition(v)
		case func(HopRequest) (HopResult, error):
			cfg.Audited = func(s *Session, req HopRequest) (*Session, error) {
				res, err := v(bindRequest(s, req))
				if err != nil {
					return nil, err
				}
				return res.Session, nil
			}
		case func(*Session, HopRequest) (HopResult, error):
			cfg.Audited = func(s *Session, req HopRequest) (*Session, error) {
				res, err := v(s, bindRequest(s, req))
				if err != nil {
					return nil, err
				}
				return res.Session, nil
			}
		case AuditConfig:
			cfg.Audited = AuditedTransitionWithConfig(v)
		case Flaw:
			cfg.Audited = AuditedTransitionWithConfig(AuditConfig{Flaws: v})
		case int:
			if v > 0 {
				cfg.MaxDepth = v
			}
		case Scenario:
			scenario = &v
			if v.Policy != nil {
				cfg.Policy = v.Policy
			}
		case *Scenario:
			if v != nil {
				scenario = v
				if v.Policy != nil {
					cfg.Policy = v.Policy
				}
			}
		}
	}

	if cfg.Policy == nil {
		return nil, fmt.Errorf("%w: no policy supplied", ErrInvalidInput)
	}
	if cfg.MaxDepth <= 0 || cfg.MaxDepth > MaxDepth {
		cfg.MaxDepth = MaxDepth
	}
	if cfg.MaxStates <= 0 {
		cfg.MaxStates = 200000
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = 20000
	}
	if cfg.Audited == nil {
		cfg.Audited = AuditedTransition
	}

	initialDecision := Decide(cfg.Policy, root.Values())
	if initialDecision == Allow {
		return nil, ErrInitialAllow
	}

	var provider CandidateProvider
	var requests []HopRequest
	if p, ok := candidates.(CandidateProvider); ok {
		provider = p
	} else if p, ok := candidates.(func(*Session, int) []HopRequest); ok {
		provider = CandidateProvider(p)
	} else if scenario != nil && (candidates == nil || isScenario(candidates)) {
		// Scenario generation supplies the frontier.
	} else if roles, ok := candidates.([]RoleSpec); ok {
		scenario = &Scenario{
			InitialLabels:     root.Values(),
			InitialTransitive: root.TransitiveKeys(),
			Roles:             roles,
		}
	} else {
		var err error
		requests, err = asRequests(candidates)
		if err != nil {
			return nil, err
		}
	}

	type node struct {
		state    *Session
		reqs     []HopRequest
		states   []*Session
		modelErr error
		rejected int
	}

	initialKey := escalationStateKey(root, false)
	queue := []node{{
		state:  root,
		states: []*Session{root.Clone()},
	}}
	visited := map[string]bool{initialKey: true}
	statesSeen := 1

	for depth := 0; depth < cfg.MaxDepth; depth++ {
		next := make([]node, 0)
		for _, cur := range queue {
			var candidatesHere []HopRequest
			if provider != nil {
				candidatesHere = provider(cur.state, depth+1)
			} else if scenario != nil && len(requests) == 0 {
				candidatesHere = scenario.Candidates(cur.state, depth+1)
			} else {
				candidatesHere = requests
			}
			if len(candidatesHere) > cfg.MaxCandidates {
				candidatesHere = candidatesHere[:cfg.MaxCandidates]
			}

			for _, candidate := range candidatesHere {
				req := bindRequest(cur.state, candidate)
				auditedState, aerr := cfg.Audited(cur.state, req)
				if aerr != nil || auditedState == nil {
					continue
				}

				modelState, merr := SecureTransition(cur.state, req)
				_ = modelState

				child := node{
					state:    auditedState,
					reqs:     appendCopyRequests(cur.reqs, req),
					states:   appendCopyStates(cur.states, auditedState),
					modelErr: cur.modelErr,
					rejected: cur.rejected,
				}
				if merr != nil && child.modelErr == nil {
					child.modelErr = merr
					child.rejected = depth + 1
				}

				if child.modelErr != nil && Decide(cfg.Policy, auditedState.Values()) == Allow {
					return &Witness{
						Requests:        appendCopyRequests(nil, child.reqs...),
						AuditedStates:   appendCopyStates(nil, child.states...),
						ModelError:      child.modelErr,
						RejectedAt:      child.rejected,
						InitialDecision: Deny,
						FinalDecision:   Allow,
						InitialLabels:   root.Values(),
						FinalLabels:     auditedState.Values(),
					}, nil
				}

				if depth+1 >= cfg.MaxDepth {
					continue
				}
				key := escalationStateKey(auditedState, child.modelErr != nil)
				if visited[key] {
					continue
				}
				visited[key] = true
				statesSeen++
				if statesSeen > cfg.MaxStates {
					return nil, fmt.Errorf("%w: state budget exhausted", ErrNoEscalation)
				}
				next = append(next, child)
			}
		}
		queue = next
		if len(queue) == 0 {
			break
		}
	}
	return nil, ErrNoEscalation
}

func hopResultTransition(f func(HopRequest) (*HopResult, error)) TransitionFunc {
	return func(s *Session, req HopRequest) (*Session, error) {
		res, err := f(bindRequest(s, req))
		if err != nil {
			return nil, err
		}
		if res == nil || res.Session == nil {
			return nil, fmt.Errorf("%w: transition returned no session", ErrInvalidInput)
		}
		return res.Session, nil
	}
}

// CandidateProvider lets a test supply an exact search frontier.
type CandidateProvider func(*Session, int) []HopRequest

// FindEscalationWith is the strongly typed form of FindEscalation.
func FindEscalationWith(root *Session, candidates []HopRequest, audited TransitionFunc, policy PolicyFunc, maxDepth int) (*Witness, error) {
	if maxDepth <= 0 {
		maxDepth = MaxDepth
	}
	return FindEscalation(root, candidates, audited, policy, maxDepth)
}

// EvaluatePath checks one concrete request sequence.  It returns a Witness
// only if the audited path is accepted, the reference model rejects at least
// one hop, and the policy changes from deny to allow.
func EvaluatePath(start any, path []HopRequest, audited TransitionFunc, policy any) (*Witness, error) {
	root, _, err := sessionFrom(start)
	if err != nil {
		return nil, err
	}
	if len(path) > MaxDepth {
		return nil, ErrDepthExceeded
	}
	if Decide(policy, root.Values()) == Allow {
		return nil, ErrInitialAllow
	}
	if audited == nil {
		audited = AuditedTransition
	}
	cur := root
	states := []*Session{root.Clone()}
	reqs := make([]HopRequest, 0, len(path))
	var modelErr error
	rejectedAt := 0
	for i, candidate := range path {
		req := bindRequest(cur, candidate)
		next, err := audited(cur, req)
		if err != nil {
			return nil, err
		}
		if _, err := SecureTransition(cur, req); err != nil && modelErr == nil {
			modelErr = err
			rejectedAt = i + 1
		}
		cur = next
		reqs = append(reqs, req)
		states = append(states, next.Clone())
		if modelErr != nil && Decide(policy, cur.Values()) == Allow {
			return &Witness{
				Requests:        appendCopyRequests(nil, reqs...),
				AuditedStates:   appendCopyStates(nil, states...),
				ModelError:      modelErr,
				RejectedAt:      rejectedAt,
				InitialDecision: Deny,
				FinalDecision:   Allow,
				InitialLabels:   root.Values(),
				FinalLabels:     cur.Values(),
			}, nil
		}
	}
	return nil, ErrNoEscalation
}

// CheckPath is an alias for EvaluatePath.
func CheckPath(start any, path []HopRequest, audited TransitionFunc, policy any) (*Witness, error) {
	return EvaluatePath(start, path, audited, policy)
}

// Candidates generates a bounded, deterministic candidate set for one state.
func (s Scenario) Candidates(state *Session, depth int) []HopRequest {
	if state == nil || depth > MaxDepth {
		return nil
	}
	limit := s.MaxCandidates
	if limit <= 0 {
		limit = 20000
	}
	values := s.Values
	if len(values) == 0 {
		values = defaultSearchValues(s, state)
	}
	roles := append([]RoleSpec(nil), s.Roles...)
	sort.SliceStable(roles, func(i, j int) bool { return roles[i].RoleID < roles[j].RoleID })

	var out []HopRequest
	seen := make(map[string]bool)
	for _, role := range roles {
		keySet := make(map[string]bool)
		for _, k := range role.OverrideKeys {
			keySet[k] = true
		}
		for k := range role.FixedLabels {
			keySet[k] = true
		}
		for _, k := range s.Keys {
			keySet[k] = true
		}
		for k := range state.Labels {
			keySet[k] = true
		}
		keys := make([]string, 0, len(keySet))
		for k := range keySet {
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			continue
		}
		sort.Strings(keys)
		labelSets := candidateLabelSets(keys, values, limit)

		base := make(map[string]bool)
		for _, k := range state.TransitiveKeys() {
			base[k] = true
		}
		for _, k := range role.FixedLabels {
			base[k] = true
		}
		for _, k := range s.Keys {
			base[k] = true
		}
		for _, k := range role.OverrideKeys {
			base[k] = true
		}
		for k := range state.Labels {
			base[k] = true
		}
		baseKeys := make([]string, 0, len(base))
		for k := range base {
			baseKeys = append(baseKeys, k)
		}
		sort.Strings(baseKeys)
		transitiveSets := candidateTransitiveSets(baseKeys, limit)

		for _, labels := range labelSets {
			for _, trans := range transitiveSets {
				targetTransitive := role.TargetTransitiveKeys
				if targetTransitive == nil && role.AllowedTransitiveKeys != nil {
					targetTransitive = role.AllowedTransitiveKeys
				}
				req := HopRequest{
					CurrentLabelSet:      cloneLabelMap(state.Labels),
					CurrentTransitiveSet: cloneBoolMap(state.Transitive),
					NonTransitiveSet:     cloneBoolMap(state.NonTransitive),
					CurrentRoleIDs:       append([]string(nil), state.RoleIDs...),
					RequestedLabels:      cloneStringMap(labels),
					RequestedTransitive:  append([]string(nil), trans...),
					FixedLabels:          cloneStringMap(role.FixedLabels),
					OverrideKeys:         append([]string(nil), role.OverrideKeys...),
					TargetTransitiveKeys: targetTransitive,
					FixedTransitiveKeys:  role.FixedTransitiveKeys,
					Depth:                state.Depth + 1,
					RoleID:               role.RoleID,
				}
				key := requestSearchKey(req)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, req)
				if len(out) >= limit {
					return out
				}
			}
		}
	}
	return out
}

func defaultSearchValues(s Scenario, state *Session) []string {
	set := make(map[string]bool)
	for _, role := range s.Roles {
		for _, v := range role.FixedLabels {
			set[v] = true
		}
	}
	if state != nil {
		for _, v := range state.Values() {
			set[v] = true
		}
	}
	for _, v := range []string{"admin", "root", "user", "guest", "owner", "internal", "public", "private", "prod", "dev", "read", "write", "true", "false"} {
		set[v] = true
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func candidateLabelSets(keys, values []string, limit int) []map[string]string {
	max := limit
	if max <= 0 || max > 4096 {
		max = 4096
	}
	out := []map[string]string{{}}
	// One key at a time.
	for _, k := range keys {
		for _, v := range values {
			out = append(out, map[string]string{k: v})
			if len(out) >= max {
				return out
			}
		}
	}
	// Pairs and then three-key combinations, budget permitting.
	for size := 2; size <= 3; size++ {
		combos := keyCombinations(keys, size)
		for _, combo := range combos {
			for _, vals := range valueProducts(values, size) {
				m := make(map[string]string, size)
				for i, k := range combo {
					m[k] = vals[i]
				}
				out = append(out, m)
				if len(out) >= max {
					return out
				}
			}
		}
	}
	// Full assignment with the first value is useful for fixed-value policies.
	if len(keys) > 0 && len(values) > 0 {
		m := make(map[string]string, len(keys))
		for _, k := range keys {
			m[k] = values[0]
		}
		out = append(out, m)
	}
	return out
}

func candidateTransitiveSets(keys []string, limit int) [][]string {
	max := limit
	if max <= 0 || max > 4096 {
		max = 4096
	}
	out := [][]string{{}}
	for _, k := range keys {
		out = append(out, []string{k})
		if len(out) >= max {
			return out
		}
	}
	for size := 2; size <= 3; size++ {
		for _, combo := range keyCombinations(keys, size) {
			out = append(out, combo)
			if len(out) >= max {
				return out
			}
		}
	}
	if len(keys) > 0 {
		out = append(out, append([]string(nil), keys...))
	}
	return out
}

func keyCombinations(keys []string, size int) [][]string {
	var out [][]string
	var walk func(start int, cur []string)
	walk = func(start int, cur []string) {
		if len(cur) == size {
			out = append(out, append([]string(nil), cur...))
			return
		}
		for i := start; i < len(keys); i++ {
			walk(i+1, append(cur, keys[i]))
		}
	}
	walk(0, nil)
	return out
}

func valueProducts(values []string, n int) [][]string {
	out := [][]string{{}}
	for i := 0; i < n; i++ {
		var next [][]string
		for _, prefix := range out {
			for _, v := range values {
				row := append(append([]string(nil), prefix...), v)
				next = append(next, row)
			}
		}
		out = next
	}
	return out
}

func sessionFrom(v any) (*Session, *Scenario, error) {
	switch s := v.(type) {
	case *Session:
		if s == nil {
			return nil, nil, fmt.Errorf("%w: nil session", ErrInvalidInput)
		}
		return s.Clone(), nil, nil
	case Session:
		return (&s).Clone(), nil, nil
	case *HopResult:
		if s == nil || s.Session == nil {
			return nil, nil, fmt.Errorf("%w: nil result", ErrInvalidInput)
		}
		return s.Session.Clone(), nil, nil
	case HopResult:
		if s.Session == nil {
			return nil, nil, fmt.Errorf("%w: result has no session", ErrInvalidInput)
		}
		return s.Session.Clone(), nil, nil
	case map[string]string:
		return NewSession(s), nil, nil
	case map[string]Label:
		return NewSessionFromLabels(s, nil, 0, nil), nil, nil
	case Scenario:
		return NewSession(s.InitialLabels, s.InitialTransitive...), &s, nil
	case *Scenario:
		if s == nil {
			return nil, nil, fmt.Errorf("%w: nil scenario", ErrInvalidInput)
		}
		return NewSession(s.InitialLabels, s.InitialTransitive...), s, nil
	case nil:
		return NewSession(nil), nil, nil
	default:
		return nil, nil, fmt.Errorf("%w: unsupported start type %T", ErrInvalidInput, v)
	}
}

func asScenario(v any) (Scenario, bool) {
	switch s := v.(type) {
	case Scenario:
		return s, true
	case *Scenario:
		if s != nil {
			return *s, true
		}
	}
	return Scenario{}, false
}

func isScenario(v any) bool {
	switch v.(type) {
	case Scenario, *Scenario:
		return true
	}
	return false
}

func asRequests(v any) ([]HopRequest, error) {
	switch r := v.(type) {
	case []HopRequest:
		return append([]HopRequest(nil), r...), nil
	case []*HopRequest:
		out := make([]HopRequest, 0, len(r))
		for _, p := range r {
			if p != nil {
				out = append(out, *p)
			}
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("%w: no candidates", ErrInvalidInput)
	default:
		return nil, fmt.Errorf("%w: unsupported candidate type %T", ErrInvalidInput, v)
	}
}

func bindRequest(state *Session, candidate HopRequest) HopRequest {
	req := candidate
	req.CurrentLabels = nil
	req.CurrentPairs = nil
	req.CurrentLabelSet = cloneLabelMap(state.Labels)
	req.CurrentTransitive = nil
	req.CurrentTransitiveSet = cloneBoolMap(state.Transitive)
	req.NonTransitiveKeys = nil
	req.TaintedKeys = nil
	req.NonTransitiveSet = cloneBoolMap(state.NonTransitive)
	req.TaintedSet = nil
	req.CurrentRoleIDs = append([]string(nil), state.RoleIDs...)
	req.Depth = state.Depth + 1
	return req
}

func escalationStateKey(s *Session, modelRejected bool) string {
	var b strings.Builder
	if modelRejected {
		b.WriteString("reject\x00")
	} else {
		b.WriteString("clean\x00")
	}
	b.WriteString(fmt.Sprint(s.Depth))
	b.WriteByte(0)
	for _, role := range s.RoleIDs {
		b.WriteString(role)
		b.WriteByte(0)
	}
	for _, key := range s.NonTransitiveKeys() {
		b.WriteByte('N')
		b.WriteString(key)
		b.WriteByte(0)
	}
	for _, key := range sortedLabelKeys(s.Labels) {
		l := s.Labels[key]
		writeProofField(&b, key)
		writeProofField(&b, l.Value)
		if l.Fixed {
			b.WriteByte('F')
		}
		if s.Transitive[key] {
			b.WriteByte('T')
		}
		b.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func requestSearchKey(req HopRequest) string {
	var b strings.Builder
	writeProofField(&b, req.RoleID)
	writeProofField(&b, fmt.Sprint(req.Depth))
	for _, k := range sortedKeys(req.RequestedLabels) {
		writeProofField(&b, k)
		writeProofField(&b, req.RequestedLabels[k])
	}
	for _, k := range append([]string(nil), req.RequestedTransitive...) {
		writeProofField(&b, k)
	}
	for _, k := range sortedKeys(req.FixedLabels) {
		writeProofField(&b, k)
	}
	for _, k := range append([]string(nil), req.OverrideKeys...) {
		writeProofField(&b, k)
	}
	if keys, _, err := keyListFromAny(req.TargetTransitiveKeys); err == nil {
		for _, k := range keys {
			writeProofField(&b, "tt:"+k)
		}
	}
	if keys, _, err := keyListFromAny(req.FixedTransitiveKeys); err == nil {
		for _, k := range keys {
			writeProofField(&b, "ft:"+k)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func appendCopyRequests(dst []HopRequest, reqs ...HopRequest) []HopRequest {
	out := make([]HopRequest, 0, len(dst)+len(reqs))
	out = append(out, dst...)
	out = append(out, reqs...)
	return out
}

func appendCopyStates(dst []*Session, states ...*Session) []*Session {
	out := make([]*Session, 0, len(dst)+len(states))
	out = append(out, dst...)
	out = append(out, states...)
	return out
}
