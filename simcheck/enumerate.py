"""Independent exhaustive verification for small state spaces.

By Knaster-Tarski, the greatest simulation is the union of all post-fixed
relations (relations R with R subset of F(R)).  This module enumerates *every*
relation over ``Q_impl x Q_spec`` and keeps the post-fixed ones, giving an
oracle that is independent of the iterative fixpoint algorithm: the iterative
result must equal the union computed here, and "pass" must agree with
membership of the initial pair.

Only feasible for small models; a budget on the number of enumerated
relations keeps the answer UNDECIDED instead of silently incomplete.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .lts import LTS
from .relations import ATTACK_FUNCTIONS, divergent_states

OK = "ok"
UNDECIDED = "undecided"

DEFAULT_MAX_RELATIONS = 1 << 20


@dataclass
class EnumerationResult:
    status: str
    mode: str
    divergence_sensitive: bool
    universe_size: int
    relations_checked: int = 0
    postfixed_count: int = 0
    greatest: set[tuple[str, str]] = field(default_factory=set)
    initial_simulated: bool | None = None
    reason: str = ""


def enumerate_greatest_simulation(
    impl: LTS,
    spec: LTS,
    mode: str = "weak",
    divergence_sensitive: bool = False,
    max_relations: int = DEFAULT_MAX_RELATIONS,
) -> EnumerationResult:
    if mode not in ATTACK_FUNCTIONS:
        raise ValueError(f"unknown mode {mode!r}")
    compute_attacks = ATTACK_FUNCTIONS[mode]
    impl_divergent = divergent_states(impl) if divergence_sensitive else None
    spec_divergent = divergent_states(spec) if divergence_sensitive else None

    universe = sorted((t, s) for t in impl.states() for s in spec.states())
    size = len(universe)
    total = 1 << size
    initial_pair = (impl.initial, spec.initial)
    if total > max_relations:
        return EnumerationResult(
            UNDECIDED,
            mode,
            divergence_sensitive,
            size,
            reason=(
                f"enumeration budget {max_relations} exceeded: {size} state pairs "
                f"would require {total} candidate relations"
            ),
        )

    greatest: set[tuple[str, str]] = set()
    postfixed_count = 0
    checked = 0
    for mask in range(total):
        checked += 1
        relation = {universe[i] for i in range(size) if (mask >> i) & 1}
        is_postfixed = True
        for impl_state, spec_state in sorted(relation):
            if compute_attacks(
                impl,
                spec,
                relation,
                impl_state,
                spec_state,
                impl_divergent,
                spec_divergent,
            ):
                is_postfixed = False
                break
        if is_postfixed:
            postfixed_count += 1
            greatest |= relation

    return EnumerationResult(
        OK,
        mode,
        divergence_sensitive,
        size,
        relations_checked=checked,
        postfixed_count=postfixed_count,
        greatest=greatest,
        initial_simulated=initial_pair in greatest,
        reason="exhaustive enumeration complete",
    )
