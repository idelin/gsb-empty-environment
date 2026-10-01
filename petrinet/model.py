"""Petri net model: finite-capacity places, four separately defined arc kinds.

Arc kinds (per transition, each a dict place_id -> positive int weight):
  inputs    consuming arcs   (need >= weight tokens; tokens are consumed)
  outputs   producing arcs   (tokens are produced; capacity is enforced)
  read      read arcs        (need >= weight tokens; nothing is consumed)
  inhibitor inhibitor arcs   (enabled iff tokens < weight; nothing is consumed)

All places have a finite integer capacity, so the state space is finite and
reachability computed here is exact -- never an unbounded-net coverability
conclusion.
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass, field

ARC_KINDS = ("inputs", "outputs", "read", "inhibitor")


@dataclass(frozen=True)
class Place:
    id: str
    capacity: int

    def __post_init__(self):
        # Finite capacity is mandatory: the verification object is a
        # finite-state model, not an unbounded (coverability) model.
        if not isinstance(self.capacity, int) or isinstance(self.capacity, bool) or self.capacity < 1:
            raise ValueError(
                f"place {self.id!r}: capacity must be a positive integer "
                "(finite-state model required)"
            )


@dataclass
class Transition:
    id: str
    inputs: dict = field(default_factory=dict)
    outputs: dict = field(default_factory=dict)
    read: dict = field(default_factory=dict)
    inhibitor: dict = field(default_factory=dict)

    def signature(self):
        def norm(d):
            return tuple(sorted(d.items()))
        return (norm(self.inputs), norm(self.outputs),
                norm(self.read), norm(self.inhibitor))


class PetriNet:
    """A finite-capacity Petri net with atomic consume/produce transitions."""

    def __init__(self, places, transitions):
        self.places = {}
        for p in places:
            if p.id in self.places:
                raise ValueError(f"duplicate place id {p.id!r}")
            self.places[p.id] = p
        self.transitions = {}
        for t in transitions:
            self._check_transition(t)
            if t.id in self.transitions:
                raise ValueError(f"duplicate transition id {t.id!r}")
            self.transitions[t.id] = t
        self.place_order = tuple(sorted(self.places))
        self.index = {pid: i for i, pid in enumerate(self.place_order)}
        self.version = 0
        self._dirty = set()

    # -- validation ------------------------------------------------------
    def _check_transition(self, t):
        for kind in ARC_KINDS:
            for pid, w in getattr(t, kind).items():
                if pid not in self.places:
                    raise ValueError(
                        f"transition {t.id!r}: unknown place {pid!r} in {kind}")
                if not isinstance(w, int) or isinstance(w, bool) or w < 1:
                    raise ValueError(
                        f"transition {t.id!r}: {kind} arc weight must be "
                        f"a positive integer, got {w!r}")

    # -- structural editing (tracked for incremental certificate checks) --
    def add_transition(self, t):
        self._check_transition(t)
        if t.id in self.transitions:
            raise ValueError(f"duplicate transition id {t.id!r}")
        self.transitions[t.id] = t
        self._dirty.add(t.id)
        self.version += 1

    def remove_transition(self, tid):
        if tid not in self.transitions:
            raise KeyError(tid)
        del self.transitions[tid]
        self._dirty.add(tid)
        self.version += 1

    def replace_transition(self, t):
        self._check_transition(t)
        if t.id not in self.transitions:
            raise KeyError(t.id)
        self.transitions[t.id] = t
        self._dirty.add(t.id)
        self.version += 1

    def dirty_transitions(self):
        return frozenset(self._dirty)

    def clear_dirty(self):
        self._dirty.clear()

    def fingerprint(self):
        """Structural hash; changes whenever the net structure changes."""
        h = hashlib.sha256()
        for pid in self.place_order:
            h.update(f"P{pid}:{self.places[pid].capacity};".encode())
        for tid in sorted(self.transitions):
            h.update(f"T{tid}:{self.transitions[tid].signature()};".encode())
        return h.hexdigest()

    # -- marking helpers ---------------------------------------------------
    def encode(self, marking_dict):
        """dict place_id -> tokens  ==>  tuple aligned with place_order."""
        m = [0] * len(self.place_order)
        for pid, n in marking_dict.items():
            if pid not in self.index:
                raise ValueError(f"unknown place {pid!r} in marking")
            if not isinstance(n, int) or n < 0 or n > self.places[pid].capacity:
                raise ValueError(
                    f"marking of {pid!r} must be within [0, "
                    f"{self.places[pid].capacity}], got {n!r}")
            m[self.index[pid]] = n
        return tuple(m)

    def decode(self, marking):
        """tuple marking  ==>  dict keyed by real place ids."""
        return {pid: marking[i] for i, pid in enumerate(self.place_order)}
