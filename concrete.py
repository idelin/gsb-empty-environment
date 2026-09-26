"""Concrete semantics, bounded exhaustive exploration and witness replay.

This module is the ground truth.  Ownership is tracked *explicitly* per
resource (who holds it right now) - never as a global reference count - so
ownership transfer (move) is observable: after ``transfer`` the previous
owner can no longer release, and a second release is a distinct error.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass

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

# Violation kinds.
DOUBLE_ACQUIRE = "double_acquire"
DOUBLE_RELEASE = "double_release"
RELEASE_WITHOUT_ACQUIRE = "release_without_acquire"
RELEASE_AFTER_MOVE = "release_after_move"
RELEASE_WHILE_BORROWED = "release_while_borrowed"
TRANSFER_OF_UNOWNED = "transfer_of_unowned"
TRANSFER_AFTER_MOVE = "transfer_after_move"
BORROW_OF_UNOWNED = "borrow_of_unowned"
LEAK = "leak"
UNRETURNED_BORROW = "unreturned_borrow"

_CURRENT_OWNER = object()  # sentinel: actor resolved through an alias


@dataclass(frozen=True)
class CState:
    """Concrete state (hashable, so the explored state space is finite)."""

    held: tuple  # sorted (res, owner) pairs; absent = not currently held
    released: frozenset  # resources released at least once, not re-acquired
    borrows: frozenset  # (res, borrower) active borrows
    aliases: tuple  # sorted (alias, res) pairs
    env: tuple  # sorted (var, bool) pairs

    @staticmethod
    def make(held, released, borrows, aliases, env) -> "CState":
        return CState(
            tuple(sorted(held.items())),
            frozenset(released),
            frozenset(borrows),
            tuple(sorted(aliases.items())),
            tuple(sorted(env.items())),
        )

    def held_map(self) -> dict:
        return dict(self.held)

    def alias_map(self) -> dict:
        return dict(self.aliases)

    def env_map(self) -> dict:
        return dict(self.env)


def initial_state(prog: Program) -> CState:
    return CState.make({}, set(), set(), {}, {v: False for v in prog.vars})


def _resolve_actor(who: str, res: str, aliases: dict):
    """An alias naming ``res`` acts as its current owner; never owns itself."""
    if who in aliases and aliases[who] == res:
        return _CURRENT_OWNER
    return who


def apply_op(prog: Program, node, cs: CState):
    """Execute ``node.op``.  Returns ``(new_state, error)`` where ``error``
    is ``(kind, resource)`` or ``None``.  An error ends the concrete path."""
    op = node.op
    held = cs.held_map()
    released = set(cs.released)
    borrows = set(cs.borrows)
    aliases = cs.alias_map()
    env = cs.env_map()

    if isinstance(op, (Nop, Raise)):
        pass
    elif isinstance(op, Set):
        env[op.var] = op.value
    elif isinstance(op, Alias):
        aliases[op.name] = op.res
    elif isinstance(op, Acquire):
        if op.res in held:
            return cs, (DOUBLE_ACQUIRE, op.res)
        held[op.res] = op.owner
        released.discard(op.res)
        borrows = {b for b in borrows if b[0] != op.res}
    elif isinstance(op, Release):
        actor = _resolve_actor(op.who, op.res, aliases)
        if op.res in released:
            return cs, (DOUBLE_RELEASE, op.res)
        if op.res not in held:
            return cs, (RELEASE_WITHOUT_ACQUIRE, op.res)
        if actor is not _CURRENT_OWNER and held[op.res] != actor:
            return cs, (RELEASE_AFTER_MOVE, op.res)
        if any(b[0] == op.res for b in borrows):
            return cs, (RELEASE_WHILE_BORROWED, op.res)
        del held[op.res]
        released.add(op.res)
        borrows = {b for b in borrows if b[0] != op.res}
    elif isinstance(op, Transfer):
        actor = _resolve_actor(op.src, op.res, aliases)
        if op.res not in held:
            return cs, (TRANSFER_OF_UNOWNED, op.res)
        if actor is not _CURRENT_OWNER and held[op.res] != actor:
            return cs, (TRANSFER_AFTER_MOVE, op.res)
        held[op.res] = op.dst  # move: previous owner loses all rights
    elif isinstance(op, Borrow):
        if held.get(op.res) != op.owner:
            return cs, (BORROW_OF_UNOWNED, op.res)
        borrows.add((op.res, op.borrower))
    elif isinstance(op, EndBorrow):
        borrows.discard((op.res, op.borrower))
    else:
        raise TypeError(f"unknown op {op!r}")
    return CState.make(held, released, borrows, aliases, env), None


def _edge_open(edge, cs: CState) -> bool:
    if edge.guard is None:
        return True
    var, val = edge.guard
    return cs.env_map().get(var, False) == val


def exit_violations(node, cs: CState):
    """Ownership invariant checked at every exit (normal AND failure)."""
    if not node.is_exit:
        return []
    out = [(LEAK, r) for r in cs.held_map()]
    out += [(UNRETURNED_BORROW, r) for (r, _b) in cs.borrows]
    return out


@dataclass
class Violation:
    kind: str
    res: str
    node: str
    trace: tuple  # node names from the entry to the violation, replayable


def _node_trace(parents: dict, key) -> tuple:
    seq = []
    while key is not None:
        seq.append(key[0])
        key = parents[key]
    return tuple(reversed(seq))


def explore(prog: Program, max_states: int = 100_000, max_steps: int = 200_000):
    """Bounded exhaustive BFS over the concrete state graph.

    Returns ``(violations, complete, num_states)``.  ``complete`` is False
    when a bound was hit, in which case results must stay *pending*.
    """
    start = (prog.entry, initial_state(prog))
    parents = {start: None}
    queue = deque([start])
    violations: list[Violation] = []
    steps = 0
    complete = True
    while queue:
        if steps >= max_steps or len(parents) >= max_states:
            complete = False
            break
        n, cs = queue.popleft()
        steps += 1
        node = prog.nodes[n]
        for kind, res in exit_violations(node, cs):
            violations.append(Violation(kind, res, n, _node_trace(parents, (n, cs))))
        cs2, err = apply_op(prog, node, cs)
        if err:
            violations.append(Violation(err[0], err[1], n, _node_trace(parents, (n, cs))))
            continue  # a concrete execution stops at its first error
        for e in out_edges(node):
            if not _edge_open(e, cs2):
                continue
            key = (e.target, cs2)
            if key in parents:
                continue
            parents[key] = (n, cs)
            queue.append(key)
    return violations, complete, len(parents)


def find_witness(prog: Program, target: str, pred,
                 max_states: int = 50_000, max_steps: int = 100_000):
    """Bounded concrete replay: search a path to ``target`` whose arrival
    state satisfies ``pred``.

    Returns ``(trace, exhaustive)``: ``trace`` is a replayable node
    sequence or ``None``; ``exhaustive`` is True only when the whole
    bounded concrete state graph was searched, so ``(None, True)`` means
    the abstract candidate is a *refuted* false positive.
    """
    start = (prog.entry, initial_state(prog))
    if prog.entry == target and pred(start[1]):
        return (prog.entry,), True
    parents = {start: None}
    queue = deque([start])
    steps = 0
    exhaustive = True
    while queue:
        if steps >= max_steps or len(parents) >= max_states:
            exhaustive = False
            break
        n, cs = queue.popleft()
        steps += 1
        node = prog.nodes[n]
        cs2, err = apply_op(prog, node, cs)
        if err:
            continue
        for e in out_edges(node):
            if not _edge_open(e, cs2):
                continue
            key = (e.target, cs2)
            if key in parents:
                continue
            parents[key] = (n, cs)
            if e.target == target and pred(cs2):
                return _node_trace(parents, key), True
            queue.append(key)
    return None, exhaustive


def replay(prog: Program, trace: tuple):
    """Replay a witness trace concretely; returns ``(node, CState)``.

    Raises ``AssertionError`` if the trace is not a valid concrete
    execution - witnesses must be replayable by construction.
    """
    assert trace and trace[0] == prog.entry, "trace must start at the entry"
    n, cs = prog.entry, initial_state(prog)
    for nxt in trace[1:]:
        node = prog.nodes[n]
        cs2, err = apply_op(prog, node, cs)
        if err:
            raise AssertionError(f"replay hit error {err} at {n}")
        for e in out_edges(node):
            if e.target == nxt and _edge_open(e, cs2):
                n, cs = nxt, cs2
                break
        else:
            raise AssertionError(f"no open edge {n} -> {nxt}")
    return n, cs
