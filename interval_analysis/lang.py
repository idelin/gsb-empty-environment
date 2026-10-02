"""A tiny integer language: AST, concrete semantics, and replay.

Programs are built from immutable AST nodes.  The only source of
nondeterminism is :class:`Nondet`, which chooses an integer inside a
declared finite range.  Every execution is therefore fully described by
the sequence of choices it makes, which is what makes counterexamples
replayable: re-running with the recorded choice sequence deterministically
reproduces the same execution.

Concrete arithmetic matches the abstract domain: division and modulo
truncate toward zero (C semantics).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import List, Optional, Sequence, Tuple

from .interval import _trunc_div, _trunc_mod


# ---------------------------------------------------------------------------
# Expressions
# ---------------------------------------------------------------------------
class Expr:
    __slots__ = ()


@dataclass(frozen=True)
class Const(Expr):
    value: int


@dataclass(frozen=True)
class Var(Expr):
    name: str


@dataclass(frozen=True)
class BinOp(Expr):
    op: str  # one of '+', '-', '*', '/', '%'
    left: Expr
    right: Expr


@dataclass(frozen=True)
class Neg(Expr):
    operand: Expr


@dataclass(frozen=True)
class Nondet(Expr):
    """Nondeterministic integer in [lo, hi]; bounds must be finite."""

    lo: int
    hi: int

    def __post_init__(self):
        if self.lo > self.hi:
            raise ValueError("empty nondet range")


@dataclass(frozen=True)
class Compare(Expr):
    op: str  # one of '==', '!=', '<', '<=', '>', '>='
    left: Expr
    right: Expr


# ---------------------------------------------------------------------------
# Statements
# ---------------------------------------------------------------------------
class Stmt:
    label: str = ""


@dataclass(frozen=True)
class Assign(Stmt):
    name: str
    expr: Expr
    label: str = ""


@dataclass(frozen=True)
class If(Stmt):
    cond: Expr
    then_body: "Stmt"
    else_body: Optional["Stmt"] = None
    label: str = ""


@dataclass(frozen=True)
class While(Stmt):
    cond: Expr
    body: "Stmt"
    label: str = ""


@dataclass(frozen=True)
class Assert(Stmt):
    cond: Expr
    label: str = ""


@dataclass(frozen=True)
class Seq(Stmt):
    stmts: Tuple["Stmt", ...] = ()
    label: str = ""


def seq(*stmts: Stmt) -> Stmt:
    """Convenience: flatten nested sequences."""
    flat: List[Stmt] = []
    for s in stmts:
        if isinstance(s, Seq) and not s.label:
            flat.extend(s.stmts)
        else:
            flat.append(s)
    return Seq(tuple(flat))


# ---------------------------------------------------------------------------
# Concrete execution
# ---------------------------------------------------------------------------
class ExecutionError(Exception):
    """Base class for runtime failures during concrete execution."""


class DivisionByZero(ExecutionError):
    def __init__(self, label: str):
        super().__init__("division by zero at %s" % (label or "<unlabeled>"))
        self.label = label


class AssertionFailed(ExecutionError):
    def __init__(self, label: str):
        super().__init__("assertion failed at %s" % (label or "<unlabeled>"))
        self.label = label


class OutOfInputs(ExecutionError):
    """The replay script ran out of nondeterministic choices."""


class StepLimitExceeded(ExecutionError):
    """Execution did not terminate within the configured step budget."""


@dataclass
class ExecutionResult:
    env: Optional[dict]          # final environment, None if execution failed
    error: Optional[ExecutionError]
    choices: Tuple[int, ...]     # nondet choices made, in order
    steps: int


class Interpreter:
    """Concrete interpreter.

    ``inputs`` is a replay script: the i-th evaluation of :class:`Nondet`
    yields ``inputs[i]`` (validated against the declared range).  When
    ``inputs`` is None, :class:`Nondet` resolves to its lower bound; the
    verifier never uses this mode, it enumerates inputs explicitly.
    """

    def __init__(self, inputs: Optional[Sequence[int]] = None, max_steps: int = 100000):
        self.inputs = list(inputs) if inputs is not None else None
        self.pos = 0
        self.choices: List[int] = []
        self.max_steps = max_steps
        self.steps = 0
        self._label = ""

    # -- expression evaluation -------------------------------------------
    def eval(self, expr: Expr, env: dict) -> int:
        if isinstance(expr, Const):
            return expr.value
        if isinstance(expr, Var):
            return env.get(expr.name, 0)
        if isinstance(expr, Neg):
            return -self.eval(expr.operand, env)
        if isinstance(expr, Nondet):
            if self.inputs is None:
                value = expr.lo
            else:
                if self.pos >= len(self.inputs):
                    raise OutOfInputs("nondet choice #%d missing" % self.pos)
                value = self.inputs[self.pos]
                if not expr.lo <= value <= expr.hi:
                    raise ValueError(
                        "replay choice %d out of range [%d, %d]"
                        % (value, expr.lo, expr.hi)
                    )
            self.pos += 1
            self.choices.append(value)
            return value
        if isinstance(expr, BinOp):
            left = self.eval(expr.left, env)
            right = self.eval(expr.right, env)
            if expr.op == "+":
                return left + right
            if expr.op == "-":
                return left - right
            if expr.op == "*":
                return left * right
            if expr.op == "/":
                if right == 0:
                    raise DivisionByZero(self._label)
                return _trunc_div(left, right)
            if expr.op == "%":
                if right == 0:
                    raise DivisionByZero(self._label)
                return _trunc_mod(left, right)
            raise ValueError("unknown op %r" % expr.op)
        if isinstance(expr, Compare):
            left = self.eval(expr.left, env)
            right = self.eval(expr.right, env)
            return int(self._compare(expr.op, left, right))
        raise TypeError("not an expression: %r" % (expr,))

    @staticmethod
    def _compare(op: str, left: int, right: int) -> bool:
        if op == "==":
            return left == right
        if op == "!=":
            return left != right
        if op == "<":
            return left < right
        if op == "<=":
            return left <= right
        if op == ">":
            return left > right
        if op == ">=":
            return left >= right
        raise ValueError("unknown comparison %r" % op)

    # -- statement execution ----------------------------------------------
    def _tick(self):
        self.steps += 1
        if self.steps > self.max_steps:
            raise StepLimitExceeded("step budget %d exceeded" % self.max_steps)

    def exec(self, stmt: Stmt, env: dict) -> None:
        self._tick()
        prev_label = self._label
        self._label = getattr(stmt, "label", "") or prev_label
        try:
            if isinstance(stmt, Assign):
                env[stmt.name] = self.eval(stmt.expr, env)
            elif isinstance(stmt, Seq):
                for s in stmt.stmts:
                    self.exec(s, env)
            elif isinstance(stmt, If):
                if self.eval(stmt.cond, env):
                    self.exec(stmt.then_body, env)
                elif stmt.else_body is not None:
                    self.exec(stmt.else_body, env)
            elif isinstance(stmt, While):
                while self.eval(stmt.cond, env):
                    self.exec(stmt.body, env)
            elif isinstance(stmt, Assert):
                if not self.eval(stmt.cond, env):
                    raise AssertionFailed(stmt.label)
            else:
                raise TypeError("not a statement: %r" % (stmt,))
        finally:
            self._label = prev_label

    def run(self, program: Stmt) -> ExecutionResult:
        env: dict = {}
        error: Optional[ExecutionError] = None
        try:
            self.exec(program, env)
        except ExecutionError as exc:
            error = exc
            env = None
        return ExecutionResult(env, error, tuple(self.choices), self.steps)


def run(program: Stmt, inputs: Optional[Sequence[int]] = None,
        max_steps: int = 100000) -> ExecutionResult:
    """Execute ``program``; ``inputs`` replays a recorded choice sequence."""
    return Interpreter(inputs=inputs, max_steps=max_steps).run(program)
