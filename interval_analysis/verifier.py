"""Explicit-state verifier for finite-state models.

The structured program is compiled to a small control-flow graph and all
reachable ``(pc, environment)`` states are explored.  Nondeterminism comes
only from :class:`interval_analysis.lang.Nondet`, whose ranges are finite,
so a model is finite-state iff variable values stay bounded along every
execution.  Exploration therefore always terminates, either by exhausting
the state space or by hitting the ``max_states`` budget:

* ``SAFE``    -- every reachable state checked, no violation;
* ``UNSAFE``  -- a violation (assertion failure or division by zero) was
  reached; the counterexample is the exact sequence of nondet choices and
  can be replayed deterministically (:func:`interval_analysis.lang.run`);
* ``UNKNOWN`` -- the budget was exhausted before the search completed;
  the verdict stays pending and no claim is made.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple

from . import lang
from .lang import (
    Assert, Assign, BinOp, Compare, Const, Expr, If, Neg, Nondet, Seq, Stmt,
    Var, While,
)


class Verdict(enum.Enum):
    SAFE = "safe"
    UNSAFE = "unsafe"
    UNKNOWN = "unknown"  # 未决: search incomplete, no claim made


# ---------------------------------------------------------------------------
# Control-flow graph
# ---------------------------------------------------------------------------
@dataclass
class _Node:
    kind: str                      # 'assign' | 'branch' | 'assert' | 'halt'
    label: str = ""
    name: str = ""                 # assign target
    expr: Optional[Expr] = None    # assign rhs / branch cond / assert cond
    next: int = -1
    other: int = -1                # branch else-edge


def _compile(program: Stmt) -> Tuple[List[_Node], int]:
    nodes: List[_Node] = []

    def add(node: _Node) -> int:
        nodes.append(node)
        return len(nodes) - 1

    def build(stmt: Stmt, next_id: int) -> int:
        if isinstance(stmt, Assign):
            return add(_Node("assign", label=stmt.label, name=stmt.name,
                             expr=stmt.expr, next=next_id))
        if isinstance(stmt, Assert):
            return add(_Node("assert", label=stmt.label, expr=stmt.cond,
                             next=next_id))
        if isinstance(stmt, Seq):
            for s in reversed(stmt.stmts):
                next_id = build(s, next_id)
            return next_id
        if isinstance(stmt, If):
            else_entry = (build(stmt.else_body, next_id)
                          if stmt.else_body is not None else next_id)
            then_entry = build(stmt.then_body, next_id)
            return add(_Node("branch", label=stmt.label, expr=stmt.cond,
                             next=then_entry, other=else_entry))
        if isinstance(stmt, While):
            branch_id = add(_Node("branch", label=stmt.label, expr=stmt.cond,
                                  next=-1, other=next_id))
            body_entry = build(stmt.body, branch_id)
            nodes[branch_id].next = body_entry
            return branch_id
        raise TypeError("not a statement: %r" % (stmt,))

    halt = add(_Node("halt"))
    entry = build(program, halt)
    return nodes, entry


# ---------------------------------------------------------------------------
# Branching expression evaluation (enumerates nondet choices)
# ---------------------------------------------------------------------------
def _eval_all(expr: Expr, env: Dict[str, int]) -> List[Tuple[int, Tuple[int, ...]]]:
    """Return all (value, choices) pairs for ``expr`` under ``env``."""
    if isinstance(expr, Const):
        return [(expr.value, ())]
    if isinstance(expr, Var):
        return [(env.get(expr.name, 0), ())]
    if isinstance(expr, Neg):
        return [(-v, c) for v, c in _eval_all(expr.operand, env)]
    if isinstance(expr, Nondet):
        return [(v, (v,)) for v in range(expr.lo, expr.hi + 1)]
    if isinstance(expr, BinOp):
        out = []
        for lv, lc in _eval_all(expr.left, env):
            try:
                right_values = _eval_all(expr.right, env)
            except lang.DivisionByZero as exc:
                exc.choices = lc + getattr(exc, "choices", ())
                raise
            for rv, rc in right_values:
                choices = lc + rc
                if expr.op == "+":
                    out.append((lv + rv, choices))
                elif expr.op == "-":
                    out.append((lv - rv, choices))
                elif expr.op == "*":
                    out.append((lv * rv, choices))
                elif expr.op == "/":
                    if rv == 0:
                        raise _div_zero(choices)
                    out.append((lang._trunc_div(lv, rv), choices))
                elif expr.op == "%":
                    if rv == 0:
                        raise _div_zero(choices)
                    out.append((lang._trunc_mod(lv, rv), choices))
                else:
                    raise ValueError("unknown op %r" % expr.op)
        return out
    if isinstance(expr, Compare):
        out = []
        for lv, lc in _eval_all(expr.left, env):
            try:
                right_values = _eval_all(expr.right, env)
            except lang.DivisionByZero as exc:
                exc.choices = lc + getattr(exc, "choices", ())
                raise
            for rv, rc in right_values:
                out.append((int(lang.Interpreter._compare(expr.op, lv, rv)), lc + rc))
        return out
    raise TypeError("not an expression: %r" % (expr,))


def _div_zero(choices: Tuple[int, ...]) -> lang.DivisionByZero:
    """DivisionByZero carrying the nondet choices consumed so far."""
    exc = lang.DivisionByZero("")
    exc.choices = tuple(choices)
    return exc


# ---------------------------------------------------------------------------
# Verification
# ---------------------------------------------------------------------------
@dataclass
class Counterexample:
    """A replayable counterexample: the nondet choices leading to the error."""

    choices: Tuple[int, ...]
    error_kind: str                # 'assertion' | 'division_by_zero'
    label: str

    def replay(self, program: Stmt, max_steps: int = 1000000) -> lang.ExecutionResult:
        """Deterministically re-execute ``program`` with the recorded choices."""
        return lang.run(program, inputs=self.choices, max_steps=max_steps)


@dataclass
class VerificationResult:
    verdict: Verdict
    explored_states: int
    counterexample: Optional[Counterexample] = None

    def __str__(self):
        base = "%s (%d states explored)" % (self.verdict.value, self.explored_states)
        if self.counterexample is not None:
            base += "; counterexample choices=%s (%s at %s)" % (
                self.counterexample.choices,
                self.counterexample.error_kind,
                self.counterexample.label or "<unlabeled>",
            )
        return base


class Verifier:
    def __init__(self, max_states: int = 100000):
        if max_states <= 0:
            raise ValueError("max_states must be positive")
        self.max_states = max_states

    def verify(self, program: Stmt) -> VerificationResult:
        nodes, entry = _compile(program)
        initial = (entry, ())
        visited = {initial}
        # parent[state] = (predecessor, choices taken from predecessor)
        parent: Dict[tuple, Optional[tuple]] = {initial: None}
        worklist: List[tuple] = [initial]

        while worklist:
            if len(visited) > self.max_states:
                return VerificationResult(Verdict.UNKNOWN, len(visited))
            pc, env_key = worklist.pop()
            env = dict(env_key)
            node = nodes[pc]

            if node.kind == "halt":
                continue
            try:
                if node.kind == "assign":
                    for value, choices in _eval_all(node.expr, env):
                        new_env = dict(env)
                        new_env[node.name] = value
                        self._succ(node.next, new_env, choices,
                                   (pc, env_key), visited, parent, worklist)
                elif node.kind == "branch":
                    for value, choices in _eval_all(node.expr, env):
                        target = node.next if value else node.other
                        self._succ(target, env, choices,
                                   (pc, env_key), visited, parent, worklist)
                elif node.kind == "assert":
                    for value, choices in _eval_all(node.expr, env):
                        if not value:
                            cex = self._build_cex(
                                (pc, env_key), choices, parent,
                                "assertion", node.label)
                            return VerificationResult(
                                Verdict.UNSAFE, len(visited), cex)
                        self._succ(node.next, env, choices,
                                   (pc, env_key), visited, parent, worklist)
                else:
                    raise RuntimeError("bad node kind %r" % node.kind)
            except lang.DivisionByZero as exc:
                cex = self._build_cex((pc, env_key),
                                      getattr(exc, "choices", ()), parent,
                                      "division_by_zero", node.label)
                return VerificationResult(Verdict.UNSAFE, len(visited), cex)

        return VerificationResult(Verdict.SAFE, len(visited))

    @staticmethod
    def _succ(pc, env, choices, origin, visited, parent, worklist):
        state = (pc, tuple(sorted(env.items())))
        if state not in visited:
            visited.add(state)
            parent[state] = (origin, choices)
            worklist.append(state)

    @staticmethod
    def _build_cex(state, last_choices, parent, error_kind, label) -> Counterexample:
        """Reconstruct the choice sequence from the initial state."""
        segments: List[Tuple[int, ...]] = [tuple(last_choices)]
        cursor = state
        while parent[cursor] is not None:
            prev, choices = parent[cursor]
            segments.append(tuple(choices))
            cursor = prev
        segments.reverse()
        flat: List[int] = []
        for seg in segments:
            flat.extend(seg)
        return Counterexample(tuple(flat), error_kind, label)
