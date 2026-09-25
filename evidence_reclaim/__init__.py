"""证据回收引擎：保留策略 × 证据引用版本的最小不可回收闭包。"""

from .engine import (
    CommitRejection,
    DeletionCertificate,
    LivenessProof,
    ReclaimEngine,
    ReclaimPlan,
    replay_certificate,
)
from .interleave import enumerate_interleavings
from .protection import ChainStep, ProtectionResult, compute_closure
from .rules import RULES
from .scenario import build_sample_engine

__all__ = [
    "ChainStep",
    "CommitRejection",
    "DeletionCertificate",
    "LivenessProof",
    "ProtectionResult",
    "ReclaimEngine",
    "ReclaimPlan",
    "RULES",
    "build_sample_engine",
    "compute_closure",
    "enumerate_interleavings",
    "replay_certificate",
]
