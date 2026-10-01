"""Concrete semantics for the handle model.

* ``replay(program, trace)`` -- deterministically re-execute a witness trace
  and report the concrete error it reaches plus the concrete state.

* ``explore(program)`` -- exhaustively enumerate every concrete execution of
  a (small) program, collecting real errors each with a replayable concrete
  witness trace and the concrete state at the error.

* ``confirm(program, report)`` -- match the analyzer's possible errors
  against concrete witnesses, turning them into concretizable, replayable
  counterexamples.
"""
from __future__ import annotations

import sys

from model import (Alias, BorrowMut, BorrowShared, Call, Close, EndBorrow, If,
                   Move, Open, Read, Return, While, Write)


class ConcreteError(Exception):
    def __init__(self, kind, detail=""):
        super().__init__(detail)
        self.kind = kind
        self.detail = detail


class _Ret(Exception):
    def __init__(self, st, ret_id, ev=None):
        self.st = st
        self.ret_id = ret_id
        self.ev = ev


class _Fuel(Exception):
    pass


class ReplayMismatch(Exception):
    pass


class Heap:
    def __init__(self):
        self.res = {}       # rid -> {"status", "shared": set, "mut": var|None}
        self.next_id = 0


class CState:
    def __init__(self, env=None, moved=None, heap=None):
        self.env = env if env is not None else {}
        self.moved = moved if moved is not None else set()
        self.heap = heap if heap is not None else Heap()

    def copy(self):
        return CState(dict(self.env), set(self.moved), self.heap)

    def snapshot(self):
        return {"env": dict(self.env),
                "moved": sorted(self.moved),
                "res": {r: {"status": i["status"], "shared": sorted(i["shared"]),
                            "mut": i["mut"]}
                        for r, i in sorted(self.heap.res.items())}}


def do_atomic(s, st):
    heap = st.heap
    if isinstance(s, Open):
        rid = heap.next_id
        heap.next_id += 1
        st.env[s.var] = rid
        heap.res[rid] = {"status": "open", "shared": set(), "mut": None}
        st.moved.discard(s.var)
        return
    if isinstance(s, Close):
        v = s.var
        if v in st.moved:
            raise ConcreteError("use-after-move", "close of moved '%s'" % v)
        rid = st.env.get(v)
        if rid is None:
            return
        r = heap.res[rid]
        if r["status"] == "closed":
            raise ConcreteError("double-close", "close of %s" % rid)
        if r["shared"] or r["mut"]:
            raise ConcreteError("close-while-borrowed", "close of %s" % rid)
        r["status"] = "closed"
        return
    if isinstance(s, Alias):
        if s.src in st.moved:
            raise ConcreteError("use-after-move", "alias of moved '%s'" % s.src)
        if s.src in st.env:
            st.env[s.dst] = st.env[s.src]
            st.moved.discard(s.dst)
        return
    if isinstance(s, Move):
        if s.src in st.moved:
            raise ConcreteError("use-after-move", "move from moved '%s'" % s.src)
        if s.src not in st.env:
            return
        rid = st.env.pop(s.src)
        r = heap.res[rid]
        if r["shared"] or r["mut"]:
            raise ConcreteError("move-while-borrowed", "move of %s" % rid)
        st.env[s.dst] = rid
        st.moved.add(s.src)
        st.moved.discard(s.dst)
        return
    if isinstance(s, (BorrowShared, BorrowMut)):
        owner, b = s.owner, s.borrower
        if owner in st.moved:
            raise ConcreteError("use-after-move", "borrow of moved '%s'" % owner)
        rid = st.env.get(owner)
        if rid is None:
            return
        r = heap.res[rid]
        if r["status"] == "closed":
            raise ConcreteError("borrow-after-close", "borrow of %s" % rid)
        if isinstance(s, BorrowShared):
            if r["mut"]:
                raise ConcreteError("shared-borrow-while-mut", "%s" % rid)
            r["shared"].add(b)
        else:
            if r["shared"]:
                raise ConcreteError("mut-borrow-while-shared", "%s" % rid)
            if r["mut"]:
                raise ConcreteError("double-mut-borrow", "%s" % rid)
            r["mut"] = b
        st.env[b] = rid
        st.moved.discard(b)
        return
    if isinstance(s, EndBorrow):
        b = s.borrower
        for r in heap.res.values():
            r["shared"].discard(b)
            if r["mut"] == b:
                r["mut"] = None
        st.env.pop(b, None)
        st.moved.discard(b)
        return
    if isinstance(s, (Read, Write)):
        v = s.var
        if v in st.moved:
            raise ConcreteError("use-after-move", "use of moved '%s'" % v)
        rid = st.env.get(v)
        if rid is None:
            return
        r = heap.res[rid]
        if r["status"] == "closed":
            raise ConcreteError("use-after-close", "use of %s" % rid)
        if isinstance(s, Read):
            if r["mut"] and r["mut"] != v:
                raise ConcreteError("read-while-mut-borrowed", "%s" % rid)
        else:
            if v in r["shared"]:
                raise ConcreteError("write-through-shared-borrow", "%s" % rid)
            if r["shared"]:
                raise ConcreteError("write-while-shared-borrowed", "%s" % rid)
            if r["mut"] and r["mut"] != v:
                raise ConcreteError("write-while-mut-borrowed", "%s" % rid)
        return
    raise TypeError("unknown statement %r" % (s,))


def _bind_call(funcs, s, st):
    fn = funcs[s.func]
    for a in s.args:
        if a in st.moved:
            raise ConcreteError("use-after-move",
                                "call passes moved-from '%s'" % a)
    return fn, CState({p: st.env[a] for p, a in zip(fn.params, s.args)
                       if a in st.env}, set(), st.heap)


def _unmap_call(fn, s, st, cst, ret_id):
    for p_, a in zip(fn.params, s.args):
        if p_ in cst.env:
            st.env[a] = cst.env[p_]
        else:
            st.env.pop(a, None)
        if p_ in cst.moved:
            st.moved.add(a)
        else:
            st.moved.discard(a)
    if s.ret is not None:
        if ret_id is not None:
            st.env[s.ret] = ret_id
        else:
            st.env.pop(s.ret, None)
        st.moved.discard(s.ret)


# --------------------------------------------------------------------- replay
def replay(program, trace):
    """Follow a witness trace concretely.

    Returns ``(kind, snapshot)``: the concrete error kind reached (``None``
    when the trace completes cleanly) and the concrete state at that point.
    """
    funcs = {f.name: f for f in program.functions}
    events = list(trace)
    pos = [0]

    def nxt():
        if pos[0] >= len(events):
            raise ReplayMismatch("trace exhausted")
        ev = events[pos[0]]
        pos[0] += 1
        return ev

    def expect(tag, *rest):
        ev = nxt()
        if ev[0] != tag or (rest and ev[1] != rest[0]):
            raise ReplayMismatch("expected %s%s, got %r" % (tag, rest, ev))
        return ev

    def run_block(stmts, st):
        for s in stmts:
            run_stmt(s, st)

    def run_stmt(s, st):
        if isinstance(s, If):
            ev = nxt()
            if ev == ("if-then",):
                run_block(s.then, st)
            elif ev == ("if-else",):
                run_block(s.otherwise, st)
            else:
                raise ReplayMismatch("bad if event %r" % (ev,))
        elif isinstance(s, While):
            while True:
                ev = nxt()
                if ev == ("while-enter",):
                    run_block(s.body, st)
                elif ev == ("while-exit",):
                    break
                else:
                    raise ReplayMismatch("bad while event %r" % (ev,))
        elif isinstance(s, Call):
            expect("call", s.func)
            fn, cst = _bind_call(funcs, s, st)
            ret_id = None
            try:
                run_block(fn.body, cst)
            except _Ret as r:
                cst, ret_id = r.st, r.ret_id
            expect("call-end")
            _unmap_call(fn, s, st, cst, ret_id)
        elif isinstance(s, Return):
            expect("return")
            raise _Ret(st, st.env.get(s.var) if s.var else None)
        else:
            ev = nxt()
            if ev != ("exec", s):
                raise ReplayMismatch("expected exec %r, got %r" % (s, ev))
            do_atomic(s, st)

    st = CState()
    try:
        expect("call", "main")
        try:
            run_block(funcs["main"].body, st)
        except _Ret:
            pass
        expect("call-end")
    except ConcreteError as e:
        return e.kind, st.snapshot()
    return None, st.snapshot()


# -------------------------------------------------------------------- explore
def explore(program, max_steps=20000):
    """Exhaustively run every concrete execution.

    Returns ``(witnesses, complete)`` where ``witnesses`` maps an error kind
    to ``(trace, snapshot)`` of a replayable concrete counterexample, and
    ``complete`` is False when the step budget cut the search short.
    """
    sys.setrecursionlimit(max(sys.getrecursionlimit(), 200000))
    funcs = {f.name: f for f in program.functions}
    witnesses = {}
    steps = [0]

    def tick():
        steps[0] += 1
        if steps[0] > max_steps:
            raise _Fuel()

    def run_block(stmts, i, st, ev, cont):
        if i >= len(stmts):
            cont(st, ev)
            return
        run_stmt(stmts[i], st, ev,
                 lambda st2, ev2: run_block(stmts, i + 1, st2, ev2, cont))

    def run_stmt(s, st, ev, cont):
        try:
            run_stmt_raw(s, st, ev, cont)
        except ConcreteError as e:
            if e.kind not in witnesses:
                witnesses[e.kind] = (e.witness, st.snapshot())

    def run_stmt_raw(s, st, ev, cont):
        tick()
        if isinstance(s, If):
            run_block(s.then, 0, st.copy(), ev + (("if-then",),), cont)
            run_block(s.otherwise, 0, st.copy(), ev + (("if-else",),), cont)
        elif isinstance(s, While):
            def iterate(cur, cev):
                tick()
                cont(cur.copy(), cev + (("while-exit",),))
                run_block(s.body, 0, cur, cev + (("while-enter",),), iterate)
            iterate(st, ev)
        elif isinstance(s, Call):
            try:
                fn, cst = _bind_call(funcs, s, st)
            except ConcreteError as e:
                e.witness = ev + (("call", s.func),)
                raise

            def after(cst2, ret_id, cev):
                _unmap_call(fn, s, st, cst2, ret_id)
                cont(st, cev + (("call-end",),))
            try:
                run_block(fn.body, 0, cst, ev + (("call", s.func),),
                          lambda cst2, cev: after(cst2, None, cev))
            except _Ret as r:
                after(r.st, r.ret_id, r.ev)
        elif isinstance(s, Return):
            raise _Ret(st, st.env.get(s.var) if s.var else None,
                       ev + (("return",),))
        else:
            ev2 = ev + (("exec", s),)
            try:
                do_atomic(s, st)
            except ConcreteError as e:
                e.witness = ev2
                raise
            cont(st, ev2)

    complete = True
    try:
        try:
            run_block(funcs["main"].body, 0, CState(), (("call", "main"),),
                      lambda st2, ev2: None)
        except _Ret:
            pass
    except _Fuel:
        complete = False
    return witnesses, complete


# -------------------------------------------------------------------- confirm
def confirm(program, report, max_steps=20000):
    """Attach concrete, replayable counterexamples to a report's errors.

    Every error whose kind is witnessed by a concrete execution gets
    ``concrete_trace``/``concrete_state`` filled in; the rest remain
    *possible* errors of the conservative abstraction.  Returns True when
    the concrete search was exhaustive.
    """
    witnesses, complete = explore(program, max_steps=max_steps)
    for e in report.errors:
        w = witnesses.get(e.kind)
        if w is not None:
            e.concrete_trace, e.concrete_state = w
    return complete
