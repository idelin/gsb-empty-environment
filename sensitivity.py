"""Conservative subject-level L1 sensitivity analysis for a restricted query language.

Definitions (used throughout, and restated in README.md)
--------------------------------------------------------
* Dataset: a collection of named tables; each table is a list of rows.
  Every row belongs to exactly one *subject* (the privacy unit / user).
  Row = (subject, group, value); values are non-negative reals.
* Adjacency (subject-level add/remove): D ~ D' iff D' is obtained from D by
  adding or removing ALL rows of a single subject.
* L1 sensitivity of a query q:  sens(q) = max_{D ~ D'} ||q(D) - q(D')||_1.
* Group-by uses a FIXED, public set of group keys; empty groups report 0, so
  query outputs are fixed-dimension vectors and the L1 norm is well defined.
* Contribution constraints must be DECLARED per table, otherwise the query
  is rejected as unbounded:
    - m: maximum number of rows any single subject may have in the table.
    - b: values of the aggregated column lie in [0, b].
  Consequence (stated by the task): a sum over such a table has
  sensitivity <= m * b.  Without a contribution cap the answer is UNBOUNDED.
* Composition: Compose(...) denotes SEQUENTIAL composition over the same
  dataset, so sensitivities ADD.  Because the same subject's change can move
  every part simultaneously (contributions are correlated), the sum is tight
  and must not be replaced by a per-operator maximum.

Only synthetic data is ever used.  Finite-domain enumeration below is a
sanity check / witness generator, NOT a formal privacy proof.
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass, field
from typing import Optional

Row = tuple  # (subject: str, group: str, value: float)
Dataset = dict  # table name -> list[Row]


# ---------------------------------------------------------------------------
# Query expression AST
# ---------------------------------------------------------------------------

class Expr:
    """Base class of the query expression AST."""


@dataclass(frozen=True)
class Table(Expr):
    """Base relation with declared per-subject contribution constraints."""
    name: str
    max_rows_per_subject: Optional[int]  # m; None => undeclared => unbounded
    value_bound: Optional[float]         # b; values in [0, b]; None => undeclared


@dataclass(frozen=True)
class Clip(Expr):
    """Clip the value column to [0, hi] (values are assumed >= 0)."""
    child: Expr
    hi: float


@dataclass(frozen=True)
class Join(Expr):
    """Inner join on the subject id.

    A subject's left rows pair only with the SAME subject's right rows, so
    per-subject joined rows are bounded by m_left * m_right (the bounds are
    propagated jointly, not by an operator-local maximum).  `value_side`
    selects which side's (group, value) columns the joined relation carries.
    """
    left: Expr
    right: Expr
    value_side: str = "left"  # "left" | "right"


@dataclass(frozen=True)
class Aggregate(Expr):
    """Global aggregation of the value column: a 1-dim query."""
    child: Expr
    agg: str  # "sum" | "count"


@dataclass(frozen=True)
class GroupBy(Expr):
    """Aggregation over a FIXED, public set of groups."""
    child: Expr
    groups: tuple
    agg: str  # "sum" | "count"


@dataclass(frozen=True)
class Compose(Expr):
    """Sequential composition of queries over the same dataset (bounds add)."""
    parts: tuple


# ---------------------------------------------------------------------------
# Derivation tree
# ---------------------------------------------------------------------------

@dataclass
class Derivation:
    """One node of the step-by-step conservative derivation tree."""
    rule: str
    detail: str
    children: list = field(default_factory=list)

    def render(self, indent: int = 0) -> str:
        pad = "  " * indent
        lines = [f"{pad}- [{self.rule}] {self.detail}"]
        for child in self.children:
            lines.append(child.render(indent + 1))
        return "\n".join(lines)


@dataclass
class Analysis:
    """Result of the analysis: a numeric bound, or None meaning UNBOUNDED."""
    bound: Optional[float]
    derivation: Derivation

    @property
    def bounded(self) -> bool:
        return self.bound is not None


def _fmt(x: Optional[float]) -> str:
    return "UNBOUNDED" if x is None else f"{x:g}"


# ---------------------------------------------------------------------------
# Analyzer: conservative, correlation-preserving bound propagation
# ---------------------------------------------------------------------------

def analyze(query: Expr) -> Analysis:
    bound, deriv = _query_bound(query)
    return Analysis(bound, deriv)


def _profile(node: Expr):
    """Propagate (max_rows_per_subject, value_bound) through relational nodes.

    Returns (rows_bound, value_bound, derivation); None means unbounded.
    """
    if isinstance(node, Table):
        detail = (f"declared constraints: per-subject rows <= m = {_fmt(node.max_rows_per_subject)}, "
                  f"values in [0, b], b = {_fmt(node.value_bound)}")
        if node.max_rows_per_subject is None:
            detail += "; missing row cap => contribution UNBOUNDED"
        if node.value_bound is None:
            detail += "; missing value cap => sum contribution UNBOUNDED"
        return node.max_rows_per_subject, node.value_bound, \
            Derivation(f"table {node.name}", detail)

    if isinstance(node, Clip):
        rows, val, child = _profile(node.child)
        new_val = None if val is None else min(val, node.hi)
        detail = (f"clip values to [0, {node.hi:g}]: value bound b' = min(b, {node.hi:g}) = "
                  f"{_fmt(new_val)}; row bound unchanged m = {_fmt(rows)}")
        return rows, new_val, Derivation("clip", detail, [child])

    if isinstance(node, Join):
        rows_l, val_l, deriv_l = _profile(node.left)
        rows_r, val_r, deriv_r = _profile(node.right)
        if rows_l is None or rows_r is None:
            rows = None
            detail = ("join on subject id: BOTH sides need a per-subject row cap; "
                      f"m_left = {_fmt(rows_l)}, m_right = {_fmt(rows_r)} "
                      "=> joined contribution UNBOUNDED, query rejected")
        else:
            rows = rows_l * rows_r
            detail = (f"join on subject id: a subject's rows pair only with the SAME subject's "
                      f"rows, so per-subject joined rows <= m_left * m_right = "
                      f"{rows_l:g} * {rows_r:g} = {rows:g} (joint propagation, "
                      f"not an operator-local max)")
        val = val_l if node.value_side == "left" else val_r
        detail += f"; carried value column: {node.value_side}, b = {_fmt(val)}"
        return rows, val, Derivation("join", detail, [deriv_l, deriv_r])

    raise TypeError(f"not a relational node: {node!r}")


def _query_bound(node: Expr):
    """Return (bound, derivation) for query nodes; bound None => unbounded."""
    if isinstance(node, Aggregate):
        rows, val, child = _profile(node.child)
        if node.agg == "count":
            bound = rows
            detail = (f"count: one subject adds/removes <= m rows, so sens <= m = {_fmt(rows)}"
                      if rows is not None else
                      "count: per-subject row cap m missing => UNBOUNDED, query rejected")
        elif node.agg == "sum":
            bound = None if (rows is None or val is None) else rows * val
            detail = (f"sum: one subject contributes <= m rows, each in [0, b], "
                      f"so sens <= m * b = {bounds_prod(rows, val)}"
                      if bound is not None else
                      f"sum: need both m and b; got m = {_fmt(rows)}, b = {_fmt(val)} "
                      "=> UNBOUNDED, query rejected")
        else:
            raise ValueError(f"unknown agg {node.agg!r}")
        return bound, Derivation(f"aggregate {node.agg}", detail, [child])

    if isinstance(node, GroupBy):
        rows, val, child = _profile(node.child)
        if node.agg == "count":
            bound = rows
            per = "m"
        elif node.agg == "sum":
            bound = None if (rows is None or val is None) else rows * val
            per = "m * b"
        else:
            raise ValueError(f"unknown agg {node.agg!r}")
        if bound is None:
            detail = (f"group-by {node.agg} over fixed groups {list(node.groups)}: "
                      "contribution cap missing => UNBOUNDED, query rejected")
        else:
            detail = (f"group-by {node.agg} over FIXED groups {list(node.groups)}: "
                      f"a subject's <= m rows each land in exactly one group, so the per-group "
                      f"deltas are non-negative and sum to the total contribution; "
                      f"L1 = sum_g |delta_g| <= {per} = {bound:g} "
                      f"(NOT multiplied by the number of groups, and NOT a per-group max)")
        return bound, Derivation(f"group-by {node.agg}", detail, [child])

    if isinstance(node, Compose):
        children, bounds = [], []
        for part in node.parts:
            b, d = _query_bound(part)
            bounds.append(b)
            children.append(d)
        if all(b is not None for b in bounds):
            bound = sum(bounds)
            terms = " + ".join(f"{b:g}" for b in bounds)
            detail = (f"sequential composition over the same dataset: sensitivities ADD, "
                      f"sens <= {terms} = {bound:g}. The same subject may attain every part "
                      f"simultaneously (correlated contributions), so the sum is tight and "
                      f"must not be replaced by a per-operator max")
        else:
            bound = None
            detail = ("sequential composition: at least one part is UNBOUNDED "
                      "=> composition UNBOUNDED, query rejected")
        return bound, Derivation("compose (sequential)", detail, children)

    raise TypeError(f"not a query node: {node!r}")


def bounds_prod(rows: Optional[float], val: Optional[float]) -> str:
    if rows is None or val is None:
        return "UNBOUNDED"
    return f"{rows:g} * {val:g} = {rows * val:g}"


# ---------------------------------------------------------------------------
# Evaluator on (synthetic) datasets
# ---------------------------------------------------------------------------

def _eval_rel(node: Expr, data: Dataset) -> list:
    if isinstance(node, Table):
        return list(data.get(node.name, []))
    if isinstance(node, Clip):
        return [(s, g, min(v, node.hi)) for (s, g, v) in _eval_rel(node.child, data)]
    if isinstance(node, Join):
        left_rows = _eval_rel(node.left, data)
        right_by_subject = {}
        for row in _eval_rel(node.right, data):
            right_by_subject.setdefault(row[0], []).append(row)
        out = []
        for lrow in left_rows:
            for rrow in right_by_subject.get(lrow[0], []):
                keep = lrow if node.value_side == "left" else rrow
                out.append((lrow[0], keep[1], keep[2]))
        return out
    raise TypeError(f"not a relational node: {node!r}")


def evaluate(node: Expr, data: Dataset) -> list:
    """Evaluate a query node to a fixed-dimension output vector."""
    if isinstance(node, Aggregate):
        rows = _eval_rel(node.child, data)
        if node.agg == "sum":
            return [sum(r[2] for r in rows)]
        return [float(len(rows))]
    if isinstance(node, GroupBy):
        rows = _eval_rel(node.child, data)
        out = []
        for group in node.groups:
            sel = [r for r in rows if r[1] == group]
            out.append(sum(r[2] for r in sel) if node.agg == "sum" else float(len(sel)))
        return out
    if isinstance(node, Compose):
        out = []
        for part in node.parts:
            out.extend(evaluate(part, data))
        return out
    raise TypeError(f"not a query node: {node!r}")


# ---------------------------------------------------------------------------
# Finite-domain enumeration: adjacency witnesses and upper-bound checking
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class TableDomain:
    """Small finite row universe for one table (synthetic data only)."""
    groups: tuple
    values: tuple
    max_rows: int  # per subject, consistent with the declared m


@dataclass
class Witness:
    """An adjacent pair (D, D') achieving the largest observed L1 distance."""
    max_l1: float
    subject: str
    added_rows: dict          # table -> rows of the differing subject
    output_with: list
    output_without: list


def _contributions(domain: TableDomain) -> list:
    """All multisets of size 0..max_rows over the finite row-type universe."""
    types = [(g, v) for g in domain.groups for v in domain.values]
    out = []
    for size in range(domain.max_rows + 1):
        out.extend(itertools.combinations_with_replacement(types, size))
    return out


def _build_dataset(assignment, contribs, subjects) -> Dataset:
    data: Dataset = {}
    for subject, idx in zip(subjects, assignment):
        for table, rows in contribs[idx].items():
            data.setdefault(table, []).extend((subject, g, v) for (g, v) in rows)
    return data


def enumerate_sensitivity(query: Expr, domains: dict, subjects=("s1", "s2")) -> Witness:
    """Exhaustively enumerate adjacent pairs (D, D minus one subject) on a
    small finite domain and return the pair with the largest L1 distance.

    This yields a LOWER bound on the true sensitivity (a witness), and lets
    us check that the analyzer's UPPER bound is never violated on the domain.
    """
    tables = list(domains)
    per_table = [_contributions(domains[t]) for t in tables]
    contribs = [dict(zip(tables, combo)) for combo in itertools.product(*per_table)]
    empty_idx = next(i for i, c in enumerate(contribs)
                     if all(len(rows) == 0 for rows in c.values()))

    cache: dict = {}

    def vec(assignment):
        if assignment not in cache:
            cache[assignment] = evaluate(query, _build_dataset(assignment, contribs, subjects))
        return cache[assignment]

    best = Witness(max_l1=0.0, subject=subjects[0], added_rows={},
                   output_with=[], output_without=[])
    indices = range(len(contribs))
    for assignment in itertools.product(indices, repeat=len(subjects)):
        v_with = vec(assignment)
        for pos, subject in enumerate(subjects):
            if assignment[pos] == empty_idx:
                continue
            neighbor = assignment[:pos] + (empty_idx,) + assignment[pos + 1:]
            v_without = vec(neighbor)
            l1 = sum(abs(a - b) for a, b in zip(v_with, v_without))
            if l1 > best.max_l1:
                best = Witness(
                    max_l1=l1,
                    subject=subject,
                    added_rows={t: [(subject, g, v) for (g, v) in rows]
                                for t, rows in contribs[assignment[pos]].items() if rows},
                    output_with=list(v_with),
                    output_without=list(v_without),
                )
    return best


def verify(query: Expr, domains: dict, subjects=("s1", "s2"), tol=1e-9):
    """Analyze, then enumerate: returns (analysis, witness, ok, tight).

    ok    := enumerated max L1 never exceeds the derived upper bound.
    tight := the witness attains the bound (lower bound meets upper bound).
    """
    analysis = analyze(query)
    witness = enumerate_sensitivity(query, domains, subjects)
    ok = analysis.bound is not None and witness.max_l1 <= analysis.bound + tol
    tight = analysis.bound is not None and abs(witness.max_l1 - analysis.bound) <= tol
    return analysis, witness, ok, tight
