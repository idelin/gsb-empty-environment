"""Abstract interpretation over an explicit ownership domain.

Per resource the abstract value is a *set* of concrete statuses and the
join at control-flow merge points is pointwise union - the abstract
domain is explicit, not implied:

    FREE      - not currently held and never acquired
    RELEASED  - released (and not re-acquired)
    OWNED(o)  - currently held by owner ``o``

The lattice is finite, so the worklist fixpoint always terminates.
Guards and program variables are NOT tracked (path-insensitive), which is
the intended source of over-approximation; the concrete replay in
``checker`` filters the resulting false positives.  Ownership is tracked
per owner - no global reference counting - so moves are precise.
"""

from __future__ import annotations

from dataclasses import dataclass

import concrete as C
from model import (
    Acquire,
    Alias,
    Borrow,
    EndBorrow,
    Nop,
    Program,
    Raise,
    Release,
    Set,
    Transfer,
    out_edges,
)

FREE = ("free",)
RELEASED = ("released",)


def OWNED(owner: str) -> tuple:
    return ("owned", owner)


def is_owned(status: tuple) -> bool:
    return status[0] == "owned"


class AState:
    """Abstract state: per-resource status sets + may-borrows + must-aliases."""

    __slots__ = ("status", "borrows", "aliases")

    def __init__(self, status=None, borrows=frozenset(), aliases=None):
        self.status = status or {}  # res -> frozenset of statuses
        self.borrows = borrows  # frozenset[(res, borrower)] (may-set)
        self.aliases = aliases or {}  # alias -> res (must-map)

    def copy(self) -> "AState":
        return AState(dict(self.status), self.borrows, dict(self.aliases))

    def __eq__(self, other):
        return (
            isinstance(other, AState)
            and self.status == other.status
            and self.borrows == other.borrows
            and self.aliases == other.aliases
        )

    def __repr__(self):
        return f"AState(status={self.status}, borrows={self.borrows}, aliases={self.aliases})"


def join(a: AState | None, b: AState) -> AState:
    """Explicit join at merge points: union of statuses and borrows,
    intersection of agreeing aliases."""
    if a is None:
        return b.copy()
    out = a.copy()
    for res, ss in b.status.items():
        out.status[res] = frozenset(out.status.get(res, frozenset()) | ss)
    out.borrows = a.borrows | b.borrows
    out.aliases = {k: v for k, v in a.aliases.items() if b.aliases.get(k) == v}
    return out


@dataclass(frozen=True)
class Candidate:
    """A potential violation raised by the abstract analysis."""

    kind: str
    res: str
    node: str


def initial_state(prog: Program) -> AState:
    return AState({r: frozenset({FREE}) for r in prog.resources})


def _resolve(who: str, res: str, aliases: dict):
    """Mirror of the concrete alias resolution; None = current owner."""
    if who in aliases and aliases[who] == res:
        return None
    return who


def abs_step(prog: Program, node, st: AState):
    """Abstract transfer function; returns ``(out_state, candidates)``."""
    out = st.copy()
    cands: list[Candidate] = []
    op = node.op

    def status_of(res):
        return out.status.get(res, frozenset({FREE}))

    if isinstance(op, (Nop, Raise, Set)):
        pass  # Set is deliberately ignored: guards are not tracked
    elif isinstance(op, Alias):
        out.aliases[op.name] = op.res
    elif isinstance(op, Acquire):
        if any(is_owned(s) for s in status_of(op.res)):
            cands.append(Candidate(C.DOUBLE_ACQUIRE, op.res, node.name))
        out.status[op.res] = frozenset({OWNED(op.owner)})
        out.borrows = frozenset(b for b in out.borrows if b[0] != op.res)
    elif isinstance(op, Release):
        actor = _resolve(op.who, op.res, out.aliases)
        new = set()
        for s in status_of(op.res):
            if s == FREE:
                cands.append(Candidate(C.RELEASE_WITHOUT_ACQUIRE, op.res, node.name))
                new.add(FREE)
            elif s == RELEASED:
                cands.append(Candidate(C.DOUBLE_RELEASE, op.res, node.name))
                new.add(RELEASED)
            elif actor is not None and actor != s[1]:
                # held by someone else: moved away (or never ours)
                cands.append(Candidate(C.RELEASE_AFTER_MOVE, op.res, node.name))
                new.add(s)
            else:
                new.add(RELEASED)
        if any(b[0] == op.res for b in out.borrows):
            cands.append(Candidate(C.RELEASE_WHILE_BORROWED, op.res, node.name))
        out.status[op.res] = frozenset(new)
        out.borrows = frozenset(b for b in out.borrows if b[0] != op.res)
    elif isinstance(op, Transfer):
        actor = _resolve(op.src, op.res, out.aliases)
        new = set()
        for s in status_of(op.res):
            if is_owned(s):
                if actor is None or actor == s[1]:
                    new.add(OWNED(op.dst))  # move
                else:
                    cands.append(Candidate(C.TRANSFER_AFTER_MOVE, op.res, node.name))
                    new.add(s)
            else:
                cands.append(Candidate(C.TRANSFER_OF_UNOWNED, op.res, node.name))
                new.add(s)
        out.status[op.res] = frozenset(new)
    elif isinstance(op, Borrow):
        if any(is_owned(s) and s[1] == op.owner for s in status_of(op.res)):
            out.borrows = out.borrows | {(op.res, op.borrower)}
        else:
            cands.append(Candidate(C.BORROW_OF_UNOWNED, op.res, node.name))
    elif isinstance(op, EndBorrow):
        out.borrows = frozenset(b for b in out.borrows if b != (op.res, op.borrower))
    else:
        raise TypeError(f"unknown op {op!r}")
    return out, cands


@dataclass
class AbsResult:
    states: dict  # node name -> AState (joined)
    candidates: list
    complete: bool  # False if the step bound was hit -> stay pending
    steps: int


def run(prog: Program, max_steps: int = 100_000) -> AbsResult:
    """Worklist fixpoint; terminates because the domain is finite."""
    states = {prog.entry: initial_state(prog)}
    worklist = [prog.entry]
    candidates: list[Candidate] = []
    steps = 0
    complete = True
    while worklist:
        if steps >= max_steps:
            complete = False
            break
        name = worklist.pop()
        node = prog.nodes[name]
        out, cands = abs_step(prog, node, states[name])
        candidates.extend(cands)
        for e in out_edges(node):  # guards ignored on purpose
            merged = join(states.get(e.target), out)
            if states.get(e.target) != merged:
                states[e.target] = merged
                worklist.append(e.target)
        steps += 1
    # Ownership invariant at every exit, normal and exceptional alike.
    for node in prog.exits():
        st = states.get(node.name)
        if st is None:
            continue
        for res, ss in st.status.items():
            if any(is_owned(s) for s in ss):
                candidates.append(Candidate(C.LEAK, res, node.name))
        for res, _borrower in st.borrows:
            candidates.append(Candidate(C.UNRETURNED_BORROW, res, node.name))
    return AbsResult(states, candidates, complete, steps)
