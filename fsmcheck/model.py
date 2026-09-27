"""Finite-state model: participants with local transitions, synchronized actions."""
from __future__ import annotations

from itertools import product

GlobalState = tuple[str, ...]


class LocalTransition:
    __slots__ = ("action", "source", "target")

    def __init__(self, action: str, source: str, target: str) -> None:
        self.action = action
        self.source = source
        self.target = target

    def __repr__(self) -> str:
        return f"LocalTransition({self.action!r}, {self.source!r}, {self.target!r})"

    def __eq__(self, other: object) -> bool:
        return (
            isinstance(other, LocalTransition)
            and (self.action, self.source, self.target)
            == (other.action, other.source, other.target)
        )

    def __hash__(self) -> int:
        return hash((self.action, self.source, self.target))


class Participant:
    __slots__ = ("name", "transitions")

    def __init__(self, name: str, transitions: list[LocalTransition]) -> None:
        self.name = name
        self.transitions = tuple(transitions)

    def offers(self, local_state: str, action: str) -> list[LocalTransition]:
        return [
            t for t in self.transitions if t.source == local_state and t.action == action
        ]


class ActionSpec:
    """An action fires only when every required participant offers it locally.

    Local availability of an action in one participant is not sufficient:
    the action is globally executable only if all listed participants can
    simultaneously take a local transition labelled with it.
    """

    __slots__ = ("name", "participants")

    def __init__(self, name: str, participants: list[str]) -> None:
        self.name = name
        self.participants = tuple(participants)


class Model:
    def __init__(
        self,
        participants: list[Participant],
        actions: list[ActionSpec],
        initials: list[GlobalState],
        terminals: list[GlobalState] | None = None,
    ) -> None:
        names = [p.name for p in participants]
        if len(set(names)) != len(names):
            raise ValueError("duplicate participant names")
        if not initials:
            raise ValueError("at least one initial state is required")
        self.participants = tuple(participants)
        self.index = {name: i for i, name in enumerate(names)}
        self.actions = tuple(actions)
        for spec in self.actions:
            if not spec.participants:
                raise ValueError(f"action {spec.name!r} requires no participant")
            for pname in spec.participants:
                if pname not in self.index:
                    raise ValueError(f"unknown participant {pname!r} in action {spec.name!r}")
        width = len(names)
        for state in list(initials) + list(terminals or []):
            if len(state) != width:
                raise ValueError(f"state {state!r} does not match participant count")
        self.initials = tuple(initials)
        self.terminals = frozenset(terminals or [])

    def local_state(self, state: GlobalState, participant: str) -> str:
        return state[self.index[participant]]

    def offers_at(self, state: GlobalState, participant: str, action: str) -> list[LocalTransition]:
        p = self.participants[self.index[participant]]
        return p.offers(self.local_state(state, participant), action)

    def successors(self, state: GlobalState):
        """Yield (action, moves, next_state) for every globally executable transition.

        moves is a tuple of (participant, source, target) for the required
        participants, in ActionSpec order.
        """
        for spec in self.actions:
            per_party = []
            blocked = False
            for pname in spec.participants:
                offers = self.offers_at(state, pname, spec.name)
                if not offers:
                    blocked = True
                    break
                per_party.append((pname, offers))
            if blocked:
                continue
            for combo in product(*(offers for _, offers in per_party)):
                next_state = list(state)
                moves = []
                for (pname, _), trans in zip(per_party, combo):
                    next_state[self.index[pname]] = trans.target
                    moves.append((pname, trans.source, trans.target))
                yield spec.name, tuple(moves), tuple(next_state)
