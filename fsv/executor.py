"""Small-step executor with an explicit control stack.

Deep control flow never uses Python recursion: statements are executed by a
frame machine (`ctrl` is an explicit stack) and expressions are evaluated by
an explicit work stack.  Each call to `step` processes exactly one frame and
returns the successor state plus per-step `Evidence` carrying the source
location of what happened.

The heap is finite: at most MAX_OBJS arrays of at most MAX_CELLS cells.
Faults (overflow in ERROR mode, uninitialized reads, assertion failures,
index errors, division by zero, heap exhaustion) unwind the control stack;
`finally` cleanup blocks run during unwinding before the fault escapes.
"""

from dataclasses import dataclass, replace

from .faults import FaultError
from .lang import Assert, Assign, Bin, If, Idx, Len, Lit, NewArr, Store, Try, Un, Var, While
from .state import MISSING, Ref, State, env_get, env_set, heap_get, heap_set, show_value

MAX_OBJS = 64   # finite heap: maximum number of live arrays
MAX_CELLS = 64  # finite heap: maximum array length

# Frame tags on the explicit control stack.
DO = "do"            # (DO, stmt)                execute a statement
FINALLY = "finally"  # (FINALLY, body, try_loc)  pending cleanup block
RERAISE = "reraise"  # (RERAISE, fault)          pending fault propagation


@dataclass(frozen=True)
class Evidence:
    """What happened in one step, anchored at a source location."""

    loc: str
    kind: str  # "exec" | "cleanup" | "propagate"
    desc: str


def initial_state(program):
    ctrl = tuple((DO, s) for s in reversed(tuple(program)))
    return State(ctrl=ctrl, env=(), heap=(), next_hid=0)


# ---------------------------------------------------------------------------
# Expression evaluation (iterative, explicit work stack)
# ---------------------------------------------------------------------------

def _need_int(v, loc):
    if not isinstance(v, int):
        raise FaultError("type", f"expected int, got {show_value(v)}", loc)
    return v


def _binop(op, l, r, dom, loc):
    _need_int(l, loc)
    _need_int(r, loc)
    if op == "+":
        res = l + r
    elif op == "-":
        res = l - r
    elif op == "*":
        res = l * r
    elif op in ("//", "%"):
        if r == 0:
            raise FaultError("div_by_zero", "division by zero", loc)
        res = l // r if op == "//" else l % r
    elif op == "==":
        res = int(l == r)
    elif op == "!=":
        res = int(l != r)
    elif op == "<":
        res = int(l < r)
    elif op == "<=":
        res = int(l <= r)
    elif op == ">":
        res = int(l > r)
    elif op == ">=":
        res = int(l >= r)
    elif op == "and":
        res = int(l != 0 and r != 0)
    elif op == "or":
        res = int(l != 0 or r != 0)
    else:
        raise FaultError("type", f"unknown operator {op!r}", loc)
    return dom.check(res, loc)


def _children(node):
    if isinstance(node, Bin):
        return (node.left, node.right)
    if isinstance(node, Un):
        return (node.e,)
    if isinstance(node, Idx):
        return (node.arr, node.index)
    if isinstance(node, Len):
        return (node.arr,)
    return ()


def eval_expr(expr, env, heap, dom, loc):
    vals = []
    work = [(expr, False)]
    while work:
        node, done = work.pop()
        if not done:
            if isinstance(node, Lit):
                vals.append(node.value)
            elif isinstance(node, Var):
                v = env_get(env, node.name, MISSING)
                if v is MISSING:
                    raise FaultError(
                        "uninit_read",
                        f"read of uninitialized variable '{node.name}'",
                        loc,
                    )
                vals.append(v)
            else:
                work.append((node, True))
                for child in reversed(_children(node)):
                    work.append((child, False))
        else:
            _apply(node, vals, heap, dom, loc)
    return vals[0]


def _apply(node, vals, heap, dom, loc):
    if isinstance(node, Bin):
        r = vals.pop()
        l = vals.pop()
        vals.append(_binop(node.op, l, r, dom, loc))
    elif isinstance(node, Un):
        v = _need_int(vals.pop(), loc)
        vals.append(dom.check(-v if node.op == "neg" else int(v == 0), loc))
    elif isinstance(node, Idx):
        i = vals.pop()
        a = vals.pop()
        cells = _need_cells(a, heap, loc)
        _need_int(i, loc)
        if not 0 <= i < len(cells):
            raise FaultError(
                "index_out_of_bounds",
                f"index {i} out of bounds (length {len(cells)})",
                loc,
            )
        vals.append(cells[i])
    elif isinstance(node, Len):
        a = vals.pop()
        vals.append(dom.check(len(_need_cells(a, heap, loc)), loc))
    else:  # pragma: no cover - defensive
        raise FaultError("type", f"unknown expression {node!r}", loc)


def _need_cells(a, heap, loc):
    if not isinstance(a, Ref):
        raise FaultError("type", f"expected array reference, got {a}", loc)
    cells = heap_get(heap, a.hid, MISSING)
    if cells is MISSING:  # pragma: no cover - normalize() collects garbage
        raise FaultError("type", f"dangling reference @{a.hid}", loc)
    return cells


def _truthy(v, loc):
    return _need_int(v, loc) != 0


# ---------------------------------------------------------------------------
# Statement execution
# ---------------------------------------------------------------------------

def _exec(stmt, st, ctrl, dom):
    """Execute one statement; returns (state, description). May raise FaultError."""
    loc = stmt.loc
    if isinstance(stmt, Assign):
        v = eval_expr(stmt.expr, st.env, st.heap, dom, loc)
        if isinstance(v, int):
            v = dom.check(v, loc)
        st = replace(st, env=env_set(st.env, stmt.name, v), ctrl=tuple(ctrl))
        return st, f"{stmt.name} := {show_value(v)}"
    if isinstance(stmt, NewArr):
        n = _need_int(eval_expr(stmt.size, st.env, st.heap, dom, loc), loc)
        if n < 0 or n > MAX_CELLS:
            raise FaultError(
                "heap_overflow", f"array size {n} exceeds limit {MAX_CELLS}", loc
            )
        if len(st.heap) >= MAX_OBJS:
            raise FaultError(
                "heap_overflow", f"heap object limit {MAX_OBJS} reached", loc
            )
        hid = st.next_hid
        st = replace(
            st,
            env=env_set(st.env, stmt.name, Ref(hid)),
            heap=heap_set(st.heap, hid, (0,) * n),
            next_hid=hid + 1,
            ctrl=tuple(ctrl),
        )
        return st, f"{stmt.name} := new int[{n}] @{hid}"
    if isinstance(stmt, Store):
        a = eval_expr(stmt.arr, st.env, st.heap, dom, loc)
        i = eval_expr(stmt.index, st.env, st.heap, dom, loc)
        v = eval_expr(stmt.value, st.env, st.heap, dom, loc)
        cells = _need_cells(a, st.heap, loc)
        _need_int(i, loc)
        _need_int(v, loc)
        if not 0 <= i < len(cells):
            raise FaultError(
                "index_out_of_bounds",
                f"index {i} out of bounds (length {len(cells)})",
                loc,
            )
        v = dom.check(v, loc)
        st = replace(
            st, heap=heap_set(st.heap, a.hid, cells[:i] + (v,) + cells[i + 1 :]),
            ctrl=tuple(ctrl),
        )
        return st, f"@{a.hid}[{i}] := {v}"
    if isinstance(stmt, If):
        c = _truthy(eval_expr(stmt.cond, st.env, st.heap, dom, loc), loc)
        for s in reversed(stmt.then if c else stmt.orelse):
            ctrl.append((DO, s))
        return replace(st, ctrl=tuple(ctrl)), f"if -> {'then' if c else 'else'}"
    if isinstance(stmt, While):
        c = _truthy(eval_expr(stmt.cond, st.env, st.heap, dom, loc), loc)
        if c:
            ctrl.append((DO, stmt))
            for s in reversed(stmt.body):
                ctrl.append((DO, s))
        return replace(st, ctrl=tuple(ctrl)), f"while -> {'body' if c else 'exit'}"
    if isinstance(stmt, Try):
        ctrl.append((FINALLY, stmt.final, stmt.loc))
        for s in reversed(stmt.body):
            ctrl.append((DO, s))
        return replace(st, ctrl=tuple(ctrl)), "try"
    if isinstance(stmt, Assert):
        if not _truthy(eval_expr(stmt.cond, st.env, st.heap, dom, loc), loc):
            raise FaultError("assert", "assertion failed", loc)
        return replace(st, ctrl=tuple(ctrl)), "assert ok"
    raise FaultError("type", f"unknown statement {stmt!r}", loc)


def _unwind(ctrl, fault):
    """Pop frames up to the innermost pending cleanup.

    Returns (ctrl, None) if a FINALLY frame was found (re-armed behind a
    RERAISE marker), or ([], fault) if the fault escapes the program.
    """
    ctrl = list(ctrl)
    while ctrl:
        frame = ctrl.pop()
        if frame[0] == FINALLY:
            ctrl.append((RERAISE, fault))
            ctrl.append(frame)
            return ctrl, None
    return [], fault


def step(st, dom):
    """One small step. Returns (state, Evidence) or None when halted/faulted."""
    if st.fault is not None or not st.ctrl:
        return None
    ctrl = list(st.ctrl)
    frame = ctrl.pop()
    tag = frame[0]
    if tag == DO:
        stmt = frame[1]
        try:
            st2, desc = _exec(stmt, st, ctrl, dom)
            return st2, Evidence(stmt.loc, "exec", desc)
        except FaultError as fe:
            ctrl2, pending = _unwind(ctrl, fe.fault)
            if pending is not None:
                st2 = replace(st, ctrl=(), fault=pending)
                return st2, Evidence(stmt.loc, "exec", f"FAULT {pending}")
            st2 = replace(st, ctrl=tuple(ctrl2))
            return st2, Evidence(
                stmt.loc, "exec", f"fault {fe.fault.kind} raised; unwinding"
            )
    if tag == FINALLY:
        _, body, loc = frame
        for s in reversed(body):
            ctrl.append((DO, s))
        return replace(st, ctrl=tuple(ctrl)), Evidence(
            loc, "cleanup", "enter finally cleanup"
        )
    if tag == RERAISE:
        fault = frame[1]
        ctrl2, pending = _unwind(ctrl, fault)
        if pending is not None:
            st2 = replace(st, ctrl=(), fault=pending)
            return st2, Evidence(
                fault.loc, "propagate", f"fault {fault.kind} escapes"
            )
        st2 = replace(st, ctrl=tuple(ctrl2))
        return st2, Evidence(
            fault.loc, "propagate", f"fault {fault.kind} propagates outward"
        )
    raise AssertionError(f"corrupt control frame: {frame!r}")  # pragma: no cover
