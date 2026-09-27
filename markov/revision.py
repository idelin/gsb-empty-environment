"""Incremental recomputation after a rational revision of one matrix row.

Given a delta (exact rationals) added to row r, only states that can reach
row r (in the old or the revised chain) can change their absorption
behaviour: the remaining states form a region closed under transitions whose
equations are untouched.  We therefore re-solve only the affected states,
using exact boundary values from the untouched region, and then verify the
incremental result against a full re-decomposition state by state.
"""

from dataclasses import dataclass, field
from fractions import Fraction

from .chain import Chain
from .exact import (absorption_probabilities, absorption_residuals,
                    solve_linear)


@dataclass
class RevisionReport:
    """Evidence produced by revising one row of the transition matrix."""
    row: int = 0
    delta: tuple = ()
    old_row_sum: Fraction = Fraction(0)
    delta_sum: Fraction = Fraction(0)
    new_row_sum: Fraction = Fraction(0)
    min_entry: Fraction = Fraction(0)
    max_entry: Fraction = Fraction(0)
    bounds_ok: bool = True
    affected_states: frozenset = frozenset()
    affected_closed_classes: tuple = ()
    incremental_probs: dict = field(default_factory=dict)  # class -> {state: p}
    full_probs: dict = field(default_factory=dict)         # class -> {state: p}
    state_comparison: dict = field(default_factory=dict)   # state -> (incr, full)
    max_abs_diff: Fraction = Fraction(0)
    residuals: dict = field(default_factory=dict)          # class -> {state: r}
    revised_chain: object = None


def _subchain_probs(chain, region, target):
    """Hitting probabilities of `target` inside a transition-closed region."""
    region = sorted(region)
    target = set(target) & set(region)
    if not target:
        return {s: Fraction(0) for s in region}
    remap = {s: k for k, s in enumerate(region)}
    sub = [[chain.matrix[i][j] for j in region] for i in region]
    sub_chain = Chain(sub)
    sub_target = {remap[t] for t in target}
    probs = absorption_probabilities(sub_chain, sub_target)
    return {s: probs[remap[s]] for s in region}


def revise_row(chain, row, delta):
    """Apply `delta` to `row` and incrementally recompute absorption.

    Returns a RevisionReport with row sums, probability bounds, the affected
    reachable closed classes, linear-equation residuals, and a state-by-state
    comparison against a full re-solve.  Raises ValueError if the revised row
    is not a probability distribution.
    """
    if len(delta) != chain.n:
        raise ValueError(f"delta has length {len(delta)}, expected {chain.n}")
    delta = tuple(Fraction(d) for d in delta)
    new_row = [chain.matrix[row][j] + delta[j] for j in range(chain.n)]

    report = RevisionReport(row=row, delta=delta)
    report.old_row_sum = sum(chain.matrix[row], Fraction(0))
    report.delta_sum = sum(delta, Fraction(0))
    report.new_row_sum = sum(new_row, Fraction(0))
    report.min_entry = min(new_row)
    report.max_entry = max(new_row)
    report.bounds_ok = all(Fraction(0) <= p <= Fraction(1) for p in new_row)
    if report.new_row_sum != 1 or not report.bounds_ok:
        raise ValueError(
            f"revised row {row} is not a distribution: sum={report.new_row_sum}, "
            f"bounds=[{report.min_entry}, {report.max_entry}]")

    new_matrix = [list(r) for r in chain.matrix]
    new_matrix[row] = new_row
    revised = Chain(new_matrix)
    report.revised_chain = revised

    # States whose equations can change: anything reaching `row` in either chain.
    affected = chain.reachable({row}) | revised.reachable({row})
    report.affected_states = frozenset(affected)
    unaffected = sorted(set(range(chain.n)) - affected)

    # Closed classes of the revised chain touched by the affected region.
    affected_classes = [cls for cls in revised.closed_classes()
                        if cls & affected]
    report.affected_closed_classes = tuple(
        frozenset(c) for c in affected_classes)

    for cls in affected_classes:
        # Boundary values on the untouched region come from the OLD chain
        # restricted to that region (identical dynamics there).
        boundary = _subchain_probs(chain, unaffected, cls)
        # Incremental solve on affected transient states only.
        closed_all = set().union(*revised.closed_classes())
        active = sorted(s for s in affected if s not in closed_all)
        index = {s: k for k, s in enumerate(active)}
        mat, rhs = [], []
        for i in active:
            r_ = [Fraction(0)] * len(active)
            r_[index[i]] = Fraction(1)
            b = Fraction(0)
            for j in range(revised.n):
                p = revised.matrix[i][j]
                if p == 0:
                    continue
                if j in cls:
                    b += p
                elif j in index:
                    r_[index[j]] -= p
                elif j in closed_all:
                    pass  # other closed classes contribute 0
                else:
                    b += p * boundary[j]  # untouched transient state
            mat.append(r_)
            rhs.append(b)
        sol = solve_linear(mat, rhs) if active else []
        incr = {}
        for s in range(revised.n):
            if s in cls:
                incr[s] = Fraction(1)
            elif s in closed_all:
                incr[s] = Fraction(0)
            elif s in index:
                incr[s] = sol[index[s]]
            else:
                incr[s] = boundary[s]
        report.incremental_probs[cls] = incr

        # Full re-decomposition solve on the revised chain, for comparison.
        full = absorption_probabilities(revised, cls)
        report.full_probs[cls] = full
        for s in range(revised.n):
            diff = abs(incr[s] - full[s])
            report.state_comparison[(cls, s)] = (incr[s], full[s])
            if diff > report.max_abs_diff:
                report.max_abs_diff = diff
        report.residuals[cls] = absorption_residuals(revised, cls, full)
    return report
