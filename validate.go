package netsec

import (
	"errors"
	"fmt"
)

// validate rejects malformed specs up front, before any search runs.
func (spec Spec) validate() error {
	if len(spec.Endpoints) == 0 {
		return errors.New("spec has no endpoints")
	}
	if len(spec.Channels) == 0 {
		return errors.New("spec has no channels")
	}

	channels := map[string]*Channel{}
	for i := range spec.Channels {
		ch := &spec.Channels[i]
		if ch.Name == "" {
			return errors.New("channel with empty name")
		}
		if _, dup := channels[ch.Name]; dup {
			return fmt.Errorf("duplicate channel name %q", ch.Name)
		}
		if ch.Capacity < 1 {
			return fmt.Errorf("channel %q capacity must be >= 1", ch.Name)
		}
		channels[ch.Name] = ch
	}

	endpoints := map[string]bool{}
	for i := range spec.Endpoints {
		ep := &spec.Endpoints[i]
		if ep.Name == "" {
			return errors.New("endpoint with empty name")
		}
		if endpoints[ep.Name] {
			return fmt.Errorf("duplicate endpoint name %q", ep.Name)
		}
		endpoints[ep.Name] = true
		if ep.Init == "" {
			return fmt.Errorf("endpoint %q has empty initial state", ep.Name)
		}
		if _, ok := channels[ep.Incoming]; !ok {
			return fmt.Errorf("endpoint %q incoming channel %q not defined", ep.Name, ep.Incoming)
		}
		if len(ep.Transitions) == 0 {
			return fmt.Errorf("endpoint %q has no transitions", ep.Name)
		}

		known := map[string]bool{ep.Init: true}
		for _, tr := range ep.Transitions {
			if tr.From == "" || tr.To == "" {
				return fmt.Errorf("endpoint %q has transition with empty state", ep.Name)
			}
			known[tr.From] = true
			known[tr.To] = true
			for _, act := range tr.Do {
				if act.Kind != SendAction {
					return fmt.Errorf("endpoint %q uses unknown action kind", ep.Name)
				}
				if _, ok := channels[act.Channel]; !ok {
					return fmt.Errorf("endpoint %q sends on undefined channel %q", ep.Name, act.Channel)
				}
				if act.Message == "" {
					return fmt.Errorf("endpoint %q sends empty message on %q", ep.Name, act.Channel)
				}
			}
		}
		for _, binding := range ep.Success {
			if !known[binding.State] {
				return fmt.Errorf("endpoint %q success state %q is not used by any transition", ep.Name, binding.State)
			}
			if len(binding.RequiredTags) == 0 && !binding.RequireNoGap {
				return fmt.Errorf("endpoint %q success state %q requires no sequence numbers", ep.Name, binding.State)
			}
			tags := map[string]bool{}
			for _, tag := range binding.RequiredTags {
				if tag == "" {
					return fmt.Errorf("endpoint %q success state %q has empty required tag", ep.Name, binding.State)
				}
				if tags[tag] {
					return fmt.Errorf("endpoint %q success state %q duplicates required tag %q", ep.Name, binding.State, tag)
				}
				tags[tag] = true
			}
		}

		for _, binding := range ep.Success {
			if binding.State == ep.Init {
				return fmt.Errorf("endpoint %q declares success in initial state %q", ep.Name, ep.Init)
			}
		}
	}
	return nil
}
