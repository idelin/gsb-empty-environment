"""Rewrite rules for  sigma_p(R join S)  with explicit, checkable preconditions.

Each rule exposes:
  * `precondition` -- human-readable statement of when the rewrite is valid;
  * `check(...)`   -- executable precondition (the analyzer's verdict);
  * `apply(...)`   -- the rewritten plan, only to be used when check() holds.

Rules R1-R3 are valid equivalences under bag semantics with NULLs.
Rule X1 is a *rejected* rewrite: it is included to document that pushing a
predicate on the null-supplying (right) side of a LEFT JOIN below the join
has no general precondition, and to anchor the counterexample tests.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable

from .expr import attrs, is_null_rejecting
from .rel import Relation, inner_join, left_join, select


@dataclass(frozen=True)
class Rule:
    name: str
    precondition: str
    check: Callable[..., bool]
    apply: Callable[..., Relation]


def _references_only(pred: Any, allowed: frozenset[str]) -> bool:
    return attrs(pred) <= allowed


# ---------------------------------------------------------------------------
# R1: predicate pushdown through INNER JOIN (null-supplying side irrelevant)
#     sigma_p(R |x|_theta S)  ==  R |x|_theta sigma_p(S)
# ---------------------------------------------------------------------------
R1_PUSHDOWN_INNER = Rule(
    name="R1 pushdown-through-inner-join",
    precondition="p references only attributes of S (inner join: always valid, "
    "NULL behaviour and multiplicities unchanged)",
    check=lambda p, r_attrs, s_attrs: _references_only(p, s_attrs),
    apply=lambda R, S, theta, p, s_attrs: inner_join(R, select(S, p), theta),
)


def r1_original(R: Relation, S: Relation, theta: Any, p: Any) -> Relation:
    return select(inner_join(R, S, theta), p)


# ---------------------------------------------------------------------------
# R2: predicate pushdown through LEFT JOIN, preserved (left) side
#     sigma_p(R ⟕_theta S)  ==  sigma_p(R) ⟕_theta S
# ---------------------------------------------------------------------------
R2_PUSHDOWN_LEFTJOIN_PRESERVED = Rule(
    name="R2 pushdown-into-preserved-side",
    precondition="p references only attributes of R (the preserved side of the "
    "LEFT JOIN); filtering first cannot change padding or multiplicities",
    check=lambda p, r_attrs, s_attrs: _references_only(p, r_attrs),
    apply=lambda R, S, theta, p, s_attrs: left_join(select(R, p), S, theta, s_attrs),
)


def r2_original(R: Relation, S: Relation, theta: Any, p: Any, s_attrs) -> Relation:
    return select(left_join(R, S, theta, s_attrs), p)


# ---------------------------------------------------------------------------
# R3: LEFT JOIN collapses to INNER JOIN under a null-rejecting predicate
#     sigma_p(R ⟕_theta S)  ==  sigma_p(R |x|_theta S)   (then R1 applies)
# ---------------------------------------------------------------------------
R3_OUTER_TO_INNER = Rule(
    name="R3 outer-to-inner-join",
    precondition="p is null-rejecting on attrs(S): padded rows (all of S NULL) "
    "can never satisfy p, so dropping them loses nothing. "
    "Holds for e.g. x > 3 (NULL > 3 is UNKNOWN); fails for x IS NULL",
    check=lambda p, r_attrs, s_attrs: is_null_rejecting(p, s_attrs),
    apply=lambda R, S, theta, p, s_attrs: select(inner_join(R, S, theta), p),
)


def r3_original(R: Relation, S: Relation, theta: Any, p: Any, s_attrs) -> Relation:
    return select(left_join(R, S, theta, s_attrs), p)


# ---------------------------------------------------------------------------
# X1: REJECTED -- push predicate into the null-supplying (right) side
#     sigma_p(R ⟕_theta S)  =/=  R ⟕_theta sigma_p(S)
# ---------------------------------------------------------------------------
X1_PUSHDOWN_NULL_SUPPLYING = Rule(
    name="X1 pushdown-into-null-supplying-side (REJECTED)",
    precondition="NONE in general. Filtering S before the join changes which "
    "left rows find a match: (a) null-accepting p (x IS NULL) fabricates "
    "matches from rows the original predicate would drop; (b) even "
    "null-rejecting p (x > 3) wrongly keeps unmatched left rows as "
    "NULL-padded output. Use R3 (+R1) instead when p is null-rejecting.",
    check=lambda p, r_attrs, s_attrs: False,
    apply=lambda R, S, theta, p, s_attrs: left_join(R, select(S, p), theta, s_attrs),
)


def x1_original(R: Relation, S: Relation, theta: Any, p: Any, s_attrs) -> Relation:
    return select(left_join(R, S, theta, s_attrs), p)


ALL_RULES = (
    R1_PUSHDOWN_INNER,
    R2_PUSHDOWN_LEFTJOIN_PRESERVED,
    R3_OUTER_TO_INNER,
    X1_PUSHDOWN_NULL_SUPPLYING,
)
