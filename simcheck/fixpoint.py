"""Greatest-fixpoint computation of the largest simulation relation.

The largest weak (resp. branching) simulation relation is the greatest fixpoint

    R  =  F(R),   F(R) = { (t, s) | every move of s is matched by t through R }.

Starting from the full relation ``Q_impl x Q_spec`` (an over-approximation),
pairs that fail the check are deleted round by round until no pair changes.
The round in which a pair is deleted is its *rank*: every winning response
attacked at round ``r`` lands on a pair deleted in some earlier round
(``rank < r``), which is exactly what makes the counterexample strategy graph
well-founded.

Search may be bounded by a Budget.  When the budget is exhausted before the
fixpoint is reached (and the initial pair has not been proven losing by a
completed/sound round) the answer is UNDECIDED, never pass.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .lts import LTS
from .relations import ATTACK_FUNCTIONS, divergent_states

PASS = "pass"
FAIL = "fail"
UNDECIDED = "undecided"
STATUSES = (PASS, FAIL, UNDECIDED)


@dataclass
class Budget:
    """Optional search bounds; None means unbounded."""

    max_rounds: int | None = None
    max_pair_checks: int | None = None

    def __post_init__(self) -> None:
        for name in ("max_rounds", "max_pair_checks"):
            value = getattr(self, name)
            if value is not None and (not isinstance(value, int) or value < 0):
                raise ValueError(f"{name} must be a non-negative int or None")


@dataclass
class FixpointResult:
    status: str
    mode: str
    divergence_sensitive: bool
    relation: set[tuple[str, str]] = field(default_factory=set)
    rank: dict[tuple[str, str], int] = field(default_factory=dict)
    rounds: int = 0
    pair_checks: int = 0
    reason: str = ""

    @property
    def decided(self) -> bool:
        return self.status in (PASS, FAIL)


def greatest_simulation(
    impl: LTS,
    spec: LTS,
    mode: str = "weak",
    divergence_sensitive: bool = False,
    budget: Budget | None = None,
) -> FixpointResult:
    if mode not in ATTACK_FUNCTIONS:
        raise ValueError(f"unknown mode {mode!r}; expected 'weak' or 'branching'")
    compute_attacks = ATTACK_FUNCTIONS[mode]
    impl_divergent = divergent_states(impl) if divergence_sensitive else None
    spec_divergent = divergent_states(spec) if divergence_sensitive else None

    current = {(t, s) for t in impl.states() for s in spec.states()}
    rank: dict[tuple[str, str], int] = {}
    rounds = 0
    pair_checks = 0
    initial_pair = (impl.initial, spec.initial)

    def finish(status: str, reason: str) -> FixpointResult:
        return FixpointResult(
            status,
            mode,
            divergence_sensitive,
            relation=set(current),
            rank=dict(rank),
            rounds=rounds,
            pair_checks=pair_checks,
            reason=reason,
        )

    while True:
        if budget is not None and budget.max_rounds is not None and rounds >= budget.max_rounds:
            if initial_pair not in current:
                return finish(
                    FAIL,
                    f"initial pair proven losing before round budget {budget.max_rounds} was reached",
                )
            return finish(
                UNDECIDED,
                f"round budget {budget.max_rounds} exhausted after {rounds} "
                f"round(s); greatest fixpoint not reached",
            )

        bad: set[tuple[str, str]] = set()
        check_exhausted = False
        # Sorted iteration makes ranks deterministic for a given model file.
        for pair in sorted(current):
            if (
                budget is not None
                and budget.max_pair_checks is not None
                and pair_checks >= budget.max_pair_checks
            ):
                check_exhausted = True
                break
            pair_checks += 1
            impl_state, spec_state = pair
            attacks = compute_attacks(
                impl,
                spec,
                current,
                impl_state,
                spec_state,
                impl_divergent,
                spec_divergent,
            )
            if attacks:
                bad.add(pair)

        # Deletions of pairs bad w.r.t. the current over-approximation are
        # sound even when the round was cut short: the greatest fixpoint is a
        # subset of the current relation and contains no such pair.
        for pair in bad:
            rank[pair] = rounds
            current.discard(pair)
        rounds += 1

        if check_exhausted:
            if initial_pair not in current:
                return finish(
                    FAIL,
                    f"initial pair removed before pair-check budget was exhausted "
                    f"after {pair_checks} pair check(s)",
                )
            return finish(
                UNDECIDED,
                f"pair-check budget {budget.max_pair_checks} exhausted in round "
                f"{rounds}; greatest fixpoint not reached",
            )

        if not bad:
            break

    if initial_pair in current:
        return finish(PASS, f"initial pair {initial_pair} belongs to the greatest {mode} simulation")
    return finish(FAIL, f"initial pair {initial_pair} does not belong to any {mode} simulation")
