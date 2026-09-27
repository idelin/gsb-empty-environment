"""Concrete semantics and bounded concrete-state replay.

Replay takes a path produced by the abstract analysis and executes it with
exact concrete semantics:

- exception / cancel edges are feasible only if the source node really can
  raise / be cancelled (``may_raise`` / ``may_cancel``);
- ``maybe_alias`` is resolved by branching over both choices (bounded by
  ``max_runs``);
- a run halts at the first concrete ownership error.

Replay is used (a) to confirm abstract candidates into definite,
replayable counterexamples, and (b) to filter abstract false positives.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass, field

from . import model as M
from .domain import MOVED, NULL

# concrete error event kinds (shared vocabulary with the abstract analysis)
DOUBLE_RELEASE = "double_release"
RELEASE_AFTER_MOVE = "release_after_move"
RELEASE_OF_NULL = "release_of_null"
RELEASE_WHILE_BORROWED = "release_while_borrowed"
TRANSFER_AFTER_MOVE = "transfer_after_move"
TRANSFER_OF_NULL = "transfer_of_null"
TRANSFER_AFTER_RELEASE = "transfer_after_release"
TRANSFER_WHILE_BORROWED = "transfer_while_borrowed"
USE_AFTER_MOVE = "use_after_move"
USE_OF_NULL = "use_of_null"
USE_AFTER_RELEASE = "use_after_release"
BORROW_AFTER_MOVE = "borrow_after_move"
BORROW_OF_NULL = "borrow_of_null"
BORROW_AFTER_RELEASE = "borrow_after_release"


@dataclass
class CRes:
    state: str = "owned"  # "owned" | "released"
    owner: str | None = None
    borrows: frozenset[str] = frozenset()


@dataclass
class CEvent:
    node: str
    kind: str
    resource: str | None = None


@dataclass
class CRun:
    events: list[CEvent] = field(default_factory=list)
    env: dict[str, str] = field(default_factory=dict)  # var -> rid | NULL | MOVED
    res: dict[str, CRes] = field(default_factory=dict)
    error: bool = False      # halted on an ownership error
    completed: bool = True   # reached the end of the path without halting


@dataclass
class ReplayResult:
    runs: list[CRun]
    complete: bool  # False if the nondeterminism bound truncated exploration


def _fork(run: CRun) -> CRun:
    return CRun(
        events=list(run.events),
        env=dict(run.env),
        res={rid: CRes(c.state, c.owner, c.borrows) for rid, c in run.res.items()},
        error=run.error,
        completed=run.completed,
    )


def _fail(run: CRun, node: M.Node, kind: str, resource: str | None = None) -> None:
    run.events.append(CEvent(node.name, kind, resource))
    run.error = True
    run.completed = False


def _exec(node: M.Node, run: CRun, counter) -> None:
    op = node.op
    kind = op.kind
    if kind == M.NOP:
        return
    if kind == M.ACQUIRE:
        (var,) = op.args
        rid = f"{node.name}#{next(counter)}"
        old = run.env.get(var, NULL)
        if old not in (NULL, MOVED) and old in run.res:
            run.res[old].owner = None  # previous resource loses its owner
        run.env[var] = rid
        run.res[rid] = CRes("owned", var)
        return
    if kind == M.ALIAS:
        dst, src = op.args
        run.env[dst] = run.env.get(src, NULL)
        return
    if kind == M.RELEASE:
        (var,) = op.args
        val = run.env.get(var, NULL)
        if val == MOVED:
            _fail(run, node, RELEASE_AFTER_MOVE)
            return
        if val == NULL:
            _fail(run, node, RELEASE_OF_NULL)
            return
        res = run.res[val]
        if res.state == "released":
            _fail(run, node, DOUBLE_RELEASE, val)
            return
        if res.borrows:
            _fail(run, node, RELEASE_WHILE_BORROWED, val)
            return
        res.state = "released"
        res.owner = None
        return
    if kind == M.TRANSFER:
        src, dst = op.args
        val = run.env.get(src, NULL)
        if val == MOVED:
            _fail(run, node, TRANSFER_AFTER_MOVE)
            return
        if val == NULL:
            _fail(run, node, TRANSFER_OF_NULL)
            return
        res = run.res[val]
        if res.state == "released":
            _fail(run, node, TRANSFER_AFTER_RELEASE, val)
            return
        if res.borrows:
            _fail(run, node, TRANSFER_WHILE_BORROWED, val)
            return
        old = run.env.get(dst, NULL)
        if old not in (NULL, MOVED) and old in run.res:
            run.res[old].owner = None
        run.env[dst] = val
        run.env[src] = MOVED
        res.owner = dst
        return
    if kind == M.BORROW:
        (var,) = op.args
        val = run.env.get(var, NULL)
        if val == MOVED:
            _fail(run, node, BORROW_AFTER_MOVE)
            return
        if val == NULL:
            _fail(run, node, BORROW_OF_NULL)
            return
        res = run.res[val]
        if res.state == "released":
            _fail(run, node, BORROW_AFTER_RELEASE, val)
            return
        res.borrows = res.borrows | {f"borrow@{node.name}"}
        return
    if kind == M.END_BORROW:
        (var,) = op.args
        val = run.env.get(var, NULL)
        if val not in (NULL, MOVED) and val in run.res:
            run.res[val].borrows = frozenset()
        return
    if kind == M.USE:
        (var,) = op.args
        val = run.env.get(var, NULL)
        if val == MOVED:
            _fail(run, node, USE_AFTER_MOVE)
            return
        if val == NULL:
            _fail(run, node, USE_OF_NULL)
            return
        if run.res[val].state == "released":
            _fail(run, node, USE_AFTER_RELEASE, val)
        return
    raise ValueError(f"cannot execute op kind {kind!r}")


def _step(node: M.Node, run: CRun, counter) -> list[CRun]:
    if node.op.kind == M.MAYBE_ALIAS:
        dst, src = node.op.args
        current = run.env.get(dst, NULL)
        other = run.env.get(src, NULL)
        if current == other:
            return [run]
        alt = _fork(run)
        alt.env[dst] = other
        return [run, alt]
    _exec(node, run, counter)
    return [run]


def replay(program: M.Program, path: tuple[tuple[str, str], ...], max_runs: int = 256) -> ReplayResult:
    """Concretely execute ``path`` (sequence of ``(node, arrive_edge_kind)``).

    Returns all concrete runs (branching over ``maybe_alias`` choices),
    bounded by ``max_runs``.
    """
    counter = itertools.count()
    runs = [CRun()]
    truncated = False
    for i, (node_name, arrive_kind) in enumerate(path):
        node = program.nodes[node_name]
        if i > 0:
            prev = program.nodes[path[i - 1][0]]
            if arrive_kind == M.EXCEPTION and not prev.may_raise:
                return ReplayResult([], True)  # concretely infeasible path
            if arrive_kind == M.CANCEL and not prev.may_cancel:
                return ReplayResult([], True)
        nxt: list[CRun] = []
        for run in runs:
            if run.error:
                nxt.append(run)  # halted runs carry over unchanged
                continue
            for branch in _step(node, run, counter):
                if len(nxt) >= max_runs:
                    truncated = True
                    break
                nxt.append(branch)
            if truncated:
                break
        runs = nxt
        if truncated:
            break
    return ReplayResult(runs, not truncated)


def leaked_resources(run: CRun, acquire_node: str | None = None) -> set[str]:
    """Resources still owned at the end of ``run``.

    ``acquire_node`` restricts to resources created by that acquire node.
    A resource counts as leaked whenever it was never released, regardless
    of whether any variable still refers to it.
    """
    out: set[str] = set()
    for rid, res in run.res.items():
        if res.state != "owned":
            continue
        if acquire_node is not None and not rid.startswith(acquire_node + "#"):
            continue
        out.add(rid)
    return out
