"""Transition enabling and firing semantics.

Enabling of transition t at marking m (all conditions together):
  * input arcs:      m[p] >= weight          (enough tokens to consume)
  * read arcs:       m[p] >= weight          (tested, not consumed)
  * inhibitor arcs:  m[p] <  weight          (tested, not consumed)
  * capacity:        for every output place p, the *final* token count
                     m[p] - inputs[p] + outputs[p] must not exceed the
                     place capacity.  A self-loop (p in both inputs and
                     outputs) is judged on this combined net effect, so
                     consume and produce are evaluated together, atomically.

Firing is atomic: all inputs are consumed and all outputs produced in one
indivisible step.  Read and inhibitor arcs never move tokens.
"""
from __future__ import annotations


def is_enabled(net, marking, t):
    idx = net.index
    for pid, w in t.inputs.items():
        if marking[idx[pid]] < w:
            return False
    for pid, w in t.read.items():
        if marking[idx[pid]] < w:
            return False
    for pid, w in t.inhibitor.items():
        if marking[idx[pid]] >= w:
            return False
    for pid, w in t.outputs.items():
        after = marking[idx[pid]] - t.inputs.get(pid, 0) + w
        if after > net.places[pid].capacity:
            return False
    return True


def fire(net, marking, t):
    """Atomic consume+produce.  Caller must ensure t is enabled."""
    new = list(marking)
    for pid, w in t.inputs.items():
        new[net.index[pid]] -= w
    for pid, w in t.outputs.items():
        new[net.index[pid]] += w
    return tuple(new)


def enabled(net, marking):
    """Ids of enabled transitions, in deterministic (sorted) order."""
    return [tid for tid in sorted(net.transitions)
            if is_enabled(net, marking, net.transitions[tid])]
