"""Query plans and rewrite rules whose preconditions are checked via
null-rejection analysis. A rewrite is applied ONLY when semantic
equivalence is guaranteed; otherwise PreconditionError is raised.

Rules implemented
-----------------
R1  select-pushdown into an inner join side:
        sigma_p(A JOIN B)  ==  sigma_p(A) JOIN B
    Precondition: columns(p) subset of columns(A).

R2  select-pushdown below a LEFT OUTER JOIN onto the PRESERVED (left) side:
        sigma_p(A LEFT JOIN B)  ==  sigma_p(A) LEFT JOIN B
    Precondition: columns(p) subset of columns(A).
    (Pushing onto the null-supplying right side is NOT valid in general;
    see the counterexample tests, e.g. p = "B.x IS NULL".)

R3  outer-to-inner join conversion:
        sigma_p(A LEFT JOIN B)  ==  sigma_p(A JOIN B)
    Precondition: p is null-rejecting on columns(B) -- p can never be
    TRUE for a row whose B-part is all NULL (e.g. "B.x > 3", which is
    UNKNOWN on NULL and hence rejected by the WHERE semantics).

R4  after R3, pushdown onto the null-supplying side becomes legal:
        sigma_p(A JOIN B)  ==  A JOIN sigma_p(B)
    Precondition: R3's precondition plus columns(p) subset of columns(B).
"""

from __future__ import annotations

from dataclasses import dataclass

from relalg import (
    And,
    Arith,
    Cmp,
    Expr,
    IsNotNull,
    IsNull,
    Not,
    Or,
    Relation,
    TV,
    inner_join,
    left_join,
    select,
)


class PreconditionError(Exception):
    """Raised when a rewrite's equivalence precondition is not met."""


# ---------------------------------------------------------------------------
# Null-rejection analysis (conservative static analysis on the AST)
# ---------------------------------------------------------------------------

def _strict_unknown(pred: Expr, null_attrs: frozenset) -> bool:
    """True if `pred` certainly evaluates to UNKNOWN whenever every column
    in `null_attrs` is NULL (NULL-propagating / strict constructs)."""
    if isinstance(pred, (Cmp, Arith)):
        return bool(pred.columns() & null_attrs)
    if isinstance(pred, And):
        # UNKNOWN & anything != TRUE stays non-TRUE, but for "certainly
        # UNKNOWN" we need both sides non-FALSE; stay conservative.
        return _strict_unknown(pred.left, null_attrs) and _strict_unknown(
            pred.right, null_attrs
        )
    if isinstance(pred, Or):
        return _strict_unknown(pred.left, null_attrs) and _strict_unknown(
            pred.right, null_attrs
        )
    if isinstance(pred, Not):
        return _strict_unknown(pred.child, null_attrs)
    return False


def is_null_rejecting(pred: Expr, null_attrs) -> bool:
    """Conservative check: `pred` can NEVER be TRUE on a row where every
    column of `null_attrs` is NULL (regardless of the other columns).

    Such a predicate rejects the NULL-padded rows produced by an outer
    join, which is exactly the precondition for R3/R4.
    """
    null_attrs = frozenset(null_attrs)
    if not null_attrs:
        return False
    if isinstance(pred, (Cmp, Arith)):
        # 3VL: NULL operand -> UNKNOWN -> rejected by WHERE.
        return bool(pred.columns() & null_attrs)
    if isinstance(pred, IsNotNull):
        # IS NOT NULL is FALSE when its null-side column is NULL.
        return bool(pred.columns() & null_attrs)
    if isinstance(pred, IsNull):
        # IS NULL is TRUE on NULL padding -> NOT null-rejecting.
        return False
    if isinstance(pred, And):
        # AND is non-TRUE if either conjunct is non-TRUE.
        return is_null_rejecting(pred.left, null_attrs) or is_null_rejecting(
            pred.right, null_attrs
        )
    if isinstance(pred, Or):
        # OR is non-TRUE only if both disjuncts are non-TRUE.
        return is_null_rejecting(pred.left, null_attrs) and is_null_rejecting(
            pred.right, null_attrs
        )
    if isinstance(pred, Not):
        # NOT p is non-TRUE iff p is non-FALSE; guaranteed when p is
        # certainly UNKNOWN (e.g. NOT (x > 3) with x NULL).
        return _strict_unknown(pred.child, null_attrs)
    return False


# ---------------------------------------------------------------------------
# Plan tree
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Scan:
    rel: Relation


@dataclass(frozen=True)
class Filter:
    pred: Expr
    child: object


@dataclass(frozen=True)
class Join:
    kind: str  # 'inner' | 'left'
    left: object
    right: object
    pred: Expr


def plan_columns(plan) -> tuple:
    if isinstance(plan, Scan):
        return plan.rel.columns
    if isinstance(plan, Filter):
        return plan_columns(plan.child)
    if isinstance(plan, Join):
        return plan_columns(plan.left) + plan_columns(plan.right)
    raise TypeError(plan)


def execute(plan) -> Relation:
    if isinstance(plan, Scan):
        return plan.rel
    if isinstance(plan, Filter):
        return select(execute(plan.child), plan.pred)
    if isinstance(plan, Join):
        left_rel = execute(plan.left)
        right_rel = execute(plan.right)
        if plan.kind == "inner":
            return inner_join(left_rel, right_rel, plan.pred)
        if plan.kind == "left":
            return left_join(left_rel, right_rel, plan.pred)
        raise ValueError(plan.kind)
    raise TypeError(plan)


def render(plan) -> str:
    if isinstance(plan, Scan):
        return plan.rel.name
    if isinstance(plan, Filter):
        return f"sigma[{_sexpr(plan.pred)}]({render(plan.child)})"
    if isinstance(plan, Join):
        op = "JOIN" if plan.kind == "inner" else "LEFT JOIN"
        return f"({render(plan.left)} {op}[{_sexpr(plan.pred)}] {render(plan.right)})"
    raise TypeError(plan)


def _sexpr(expr: Expr) -> str:
    if isinstance(expr, Cmp):
        return f"{_sexpr(expr.left)} {expr.op} {_sexpr(expr.right)}"
    if isinstance(expr, Arith):
        return f"({_sexpr(expr.left)} {expr.op} {_sexpr(expr.right)})"
    if isinstance(expr, And):
        return f"({_sexpr(expr.left)} AND {_sexpr(expr.right)})"
    if isinstance(expr, Or):
        return f"({_sexpr(expr.left)} OR {_sexpr(expr.right)})"
    if isinstance(expr, Not):
        return f"(NOT {_sexpr(expr.child)})"
    if isinstance(expr, IsNull):
        return f"{_sexpr(expr.child)} IS NULL"
    if isinstance(expr, IsNotNull):
        return f"{_sexpr(expr.child)} IS NOT NULL"
    from relalg import Col, Lit

    if isinstance(expr, Col):
        return expr.name
    if isinstance(expr, Lit):
        return "NULL" if expr.value is None else repr(expr.value)
    return repr(expr)


# ---------------------------------------------------------------------------
# Rewrite rules (precondition-checked)
# ---------------------------------------------------------------------------

def pushdown_filter(plan):
    """R1/R2: push a Filter below a Join onto the side(s) its columns
    allow. For LEFT JOIN only the preserved (left) side is a legal
    target; pushing onto the null-supplying side is refused."""
    if not (isinstance(plan, Filter) and isinstance(plan.child, Join)):
        raise PreconditionError("expected Filter over Join")
    pred, join = plan.pred, plan.child
    left_cols = set(plan_columns(join.left))
    right_cols = set(plan_columns(join.right))
    pred_cols = pred.columns()

    if pred_cols <= left_cols:
        return Join(join.kind, Filter(pred, join.left), join.right, join.pred)
    if pred_cols <= right_cols:
        if join.kind != "inner":
            raise PreconditionError(
                "cannot push a predicate onto the null-supplying side of an "
                "outer join: NULL-padded rows would change (e.g. IS NULL "
                "predicates become true only after the join)"
            )
        return Join(join.kind, join.left, Filter(pred, join.right), join.pred)
    raise PreconditionError(
        f"predicate references columns from both sides: {sorted(pred_cols)}"
    )


def outer_to_inner(plan):
    """R3: sigma_p(A LEFT JOIN B) -> sigma_p(A JOIN B), valid iff p is
    null-rejecting on B's columns."""
    if not (
        isinstance(plan, Filter)
        and isinstance(plan.child, Join)
        and plan.child.kind == "left"
    ):
        raise PreconditionError("expected Filter over LEFT JOIN")
    right_cols = set(plan_columns(plan.child.right))
    if not is_null_rejecting(plan.pred, right_cols):
        raise PreconditionError(
            "predicate is not null-rejecting on the null-supplying side; "
            "NULL-padded rows might survive the filter, so the outer join "
            "cannot be converted to an inner join"
        )
    join = plan.child
    return Filter(plan.pred, Join("inner", join.left, join.right, join.pred))


def pushdown_after_conversion(plan):
    """R3+R4 composed: sigma_p(A LEFT JOIN B) -> A JOIN sigma_p(B),
    valid iff p is null-rejecting on B and references only B's columns."""
    converted = outer_to_inner(plan)  # enforces the null-rejecting check
    inner = converted.child
    right_cols = set(plan_columns(inner.right))
    if not (plan.pred.columns() <= right_cols):
        raise PreconditionError(
            "predicate must reference only null-supplying-side columns "
            "to be pushed below the converted inner join"
        )
    return Join(
        "inner", inner.left, Filter(plan.pred, inner.right), inner.pred
    )
