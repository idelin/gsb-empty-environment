package changeaccept

import (
	"fmt"
	"sort"
)

// Probe is a named liveness check. An empty InitialLabel means "some label".
type Probe struct {
	ID           string
	Pair         TenantPair
	Source       EndpointID
	Destination  EndpointID
	InitialLabel Label
}

// Connection is a concrete label history. Pair carries the immutable source and
// destination tenant identities. Path[i] has current Labels[i].
type Connection struct {
	ID     string
	Pair   TenantPair
	Path   []EndpointID
	Labels []Label
}

// InitialLabel is the label at the first endpoint.
func (c Connection) InitialLabel() Label {
	if len(c.Labels) == 0 {
		return ""
	}
	return c.Labels[0]
}

// FinalLabel is the label at the last endpoint.
func (c Connection) FinalLabel() Label {
	if len(c.Labels) == 0 {
		return ""
	}
	return c.Labels[len(c.Labels)-1]
}

// ActiveConnection is a connection established before a switch. VerifiedThrough
// is the number of leading hops whose concrete decisions were already checked
// in an earlier generation. Those hops are retained for this same label
// history; they are never used to authorize another history.
type ActiveConnection struct {
	Connection
	VerifiedThrough int
	// VerifiedHistory must equal HistoryDigest(Path[:VerifiedThrough+1],
	// Labels[:VerifiedThrough+1]) when VerifiedThrough is non-zero. It prevents
	// a retained prefix from being reattached to another label history.
	VerifiedHistory [32]byte
}

// Path is an endpoint/label witness.
type Path struct {
	Endpoints []EndpointID
	Labels    []Label
}

// ViolationKind classifies a definite acceptance failure.
type ViolationKind string

const (
	ViolationUnauthorized        ViolationKind = "end-to-end-denied"
	ViolationEgressDenied        ViolationKind = "egress-denied"
	ViolationIngressDenied       ViolationKind = "ingress-denied"
	ViolationLabelHistory        ViolationKind = "label-history-invalid"
	ViolationTenantMismatch      ViolationKind = "tenant-mismatch"
	ViolationUnknownEndpoint     ViolationKind = "unknown-endpoint"
	ViolationLiveness            ViolationKind = "liveness-failed"
	ViolationConnectionMalformed ViolationKind = "connection-malformed"
)

// Violation contains enough context to reproduce a definite failure.
type Violation struct {
	Kind           ViolationKind
	Pair           TenantPair
	Path           Path
	Hop            int
	InitialLabel   Label
	FinalLabel     Label
	Detail         string
	ConnectionID   string
	ConnectionKind string
}

type stateKey struct {
	Endpoint EndpointID
	Label    Label
	Initial  Label
	Started  bool
}

type stateInfo struct {
	Key         stateKey
	Prev        *stateKey
	EgressRule  int
	IngressRule int
}

type transitionInfo struct {
	From        stateKey
	To          stateKey
	EgressRule  int
	IngressRule int
}

type pairExploration struct {
	Pair        TenantPair
	States      []stateInfo
	Transitions []transitionInfo
	Violations  []Violation
}

type compiledTopology struct {
	endpoints map[EndpointID]Endpoint
	outgoing  map[EndpointID][]EndpointID
	labels    []Label
	sources   map[TenantID][]EndpointID
	targets   map[TenantID]map[EndpointID]struct{}
}

func compileTopology(topology Topology, labels []Label) compiledTopology {
	labelSeen := make(map[Label]struct{}, len(labels))
	for _, label := range labels {
		labelSeen[label] = struct{}{}
	}

	result := compiledTopology{
		endpoints: topology.endpointMap(),
		outgoing:  topology.outgoingMap(),
		labels:    sortedLabelSet(labelSeen),
		sources:   make(map[TenantID][]EndpointID),
		targets:   make(map[TenantID]map[EndpointID]struct{}),
	}
	for _, endpoint := range topology.Endpoints {
		result.sources[endpoint.Tenant] = append(result.sources[endpoint.Tenant], endpoint.ID)
		if result.targets[endpoint.Tenant] == nil {
			result.targets[endpoint.Tenant] = make(map[EndpointID]struct{})
		}
		result.targets[endpoint.Tenant][endpoint.ID] = struct{}{}
	}
	for tenant := range result.sources {
		sort.Slice(result.sources[tenant], func(i, j int) bool {
			return result.sources[tenant][i] < result.sources[tenant][j]
		})
	}
	return result
}

func authLabels(auth *Authorizer) []Label {
	seen := make(map[Label]struct{})
	if auth == nil {
		return nil
	}
	for _, pair := range auth.Pairs() {
		for _, rule := range auth.PairPolicy(pair) {
			for _, label := range rule.Source.Labels {
				seen[label] = struct{}{}
			}
			for _, label := range rule.Destination.Labels {
				seen[label] = struct{}{}
			}
		}
	}
	return sortedLabelSet(seen)
}

func snapshotLabels(snapshot Snapshot, extras []Label) []Label {
	seen := make(map[Label]struct{})
	if len(snapshot.Labels) > 0 {
		for _, label := range snapshot.Labels {
			seen[label] = struct{}{}
		}
	} else {
		for _, label := range snapshot.Topology.Labels() {
			seen[label] = struct{}{}
		}
		for _, label := range authLabels(snapshot.Auth) {
			seen[label] = struct{}{}
		}
	}
	for _, label := range extras {
		seen[label] = struct{}{}
	}
	return sortedLabelSet(seen)
}

func explorePair(snapshot Snapshot, pair TenantPair, labels []Label, budget *meter) (pairExploration, error) {
	compiled := compileTopology(snapshot.Topology, labels)
	result := pairExploration{Pair: pair}
	if !snapshot.Auth.IsManaged(pair.Source) || !snapshot.Auth.IsManaged(pair.Destination) {
		return result, fmt.Errorf("pair %q -> %q is not fully managed", pair.Source, pair.Destination)
	}

	sources := compiled.sources[pair.Source]
	targets := compiled.targets[pair.Destination]
	seen := make(map[stateKey]struct{})
	queue := make([]stateKey, 0)

	for _, source := range sources {
		for _, label := range compiled.labels {
			key := stateKey{Endpoint: source, Label: label, Initial: label}
			if _, exists := seen[key]; exists {
				continue
			}
			if err := budget.addState(); err != nil {
				return result, err
			}
			seen[key] = struct{}{}
			result.States = append(result.States, stateInfo{
				Key:         key,
				EgressRule:  -1,
				IngressRule: -1,
			})
			queue = append(queue, key)
		}
	}

	for head := 0; head < len(queue); head++ {
		currentKey := queue[head]
		current, ok := compiled.endpoints[currentKey.Endpoint]
		if !ok {
			continue
		}
		egress := current.Egress.Decide(currentKey.Label)
		if err := budget.addTransition(); err != nil {
			return result, err
		}
		if !egress.allowed() {
			continue
		}
		for _, nextID := range compiled.outgoing[currentKey.Endpoint] {
			next, ok := compiled.endpoints[nextID]
			if !ok {
				continue
			}
			for _, nextLabel := range current.Changes.Next(currentKey.Label) {
				ingress := next.Ingress.Decide(nextLabel)
				if err := budget.addTransition(); err != nil {
					return result, err
				}
				if !ingress.allowed() {
					continue
				}
				nextKey := stateKey{
					Endpoint: nextID,
					Label:    nextLabel,
					Initial:  currentKey.Initial,
					Started:  true,
				}
				result.Transitions = append(result.Transitions, transitionInfo{
					From:        currentKey,
					To:          nextKey,
					EgressRule:  egress.Rule,
					IngressRule: ingress.Rule,
				})
				if _, exists := seen[nextKey]; exists {
					continue
				}
				if err := budget.addState(); err != nil {
					return result, err
				}
				seen[nextKey] = struct{}{}
				prev := currentKey
				result.States = append(result.States, stateInfo{
					Key:         nextKey,
					Prev:        &prev,
					EgressRule:  egress.Rule,
					IngressRule: ingress.Rule,
				})
				queue = append(queue, nextKey)
			}
		}
	}

	for _, state := range result.States {
		if !state.Key.Started {
			continue
		}
		if _, isTarget := targets[state.Key.Endpoint]; !isTarget {
			continue
		}
		decision := snapshot.Auth.Decide(pair, state.Key.Initial, state.Key.Label)
		if decision.allowed() {
			continue
		}
		result.Violations = append(result.Violations, Violation{
			Kind:         ViolationUnauthorized,
			Pair:         pair,
			Path:         result.path(state.Key),
			InitialLabel: state.Key.Initial,
			FinalLabel:   state.Key.Label,
			Detail:       "reachable destination state has no matching allow rule",
		})
	}

	sort.Slice(result.States, func(i, j int) bool {
		return lessState(result.States[i].Key, result.States[j].Key)
	})
	sort.Slice(result.Transitions, func(i, j int) bool {
		if lessState(result.Transitions[i].From, result.Transitions[j].From) {
			return true
		}
		if lessState(result.Transitions[j].From, result.Transitions[i].From) {
			return false
		}
		return lessState(result.Transitions[i].To, result.Transitions[j].To)
	})
	return result, nil
}

func lessState(left, right stateKey) bool {
	if left.Endpoint != right.Endpoint {
		return left.Endpoint < right.Endpoint
	}
	if left.Label != right.Label {
		return left.Label < right.Label
	}
	if left.Initial != right.Initial {
		return left.Initial < right.Initial
	}
	return !left.Started && right.Started
}

func (e pairExploration) stateMap() map[stateKey]stateInfo {
	result := make(map[stateKey]stateInfo, len(e.States))
	for _, state := range e.States {
		result[state.Key] = state
	}
	return result
}

func (e pairExploration) path(target stateKey) Path {
	states := e.stateMap()
	reversedEndpoints := make([]EndpointID, 0, 4)
	reversedLabels := make([]Label, 0, 4)
	current := target
	for {
		reversedEndpoints = append(reversedEndpoints, current.Endpoint)
		reversedLabels = append(reversedLabels, current.Label)
		state, ok := states[current]
		if !ok || state.Prev == nil {
			break
		}
		current = *state.Prev
	}
	endpoints := make([]EndpointID, len(reversedEndpoints))
	labels := make([]Label, len(reversedLabels))
	for index := range reversedEndpoints {
		endpoints[len(endpoints)-1-index] = reversedEndpoints[index]
		labels[len(labels)-1-index] = reversedLabels[index]
	}
	return Path{Endpoints: endpoints, Labels: labels}
}

func findProbePath(snapshot Snapshot, probe Probe, labels []Label, budget *meter) (Path, bool, error) {
	compiled := compileTopology(snapshot.Topology, labels)
	if _, ok := compiled.endpoints[probe.Source]; !ok {
		return Path{}, false, fmt.Errorf("probe %q: unknown source %q", probe.ID, probe.Source)
	}
	if _, ok := compiled.endpoints[probe.Destination]; !ok {
		return Path{}, false, fmt.Errorf("probe %q: unknown destination %q", probe.ID, probe.Destination)
	}

	initialLabels := compiled.labels
	if probe.InitialLabel != "" {
		initialLabels = []Label{probe.InitialLabel}
	}
	seen := make(map[stateKey]stateInfo)
	queue := make([]stateKey, 0, len(initialLabels))
	for _, label := range initialLabels {
		key := stateKey{Endpoint: probe.Source, Label: label, Initial: label}
		if _, exists := seen[key]; exists {
			continue
		}
		if err := budget.addState(); err != nil {
			return Path{}, false, err
		}
		seen[key] = stateInfo{Key: key, EgressRule: -1, IngressRule: -1}
		queue = append(queue, key)
	}

	for head := 0; head < len(queue); head++ {
		currentKey := queue[head]
		if currentKey.Started && currentKey.Endpoint == probe.Destination {
			decision := snapshot.Auth.Decide(probe.Pair, currentKey.Initial, currentKey.Label)
			if decision.allowed() {
				exploration := pairExploration{States: make([]stateInfo, 0, len(seen))}
				for _, state := range seen {
					exploration.States = append(exploration.States, state)
				}
				return exploration.path(currentKey), true, nil
			}
		}
		current, ok := compiled.endpoints[currentKey.Endpoint]
		if !ok {
			continue
		}
		egress := current.Egress.Decide(currentKey.Label)
		if err := budget.addTransition(); err != nil {
			return Path{}, false, err
		}
		if !egress.allowed() {
			continue
		}
		for _, nextID := range compiled.outgoing[currentKey.Endpoint] {
			next, ok := compiled.endpoints[nextID]
			if !ok {
				continue
			}
			for _, nextLabel := range current.Changes.Next(currentKey.Label) {
				ingress := next.Ingress.Decide(nextLabel)
				if err := budget.addTransition(); err != nil {
					return Path{}, false, err
				}
				if !ingress.allowed() {
					continue
				}
				nextKey := stateKey{
					Endpoint: nextID,
					Label:    nextLabel,
					Initial:  currentKey.Initial,
					Started:  true,
				}
				if _, exists := seen[nextKey]; exists {
					continue
				}
				if err := budget.addState(); err != nil {
					return Path{}, false, err
				}
				prev := currentKey
				seen[nextKey] = stateInfo{
					Key:         nextKey,
					Prev:        &prev,
					EgressRule:  egress.Rule,
					IngressRule: ingress.Rule,
				}
				queue = append(queue, nextKey)
			}
		}
	}
	return Path{}, false, nil
}

func verifyConnection(connection Connection, snapshot Snapshot, skipHops int) []Violation {
	var violations []Violation
	if len(connection.Path) == 0 || len(connection.Path) != len(connection.Labels) {
		return []Violation{{
			Kind:         ViolationConnectionMalformed,
			Pair:         connection.Pair,
			ConnectionID: connection.ID,
			Detail:       "path and labels must have the same non-zero length",
		}}
	}
	endpoints := snapshot.Topology.endpointMap()
	source, ok := endpoints[connection.Path[0]]
	if !ok {
		return []Violation{{
			Kind:         ViolationUnknownEndpoint,
			Pair:         connection.Pair,
			ConnectionID: connection.ID,
			Detail:       fmt.Sprintf("unknown source endpoint %q", connection.Path[0]),
		}}
	}
	destination, ok := endpoints[connection.Path[len(connection.Path)-1]]
	if !ok {
		return []Violation{{
			Kind:         ViolationUnknownEndpoint,
			Pair:         connection.Pair,
			ConnectionID: connection.ID,
			Detail:       fmt.Sprintf("unknown destination endpoint %q", connection.Path[len(connection.Path)-1]),
		}}
	}
	if source.Tenant != connection.Pair.Source || destination.Tenant != connection.Pair.Destination {
		violations = append(violations, Violation{
			Kind:         ViolationTenantMismatch,
			Pair:         connection.Pair,
			Path:         Path{Endpoints: append([]EndpointID(nil), connection.Path...), Labels: append([]Label(nil), connection.Labels...)},
			ConnectionID: connection.ID,
			Detail:       "connection endpoint tenants do not match the immutable tenant pair",
		})
	}
	if skipHops < 0 {
		skipHops = 0
	}
	if skipHops > len(connection.Path)-1 {
		skipHops = len(connection.Path) - 1
	}
	for hop := 0; hop < len(connection.Path)-1; hop++ {
		current, ok := endpoints[connection.Path[hop]]
		if !ok {
			violations = append(violations, Violation{
				Kind:         ViolationUnknownEndpoint,
				Pair:         connection.Pair,
				Path:         Path{Endpoints: append([]EndpointID(nil), connection.Path...), Labels: append([]Label(nil), connection.Labels...)},
				Hop:          hop,
				ConnectionID: connection.ID,
				Detail:       fmt.Sprintf("unknown endpoint %q", connection.Path[hop]),
			})
			continue
		}
		next, ok := endpoints[connection.Path[hop+1]]
		if !ok {
			violations = append(violations, Violation{
				Kind:         ViolationUnknownEndpoint,
				Pair:         connection.Pair,
				Path:         Path{Endpoints: append([]EndpointID(nil), connection.Path...), Labels: append([]Label(nil), connection.Labels...)},
				Hop:          hop + 1,
				ConnectionID: connection.ID,
				Detail:       fmt.Sprintf("unknown endpoint %q", connection.Path[hop+1]),
			})
			continue
		}
		nextLabel := connection.Labels[hop+1]
		validTransition := false
		for _, candidate := range current.Changes.Next(connection.Labels[hop]) {
			if candidate == nextLabel {
				validTransition = true
				break
			}
		}
		if !validTransition {
			violations = append(violations, Violation{
				Kind:         ViolationLabelHistory,
				Pair:         connection.Pair,
				Path:         Path{Endpoints: append([]EndpointID(nil), connection.Path...), Labels: append([]Label(nil), connection.Labels...)},
				Hop:          hop,
				ConnectionID: connection.ID,
				Detail:       fmt.Sprintf("label %q is not allowed to become %q", connection.Labels[hop], nextLabel),
			})
		}
		if hop < skipHops {
			continue
		}
		egress := current.Egress.Decide(connection.Labels[hop])
		if !egress.allowed() {
			violations = append(violations, Violation{
				Kind:         ViolationEgressDenied,
				Pair:         connection.Pair,
				Path:         Path{Endpoints: append([]EndpointID(nil), connection.Path...), Labels: append([]Label(nil), connection.Labels...)},
				Hop:          hop,
				InitialLabel: connection.InitialLabel(),
				FinalLabel:   connection.FinalLabel(),
				ConnectionID: connection.ID,
				Detail:       fmt.Sprintf("egress at %q denied label %q", current.ID, connection.Labels[hop]),
			})
		}
		ingress := next.Ingress.Decide(nextLabel)
		if !ingress.allowed() {
			violations = append(violations, Violation{
				Kind:         ViolationIngressDenied,
				Pair:         connection.Pair,
				Path:         Path{Endpoints: append([]EndpointID(nil), connection.Path...), Labels: append([]Label(nil), connection.Labels...)},
				Hop:          hop + 1,
				InitialLabel: connection.InitialLabel(),
				FinalLabel:   connection.FinalLabel(),
				ConnectionID: connection.ID,
				Detail:       fmt.Sprintf("ingress at %q denied label %q", next.ID, nextLabel),
			})
		}
	}
	if decision := snapshot.Auth.Decide(connection.Pair, connection.InitialLabel(), connection.FinalLabel()); !decision.allowed() {
		violations = append(violations, Violation{
			Kind:         ViolationUnauthorized,
			Pair:         connection.Pair,
			Path:         Path{Endpoints: append([]EndpointID(nil), connection.Path...), Labels: append([]Label(nil), connection.Labels...)},
			InitialLabel: connection.InitialLabel(),
			FinalLabel:   connection.FinalLabel(),
			ConnectionID: connection.ID,
			Detail:       "connection label history is not in the end-to-end relation",
		})
	}
	return dedupeViolations(violations)
}

func dedupeViolations(violations []Violation) []Violation {
	if len(violations) < 2 {
		return violations
	}
	seen := make(map[string]struct{}, len(violations))
	result := make([]Violation, 0, len(violations))
	for _, violation := range violations {
		key := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s", violation.Kind, violation.Pair.Source, violation.Pair.Destination, violation.Hop, violation.InitialLabel, violation.FinalLabel, violation.Detail)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, violation)
	}
	return result
}
