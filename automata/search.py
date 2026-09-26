"""Budgeted, pausable BFS for the shortest accepted word of a lazy automaton.

The search explores the on-demand state space breadth-first, so the first
accepting path found is a shortest one.  When the state budget is
exhausted the result is UNKNOWN (undecided) -- never a wrong answer -- and
the search can later be resumed with a raised budget, exactly where it
stopped.
"""
from __future__ import annotations

import enum
from collections import deque
from dataclasses import dataclass

from .intervals import Interval


class Status(enum.Enum):
    RUNNING = "running"
    EMPTY = "empty"          # language is empty (e.g. inclusion holds)
    NONEMPTY = "nonempty"    # witness found (e.g. counterexample)
    UNKNOWN = "unknown"      # state budget exhausted -> undecided


@dataclass(frozen=True)
class EdgeEvidence:
    """One original NFA edge that fired in a search step."""

    automaton: str
    src: int
    interval: Interval
    dst: int


@dataclass(frozen=True)
class StepEvidence:
    """Evidence for one step of the witness: cell, symbol, original edges."""

    cell: Interval
    symbol: int
    edges: tuple[EdgeEvidence, ...]


@dataclass(frozen=True)
class Witness:
    """A shortest distinguishing word plus per-step original-edge evidence."""

    symbols: tuple[int, ...]
    steps: tuple[StepEvidence, ...]

    def to_dict(self) -> dict:
        return {
            "symbols": list(self.symbols),
            "steps": [
                {
                    "cell": [step.cell.lo, step.cell.hi],
                    "symbol": step.symbol,
                    "edges": [
                        {
                            "automaton": e.automaton,
                            "src": e.src,
                            "interval": [e.interval.lo, e.interval.hi],
                            "dst": e.dst,
                        }
                        for e in step.edges
                    ],
                }
                for step in self.steps
            ],
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Witness":
        steps = tuple(
            StepEvidence(
                cell=Interval(*s["cell"]),
                symbol=s["symbol"],
                edges=tuple(
                    EdgeEvidence(e["automaton"], e["src"], Interval(*e["interval"]), e["dst"])
                    for e in s["edges"]
                ),
            )
            for s in data["steps"]
        )
        return cls(symbols=tuple(data["symbols"]), steps=steps)


class EmptinessSearch:
    """Pausable/resumable BFS over a lazy automaton's state space."""

    def __init__(self, automaton, state_budget: int = 100_000):
        self.automaton = automaton
        self.state_budget = state_budget
        initial = automaton.initial()
        self._visited = {initial}
        self._frontier = deque([(initial, ())])
        self.status = Status.RUNNING
        self.witness: Witness | None = None
        self.expansions = 0
        # The empty word may itself be a (counter)example.
        self._accept_if(initial, ())

    @property
    def states_explored(self) -> int:
        return len(self._visited)

    def _accept_if(self, state, path: tuple) -> bool:
        if self.automaton.is_accept(state):
            self.status = Status.NONEMPTY
            self.witness = Witness(
                symbols=tuple(step.symbol for step in path), steps=path
            )
            return True
        return False

    def step(self, max_expansions: int = 1) -> Status:
        """Expand at most ``max_expansions`` states, then pause."""
        while self.status is Status.RUNNING and max_expansions > 0:
            if not self._frontier:
                self.status = Status.EMPTY
                break
            state, path = self._frontier.popleft()
            new_entries = []
            for cell in self.automaton.cells():
                nxt, evidence = self.automaton.step(state, cell)
                if nxt in self._visited:
                    continue
                npath = path + (
                    StepEvidence(
                        cell=cell,
                        symbol=cell.lo,
                        edges=tuple(EdgeEvidence(*e) for e in evidence),
                    ),
                )
                new_entries.append((nxt, npath))
            if len(self._visited) + len(new_entries) > self.state_budget:
                # Budget exhausted: put the state back untouched so the
                # search can be resumed soundly with a raised budget.
                self._frontier.appendleft((state, path))
                self.status = Status.UNKNOWN
                break
            for nxt, npath in new_entries:
                self._visited.add(nxt)
                if self._accept_if(nxt, npath):
                    return self.status
                self._frontier.append((nxt, npath))
            self.expansions += 1
            max_expansions -= 1
        return self.status

    def run(self, max_expansions: int | None = None) -> Status:
        """Run until decided, budget exhaustion, or the expansion limit."""
        if max_expansions is None:
            while self.status is Status.RUNNING:
                self.step(1024)
        else:
            remaining = max_expansions
            while self.status is Status.RUNNING and remaining > 0:
                batch = min(remaining, 1024)
                self.step(batch)
                remaining -= batch
        return self.status

    def resume(self, state_budget: int | None = None) -> Status:
        """Resume a paused/UNKNOWN search, optionally raising the budget."""
        if state_budget is not None:
            self.state_budget = state_budget
        if self.status is Status.UNKNOWN:
            self.status = Status.RUNNING
        return self.run()
