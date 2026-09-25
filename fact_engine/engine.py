"""Resolution pipeline.

Four deterministic steps, each bound to a versioned rule:
  1. version supersede   (rule-version-supersede)
  2. validity window     (rule-validity-window)
  3. prefer high level   (rule-prefer-high-level)
  4. same-level conflict (rule-same-level-conflict-undecided)

Input order never affects the outcome: facts are re-sorted by stable
keys before every step.
"""

from __future__ import annotations

from datetime import datetime
from typing import Iterable, Sequence

from .model import (
    DEFAULT_RULES,
    Decision,
    Fact,
    Rule,
    Step,
    SuppressedEvidence,
    SuppressionReason,
    TruthValue,
)


def _rule(rules: Sequence[Rule], rule_id: str) -> Rule:
    for rule in rules:
        if rule.rule_id == rule_id:
            return rule
    raise KeyError(f"missing rule: {rule_id}")


def _by_ref(fact: Fact) -> str:
    return fact.ref()


def resolve(
    claim: str,
    facts: Iterable[Fact],
    at: datetime,
    rules: Sequence[Rule] = DEFAULT_RULES,
) -> Decision:
    steps: list[Step] = []
    suppressed: list[SuppressedEvidence] = []

    relevant = sorted(
        (f for f in facts if f.claim == claim),
        key=lambda f: (f.fact_id, f.version),
    )

    # Step 1: keep only the highest version of each fact id.
    rule = _rule(rules, "rule-version-supersede")
    latest: dict[str, Fact] = {}
    for fact in relevant:
        current = latest.get(fact.fact_id)
        if current is None or fact.version > current.version:
            latest[fact.fact_id] = fact
    current_facts: list[Fact] = []
    for fact in relevant:
        winner = latest[fact.fact_id]
        if fact is winner:
            current_facts.append(fact)
        else:
            suppressed.append(
                SuppressedEvidence(
                    fact.ref(),
                    SuppressionReason.SUPERSEDED,
                    f"superseded by {winner.ref()}",
                )
            )
    current_facts.sort(key=_by_ref)
    steps.append(
        Step(
            step_id="step-01-version-supersede",
            version=1,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            summary=f"{len(relevant)} fact(s) in, {len(current_facts)} current version(s) kept",
            kept=tuple(f.ref() for f in current_facts),
            dropped=tuple(
                s.fact_ref for s in suppressed if s.reason is SuppressionReason.SUPERSEDED
            ),
        )
    )

    # Step 2: validity window at the query moment.
    rule = _rule(rules, "rule-validity-window")
    active: list[Fact] = []
    for fact in current_facts:
        if fact.effective_at(at):
            active.append(fact)
        elif at < fact.valid_from:
            suppressed.append(
                SuppressedEvidence(
                    fact.ref(),
                    SuppressionReason.NOT_YET_EFFECTIVE,
                    f"effective from {fact.valid_from.isoformat()}, queried at {at.isoformat()}",
                )
            )
        else:
            suppressed.append(
                SuppressedEvidence(
                    fact.ref(),
                    SuppressionReason.EXPIRED,
                    f"expired at {fact.valid_to.isoformat()}, queried at {at.isoformat()}",
                )
            )
    steps.append(
        Step(
            step_id="step-02-validity-window",
            version=1,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            summary=f"{len(active)} fact(s) effective at {at.isoformat()}",
            kept=tuple(f.ref() for f in active),
            dropped=tuple(
                s.fact_ref
                for s in suppressed
                if s.reason in (SuppressionReason.EXPIRED, SuppressionReason.NOT_YET_EFFECTIVE)
            ),
        )
    )

    # Step 3: only the highest source level may decide.
    rule = _rule(rules, "rule-prefer-high-level")
    if active:
        top_level = max(f.source_level for f in active)
    else:
        top_level = None
    top_facts: list[Fact] = []
    for fact in active:
        if fact.source_level == top_level:
            top_facts.append(fact)
        else:
            suppressed.append(
                SuppressedEvidence(
                    fact.ref(),
                    SuppressionReason.OUTRANKED,
                    f"source level {fact.source_level} outranked by level {top_level}",
                )
            )
    steps.append(
        Step(
            step_id="step-03-prefer-high-level",
            version=1,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            summary=(
                f"top source level {top_level}, {len(top_facts)} fact(s) remain"
                if top_level is not None
                else "no effective facts"
            ),
            kept=tuple(f.ref() for f in top_facts),
            dropped=tuple(
                s.fact_ref for s in suppressed if s.reason is SuppressionReason.OUTRANKED
            ),
        )
    )

    # Step 4: same-level conflict stays undecided.
    rule = _rule(rules, "rule-same-level-conflict-undecided")
    values = {f.value for f in top_facts}
    winners: list[str] = []
    contesting: list[str] = []
    if not top_facts:
        value = TruthValue.UNDECIDED
        summary = "no effective evidence; undecided"
    elif len(values) == 1:
        agreed = values.pop()
        value = TruthValue.TRUE if agreed else TruthValue.FALSE
        winners = [f.ref() for f in top_facts]
        summary = f"top-level facts agree on {value.value}"
    else:
        value = TruthValue.UNDECIDED
        contesting = [f.ref() for f in top_facts]
        summary = "same-level sources assert both true and false; undecided"
    steps.append(
        Step(
            step_id="step-04-same-level-conflict",
            version=1,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            summary=summary,
            kept=tuple(sorted(winners or contesting)),
            dropped=(),
        )
    )

    return Decision(
        claim=claim,
        at=at,
        value=value,
        winners=sorted(winners),
        contesting=sorted(contesting),
        suppressed=sorted(suppressed, key=lambda s: (s.fact_ref, s.reason.value)),
        steps=steps,
    )
