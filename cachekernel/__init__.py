"""Cache dependency analysis kernel over virtual filesystem access traces."""

from .vfs import VFS
from .evidence import build_evidence, evidence_to_json, evidence_from_json
from .index import ReverseIndex
from .query import Change, apply_change, verdict_for, VALID, INVALID, INSUFFICIENT

__all__ = [
    "VFS",
    "build_evidence",
    "evidence_to_json",
    "evidence_from_json",
    "ReverseIndex",
    "Change",
    "apply_change",
    "verdict_for",
    "VALID",
    "INVALID",
    "INSUFFICIENT",
]
