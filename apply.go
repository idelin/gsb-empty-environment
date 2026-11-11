package netsec

// apply executes one enabled transition on a copy of gs and returns the new
// state together with the trace events the step produces.
func (c *checker) apply(gs globalState, move candidate) (globalState, []Event) {
	next := gs.clone()
	ep := c.endpoint(move.epName)
	rt := next.endpoints[ep.Name]
	var events []Event

	if move.tr.OnRecv != "" {
		ch := next.channels[ep.Incoming]
		msg := ch.queue[0]
		ch.queue = ch.queue[1:]
		rt.received[msg.Seq] = true
		rt.tagAt[msg.Seq] = msg.Tag
		events = append(events, Event{
			Kind:     EvRecv,
			Endpoint: ep.Name,
			Channel:  ep.Incoming,
			Seq:      msg.Seq,
			Tag:      msg.Tag,
		})
	} else {
		events = append(events, Event{
			Kind:     EvInternal,
			Endpoint: ep.Name,
			State:    move.tr.To,
		})
	}

	rt.state = move.tr.To

	for _, act := range move.tr.Do {
		ch := next.channels[act.Channel]
		seq := ch.nextSeq
		ch.nextSeq++
		events = append(events, Event{
			Kind:     EvSend,
			Endpoint: ep.Name,
			Channel:  act.Channel,
			Seq:      seq,
			Tag:      act.Message,
		})

		if len(ch.queue) >= c.capacity(act.Channel) {
			c.stats.DroppedSends++
			switch c.policy {
			case DropNew:
				// The arriving number is consumed but never queued.
				events = append(events, Event{
					Kind: EvDrop, Endpoint: ep.Name, Channel: act.Channel,
					Seq: seq, Tag: act.Message,
				})
			case DropOld:
				dropped := ch.queue[0]
				ch.queue = ch.queue[1:]
				ch.queue = append(ch.queue, queuedMsg{Seq: seq, Tag: act.Message})
				if victim := c.endpointReceiving(act.Channel); victim != nil {
					next.endpoints[victim.Name].evicted[dropped.Seq] = true
				}
				events = append(events, Event{
					Kind: EvDrop, Endpoint: ep.Name, Channel: act.Channel,
					Seq: dropped.Seq, Tag: dropped.Tag,
				})
			}
		} else {
			ch.queue = append(ch.queue, queuedMsg{Seq: seq, Tag: act.Message})
		}
	}

	if binding, isSuccess := bindingFor(ep, move.tr.To); isSuccess {
		rt.terminal = true
		missingSeqs, missingTags := missingRequired(rt, binding)
		ev := Event{
			Kind:        EvSuccess,
			Endpoint:    ep.Name,
			State:       move.tr.To,
			Bound:       maxSeqOf(rt.received),
			Missing:     missingSeqs,
			MissingTags: missingTags,
		}
		events = append(events, ev)
	}

	return next, events
}

func bindingFor(ep *Endpoint, state string) (SuccessBinding, bool) {
	for _, binding := range ep.Success {
		if binding.State == state {
			return binding, true
		}
	}
	return SuccessBinding{}, false
}

func maxSeqOf(received map[int]bool) int {
	max := 0
	for seq := range received {
		if seq > max {
			max = seq
		}
	}
	return max
}

// missingRequired reports the unmet requirements of a success binding:
// required tags that were never delivered, and sequence-number holes when
// RequireNoGap is set. A half authentication is exactly the case where either
// result is non-empty at a success state.
func missingRequired(rt *endpointRuntime, binding SuccessBinding) ([]int, []string) {
	var missingSeqs []int
	var missingTags []string

	for _, tag := range binding.RequiredTags {
		seen := false
		for _, delivered := range rt.tagAt {
			if delivered == tag {
				seen = true
				break
			}
		}
		if !seen {
			missingTags = append(missingTags, tag)
		}
	}

	if binding.RequireNoGap {
		for seq := range rt.evicted {
			missingSeqs = append(missingSeqs, seq)
		}
	}

	return missingSeqs, missingTags
}
