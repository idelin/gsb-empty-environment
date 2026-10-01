"""Bounded Petri net model.

Places always have a finite integer capacity, so the state space is finite
and reachability is *exact* -- never a coverability approximation for an
unbounded net.

Arc kinds are defined separately on each transition:
  - inputs:     consume `weight` tokens (requires >= weight tokens present)
  - outputs:    produce `weight` tokens
  - reads:      require >= weight tokens, consume nothing
  - inhibitors: require < weight tokens, consume nothing

A self-loop (a place in both inputs and outputs of the same transition) is
judged atomically: tokens are consumed and produced together, and the
capacity check applies to the *net* effect of the firing.
"""
from __future__ import annotations

from dataclasses import dataclass


def _norm(arcs: dict[str, int] | None) -> tuple[tuple[str, int], ...]:
    """Normalise an arc mapping to a sorted tuple of (place, weight)."""
    if not arcs:
        return ()
    items = []
    for place, weight in arcs.items():
        if not isinstance(weight, int) or isinstance(weight, bool) or weight <= 0:
            raise ValueError(f"arc weight must be a positive int, got {weight!r}")
        items.append((place, weight))
    return tuple(sorted(items))


@dataclass(frozen=True)
class Transition:
    """An atomic transition: consume inputs, produce outputs, in one step."""

    name: str
    inputs: tuple[tuple[str, int], ...] = ()
    outputs: tuple[tuple[str, int], ...] = ()
    reads: tuple[tuple[str, int], ...] = ()
    inhibitors: tuple[tuple[str, int], ...] = ()

    @classmethod
    def make(
        cls,
        name: str,
        inputs: dict[str, int] | None = None,
        outputs: dict[str, int] | None = None,
        reads: dict[str, int] | None = None,
        inhibitors: dict[str, int] | None = None,
    ) -> "Transition":
        return cls(name, _norm(inputs), _norm(outputs), _norm(reads), _norm(inhibitors))

    def flow_delta(self) -> dict[str, int]:
        """Net token movement per place (read/inhibitor arcs move nothing)."""
        delta: dict[str, int] = {}
        for place, weight in self.inputs:
            delta[place] = delta.get(place, 0) - weight
        for place, weight in self.outputs:
            delta[place] = delta.get(place, 0) + weight
        return delta


class PetriNet:
    """A Petri net whose places all have finite capacity."""

    def __init__(
        self,
        capacities: dict[str, int],
        transitions: list[Transition],
        initial: dict[str, int] | None = None,
    ) -> None:
        if not capacities:
            raise ValueError("the net must declare at least one place")
        for place, cap in capacities.items():
            # Finite capacity is mandatory: this is what keeps the model a
            # finite-state system and rules out unbounded-coverability
            # semantics by construction.
            if not isinstance(cap, int) or isinstance(cap, bool) or cap < 1:
                raise ValueError(
                    f"place {place!r} must have a finite integer capacity >= 1, "
                    f"got {cap!r}"
                )
        self.capacities = dict(capacities)
        self.places: tuple[str, ...] = tuple(sorted(capacities))
        self.pindex = {p: i for i, p in enumerate(self.places)}

        self.transitions: dict[str, Transition] = {}
        for t in transitions:
            self._check_transition(t)
            if t.name in self.transitions:
                raise ValueError(f"duplicate transition name {t.name!r}")
            self.transitions[t.name] = t

        self.initial = self._marking_tuple(initial or {})
        # Revision counter + changelog drive incremental certificate
        # revalidation when transitions are modified in place.
        self.revision = 0
        self.changelog: list[tuple[int, str]] = []

    # ------------------------------------------------------------------ #
    # construction helpers
    # ------------------------------------------------------------------ #
    def _check_transition(self, t: Transition) -> None:
        for arcs in (t.inputs, t.outputs, t.reads, t.inhibitors):
            for place, _ in arcs:
                if place not in self.pindex:
                    raise ValueError(
                        f"transition {t.name!r} references unknown place {place!r}"
                    )

    def _marking_tuple(self, marking: dict[str, int]) -> tuple[int, ...]:
        for place in marking:
            if place not in self.pindex:
                raise ValueError(f"unknown place {place!r} in marking")
        vec = tuple(marking.get(p, 0) for p in self.places)
        self._check_marking(vec)
        return vec

    def _check_marking(self, vec: tuple[int, ...]) -> None:
        for place, tokens in zip(self.places, vec):
            if tokens < 0:
                raise ValueError(f"negative token count on {place!r}")
            if tokens > self.capacities[place]:
                raise ValueError(
                    f"marking exceeds capacity of {place!r} "
                    f"({tokens} > {self.capacities[place]})"
                )

    def marking_dict(self, vec: tuple[int, ...]) -> dict[str, int]:
        """Map an internal marking vector back to real place IDs."""
        return {p: vec[self.pindex[p]] for p in self.places}

    # ------------------------------------------------------------------ #
    # semantics
    # ------------------------------------------------------------------ #
    def is_enabled(self, t: Transition, m: tuple[int, ...]) -> bool:
        # Input arcs: multiplicities must be covered by available tokens.
        for place, weight in t.inputs:
            if m[self.pindex[place]] < weight:
                return False
        # Read arcs: tokens must be present but are not consumed.
        for place, weight in t.reads:
            if m[self.pindex[place]] < weight:
                return False
        # Inhibitor arcs: enabled only below the threshold weight.
        for place, weight in t.inhibitors:
            if m[self.pindex[place]] >= weight:
                return False
        # Capacity: consume and produce are judged together, so a self-loop
        # is checked against its net effect; any post-fire overflow of a
        # bounded place disables the transition.
        for place, delta in t.flow_delta().items():
            if m[self.pindex[place]] + delta > self.capacities[place]:
                return False
        return True

    def fire(self, t: Transition, m: tuple[int, ...]) -> tuple[int, ...]:
        if not self.is_enabled(t, m):
            raise ValueError(f"transition {t.name!r} is not enabled")
        nxt = list(m)
        for place, weight in t.inputs:
            nxt[self.pindex[place]] -= weight
        for place, weight in t.outputs:
            nxt[self.pindex[place]] += weight
        return tuple(nxt)

    def enabled_transitions(self, m: tuple[int, ...]) -> list[Transition]:
        return [t for t in self.transitions.values() if self.is_enabled(t, m)]

    # ------------------------------------------------------------------ #
    # incremental modification
    # ------------------------------------------------------------------ #
    def modify_transition(
        self,
        name: str,
        *,
        inputs: dict[str, int] | None = None,
        outputs: dict[str, int] | None = None,
        reads: dict[str, int] | None = None,
        inhibitors: dict[str, int] | None = None,
    ) -> Transition:
        """Replace one transition in place and bump the net revision.

        Only the arcs passed explicitly are changed. Cached certificates can
        later be revalidated against just the changed transitions.
        """
        if name not in self.transitions:
            raise KeyError(f"unknown transition {name!r}")
        old = self.transitions[name]
        new = Transition(
            name,
            _norm(inputs) if inputs is not None else old.inputs,
            _norm(outputs) if outputs is not None else old.outputs,
            _norm(reads) if reads is not None else old.reads,
            _norm(inhibitors) if inhibitors is not None else old.inhibitors,
        )
        self._check_transition(new)
        self.transitions[name] = new
        self.revision += 1
        self.changelog.append((self.revision, name))
        return new
