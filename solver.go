package proofaccept

import (
	"context"
	"fmt"
	"sort"
)

type candidate struct {
	include EventID
	exclude EventID
	target  PageState
}

type choiceGroup struct {
	frame      FrameID
	candidates []candidate
}

type prepared struct {
	samples  []SampleID
	after    map[EventID]struct{}
	before   map[EventID]struct{}
	groups   []choiceGroup
	buildErr bool
}

type solution struct {
	choices  []candidate
	required map[EventID]struct{}
	excluded map[EventID]struct{}
	closure  map[EventID]struct{}
}

func sameMapping(a, b PageState) bool {
	return a.Object == b.Object &&
		a.Generation == b.Generation &&
		a.Digest == b.Digest &&
		a.Executable == b.Executable &&
		a.Signer == b.Signer
}

func (c *compiled) incompleteFrames(sample Sample) map[FrameID]struct{} {
	out := make(map[FrameID]struct{}, len(sample.IncompletePages))
	for _, page := range sample.IncompletePages {
		frame, err := c.frameOf(page, "")
		if err == nil {
			out[frame] = struct{}{}
		}
	}
	return out
}

func (c *compiled) prepare(ids []SampleID, includeIncomplete bool) *prepared {
	p := &prepared{
		samples: append([]SampleID(nil), ids...),
		after:   make(map[EventID]struct{}),
		before:  make(map[EventID]struct{}),
	}
	sortSampleIDs(p.samples)

	groups := make(map[FrameID]PageState)
	for _, id := range p.samples {
		sample, ok := c.samples[id]
		if !ok {
			p.buildErr = true
			return p
		}
		for _, event := range sample.After {
			if _, ok := c.events[event]; !ok {
				p.buildErr = true
				return p
			}
			p.after[event] = struct{}{}
		}
		for _, event := range sample.Before {
			if _, ok := c.events[event]; !ok {
				p.buildErr = true
				return p
			}
			p.before[event] = struct{}{}
		}

		incomplete := c.incompleteFrames(sample)

		seen := make(map[FrameID]PageState)
		for _, state := range sample.States {
			frame, err := c.frameOf(state.Page, state.Frame)
			if err != nil {
				p.buildErr = true
				return p
			}
			state.Frame = frame
			if _, unknown := incomplete[frame]; unknown && !includeIncomplete {
				continue
			}
			if previous, ok := seen[frame]; ok && !sameMapping(previous, state) {
				p.buildErr = true
				return p
			}
			seen[frame] = state
			if previous, ok := groups[frame]; ok {
				if !sameMapping(previous, state) {
					p.buildErr = true
					return p
				}
			} else {
				groups[frame] = state
			}
		}
	}

	frames := make([]FrameID, 0, len(groups))
	for frame := range groups {
		frames = append(frames, frame)
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	for _, frame := range frames {
		candidates := c.candidatesFor(frame, groups[frame])
		if len(candidates) == 0 {
			p.buildErr = true
			return p
		}
		p.groups = append(p.groups, choiceGroup{frame: frame, candidates: candidates})
	}
	return p
}

func (c *compiled) candidatesFor(frame FrameID, target PageState) []candidate {
	target.Frame = frame
	var out []candidate

	line := c.lines[frame]
	var initial *PageState
	if line != nil {
		initial = line.initial
	} else if state, ok := c.initial[frame]; ok {
		copy := state
		initial = &copy
	}
	if initial != nil && sameMapping(*initial, target) {
		candidate := candidate{target: target}
		if line != nil && len(line.events) > 0 {
			candidate.exclude = line.events[0]
		}
		out = append(out, candidate)
	}
	if line == nil {
		return out
	}
	for i, state := range line.after {
		if state == nil || !sameMapping(*state, target) {
			continue
		}
		candidate := candidate{
			include: line.events[i],
			target:  target,
		}
		if i+1 < len(line.events) {
			candidate.exclude = line.events[i+1]
		}
		out = append(out, candidate)
	}
	return out
}

func (c *compiled) invalidPartial(required, excluded map[EventID]struct{}) bool {
	if intersects(required, excluded) {
		return true
	}
	for id := range required {
		for ancestor := range c.anc[id] {
			if _, blocked := excluded[ancestor]; blocked {
				return true
			}
		}
	}
	return false
}

func (c *compiled) solveFirst(ctx context.Context, ids []SampleID, includeIncomplete bool) *solution {
	p := c.prepare(ids, includeIncomplete)
	if p.buildErr {
		return nil
	}

	required := cloneSet(p.after)
	excluded := cloneSet(p.before)
	choices := make([]candidate, 0, len(p.groups))
	var found *solution

	var walk func(int) bool
	walk = func(index int) bool {
		if err := c.checkContext(ctx); err != nil {
			return true
		}
		if index == len(p.groups) {
			closure := c.closure(required)
			if intersects(closure, excluded) {
				return false
			}
			found = &solution{
				choices:  append([]candidate(nil), choices...),
				required: cloneSet(required),
				excluded: cloneSet(excluded),
				closure:  closure,
			}
			return true
		}
		for _, choice := range p.groups[index].candidates {
			if choice.include != "" {
				required[choice.include] = struct{}{}
			}
			if choice.exclude != "" {
				excluded[choice.exclude] = struct{}{}
			}
			choices = append(choices, choice)
			if !c.invalidPartial(required, excluded) && walk(index+1) {
				return true
			}
			choices = choices[:len(choices)-1]
			if choice.exclude != "" {
				delete(excluded, choice.exclude)
			}
			if choice.include != "" {
				delete(required, choice.include)
			}
		}
		return false
	}
	if walk(0) {
		return found
	}
	return nil
}

type cutAnalysis struct {
	compatible  bool
	safeFound   bool
	unsafeFound bool
	first       *solution
	safeCut     map[EventID]struct{}
	unsafeCut   map[EventID]struct{}
}

func (c *compiled) analyze(ctx context.Context, ids []SampleID) cutAnalysis {
	p := c.prepare(ids, false)
	if p.buildErr {
		return cutAnalysis{}
	}

	required := cloneSet(p.after)
	excluded := cloneSet(p.before)
	var outcome cutAnalysis

	var walk func(int) bool
	walk = func(index int) bool {
		if err := c.checkContext(ctx); err != nil {
			return true
		}
		if index == len(p.groups) {
			closure := c.closure(required)
			if intersects(closure, excluded) {
				return false
			}
			outcome.compatible = true
			if outcome.first == nil {
				outcome.first = &solution{
					choices:  nil,
					required: cloneSet(required),
					excluded: cloneSet(excluded),
					closure:  cloneSet(closure),
				}
			}
			if !outcome.safeFound {
				if safe, ok := c.repairSafe(closure, excluded); ok {
					outcome.safeFound = true
					outcome.safeCut = safe
				}
			}
			if !outcome.unsafeFound {
				if unsafe, ok := c.findUnsafeExtension(closure, excluded); ok {
					outcome.unsafeFound = true
					outcome.unsafeCut = unsafe
				}
			}
			return outcome.safeFound && outcome.unsafeFound
		}
		group := p.groups[index]
		for _, choice := range group.candidates {
			if choice.include != "" {
				required[choice.include] = struct{}{}
			}
			if choice.exclude != "" {
				excluded[choice.exclude] = struct{}{}
			}
			stop := false
			if !c.invalidPartial(required, excluded) {
				stop = walk(index + 1)
			}
			if choice.exclude != "" {
				delete(excluded, choice.exclude)
			}
			if choice.include != "" {
				delete(required, choice.include)
			}
			if stop {
				return true
			}
		}
		return false
	}
	walk(0)
	return outcome
}

func cloneSet(src map[EventID]struct{}) map[EventID]struct{} {
	out := make(map[EventID]struct{}, len(src))
	for id := range src {
		out[id] = struct{}{}
	}
	return out
}

type mappedState struct {
	frame FrameID
	state PageState
}

func (c *compiled) statesAt(closure map[EventID]struct{}) []mappedState {
	seen := make(map[FrameID]struct{})
	var out []mappedState
	for frame, line := range c.lines {
		seen[frame] = struct{}{}
		count := 0
		for _, id := range line.events {
			if _, ok := closure[id]; ok {
				count++
			}
		}
		if count == 0 {
			if line.initial != nil {
				out = append(out, mappedState{frame: frame, state: *line.initial})
			}
			continue
		}
		state := line.after[count-1]
		if state != nil {
			out = append(out, mappedState{frame: frame, state: *state})
		}
	}
	for frame, state := range c.initial {
		if _, ok := seen[frame]; !ok {
			out = append(out, mappedState{frame: frame, state: state})
		}
	}
	return out
}

func (c *compiled) revokedAt(closure map[EventID]struct{}) map[ObjectID]struct{} {
	out := make(map[ObjectID]struct{})
	for id := range closure {
		event := c.events[id]
		if event.Op == OpRevoke && event.Object != "" {
			out[event.Object] = struct{}{}
		}
	}
	return out
}

func (c *compiled) objectTrusted(object ObjectID, closure map[EventID]struct{}, revoked map[ObjectID]struct{}) bool {
	if _, dead := revoked[object]; dead {
		return false
	}
	if c.trustedInitial[object] {
		return true
	}
	for _, id := range c.trustedMapEvents[object] {
		if _, ok := closure[id]; ok {
			return true
		}
	}
	return false
}

func (c *compiled) cutSafe(closure map[EventID]struct{}) bool {
	revoked := c.revokedAt(closure)
	for _, mapped := range c.statesAt(closure) {
		if !mapped.state.Executable {
			continue
		}
		if !c.objectTrusted(mapped.state.Object, closure, revoked) {
			return false
		}
	}
	return true
}

func (c *compiled) repairSafe(closure, excluded map[EventID]struct{}) (map[EventID]struct{}, bool) {
	current := cloneSet(closure)
	for {
		revoked := c.revokedAt(current)
		var untrusted []ObjectID
		for _, mapped := range c.statesAt(current) {
			if mapped.state.Executable && !c.objectTrusted(mapped.state.Object, current, revoked) {
				untrusted = append(untrusted, mapped.state.Object)
			}
		}
		if len(untrusted) == 0 {
			return current, true
		}
		progress := false
		for _, object := range untrusted {
			if _, dead := revoked[object]; dead {
				return nil, false
			}
			for _, id := range c.trustedMapEvents[object] {
				if _, already := current[id]; already {
					continue
				}
				required := cloneSet(current)
				required[id] = struct{}{}
				next := c.closure(required)
				if intersects(next, excluded) {
					continue
				}
				current = next
				progress = true
				break
			}
			if progress {
				break
			}
		}
		if !progress {
			return nil, false
		}
	}
}

func (c *compiled) findUnsafeExtension(closure, excluded map[EventID]struct{}) (map[EventID]struct{}, bool) {
	if !c.cutSafe(closure) {
		return cloneSet(closure), true
	}
	for _, id := range c.order {
		if _, already := closure[id]; already {
			continue
		}
		required := cloneSet(closure)
		required[id] = struct{}{}
		next := c.closure(required)
		if intersects(next, excluded) {
			continue
		}
		if !c.cutSafe(next) {
			return next, true
		}
	}
	return nil, false
}

func (c *compiled) minimalConflict(ctx context.Context, all []SampleID) []SampleID {
	ids := append([]SampleID(nil), all...)
	sortSampleIDs(ids)
	n := len(ids)
	if n == 0 {
		return nil
	}
	if c.solveFirst(ctx, ids, false) != nil {
		return nil
	}
	for size := 1; size <= n; size++ {
		combo := make([]int, size)
		for i := range combo {
			combo[i] = i
		}
		for {
			if err := c.checkContext(ctx); err != nil {
				return nil
			}
			subset := make([]SampleID, size)
			for i, index := range combo {
				subset[i] = ids[index]
			}
			if c.solveFirst(ctx, subset, false) == nil {
				return subset
			}
			if !nextCombination(combo, n) {
				break
			}
		}
	}
	return nil
}

func nextCombination(combo []int, n int) bool {
	for i := len(combo) - 1; i >= 0; i-- {
		if combo[i] < n-(len(combo)-i) {
			combo[i]++
			for j := i + 1; j < len(combo); j++ {
				combo[j] = combo[j-1] + 1
			}
			return true
		}
	}
	return false
}

func (c *compiled) sampleIDs() []SampleID {
	out := make([]SampleID, 0, len(c.samples))
	for id := range c.samples {
		out = append(out, id)
	}
	sortSampleIDs(out)
	return out
}

func (c *compiled) missingPages(ids []SampleID) []PageID {
	frames := make(map[FrameID]struct{})
	for _, id := range ids {
		sample, ok := c.samples[id]
		if !ok {
			continue
		}
		for frame := range c.incompleteFrames(sample) {
			frames[frame] = struct{}{}
		}
	}
	out := make([]PageID, 0, len(frames))
	for frame := range frames {
		out = append(out, PageID(frame))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (c *compiled) resultForAnalysis(analysis cutAnalysis) Result {
	result := Result{CommonPrefix: analysis.compatible}
	switch {
	case analysis.safeFound && analysis.unsafeFound:
		result.Status = StatusPossiblyUnsafe
		result.Safe = false
		result.Frontier = c.frontier(analysis.safeCut)
	case analysis.safeFound:
		result.Status = StatusCompatible
		result.Safe = true
		result.Frontier = c.frontier(analysis.safeCut)
	case analysis.unsafeFound:
		result.Status = StatusUnsafe
		result.Safe = false
		result.Frontier = c.frontier(analysis.unsafeCut)
	default:
		result.Status = StatusInvalidInput
		result.Reason = fmt.Sprintf("no trust classification for compatible cut")
	}
	return result
}
