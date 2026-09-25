"""factver：版本化事实 + 字段级依赖追踪的可重放推导引擎。"""
from .model import (
    Correction,
    Dependency,
    FactVersion,
    FieldValue,
    StepRecord,
    diff_fields,
)
from .store import (
    BatchCorrectionError,
    FactStore,
    UnknownFactError,
    VersionConflictError,
)
from .rules import FactReader, Rule
from .engine import Engine
from .replay import ReplayReport, replay_against_store, replay_from_evidence

__all__ = [
    "Correction",
    "Dependency",
    "FactVersion",
    "FieldValue",
    "StepRecord",
    "diff_fields",
    "BatchCorrectionError",
    "FactStore",
    "UnknownFactError",
    "VersionConflictError",
    "FactReader",
    "Rule",
    "Engine",
    "ReplayReport",
    "replay_against_store",
    "replay_from_evidence",
]
