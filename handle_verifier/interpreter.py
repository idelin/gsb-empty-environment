"""Abstract interpreter: exact exploration of the finite-state model.

The analysis keeps, per program point, the *set* of possible configurations
(alias map + per-resource state).  Control-flow merges take the union, so
the possible ownership set is preserved across joins.  Loops are iterated
to a fixpoint.  Function calls use summaries (input resource states ->
outcomes + errors) which are themselves computed by a monotone fixpoint
over the whole program, so recursive functions are supported.

The model is finite-state, therefore exploration is exact and terminates
unless a budget / loop / iteration cap is hit or an unknown function is
called -- in those cases the verdict stays PENDING ("not fully explored").
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field

from .errors import Certainty, ErrorKind, ErrorRecord
from .model import (Assign, Borrow, Call, Close, EndBorrow, If, Move, Open,
                    Return, Use, While)
from .semantics import step_simple_maps
from .state import RState, from_maps, to_maps

SAFE = "safe"
UNSAFE = "unsafe"
PENDING = "pending"


class _BudgetExceeded(Exception):
    pass


@dataclass
class FuncAnalysis:
    """Accumulated facts for one function under one input-state signature."""
    key: tuple
    outcomes: dict = field(default_factory=dict)      # (origin, state) -> choices
    reach: dict = field(default_factory=dict)         # uid -> {Config}
    hits: dict = field(default_factory=dict)          # (uid, kind) -> {Config: bool}
    best_choices: dict = field(default_factory=dict)  # (uid, kind) -> choices
    detail: dict = field(default_factory=dict)        # (uid, kind) -> str

    def add_reach(self, uid, configs):
        self.reach.setdefault(uid, set()).update(configs)

    def add_hit(self, uid, kind, config, choices, definite, detail):
        key = (uid, kind)
        votes = self.hits.setdefault(key, {})
        votes[config] = votes.get(config, False) or definite
        best = self.best_choices.get(key)
        if best is None or len(choices) < len(best):
            self.best_choices[key] = tuple(choices)
        self.detail.setdefault(key, detail)

    def add_outcome(self, origin, state, choices):
        """Record one way the function can return; keeps the shortest
        choice sequence so counterexamples stay replayable."""
        key = (origin, state)
        best = self.outcomes.get(key)
        if best is None or len(choices) < len(best):
            self.outcomes[key] = tuple(choices)

    def is_definite(self, uid, kind):
        votes = self.hits[(uid, kind)]
        reached = self.reach.get(uid, set())
        return bool(reached) and reached <= votes.keys() and \
            all(votes[c] for c in reached)

    def snapshot(self):
        return (
            frozenset(self.outcomes.items()),
            frozenset(self.hits.keys()),
            frozenset(k for k in self.hits if self.is_definite(*k)),
        )

    def error_records(self, where):
        records = []
        for uid, kind in sorted(self.hits, key=lambda k: (k[0], k[1].value)):
            certainty = (Certainty.DEFINITE if self.is_definite(uid, kind)
                         else Certainty.POSSIBLE)
            records.append(ErrorRecord(
                kind=kind, certainty=certainty, where=where(uid),
                detail=self.detail[(uid, kind)],
                choices=self.best_choices[(uid, kind)]))
        return records


@dataclass
class AnalysisResult:
    verdict: str
    errors: list
    reasons: list
    steps: int

    @property
    def ok(self):
        return self.verdict == SAFE

    def __str__(self):
        lines = [f"verdict: {self.verdict} (steps={self.steps})"]
        for reason in self.reasons:
            lines.append(f"  incomplete: {reason}")
        for err in self.errors:
            lines.append(f"  {err}")
        return "\n".join(lines)


def index_program(program):
    """Assign a stable uid to every statement object in the program."""
    uid_of, stmt_of, func_of = {}, {}, {}
    counter = [0]

    def walk(func, stmts):
        for stmt in stmts:
            uid_of[id(stmt)] = counter[0]
            stmt_of[counter[0]] = stmt
            func_of[counter[0]] = func.name
            counter[0] += 1
            if isinstance(stmt, If):
                walk(func, stmt.then_body)
                walk(func, stmt.else_body)
            elif isinstance(stmt, While):
                walk(func, stmt.body)

    for func in program.functions.values():
        walk(func, func.body)
    return uid_of, stmt_of, func_of


def describe_stmt(stmt):
    if isinstance(stmt, Open):
        return f"open {stmt.var}"
    if isinstance(stmt, Assign):
        return f"{stmt.dst} = {stmt.src}"
    if isinstance(stmt, Borrow):
        return f"borrow {stmt.dst} = &{stmt.mode} {stmt.src}"
    if isinstance(stmt, EndBorrow):
        return f"endborrow {stmt.var}"
    if isinstance(stmt, Close):
        return f"close {stmt.var}"
    if isinstance(stmt, Use):
        return f"{stmt.access} {stmt.var}"
    if isinstance(stmt, Move):
        return f"move {stmt.var}"
    if isinstance(stmt, If):
        return "if ?"
    if isinstance(stmt, While):
        return "while ?"
    if isinstance(stmt, Call):
        return f"{stmt.ret or '_'} = call {stmt.func}({', '.join(stmt.args)})"
    if isinstance(stmt, Return):
        return f"return {stmt.var}"
    return repr(stmt)


class Analyzer:
    def __init__(self, program, max_steps=200_000, loop_cap=512,
                 max_iterations=64):
        self.program = program
        self.max_steps = max_steps
        self.loop_cap = loop_cap
        self.max_iterations = max_iterations
        self.steps = 0
        self.incomplete_reasons = []
        self._uid_of, self._stmt_of, self._func_of = index_program(program)
        self._old = {}
        self._new = {}
        self._worklist = deque()

    # -- helpers ---------------------------------------------------------

    def uid(self, stmt):
        return self._uid_of[id(stmt)]

    def where(self, uid):
        return f"{self._func_of[uid]}#{uid}: {describe_stmt(self._stmt_of[uid])}"

    def _tick(self):
        self.steps += 1
        if self.steps > self.max_steps:
            raise _BudgetExceeded

    def _incomplete(self, why):
        if why not in self.incomplete_reasons:
            self.incomplete_reasons.append(why)

    def _hit(self, fa, uid, kind, config, choices, definite, detail):
        fa.add_hit(uid, kind, config, choices, definite, detail)

    # -- summaries ---------------------------------------------------------

    def get_summary(self, key):
        """Summary for (function, input states); monotone approximation."""
        if key in self._new:
            return self._new[key]
        if key in self._old:
            return self._old[key]
        if key[0] not in self.program.functions:
            self._incomplete(f"unknown function '{key[0]}'")
            return None
        # Not computed yet: use the empty approximation for this iteration
        # and schedule the key so the next iteration sees the real one.
        if key not in self._worklist:
            self._worklist.append(key)
        return FuncAnalysis(key)

    def _compute(self, key):
        fname, in_states = key
        func = self.program.functions[fname]
        fa = FuncAnalysis(key)
        env = {p: i for i, p in enumerate(func.params)}
        start = from_maps(env,
                          {i: s for i, s in enumerate(in_states)},
                          {i: (0, 0) for i in range(len(in_states))},
                          {})
        normal, returned = self._exec_stmts(func, func.body, {start: ()}, fa)
        for config, choices in normal.items():
            fa.add_outcome(None, None, choices)
        for config, choices, stmt in returned:
            uid = self.uid(stmt)
            if stmt.var is None:
                fa.add_outcome(None, None, choices)
                continue
            rid = dict(config.env).get(stmt.var)
            if rid is None:
                self._hit(fa, uid, ErrorKind.UNBOUND_VAR, config, choices,
                          True, f"return of unbound variable '{stmt.var}'")
                continue
            state = dict(config.rstate)[rid]
            if stmt.var in func.params:
                fa.add_outcome(func.params.index(stmt.var), state, choices)
            else:
                fa.add_outcome("local", state, choices)
        return fa

    # -- statement execution ---------------------------------------------

    def _exec_stmts(self, func, stmts, configs, fa):
        """Returns (normal configs, returned [(config, choices, stmt)])."""
        returned = []
        current = dict(configs)
        for stmt in stmts:
            if not current:
                break
            uid = self.uid(stmt)
            fa.add_reach(uid, current.keys())
            nxt = {}

            def put(config, choices):
                if config not in nxt:
                    nxt[config] = choices

            for config, choices in current.items():
                self._tick()
                if isinstance(stmt, If):
                    out_t, ret_t = self._exec_stmts(
                        func, stmt.then_body, {config: choices + ("then",)}, fa)
                    out_e, ret_e = self._exec_stmts(
                        func, stmt.else_body, {config: choices + ("else",)}, fa)
                    for c2, ch2 in out_t.items():
                        put(c2, ch2)
                    for c2, ch2 in out_e.items():
                        put(c2, ch2)
                    returned += ret_t + ret_e
                elif isinstance(stmt, While):
                    exits, ret_w = self._exec_while(func, stmt, config,
                                                    choices, fa)
                    for c2, ch2 in exits.items():
                        put(c2, ch2)
                    returned += ret_w
                elif isinstance(stmt, Call):
                    for c2, extra in self._exec_call(fa, stmt, uid, config,
                                                     choices):
                        put(c2, choices + extra)
                elif isinstance(stmt, Return):
                    returned.append((config, choices, stmt))
                else:
                    env, rstate, borrows, borrow_of = to_maps(config)
                    errs = step_simple_maps(stmt, env, rstate, borrows,
                                            borrow_of)
                    for kind, detail in errs:
                        self._hit(fa, uid, kind, config, choices, True, detail)
                    put(from_maps(env, rstate, borrows, borrow_of), choices)
            current = nxt
        return current, returned

    def _exec_while(self, func, stmt, config, choices, fa):
        """Fixpoint over the loop head; keeps the full possibility set."""
        returned = []
        seen = {config}
        frontier = {config: choices}
        exits = {}
        for _ in range(self.loop_cap):
            if not frontier:
                break
            entered = {c: ch + ("enter",) for c, ch in frontier.items()}
            body_out, body_ret = self._exec_stmts(func, stmt.body, entered, fa)
            returned += body_ret
            new = {}
            for c2, ch2 in body_out.items():
                if c2 not in seen:
                    seen.add(c2)
                    new[c2] = ch2
            for c2, ch2 in frontier.items():
                exits.setdefault(c2, ch2 + ("exit",))
            frontier = new
        else:
            self._incomplete(f"loop fixpoint cap {self.loop_cap} reached")
        return exits, returned

    def _exec_call(self, fa, stmt, uid, config, choices):
        env, rstate, borrows, borrow_of = to_maps(config)
        rids = []
        for arg in stmt.args:
            if arg in borrow_of:
                self._hit(fa, uid, ErrorKind.INVALID_OPERATION, config,
                          choices, True,
                          "borrow handle passed as owned argument")
                return [(config, ())]
            rid = env.get(arg)
            if rid is None:
                self._hit(fa, uid, ErrorKind.UNBOUND_VAR, config, choices,
                          True, f"unbound variable '{arg}'")
                return [(config, ())]
            if rid in rids:
                self._hit(fa, uid, ErrorKind.INVALID_OPERATION, config,
                          choices, True, "same resource moved twice")
                return [(config, ())]
            if borrows.get(rid, (0, 0)) != (0, 0):
                self._hit(fa, uid, ErrorKind.BORROW_CONFLICT, config, choices,
                          True, "resource moved into call while borrowed")
                return [(config, ())]
            rids.append(rid)
        in_states = tuple(rstate[r] for r in rids)
        summary = self.get_summary((stmt.func, in_states))
        if summary is None:
            return [(config, ())]
        # Propagate callee errors to this call site (with combined choices).
        for (uid2, kind) in summary.hits:
            definite = summary.is_definite(uid2, kind)
            self._hit(fa, uid, kind, config,
                      choices + summary.best_choices[(uid2, kind)], definite,
                      f"in call to '{stmt.func}': {summary.detail[(uid2, kind)]}")
        out = []
        for (origin, state), ochoices in summary.outcomes.items():
            env2, rstate2 = dict(env), dict(rstate)
            borrows2 = dict(borrows)
            for rid in rids:
                rstate2[rid] = RState.MOVED
            if origin == "local":
                if stmt.ret is not None:
                    fresh = (max(rstate2) + 1) if rstate2 else 0
                    env2[stmt.ret] = fresh
                    rstate2[fresh] = state
                    borrows2[fresh] = (0, 0)
            elif origin is not None:
                rid_ret = rids[origin]
                rstate2[rid_ret] = state
                if stmt.ret is not None:
                    env2[stmt.ret] = rid_ret
            out.append((from_maps(env2, rstate2, borrows2, borrow_of),
                        ochoices))
        return out

    # -- driver ------------------------------------------------------------

    def analyze(self):
        if self.program.main not in self.program.functions:
            raise KeyError(
                f"program has no main function '{self.program.main}'")
        main_key = (self.program.main, ())
        old = {}
        final = None
        converged = False
        try:
            for _ in range(self.max_iterations):
                new = {}
                self._new = new
                self._old = old
                self._worklist = deque(
                    [main_key] + [k for k in old if k != main_key])
                while self._worklist:
                    key = self._worklist.popleft()
                    if key not in new:
                        new[key] = self._compute(key)
                if set(new) == set(old) and all(
                        new[k].snapshot() == old[k].snapshot() for k in new):
                    final = new
                    converged = True
                    break
                old = new
        except _BudgetExceeded:
            self._incomplete("step budget exhausted")
            final = new if new else old
        if final is None:
            final = old
        if not converged and not self.incomplete_reasons:
            self._incomplete("summary fixpoint did not converge")
        main_fa = final.get(main_key, FuncAnalysis(main_key))
        errors = main_fa.error_records(self.where)
        if self.incomplete_reasons:
            verdict = PENDING
        elif errors:
            verdict = UNSAFE
        else:
            verdict = SAFE
        return AnalysisResult(verdict=verdict, errors=errors,
                              reasons=list(self.incomplete_reasons),
                              steps=self.steps)
