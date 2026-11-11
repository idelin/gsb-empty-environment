package dnswindow

import (
	"sort"
	"time"
)

// groupKey identifies one feature group inside a window.
type groupKey struct {
	client           string
	registeredDomain string
}

// window is one half-open interval [start, end) with the live events it
// contains and their per-group incremental aggregates.
type window struct {
	start  time.Time
	end    time.Time
	events map[string]Event
	groups map[groupKey]*groupState
}

func newWindow(start time.Time, size time.Duration) *window {
	return &window{
		start:  start,
		end:    start.Add(size),
		events: make(map[string]Event),
		groups: make(map[groupKey]*groupState),
	}
}

// add stores an event and folds its contribution into its group.
func (w *window) add(event Event) {
	w.events[event.ID] = event
	w.stateFor(groupKey{event.Client, event.RegisteredDomain}).add(event)
}

// remove deletes an event and subtracts its contribution. Groups that become
// empty are pruned so withdrawn corrections leave no residue.
func (w *window) remove(id string) {
	event, ok := w.events[id]
	if !ok {
		return
	}
	key := groupKey{event.Client, event.RegisteredDomain}
	state := w.groups[key]
	state.remove(event)
	if state.totalEvents == 0 {
		delete(w.groups, key)
	}
	delete(w.events, id)
}

func (w *window) stateFor(key groupKey) *groupState {
	state, ok := w.groups[key]
	if !ok {
		state = &groupState{
			labelHist:   make(map[int]int64),
			labelCounts: make(map[string]int64),
			client:      key.client,
			registered:  key.registeredDomain,
		}
		w.groups[key] = state
	}
	return state
}

// snapshot freezes the window into an immutable WindowResult. Map contents are
// copied so later reuse of the Manager cannot mutate the returned value.
func (w *window) snapshot() WindowResult {
	keys := make([]groupKey, 0, len(w.groups))
	for key := range w.groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].client != keys[j].client {
			return keys[i].client < keys[j].client
		}
		return keys[i].registeredDomain < keys[j].registeredDomain
	})

	groups := make([]GroupFeatures, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, w.groups[key].snapshot())
	}
	return WindowResult{Start: w.start, End: w.end, Groups: groups}
}

// groupState is an incrementally maintained, exactly retractable aggregate for
// one (client, registered domain) group. Every counter added with add can be
// reversed with remove, so corrections never double-count.
type groupState struct {
	client     string
	registered string

	totalEvents    int64
	nxdomainEvents int64
	totalNameBytes int64

	labelHist   map[int]int64
	labelCounts map[string]int64

	byteFreq [256]int64
}

// add folds one event's queried name and rcode into the aggregate.
func (g *groupState) add(event Event) {
	g.totalEvents++
	if event.NXDOMAIN {
		g.nxdomainEvents++
	}

	name := trimTrailingDot(event.QueriedName)
	g.totalNameBytes += int64(len(name))
	for i := 0; i < len(name); i++ {
		g.byteFreq[name[i]]++
	}

	for _, label := range labelsOf(event.QueriedName) {
		g.labelHist[len(label)]++
		g.labelCounts[label]++
	}
}

// remove reverses add for the exact same event.
func (g *groupState) remove(event Event) {
	g.totalEvents--
	if event.NXDOMAIN {
		g.nxdomainEvents--
	}

	name := trimTrailingDot(event.QueriedName)
	g.totalNameBytes -= int64(len(name))
	for i := 0; i < len(name); i++ {
		g.byteFreq[name[i]]--
	}

	for _, label := range labelsOf(event.QueriedName) {
		g.labelHist[len(label)]--
		if g.labelHist[len(label)] == 0 {
			delete(g.labelHist, len(label))
		}
		g.labelCounts[label]--
		if g.labelCounts[label] == 0 {
			delete(g.labelCounts, label)
		}
	}
}

// snapshot produces the immutable, fully derived feature row for the group.
func (g *groupState) snapshot() GroupFeatures {
	hist := make(map[int]int64, len(g.labelHist))
	for length, count := range g.labelHist {
		hist[length] = count
	}

	features := GroupFeatures{
		Client:           g.client,
		RegisteredDomain: g.registered,
		TotalEvents:      g.totalEvents,
		NXDOMAINEvents:   g.nxdomainEvents,
		LabelLengthHist:  hist,
		DistinctLabels:   int64(len(g.labelCounts)),
		TotalNameBytes:   g.totalNameBytes,
		ByteFrequency:    g.byteFreq,
		ByteEntropy:      shannonEntropy(g.byteFreq, g.totalNameBytes),
	}
	if g.totalEvents > 0 {
		features.NXDOMAINRatio = float64(g.nxdomainEvents) / float64(g.totalEvents)
	}
	return features
}

// trimTrailingDot removes at most one trailing dot, matching labelsOf.
func trimTrailingDot(name string) string {
	if len(name) > 0 && name[len(name)-1] == '.' {
		return name[:len(name)-1]
	}
	return name
}
