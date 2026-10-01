"""Exact reachability search over the finite state space.

Breadth-first exploration of all markings reachable under the finite-
capacity firing rule.  Because every place has a finite capacity the state
space is finite, so a fully explored search gives an exact safe/violated
verdict -- this is never an unbounded-net coverability approximation.
If the search is cut short by SearchLimits the verdict stays PENDING.
"""
from __future__ import annotations

import json
from collections import deque
from dataclasses import dataclass, field

from .engine import enabled, fire
from .invariants import invariant_value
from .replay import replay

SAFE = "safe"
VIOLATED = "violated"
PENDING = "pending"


@dataclass
class SearchLimits:
    max_states: int | None = None
    max_depth: int | None = None


@dataclass
class Counterexample:
    """A replayable witness: transition ids from the initial marking."""
    transitions: list
    initial: dict
    final: dict  # keyed by real place ids

    def replay(self, net):
        return replay(net, self.initial, self.transitions)

    def verify(self, net):
        """Replay and check the final marking matches."""
        try:
            return self.replay(net) == self.final
        except Exception:
            return False

    def to_json(self):
        return json.dumps({
            "transitions": self.transitions,
            "initial": self.initial,
            "final": self.final,
        }, sort_keys=True)

    @staticmethod
    def from_json(text):
        data = json.loads(text)
        return Counterexample(list(data["transitions"]),
                              dict(data["initial"]), dict(data["final"]))


@dataclass
class SearchResult:
    verdict: str
    explored: int
    truncated: bool
    markings: set
    terminal_markings: list
    certificates_ok: dict
    terminal_certificates_ok: dict
    counterexample: Counterexample | None
    _net: object = field(default=None, repr=False)
    _parent: dict = field(default_factory=dict, repr=False)
    _node: dict = field(default_factory=dict, repr=False)
    _key_of: object = field(default=None, repr=False)

    def path_to(self, marking_dict):
        """Transition-id path from the initial marking to `marking_dict`
        (real place ids), or None if that marking was not visited."""
        net = self._net
        key = self._key_of(net.encode(marking_dict))
        if key not in self._node:
            return None
        seq = []
        k = key
        while k in self._parent:
            k, tid = self._parent[k]
            seq.append(tid)
        seq.reverse()
        return seq


def search(net, initial, bad=None, limits=None, certificates=(), perms=None):
    """BFS over reachable markings.

    net          PetriNet (finite capacities only)
    initial      dict place_id -> tokens (real place ids)
    bad          optional predicate on a marking dict; first marking
                 satisfying it yields a VIOLATED verdict + counterexample
    limits       optional SearchLimits; hitting a limit => PENDING
    certificates invariant certificates checked on every visited marking
                 (terminal markings are checked and reported separately)
    perms        optional symmetry group (index perms); dedupe by orbit
                 representative while keeping real markings for replay
    """
    if bad is None:
        bad = lambda m: False
    limits = limits or SearchLimits()
    if perms:
        from .symmetry import canonical
        key_of = lambda m: canonical(m, perms)
    else:
        key_of = lambda m: m

    start = net.encode(initial)
    start_key = key_of(start)
    node = {start_key: start}
    parent = {}
    queue = deque([(start, 0)])
    markings = set()
    terminals = []
    certs = list(certificates)
    expected = {c.name: invariant_value(net, c, start) for c in certs}
    certs_ok = {c.name: True for c in certs}
    term_certs_ok = {c.name: True for c in certs}
    counterexample = None
    truncated = False
    explored = 0

    def build_counterexample(key, marking):
        seq = []
        k = key
        while k in parent:
            k, tid = parent[k]
            seq.append(tid)
        seq.reverse()
        return Counterexample(seq, dict(initial), net.decode(marking))

    while queue:
        if limits.max_states is not None and explored >= limits.max_states:
            truncated = True
            break
        m, depth = queue.popleft()
        explored += 1
        markings.add(m)
        key = key_of(m)

        for c in certs:
            if invariant_value(net, c, m) != expected[c.name]:
                certs_ok[c.name] = False
                counterexample = build_counterexample(key, m)
                break
        if counterexample is not None:
            break

        if bad(net.decode(m)):
            counterexample = build_counterexample(key, m)
            break

        succs = enabled(net, m)
        if not succs:
            terminals.append(m)
            for c in certs:
                if invariant_value(net, c, m) != expected[c.name]:
                    term_certs_ok[c.name] = False
        if limits.max_depth is not None and depth >= limits.max_depth:
            if succs:
                truncated = True
            continue
        for tid in succs:
            m2 = fire(net, m, net.transitions[tid])
            k2 = key_of(m2)
            if k2 not in node:
                node[k2] = m2
                parent[k2] = (key, tid)
                queue.append((m2, depth + 1))

    if counterexample is not None:
        verdict = VIOLATED
    elif truncated:
        verdict = PENDING
    else:
        verdict = SAFE

    return SearchResult(
        verdict=verdict,
        explored=explored,
        truncated=truncated,
        markings=markings,
        terminal_markings=terminals,
        certificates_ok=certs_ok,
        terminal_certificates_ok=term_certs_ok,
        counterexample=counterexample,
        _net=net,
        _parent=parent,
        _node=node,
        _key_of=key_of,
    )
