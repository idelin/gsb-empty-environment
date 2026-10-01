"""Conservative abstract interpreter for handle lifecycle checking.

Properties checked (all reported as *possible* errors with a witness trace):
  use-after-close, double-close, borrow-after-close, close-while-borrowed,
  shared/mutual-exclusion borrow violations, read/write through the wrong
  borrow, use-after-move, move-while-borrowed.

Control-flow joins keep the union of possible ownership sets; loops are
solved by ascending fixpoint; functions are summarized by abstract input
signature and recursion is solved by an outer fixpoint over summaries.  If
any budget is exhausted before the search is complete the verdict stays
PENDING instead of claiming safety.
"""
from __future__ import annotations

from dataclasses import dataclass

from model import (Alias, BorrowMut, BorrowShared, Call, Close, EndBorrow, If,
                   Move, Open, Read, Return, While, Write)
from state import CLOSED, OPEN, AState, ResourceInfo


class Pending(Exception):
    """Search budget exhausted: the model was not fully explored."""


@dataclass
class Error:
    kind: str
    detail: str
    trace: tuple          # abstract witness trace (may path to the error)
    state: AState         # abstract state at the error
    concrete_trace: tuple | None = None   # replayable concrete witness
    concrete_state: dict | None = None    # concretized state at the error


@dataclass(frozen=True)
class Summary:
    outputs: tuple        # tuple of (AState, frozenset ret_ids, tuple trace)


@dataclass
class Report:
    verdict: str          # "SAFE" | "UNSAFE" | "PENDING"
    errors: list
    pending_reason: object
    states_explored: int


@dataclass
class Path:
    st: AState
    trace: tuple
    ret: object = None    # frozenset of returned resource ids, or None


def _summaries_equal(a, b):
    if a.keys() != b.keys():
        return False
    for key in a:
        def shape(summ):
            return {(o[0].key(), tuple(sorted(o[1]))) for o in summ.outputs}
        if shape(a[key]) != shape(b[key]):
            return False
    return True


class Analyzer:
    def __init__(self, program, max_steps=20000, max_loop_iters=64,
                 max_outer=64, instance_cap=2):
        self.funcs = {f.name: f for f in program.functions}
        if "main" not in self.funcs:
            raise ValueError("program has no main function")
        self.max_steps = max_steps
        self.max_loop_iters = max_loop_iters
        self.max_outer = max_outer
        self.instance_cap = instance_cap
        self.errors = []
        self._err_keys = set()
        self.pending_reason = None
        self.states = set()
        self.steps = 0
        self._sites = {}
        self.prev = {}
        self.current = {}
        self.computing = set()

    # ------------------------------------------------------------------ util
    def _tick(self, p):
        self.steps += 1
        if self.steps > self.max_steps:
            raise Pending("step budget exceeded (%d)" % self.max_steps)
        self.states.add(p.st.key())

    def _site(self, stmt):
        key = id(stmt)
        if key not in self._sites:
            self._sites[key] = len(self._sites)
        return self._sites[key]

    def _alloc(self, st, site):
        for k in range(self.instance_cap + 1):
            if (site, k) not in st.res:
                return (site, k)
        return (site, self.instance_cap)   # widening: merge further instances

    def _err(self, kind, detail, trace, st):
        key = (kind, detail)
        if key in self._err_keys:
            return
        self._err_keys.add(key)
        self.errors.append(Error(kind, detail, trace, st.copy()))

    def _join_paths(self, paths):
        st, tr = paths[0].st, paths[0].trace
        for p in paths[1:]:
            st = st.join(p.st)
            if len(p.trace) < len(tr):
                tr = p.trace
        return Path(st, tr)

    # ---------------------------------------------------------------- driver
    def analyze(self):
        converged = False
        self.prev = {}
        for _ in range(self.max_outer):
            self.current = {}
            self.computing = set()
            self.steps = 0
            try:
                self.get_summary("main", (), AState())
            except Pending as exc:
                self.pending_reason = str(exc)
            if _summaries_equal(self.prev, self.current):
                converged = True
                break
            self.prev = self.current
        if not converged and self.pending_reason is None:
            self.pending_reason = "summary fixpoint did not converge"
        if self.errors:
            verdict = "UNSAFE"
        elif self.pending_reason is not None:
            verdict = "PENDING"
        else:
            verdict = "SAFE"
        return Report(verdict, list(self.errors), self.pending_reason,
                      len(self.states))

    # --------------------------------------------------------------- blocks
    def exec_block(self, stmts, paths):
        rets = []
        for s in stmts:
            nxt = []
            for p in paths:
                outs, rs = self.exec_stmt(s, p)
                nxt.extend(outs)
                rets.extend(rs)
            paths = nxt
            if not paths:
                break
        return paths, rets

    def exec_stmt(self, s, p):
        self._tick(p)
        if isinstance(s, If):
            t_outs, t_rets = self.exec_block(
                s.then, [Path(p.st.copy(), p.trace + (("if-then",),))])
            e_outs, e_rets = self.exec_block(
                s.otherwise, [Path(p.st.copy(), p.trace + (("if-else",),))])
            outs = t_outs + e_outs
            return ([self._join_paths(outs)] if outs else []), t_rets + e_rets
        if isinstance(s, While):
            return self._exec_while(s, p)
        if isinstance(s, Call):
            return self._exec_call(s, p), []
        if isinstance(s, Return):
            tr = p.trace + (("return",),)
            if s.var is not None and s.var in p.st.moved:
                self._err("use-after-move",
                          "return of moved-from '%s'" % s.var, tr, p.st)
            ret = p.st.env.get(s.var, frozenset()) if s.var else frozenset()
            return [], [Path(p.st, p.trace, ret=ret)]
        return [self._exec_atomic(s, p)], []

    def _exec_while(self, s, p):
        inv_st, inv_tr = p.st, p.trace
        rets = []
        for _ in range(self.max_loop_iters):
            body_ps, body_rs = self.exec_block(
                s.body, [Path(inv_st.copy(), inv_tr + (("while-enter",),))])
            rets.extend(body_rs)
            if not body_ps:
                return [Path(inv_st, inv_tr + (("while-exit",),))], rets
            out = self._join_paths(body_ps)
            new_st = inv_st.join(out.st)
            new_tr = out.trace if len(out.trace) > len(inv_tr) else inv_tr
            if new_st.leq(inv_st):
                return [Path(new_st, inv_tr + (("while-exit",),))], rets
            inv_st, inv_tr = new_st, new_tr
        raise Pending("while fixpoint did not converge (%d iterations)"
                      % self.max_loop_iters)

    # ---------------------------------------------------------------- calls
    def get_summary(self, name, args, st):
        fn = self.funcs[name]
        reachable = set()
        for a in args:
            reachable |= set(st.env.get(a, ()))
        sub_env = {}
        for param, a in zip(fn.params, args):
            ids = st.env.get(a, frozenset())
            if ids:
                sub_env[param] = ids
        sub = AState(sub_env,
                     {r: st.res[r] for r in reachable if r in st.res},
                     frozenset()).gc()
        key = (name, sub.key())
        if key in self.current:
            return self.current[key]
        if key in self.computing:
            # recursive cycle: use the previous outer iteration's summary
            return self.prev.get(key, Summary(()))
        self.computing.add(key)
        paths, rets = self.exec_block(fn.body, [Path(sub, (("call", name),))])
        outputs = []
        for q in paths:
            outputs.append((q.st.gc(), frozenset(), q.trace + (("call-end",),)))
        for q in rets:
            outputs.append((q.st.gc(extra=q.ret or frozenset()),
                            q.ret or frozenset(),
                            q.trace + (("call-end",),)))
        summ = Summary(tuple(outputs))
        self.current[key] = summ
        self.computing.discard(key)
        return summ

    def _exec_call(self, s, p):
        st = p.st
        tr = p.trace
        for a in s.args:
            if a in st.moved:
                self._err("use-after-move",
                          "call %s passes moved-from '%s'" % (s.func, a),
                          tr, st)
        summary = self.get_summary(s.func, s.args, st)
        fn = self.funcs[s.func]
        outs = []
        for out_st, ret_ids, rel_tr in summary.outputs:
            nst = st.copy()
            for rid, info in out_st.res.items():
                nst.res[rid] = info
            moved = set(nst.moved)
            for param, arg in zip(fn.params, s.args):
                ids = out_st.env.get(param, frozenset())
                if ids:
                    nst.env[arg] = ids
                else:
                    nst.env.pop(arg, None)
                if param in out_st.moved:
                    moved.add(arg)
                else:
                    moved.discard(arg)
            if s.ret is not None:
                if ret_ids:
                    nst.env[s.ret] = ret_ids
                else:
                    nst.env.pop(s.ret, None)
                moved.discard(s.ret)
            nst.moved = frozenset(moved)
            outs.append(Path(nst.gc(), tr + rel_tr))
        return outs

    # -------------------------------------------------------------- atomics
    def _exec_atomic(self, s, p):
        st = p.st.copy()
        tr = p.trace + (("exec", s),)

        if isinstance(s, Open):
            rid = self._alloc(st, self._site(s))
            st.env[s.var] = frozenset({rid})
            st.res[rid] = ResourceInfo(frozenset({OPEN}))
            st.moved = st.moved - {s.var}
            return Path(st.gc(), tr)

        if isinstance(s, Close):
            v = s.var
            if v in st.moved:
                self._err("use-after-move", "close('%s') after move" % v, tr, st)
            for rid in st.env.get(v, ()):
                info = st.res.get(rid)
                if info is None:
                    continue
                if CLOSED in info.statuses:
                    self._err("double-close", "close('%s') on %s" % (v, rid),
                              tr, st)
                if info.shared or info.mut:
                    self._err("close-while-borrowed",
                              "close('%s') on %s with active borrows" % (v, rid),
                              tr, st)
                st.res[rid] = ResourceInfo(frozenset({CLOSED}),
                                           info.shared, info.mut)
            return Path(st.gc(), tr)

        if isinstance(s, Alias):
            if s.src in st.moved:
                self._err("use-after-move", "alias of moved-from '%s'" % s.src,
                          tr, st)
            ids = st.env.get(s.src, frozenset())
            if ids:
                st.env[s.dst] = ids          # same identity: shared resource
                st.moved = st.moved - {s.dst}
            return Path(st.gc(), tr)

        if isinstance(s, Move):
            if s.src in st.moved:
                self._err("use-after-move", "move from moved-from '%s'" % s.src,
                          tr, st)
            ids = st.env.get(s.src, frozenset())
            if not ids:
                return Path(st.gc(), tr)
            for rid in ids:
                info = st.res.get(rid)
                if info and (info.shared or info.mut):
                    self._err("move-while-borrowed",
                              "move of '%s' on %s with active borrows"
                              % (s.src, rid), tr, st)
            st.env[s.dst] = ids
            st.env.pop(s.src, None)
            st.moved = (st.moved | {s.src}) - {s.dst}
            return Path(st.gc(), tr)

        if isinstance(s, (BorrowShared, BorrowMut)):
            owner, b = s.owner, s.borrower
            if owner in st.moved:
                self._err("use-after-move", "borrow of moved-from '%s'" % owner,
                          tr, st)
            ids = st.env.get(owner, frozenset())
            for rid in ids:
                info = st.res.get(rid)
                if info is None:
                    continue
                if CLOSED in info.statuses:
                    self._err("borrow-after-close",
                              "borrow of '%s' on closed %s" % (owner, rid),
                              tr, st)
                if isinstance(s, BorrowShared):
                    if info.mut:
                        self._err("shared-borrow-while-mut",
                                  "shared borrow of %s while mutably borrowed"
                                  % (rid,), tr, st)
                    st.res[rid] = ResourceInfo(info.statuses,
                                               info.shared | {b}, info.mut)
                else:
                    if info.shared:
                        self._err("mut-borrow-while-shared",
                                  "mut borrow of %s while shared-borrowed"
                                  % (rid,), tr, st)
                    if info.mut:
                        self._err("double-mut-borrow",
                                  "second mut borrow of %s" % (rid,), tr, st)
                    st.res[rid] = ResourceInfo(info.statuses, info.shared,
                                               info.mut | {b})
            if ids:
                st.env[b] = ids
                st.moved = st.moved - {b}
            return Path(st.gc(), tr)

        if isinstance(s, EndBorrow):
            b = s.borrower
            for rid, info in list(st.res.items()):
                st.res[rid] = ResourceInfo(info.statuses,
                                           info.shared - {b},
                                           info.mut - {b})
            st.env.pop(b, None)
            st.moved = st.moved - {b}
            return Path(st.gc(), tr)

        if isinstance(s, (Read, Write)):
            v = s.var
            if v in st.moved:
                self._err("use-after-move", "use of moved-from '%s'" % v, tr, st)
            for rid in st.env.get(v, ()):
                info = st.res.get(rid)
                if info is None:
                    continue
                if CLOSED in info.statuses:
                    self._err("use-after-close",
                              "use of '%s' on closed %s" % (v, rid), tr, st)
                if isinstance(s, Read):
                    if info.mut and v not in info.mut:
                        self._err("read-while-mut-borrowed",
                                  "read of %s through '%s' while mutably "
                                  "borrowed" % (rid, v), tr, st)
                else:
                    if v in info.shared:
                        self._err("write-through-shared-borrow",
                                  "write of %s through shared borrow '%s'"
                                  % (rid, v), tr, st)
                    elif info.shared:
                        self._err("write-while-shared-borrowed",
                                  "write of %s while shared borrows %s active"
                                  % (rid, sorted(info.shared)), tr, st)
                    if info.mut and v not in info.mut:
                        self._err("write-while-mut-borrowed",
                                  "write of %s through '%s' while mutably "
                                  "borrowed" % (rid, v), tr, st)
            return Path(st.gc(), tr)

        raise TypeError("unknown statement %r" % (s,))
