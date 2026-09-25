"""Deterministic fact-resolution engine.

Facts carry a source reliability level and a validity interval.
Rules prefer higher-level sources; same-level contradictions stay
UNDECIDED. Suppressed evidence is reported with reasons. Every fact,
rule and resolution step carries a stable id and version, and every
decision can be replayed from its evidence bundle.
"""

from .model import (
    ENGINE_ID,
    ENGINE_VERSION,
    DEFAULT_RULES,
    Decision,
    Fact,
    Rule,
    Step,
    SuppressedEvidence,
    SuppressionReason,
    TruthValue,
)
from .engine import resolve
from .replay import build_evidence, replay, verify

__all__ = [
    "ENGINE_ID",
    "ENGINE_VERSION",
    "DEFAULT_RULES",
    "Decision",
    "Fact",
    "Rule",
    "Step",
    "SuppressedEvidence",
    "SuppressionReason",
    "TruthValue",
    "resolve",
    "build_evidence",
    "replay",
    "verify",
]
