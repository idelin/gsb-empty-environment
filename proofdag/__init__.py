"""Canonicalization, deduplication and replayable evidence for proof DAGs."""

from .canon import (
    CanonicalNode,
    CanonicalProof,
    canonicalize,
    default_hash,
    summaries_equal,
)
from .dag import CycleError, ProofDAG, UnknownNodeError
from .evidence import export_evidence, replay, verify
from .model import Fact, Rule, Step

__all__ = [
    "CanonicalNode",
    "CanonicalProof",
    "CycleError",
    "Fact",
    "ProofDAG",
    "Rule",
    "Step",
    "UnknownNodeError",
    "canonicalize",
    "default_hash",
    "export_evidence",
    "replay",
    "summaries_equal",
    "verify",
]
