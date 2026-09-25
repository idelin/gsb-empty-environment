"""causal：两时刻推导结论对比与最小因果切片。"""

from .model import Cond, Fact, FactRef, Rule, Snapshot, Step
from .engine import Derivation, derive
from .diff import (
    ADDED,
    REMOVED,
    SUPPORT_CHANGED,
    CAUSE_NEW_FACT,
    CAUSE_RULE_CHANGE,
    CAUSE_EVIDENCE_LOST,
    CAUSE_EVIDENCE_UPDATED,
    ConclusionChange,
    Report,
    RootDiff,
    apply_diffs,
    compare,
    root_diffs,
)

__all__ = [
    "Cond", "Fact", "FactRef", "Rule", "Snapshot", "Step",
    "Derivation", "derive",
    "ADDED", "REMOVED", "SUPPORT_CHANGED",
    "CAUSE_NEW_FACT", "CAUSE_RULE_CHANGE", "CAUSE_EVIDENCE_LOST", "CAUSE_EVIDENCE_UPDATED",
    "ConclusionChange", "Report", "RootDiff",
    "apply_diffs", "compare", "root_diffs",
]
