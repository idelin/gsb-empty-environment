"""An independent interpreter used to replay counterexample traces.

This is a second, deliberately separate implementation of the language
semantics (mutable environment/heap, its own control stack).  `interpret`
records the (loc, kind) event stream of a run; `replay_counterexample`
checks that the stream and the final fault exactly reproduce a trace found
by the explorer, so every reported counterexample is independently
replayable.
"""

from dataclasses import dataclass, field

from .executor import MAX_CELLS, MAX_OBJS, _binop, _need_int
from .faults import FaultError
from .lang import Assert, Assign, Bin, If, Idx, Len, Lit, NewArr, Store, Try, Un, Var, While
from .state import Ref


@dataclass
class ReplayResult:
    events: list = field(default_factory=list)  # [(loc, kind)]
    fault: object = None                        # Fault | None
    steps: int = 0
    completed: bool = True                      # False if the step cap was hit


class _Prop:
    """Sentinel work item: re-raise a pending fault after a cleanup block."""

    __slots__ = ("fault",)

    def __init__(self, fault):
        self.fault = fault


def _eval(e, env, heap, dom, loc):
    if isinstance(e, Lit):
        return e.value
    if isinstance(e, Var):
        if e.name not in env:
            raise FaultError(
                "uninit_read", f"read of uninitialized variable '{e.name}'", loc
            )
        return env[e.name]
    if isinstance(e, Bin):
        return _binop(
            e.op, _eval(e.left, env, heap, dom, loc),
            _eval(e.right, env, heap, dom, loc), dom, loc,
        )
    if isinstance(e, Un):
        v = _need_int(_eval(e.e, env, heap, dom, loc), loc)
        return dom.check(-v if e.op == "neg" else int(v == 0), loc)
    if isinstance(e, Idx):
        cells = _cells_of(_eval(e.arr, env, heap, dom, loc), heap, loc)
        i = _need_int(_eval(e.index, env, heap, dom, loc), loc)
        if not 0 <= i < len(cells):
            raise FaultError(
                "index_out_of_bounds",
                f"index {i} out of bounds (length {len(cells)})",
                loc,
            )
        return cells[i]
    if isinstance(e, Len):
        return dom.check(
            len(_cells_of(_eval(e.arr, env, heap, dom, loc), heap, loc)), loc
        )
    raise FaultError("type", f"unknown expression {e!r}", loc)  # pragma: no cover


def _cells_of(a, heap, loc):
    if not isinstance(a, Ref) or a.hid not in heap:
        raise FaultError("type", "expected a live array reference", loc)
    return heap[a.hid]


def _run_stmt(stmt, env, heap, work, handlers, dom):
    loc = stmt.loc
    if isinstance(stmt, Assign):
        v = _eval(stmt.expr, env, heap, dom, loc)
        env[stmt.name] = dom.check(v, loc) if isinstance(v, int) else v
    elif isinstance(stmt, NewArr):
        n = _need_int(_eval(stmt.size, env, heap, dom, loc), loc)
        if n < 0 or n > MAX_CELLS or len(heap) >= MAX_OBJS:
            raise FaultError("heap_overflow", "finite heap exhausted", loc)
        env[stmt.name] = Ref(len(heap))
        heap[len(heap)] = [0] * n
    elif isinstance(stmt, Store):
        cells = _cells_of(_eval(stmt.arr, env, heap, dom, loc), heap, loc)
        i = _need_int(_eval(stmt.index, env, heap, dom, loc), loc)
        v = _need_int(_eval(stmt.value, env, heap, dom, loc), loc)
        if not 0 <= i < len(cells):
            raise FaultError(
                "index_out_of_bounds",
                f"index {i} out of bounds (length {len(cells)})",
                loc,
            )
        cells[i] = dom.check(v, loc)
    elif isinstance(stmt, If):
        c = _need_int(_eval(stmt.cond, env, heap, dom, loc), loc) != 0
        work.extend(reversed(stmt.then if c else stmt.orelse))
    elif isinstance(stmt, While):
        if _need_int(_eval(stmt.cond, env, heap, dom, loc), loc) != 0:
            work.append(stmt)
            work.extend(reversed(stmt.body))
    elif isinstance(stmt, Try):
        handlers.append((stmt.final, stmt.loc, len(work)))
        work.extend(reversed(stmt.body))
    elif isinstance(stmt, Assert):
        if _need_int(_eval(stmt.cond, env, heap, dom, loc), loc) == 0:
            raise FaultError("assert", "assertion failed", loc)
    else:  # pragma: no cover - defensive
        raise FaultError("type", f"unknown statement {stmt!r}", loc)


def interpret(program, dom, step_cap=1_000_000):
    env = {}
    heap = {}
    events = []
    work = list(reversed(tuple(program)))
    handlers = []  # (final_body, try_loc, work_depth_at_entry)
    res = ReplayResult(events=events)
    while work:
        if res.steps >= step_cap:
            res.completed = False
            return res
        item = work.pop()
        if isinstance(item, _Prop):
            fault = item.fault
            events.append((fault.loc, "propagate"))
            if not handlers:
                res.fault = fault
                return res
            final, tloc, depth = handlers.pop()
            del work[depth:]
            events.append((tloc, "cleanup"))
            work.append(_Prop(fault))
            work.extend(reversed(final))
            continue
        res.steps += 1
        events.append((item.loc, "exec"))
        try:
            _run_stmt(item, env, heap, work, handlers, dom)
        except FaultError as fe:
            if not handlers:
                res.fault = fe.fault
                return res
            final, tloc, depth = handlers.pop()
            del work[depth:]
            events.append((tloc, "cleanup"))
            work.append(_Prop(fe.fault))
            work.extend(reversed(final))
        while handlers and len(work) == handlers[-1][2]:
            final, tloc, _ = handlers.pop()
            events.append((tloc, "cleanup"))
            work.extend(reversed(final))
    return res


def replay_counterexample(program, dom, trace, fault_kind):
    """True iff the independent interpreter reproduces the trace exactly."""
    cap = max(1_000, 4 * (len(trace) + 16))
    res = interpret(program, dom, step_cap=cap)
    if not res.completed or res.fault is None or res.fault.kind != fault_kind:
        return False
    if len(res.events) != len(trace):
        return False
    return all(
        loc == t.loc and kind == t.kind
        for (loc, kind), t in zip(res.events, trace)
    )
