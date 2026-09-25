"""End-to-end pipeline: coverage + minimal subset, with a replayable log."""

from __future__ import annotations

from dataclasses import dataclass

from .model import EvidenceItem, Rule
from .replay import StepRecord, stable_digest
from .subset import SubsetResult, minimal_subset
from .sweep import Coverage, compute_coverage

PIPELINE_VERSION = 1


@dataclass(frozen=True)
class Report:
    coverage: Coverage
    subset: SubsetResult
    steps: tuple[StepRecord, ...]


def _facts_digest(rule: Rule, items: list[EvidenceItem]) -> str:
    return stable_digest(
        {"rule": rule, "items": sorted(items, key=lambda it: it.id)}
    )


def run_pipeline(
    rule: Rule, items: list[EvidenceItem], budget: int = 4096
) -> Report:
    facts_in = _facts_digest(rule, items)

    coverage = compute_coverage(rule, items)
    step_coverage = StepRecord(
        step_id="step-coverage",
        version=PIPELINE_VERSION,
        kind="coverage",
        input_digest=facts_in,
        output_digest=stable_digest(coverage),
    )

    subset = minimal_subset(rule, items, budget=budget)
    step_subset = StepRecord(
        step_id="step-subset",
        version=PIPELINE_VERSION,
        kind="subset",
        input_digest=facts_in,
        output_digest=stable_digest(subset),
    )

    return Report(
        coverage=coverage, subset=subset, steps=(step_coverage, step_subset)
    )


def replay(
    report: Report, rule: Rule, items: list[EvidenceItem], budget: int = 4096
) -> bool:
    """Recompute from the same facts and verify every step digest matches."""
    fresh = run_pipeline(rule, items, budget=budget)
    return fresh.steps == report.steps
