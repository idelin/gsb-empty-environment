"""Abstract interpreter over the interval domain.

Structured (syntax-directed) analysis:

* branches are contracted by refining variable intervals under the branch
  condition (:meth:`Interval.assume_*`);
* loops are solved by fixed-point iteration with widening at the loop
  head, followed by a configurable number of narrowing iterations
  (``narrowing_iterations=0`` disables narrowing);
* division/modulo by a possibly-zero divisor and possibly-false
  assertions produce diagnostics.  Because the domain over-approximates,
  alarms are marked ``possible`` unless the bad case is unavoidable
  (``definite``);
* refinement that cannot be represented exactly in the interval domain
  (e.g. ``x != 0`` on an interval straddling zero) keeps the sound
  over-approximation and records a precision note -- it is never
  presented as exact.

Termination: widening is applied after ``widen_delay`` iterations of each
loop, which guarantees convergence of the ascending phase; narrowing runs
for a fixed number of iterations.  A hard iteration cap acts as a safety
net only.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Optional

from .interval import BOTTOM, Interval
from .lang import Assert, Assign, BinOp, Compare, Const, If, Neg, Nondet, Seq, Stmt, Var, While

# Abstract state: None is bottom (unreachable), otherwise var -> Interval.
AState = Optional[Dict[str, Interval]]

_MAX_LOOP_ITERATIONS = 10000  # safety net; widening makes this unreachable


@dataclass
class Diagnostic:
    kind: str            # 'division_by_zero' | 'assertion'
    certainty: str       # 'possible' | 'definite'
    label: str
    message: str

    def __str__(self):
        where = self.label or "<unlabeled>"
        return "[%s] %s at %s: %s" % (self.certainty, self.kind, where, self.message)


@dataclass
class LoopFixpoint:
    label: str
    iterations: int
    widening_applied: bool
    narrowing_iterations: int
    invariant: Dict[str, Interval]


@dataclass
class AnalysisResult:
    diagnostics: List[Diagnostic]
    fixpoints: List[LoopFixpoint]
    precision_notes: List[str]
    exit_state: AState

    @property
    def definite(self) -> List[Diagnostic]:
        return [d for d in self.diagnostics if d.certainty == "definite"]

    @property
    def possible(self) -> List[Diagnostic]:
        return [d for d in self.diagnostics if d.certainty == "possible"]


def _state_join(a: AState, b: AState) -> AState:
    if a is None:
        return b
    if b is None:
        return a
    keys = set(a) | set(b)
    return {k: a.get(k, BOTTOM).join(b.get(k, BOTTOM)) for k in keys}


def _state_leq(a: AState, b: AState) -> bool:
    """True if every interval of ``a`` is included in ``b``'s."""
    if a is None:
        return True
    if b is None:
        return False
    for k, v in a.items():
        u = b.get(k, BOTTOM)
        if v.is_bottom:
            continue
        if u.is_bottom or v.lo < u.lo or v.hi > u.hi:
            return False
    return True


def _state_widen(a: AState, b: AState) -> AState:
    if a is None:
        return b
    if b is None:
        return a
    keys = set(a) | set(b)
    return {k: a.get(k, BOTTOM).widen(b.get(k, BOTTOM)) for k in keys}


def _state_narrow(a: AState, b: AState) -> AState:
    if a is None or b is None:
        return None
    keys = set(a) | set(b)
    out = {}
    for k in keys:
        v = a.get(k, BOTTOM).narrow(b.get(k, BOTTOM))
        if v.is_bottom:
            return None
        out[k] = v
    return out


def _collect_vars(stmt: Stmt, acc: set) -> None:
    def walk_expr(e):
        if isinstance(e, Var):
            acc.add(e.name)
        elif isinstance(e, BinOp):
            walk_expr(e.left)
            walk_expr(e.right)
        elif isinstance(e, Neg):
            walk_expr(e.operand)
        elif isinstance(e, Compare):
            walk_expr(e.left)
            walk_expr(e.right)

    if isinstance(stmt, Assign):
        acc.add(stmt.name)
        walk_expr(stmt.expr)
    elif isinstance(stmt, Seq):
        for s in stmt.stmts:
            _collect_vars(s, acc)
    elif isinstance(stmt, If):
        walk_expr(stmt.cond)
        _collect_vars(stmt.then_body, acc)
        if stmt.else_body is not None:
            _collect_vars(stmt.else_body, acc)
    elif isinstance(stmt, While):
        walk_expr(stmt.cond)
        _collect_vars(stmt.body, acc)
    elif isinstance(stmt, Assert):
        walk_expr(stmt.cond)


class Analyzer:
    def __init__(self, widening_delay: int = 1, narrowing_iterations: int = 2):
        if narrowing_iterations < 0:
            raise ValueError("narrowing_iterations must be >= 0")
        self.widening_delay = widening_delay
        self.narrowing_iterations = narrowing_iterations
        self.diagnostics: List[Diagnostic] = []
        self.fixpoints: List[LoopFixpoint] = []
        self.precision_notes: List[str] = []
        self._label = ""

    # -- public entry ------------------------------------------------------
    def analyze(self, program: Stmt, initial: Optional[Dict[str, Interval]] = None) -> AnalysisResult:
        self.diagnostics = []
        self.fixpoints = []
        self.precision_notes = []
        self._label = ""
        state: AState = dict(initial) if initial is not None else {}
        # Concrete semantics reads unassigned variables as 0; mirror that.
        vars_in_program: set = set()
        _collect_vars(program, vars_in_program)
        for name in vars_in_program:
            state.setdefault(name, Interval.const(0))
        exit_state = self._exec(program, state)
        return AnalysisResult(
            diagnostics=list(self.diagnostics),
            fixpoints=list(self.fixpoints),
            precision_notes=list(self.precision_notes),
            exit_state=exit_state,
        )

    # -- expressions ---------------------------------------------------------
    def _eval(self, expr, state: AState) -> Interval:
        if state is None:
            return BOTTOM
        if isinstance(expr, Const):
            return Interval.const(expr.value)
        if isinstance(expr, Var):
            return state.get(expr.name, Interval.const(0))
        if isinstance(expr, Neg):
            return self._eval(expr.operand, state).neg()
        if isinstance(expr, Nondet):
            return Interval.of(expr.lo, expr.hi)
        if isinstance(expr, BinOp):
            left = self._eval(expr.left, state)
            right = self._eval(expr.right, state)
            if expr.op == "+":
                return left.add(right)
            if expr.op == "-":
                return left.sub(right)
            if expr.op == "*":
                return left.mul(right)
            if expr.op in ("/", "%"):
                self._check_divisor(expr.op, right)
                return left.div(right) if expr.op == "/" else left.mod(right)
            raise ValueError("unknown op %r" % expr.op)
        if isinstance(expr, Compare):
            left = self._eval(expr.left, state)
            right = self._eval(expr.right, state)
            return self._eval_compare(expr.op, left, right)
        raise TypeError("not an expression: %r" % (expr,))

    def _eval_compare(self, op: str, left: Interval, right: Interval) -> Interval:
        if op == "==":
            if left.must_eq(right):
                return Interval.const(1)
            if not left.maybe_eq(right):
                return Interval.const(0)
        elif op == "!=":
            if left.must_eq(right):
                return Interval.const(0)
            if not left.maybe_eq(right):
                return Interval.const(1)
        elif op in ("<", "<=", ">", ">="):
            a, b = (left, right) if op in ("<", "<=") else (right, left)
            strict = op in ("<", ">")
            if a.is_bottom or b.is_bottom:
                return Interval.const(0)
            if strict:
                must = a.hi < b.lo
                maybe = a.lo < b.hi
            else:
                must = a.hi <= b.lo
                maybe = a.lo <= b.hi
            if must:
                return Interval.const(1)
            if not maybe:
                return Interval.const(0)
        else:
            raise ValueError("unknown comparison %r" % op)
        return Interval.of(0, 1)

    def _check_divisor(self, op: str, divisor: Interval) -> None:
        if divisor.is_bottom or not divisor.contains_zero():
            return
        if divisor.is_singleton():  # divisor is exactly [0, 0]
            certainty = "definite"
        else:
            certainty = "possible"
        self.diagnostics.append(Diagnostic(
            kind="division_by_zero",
            certainty=certainty,
            label=self._label,
            message="divisor of '%s' may be zero in %s" % (op, divisor),
        ))

    # -- branch contraction ---------------------------------------------------
    def _assume(self, cond, state: AState, polarity: bool) -> AState:
        """Refine ``state`` so that ``cond`` has truth value ``polarity``."""
        if state is None:
            return None
        if isinstance(cond, Compare):
            op = cond.op if polarity else _negate(cond.op)
            return self._refine_compare(op, cond.left, cond.right, state)
        # Generic truthiness: expr is true iff expr != 0.
        if polarity:
            return self._refine_compare("!=", cond, Const(0), state)
        return self._refine_compare("==", cond, Const(0), state)

    def _refine_compare(self, op: str, left, right, state: AState) -> AState:
        il = self._eval(left, state)
        ir = self._eval(right, state)
        if il.is_bottom or ir.is_bottom:
            return None
        out = dict(state)
        if isinstance(left, Var):
            refined, precise = _assume_op(il, op, ir)
            if refined.is_bottom:
                return None
            out[left.name] = refined
            if not precise:
                self._note_imprecision(left.name, op, ir, il)
        if isinstance(right, Var):
            refined, precise = _assume_op(ir, _flip(op), il)
            if refined.is_bottom:
                return None
            current = out.get(right.name, state[right.name])
            meet = current.meet(refined)
            if meet.is_bottom:
                return None
            out[right.name] = meet
            if not precise:
                self._note_imprecision(right.name, _flip(op), il, ir)
        return out

    def _note_imprecision(self, name: str, op: str, other: Interval, original: Interval):
        self.precision_notes.append(
            "at %s: refinement of '%s %s %s' on %s=%s is not representable "
            "exactly; kept sound over-approximation %s"
            % (self._label or "<unlabeled>", name, op, other, name, original, original)
        )

    # -- statements ---------------------------------------------------------
    def _exec(self, stmt: Stmt, state: AState) -> AState:
        if state is None:
            return None
        prev_label = self._label
        self._label = getattr(stmt, "label", "") or prev_label
        try:
            if isinstance(stmt, Assign):
                out = dict(state)
                out[stmt.name] = self._eval(stmt.expr, state)
                return out
            if isinstance(stmt, Seq):
                for s in stmt.stmts:
                    state = self._exec(s, state)
                return state
            if isinstance(stmt, If):
                then_state = self._exec(stmt.then_body, self._assume(stmt.cond, state, True))
                else_state = state
                if stmt.else_body is not None:
                    else_state = self._exec(stmt.else_body, self._assume(stmt.cond, state, False))
                else:
                    else_state = self._assume(stmt.cond, state, False)
                return _state_join(then_state, else_state)
            if isinstance(stmt, While):
                return self._exec_while(stmt, state)
            if isinstance(stmt, Assert):
                self._check_assert(stmt, state)
                return state
            raise TypeError("not a statement: %r" % (stmt,))
        finally:
            self._label = prev_label

    def _check_assert(self, stmt: Assert, state: AState) -> None:
        verdict = self._eval(stmt.cond, state)
        if verdict.is_singleton() and verdict.lo == 1:
            return  # definitely holds
        if verdict.is_singleton() and verdict.lo == 0:
            certainty = "definite"
        else:
            certainty = "possible"
        self.diagnostics.append(Diagnostic(
            kind="assertion",
            certainty=certainty,
            label=stmt.label or self._label,
            message="assertion evaluates to %s" % verdict,
        ))

    def _exec_while(self, stmt: While, pre: AState) -> AState:
        # Ascending phase with widening at the loop head.
        head: AState = pre
        iterations = 0
        widened = False
        while True:
            iterations += 1
            if iterations > _MAX_LOOP_ITERATIONS:
                raise RuntimeError("loop fixpoint did not converge (safety cap)")
            body_out = self._exec(stmt.body, self._assume(stmt.cond, head, True))
            joined = _state_join(pre, body_out)
            if _state_leq(joined, head):
                break
            if iterations > self.widening_delay:
                head = _state_widen(head, joined)
                widened = True
            else:
                head = joined
        # Narrowing phase (optional, bounded).
        narrowing_done = 0
        if widened:
            for _ in range(self.narrowing_iterations):
                body_out = self._exec(stmt.body, self._assume(stmt.cond, head, True))
                narrowed = _state_narrow(head, _state_join(pre, body_out))
                if narrowed is None or _state_leq(narrowed, head) and _state_leq(head, narrowed):
                    head = narrowed
                    narrowing_done += 1
                    break
                head = narrowed
                narrowing_done += 1
        self.fixpoints.append(LoopFixpoint(
            label=stmt.label or self._label,
            iterations=iterations,
            widening_applied=widened,
            narrowing_iterations=narrowing_done,
            invariant=dict(head) if head is not None else {},
        ))
        return self._assume(stmt.cond, head, False)


def _negate(op: str) -> str:
    return {"==": "!=", "!=": "==", "<": ">=", "<=": ">", ">": "<=", ">=": "<"}[op]


def _flip(op: str) -> str:
    return {"==": "==", "!=": "!=", "<": ">", "<=": ">=", ">": "<", ">=": "<="}[op]


def _assume_op(interval: Interval, op: str, other: Interval):
    if op == "==":
        return interval.assume_eq(other)
    if op == "!=":
        return interval.assume_ne(other)
    if op == "<":
        return interval.assume_lt(other)
    if op == "<=":
        return interval.assume_le(other)
    if op == ">":
        return interval.assume_gt(other)
    if op == ">=":
        return interval.assume_ge(other)
    raise ValueError("unknown comparison %r" % op)
