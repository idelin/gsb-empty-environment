package proofaccept

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type compiled struct {
	in Input

	aliases map[PageID]FrameID

	events   map[EventID]Event
	order    []EventID
	index    map[EventID]int
	children map[EventID][]EventID
	anc      map[EventID]map[EventID]struct{}

	samples map[SampleID]Sample

	initial map[FrameID]PageState

	lines map[FrameID]*pageLine

	trustedInitial map[ObjectID]bool

	trustedMapEvents map[ObjectID][]EventID
}

type pageLine struct {
	frame   FrameID
	initial *PageState
	events  []EventID
	after   []*PageState
}

func compile(in Input) (*compiled, error) {
	c := &compiled{
		in:               in,
		aliases:          make(map[PageID]FrameID, len(in.Aliases)),
		events:           make(map[EventID]Event, len(in.Events)),
		index:            make(map[EventID]int, len(in.Events)),
		children:         make(map[EventID][]EventID),
		anc:              make(map[EventID]map[EventID]struct{}, len(in.Events)),
		samples:          make(map[SampleID]Sample, len(in.Samples)),
		initial:          make(map[FrameID]PageState),
		lines:            make(map[FrameID]*pageLine),
		trustedInitial:   make(map[ObjectID]bool, len(in.TrustedObjects)),
		trustedMapEvents: make(map[ObjectID][]EventID),
	}

	for page, frame := range in.Aliases {
		if page == "" || frame == "" {
			return nil, errors.New("alias entries need non-empty page and frame")
		}
		c.aliases[page] = frame
	}
	for object, trusted := range in.TrustedObjects {
		c.trustedInitial[object] = trusted
	}

	sampleIDs := make(map[SampleID]struct{}, len(in.Samples))
	for _, sample := range in.Samples {
		if sample.ID == "" {
			return nil, errors.New("sample ID must be non-empty")
		}
		if _, ok := sampleIDs[sample.ID]; ok {
			return nil, fmt.Errorf("duplicate sample ID %q", sample.ID)
		}
		sampleIDs[sample.ID] = struct{}{}
		c.samples[sample.ID] = sample
	}

	indegree := make(map[EventID]int, len(in.Events))
	for _, event := range in.Events {
		if event.ID == "" {
			return nil, errors.New("event ID must be non-empty")
		}
		if _, ok := c.events[event.ID]; ok {
			return nil, fmt.Errorf("duplicate event ID %q", event.ID)
		}
		if err := validateEventShape(event); err != nil {
			return nil, fmt.Errorf("event %q: %w", event.ID, err)
		}
		c.events[event.ID] = event
		indegree[event.ID] = 0
	}
	for _, event := range in.Events {
		seen := make(map[EventID]struct{}, len(event.Deps))
		for _, dep := range event.Deps {
			if dep == event.ID {
				return nil, fmt.Errorf("event %q depends on itself", event.ID)
			}
			if _, ok := c.events[dep]; !ok {
				return nil, fmt.Errorf("event %q has unknown dependency %q", event.ID, dep)
			}
			if _, ok := seen[dep]; ok {
				return nil, fmt.Errorf("event %q repeats dependency %q", event.ID, dep)
			}
			seen[dep] = struct{}{}
			indegree[event.ID]++
			c.children[dep] = append(c.children[dep], event.ID)
		}
	}

	// Deterministic Kahn order makes frontiers stable across runs.
	ready := make([]EventID, 0, len(in.Events))
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sortEventIDs(ready)
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		c.index[id] = len(c.order)
		c.order = append(c.order, id)
		next := append([]EventID(nil), c.children[id]...)
		sortEventIDs(next)
		for _, child := range next {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sortEventIDs(ready)
			}
		}
	}
	if len(c.order) != len(in.Events) {
		return nil, errors.New("event dependency graph contains a cycle")
	}

	for _, id := range c.order {
		event := c.events[id]
		anc := map[EventID]struct{}{id: {}}
		for _, dep := range event.Deps {
			for ancestor := range c.anc[dep] {
				anc[ancestor] = struct{}{}
			}
		}
		c.anc[id] = anc
		if mappingEvent(event) && event.Trusted && event.Object != "" {
			c.trustedMapEvents[event.Object] = append(c.trustedMapEvents[event.Object], id)
		}
	}
	for id := range c.children {
		sortEventIDs(c.children[id])
	}

	for _, state := range in.Initial {
		frame, err := c.frameOf(state.Page, state.Frame)
		if err != nil {
			return nil, fmt.Errorf("initial state: %w", err)
		}
		if _, ok := c.initial[frame]; ok {
			return nil, fmt.Errorf("duplicate initial state for frame %q", frame)
		}
		state.Frame = frame
		c.initial[frame] = state
	}
	if err := c.buildPageLines(); err != nil {
		return nil, err
	}
	return c, nil
}

func validateEventShape(event Event) error {
	switch event.Op {
	case OpMap, OpModify, OpRemap:
		if event.Page == "" && event.Frame == "" {
			return errors.New("mapping event needs a page or frame")
		}
	case OpRevoke:
		if event.Page == "" && event.Frame == "" && event.Object == "" {
			return errors.New("revoke event needs a page, frame, or object")
		}
	default:
		return fmt.Errorf("unknown operation %q", event.Op)
	}
	return nil
}

func (c *compiled) frameOf(page PageID, frame FrameID) (FrameID, error) {
	if frame != "" {
		return frame, nil
	}
	if page == "" {
		return "", errors.New("empty page and frame")
	}
	if alias := c.aliases[page]; alias != "" {
		return alias, nil
	}
	return FrameID(page), nil
}

func mappingEvent(event Event) bool {
	switch event.Op {
	case OpMap, OpModify, OpRemap:
		return true
	default:
		return false
	}
}

func pageEvent(event Event) bool {
	return mappingEvent(event) || (event.Op == OpRevoke && (event.Page != "" || event.Frame != ""))
}

func (c *compiled) stateOfEvent(event Event) PageState {
	frame, _ := c.frameOf(event.Page, event.Frame)
	return PageState{
		Page:       event.Page,
		Frame:      frame,
		Object:     event.Object,
		Generation: event.Generation,
		Digest:     event.Digest,
		Executable: event.Executable,
		Signer:     event.Signer,
	}
}

func (c *compiled) buildPageLines() error {
	for _, id := range c.order {
		event := c.events[id]
		if !pageEvent(event) {
			continue
		}
		frame, err := c.frameOf(event.Page, event.Frame)
		if err != nil {
			return fmt.Errorf("event %q: %w", id, err)
		}
		line := c.lines[frame]
		if line == nil {
			line = &pageLine{frame: frame}
			if initial, ok := c.initial[frame]; ok {
				copy := initial
				line.initial = &copy
			}
			c.lines[frame] = line
		}
		line.events = append(line.events, id)
	}

	// A page state at a cut must be unambiguous.  Concurrent page operations
	// would make the state depend on an unstated linear extension.
	for frame, line := range c.lines {
		for i := 0; i < len(line.events); i++ {
			for j := i + 1; j < len(line.events); j++ {
				a, b := line.events[i], line.events[j]
				if !c.before(a, b) && !c.before(b, a) {
					return fmt.Errorf("concurrent operations on page %q: %q and %q need a causal order", frame, a, b)
				}
			}
		}
	}

	for _, line := range c.lines {
		var current *PageState
		if line.initial != nil {
			copy := *line.initial
			current = &copy
		}
		for _, id := range line.events {
			event := c.events[id]
			if mappingEvent(event) {
				state := c.stateOfEvent(event)
				current = &state
			} else {
				if event.Generation == 0 || (current != nil && current.Generation == event.Generation) {
					current = nil
				}
			}
			var copy *PageState
			if current != nil {
				value := *current
				copy = &value
			}
			line.after = append(line.after, copy)
		}
	}
	return nil
}

func (c *compiled) before(a, b EventID) bool {
	if a == b {
		return false
	}
	_, ok := c.anc[b][a]
	return ok
}

func sortEventIDs(ids []EventID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

func sortSampleIDs(ids []SampleID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

func (c *compiled) closure(required map[EventID]struct{}) map[EventID]struct{} {
	out := make(map[EventID]struct{}, len(required))
	for id := range required {
		for ancestor := range c.anc[id] {
			out[ancestor] = struct{}{}
		}
	}
	return out
}

func intersects(a, b map[EventID]struct{}) bool {
	small, large := a, b
	if len(small) > len(large) {
		small, large = large, small
	}
	for id := range small {
		if _, ok := large[id]; ok {
			return true
		}
	}
	return false
}

func (c *compiled) frontier(closure map[EventID]struct{}) []EventID {
	maximal := make(map[EventID]struct{}, len(closure))
	for id := range closure {
		maximal[id] = struct{}{}
	}
	for id := range closure {
		for _, child := range c.children[id] {
			if _, ok := closure[child]; ok {
				delete(maximal, id)
				break
			}
		}
	}
	out := make([]EventID, 0, len(maximal))
	for id := range maximal {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := c.index[out[i]], c.index[out[j]]
		if oi != oj {
			return oi < oj
		}
		return out[i] < out[j]
	})
	return out
}

func (c *compiled) canonicalFrontier(ids []EventID) ([]EventID, error) {
	required := make(map[EventID]struct{}, len(ids))
	seen := make(map[EventID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := c.events[id]; !ok {
			return nil, fmt.Errorf("unknown frontier event %q", id)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("duplicate frontier event %q", id)
		}
		seen[id] = struct{}{}
		required[id] = struct{}{}
	}
	closure := c.closure(required)
	canonical := c.frontier(closure)
	if len(canonical) != len(required) {
		return nil, errors.New("frontier is not compact (it contains non-maximal events)")
	}
	for _, id := range canonical {
		if _, ok := required[id]; !ok {
			return nil, errors.New("frontier does not match its predecessor closure")
		}
	}
	return canonical, nil
}

func (c *compiled) checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
