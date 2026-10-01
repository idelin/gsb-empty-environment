"""有限状态活性检查：请求后最终响应（G(req -> F resp)）。"""
from .model import Model, Transition
from .checker import (
    CheckResult,
    Counterexample,
    FairnessEvidence,
    FairnessSet,
    Verdict,
    check,
)
from .replay import ReplayReport, replay

__all__ = [
    "Model",
    "Transition",
    "FairnessSet",
    "FairnessEvidence",
    "Counterexample",
    "CheckResult",
    "Verdict",
    "check",
    "replay",
    "ReplayReport",
]
