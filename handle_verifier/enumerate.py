"""Reliability checking by enumerating small finite programs.

Random small programs are generated and each one is checked two ways:

- abstractly, with the fixpoint analyzer (must be complete, not PENDING);
- concretely, by brute-force enumeration of every choice sequence.

Reliability requirements verified here:

- soundness: every error the concrete enumeration finds must be reported
  by the analyzer (no missed errors);
- exactness on loop-free programs: every error the analyzer reports must
  also show up concretely (no phantom errors);
- replayability: every reported counterexample must replay concretely.
"""
from __future__ import annotations

import random
from dataclasses import dataclass, field

from .concrete import enumerate_concrete_errors, replay
from .interpreter import PENDING, Analyzer
from .model import (EXCLUSIVE, READ, SHARED, WRITE, Assign, Borrow, Call,
                    Close, EndBorrow, Function, If, Move, Open, Program,
                    Return, Use, While)

OWNERS = ("x", "y", "z")
HANDLES = ("b", "c")


def gen_stmt(rng, depth, helpers, loops):
    options = ["open", "assign", "borrow", "endborrow", "close", "use",
               "use", "move"]
    if depth < 2:
        options += ["if", "if"]
    if depth < 1 and loops[0] > 0:
        options.append("while")
    if helpers:
        options += ["call", "call"]
    kind = rng.choice(options)
    if kind == "open":
        return Open(rng.choice(OWNERS))
    if kind == "assign":
        return Assign(rng.choice(OWNERS), rng.choice(OWNERS))
    if kind == "borrow":
        return Borrow(rng.choice(HANDLES), rng.choice(OWNERS + HANDLES),
                      rng.choice([SHARED, EXCLUSIVE]))
    if kind == "endborrow":
        return EndBorrow(rng.choice(HANDLES))
    if kind == "close":
        return Close(rng.choice(OWNERS))
    if kind == "use":
        return Use(rng.choice(OWNERS + HANDLES), rng.choice([READ, WRITE]))
    if kind == "move":
        return Move(rng.choice(OWNERS))
    if kind == "if":
        return If(gen_stmts(rng, depth + 1, helpers, loops),
                  gen_stmts(rng, depth + 1, helpers, loops))
    if kind == "while":
        loops[0] -= 1
        return While(gen_stmts(rng, depth + 1, (), loops))
    name = rng.choice(helpers)
    ret = rng.choice([None, rng.choice(OWNERS)])
    return Call(ret, name, (rng.choice(OWNERS),))


def gen_stmts(rng, depth, helpers, loops):
    return tuple(gen_stmt(rng, depth, helpers, loops)
                 for _ in range(rng.randint(2, 4)))


def gen_program(rng, with_helpers=True):
    functions = {}
    helpers = []
    loops = [2]
    if with_helpers:
        for i in range(rng.randint(0, 2)):
            name = f"helper{i}"
            body = list(gen_stmts(rng, 1, (), loops))
            if rng.random() < 0.7:
                body.append(Return(rng.choice(["p", None])))
            functions[name] = Function(name, ("p",), tuple(body))
            helpers.append(name)
    functions["main"] = Function("main", (), gen_stmts(rng, 0, helpers, loops))
    return Program(functions)


def has_while(stmts):
    for stmt in stmts:
        if isinstance(stmt, While):
            return True
        if isinstance(stmt, If) and (has_while(stmt.then_body)
                                     or has_while(stmt.else_body)):
            return True
    return False


def count_choices(stmts):
    total = 0
    for stmt in stmts:
        if isinstance(stmt, If):
            total += 1 + count_choices(stmt.then_body) \
                + count_choices(stmt.else_body)
        elif isinstance(stmt, While):
            total += 2 + count_choices(stmt.body)
    return total


def program_has_while(program):
    return any(has_while(f.body) for f in program.functions.values())


def program_choice_bound(program):
    return sum(count_choices(f.body) for f in program.functions.values())


@dataclass
class ReliabilityReport:
    total: int = 0
    pending: list = field(default_factory=list)
    soundness_violations: list = field(default_factory=list)
    exactness_violations: list = field(default_factory=list)
    replay_failures: list = field(default_factory=list)
    concrete_runs: int = 0

    @property
    def ok(self):
        return not (self.soundness_violations or self.exactness_violations
                    or self.replay_failures)


def check_program(index, program, report):
    result = Analyzer(program).analyze()
    if result.verdict == PENDING:
        report.pending.append((index, list(result.reasons)))
        return
    max_choices = program_choice_bound(program) + 2
    concrete_kinds, runs, _truncated = enumerate_concrete_errors(
        program, max_choices=max_choices)
    report.concrete_runs += runs
    abstract_kinds = {e.kind for e in result.errors}
    missing = concrete_kinds - abstract_kinds
    if missing:
        report.soundness_violations.append((index, missing))
    if not program_has_while(program):
        phantom = abstract_kinds - concrete_kinds
        if phantom:
            report.exactness_violations.append((index, phantom))
    for record in result.errors:
        if not replay(program, record):
            report.replay_failures.append((index, record))


def check_reliability(count=60, seed=20241001):
    rng = random.Random(seed)
    report = ReliabilityReport()
    for i in range(count):
        report.total += 1
        check_program(i, gen_program(rng), report)
    return report
