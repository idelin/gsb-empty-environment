"""Concrete executor: replays choice sequences through the same semantics.

Used for two things:

- replaying a counterexample (``replay``) -- an ErrorRecord carries the
  non-deterministic choices that lead to the error, and re-executing them
  concretely must raise the same error kind;
- brute-force enumeration (``enumerate_concrete_errors``) -- explores every
  choice sequence up to a bound so small finite programs can be checked
  against the abstract analysis for reliability.

Simple statements are executed by the very same ``step_simple_maps`` the
abstract interpreter uses, so the engines cannot drift apart.
"""
from __future__ import annotations

from .errors import ErrorKind
from .interpreter import describe_stmt, index_program
from .model import EXCLUSIVE, SHARED, Call, If, Return, While
from .semantics import step_simple_maps
from .state import RState


class _CapExceeded(Exception):
    def __init__(self, why):
        super().__init__(why)
        self.why = why


class ConcreteRun:
    def __init__(self):
        self.errors = []       # [(ErrorKind, where, detail)]
        self.choices_used = 0
        self.needed = None     # "if" | "while" when a choice was missing
        self.incomplete = None


def run_concrete(program, choices=(), step_cap=20_000, call_cap=64):
    """Execute main, taking non-deterministic branches from ``choices``.

    When the sequence is exhausted, defaults are taken (else / exit) and
    ``run.needed`` records the first point that wanted a real choice.
    """
    uid_of, stmt_of, _func_of = index_program(program)
    run = ConcreteRun()
    choices = tuple(choices)
    pos = [0]

    def take(kind, default):
        if pos[0] < len(choices):
            value = choices[pos[0]]
            pos[0] += 1
            run.choices_used = pos[0]
            return value
        if run.needed is None:
            run.needed = kind
        return default

    counter = [0]

    def tick():
        counter[0] += 1
        if counter[0] > step_cap:
            raise _CapExceeded("step cap")

    rstate, borrows = {}, {}

    def record(kind, func, stmt, detail):
        uid = uid_of[id(stmt)]
        run.errors.append(
            (kind, f"{func.name}#{uid}: {describe_stmt(stmt)}", detail))

    def exec_func(func, arg_rids, depth):
        env = dict(zip(func.params, arg_rids))
        borrow_of = {}
        for rid in arg_rids:
            rstate.setdefault(rid, RState.OPEN)
            borrows.setdefault(rid, (0, 0))
        status, rid_ret = exec_stmts(func, func.body, env, borrow_of, depth)
        # Borrow handles are lexical: they die at the function boundary.
        for _var, (rid, mode) in borrow_of.items():
            shared, exclusive = borrows[rid]
            borrows[rid] = (shared - (mode == SHARED),
                            exclusive - (mode == EXCLUSIVE))
        return status, rid_ret

    def exec_stmts(func, stmts, env, borrow_of, depth):
        for stmt in stmts:
            tick()
            if isinstance(stmt, If):
                branch = take("if", "else")
                body = stmt.then_body if branch == "then" else stmt.else_body
                result = exec_stmts(func, body, env, borrow_of, depth)
                if result[0] == "return":
                    return result
            elif isinstance(stmt, While):
                while take("while", "exit") == "enter":
                    result = exec_stmts(func, stmt.body, env, borrow_of, depth)
                    if result[0] == "return":
                        return result
            elif isinstance(stmt, Call):
                do_call(func, stmt, env, borrow_of, depth)
            elif isinstance(stmt, Return):
                if stmt.var is None:
                    return ("return", None)
                rid = env.get(stmt.var)
                if rid is None:
                    record(ErrorKind.UNBOUND_VAR, func, stmt,
                           f"return of unbound variable '{stmt.var}'")
                    return ("return", None)
                return ("return", rid)
            else:
                errs = step_simple_maps(stmt, env, rstate, borrows, borrow_of)
                for kind, detail in errs:
                    record(kind, func, stmt, detail)
        return ("falloff", None)

    def do_call(func, stmt, env, borrow_of, depth):
        rids = []
        for arg in stmt.args:
            if arg in borrow_of:
                record(ErrorKind.INVALID_OPERATION, func, stmt,
                       "borrow handle passed as owned argument")
                return
            rid = env.get(arg)
            if rid is None:
                record(ErrorKind.UNBOUND_VAR, func, stmt,
                       f"unbound variable '{arg}'")
                return
            if rid in rids:
                record(ErrorKind.INVALID_OPERATION, func, stmt,
                       "same resource moved twice")
                return
            if borrows.get(rid, (0, 0)) != (0, 0):
                record(ErrorKind.BORROW_CONFLICT, func, stmt,
                       "resource moved into call while borrowed")
                return
            rids.append(rid)
        if stmt.func not in program.functions:
            run.incomplete = f"unknown function '{stmt.func}'"
            return
        if depth >= call_cap:
            run.incomplete = "call depth cap"
            return
        _status, rid_ret = exec_func(program.functions[stmt.func], rids,
                                     depth + 1)
        if stmt.ret is not None and rid_ret is not None:
            env[stmt.ret] = rid_ret
        for rid in rids:
            if rid != rid_ret:
                rstate[rid] = RState.MOVED

    main = program.functions[program.main]
    try:
        exec_func(main, (), 0)
    except _CapExceeded as exc:
        run.incomplete = exc.why
    return run


def replay(program, record, **kwargs):
    """True iff concretely re-executing record.choices raises record.kind."""
    run = run_concrete(program, record.choices, **kwargs)
    return any(kind is record.kind for kind, _where, _detail in run.errors)


def enumerate_concrete_errors(program, max_choices=12, max_runs=2_000,
                              **kwargs):
    """Brute-force every choice sequence up to ``max_choices`` long.

    Returns (error kinds found, number of concrete runs, truncated flag).
    """
    kinds = set()
    queue = [()]
    seen = {()}
    runs = 0
    while queue and runs < max_runs:
        seq = queue.pop()
        runs += 1
        run = run_concrete(program, seq, **kwargs)
        kinds.update(kind for kind, _where, _detail in run.errors)
        if run.needed is not None and len(seq) < max_choices:
            branches = ("then", "else") if run.needed == "if" \
                else ("enter", "exit")
            for branch in branches:
                extended = seq + (branch,)
                if extended not in seen:
                    seen.add(extended)
                    queue.append(extended)
    return kinds, runs, bool(queue)
