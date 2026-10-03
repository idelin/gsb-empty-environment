"""Merkle-tree anchored ordered evidence log (Python 3.11, stdlib only).

Public API:
    model    -- facts, rules, steps, records, logs (stable IDs + versions)
    tree     -- MerkleTree with deterministic missing-leaf padding rule
    proofs   -- membership / multi (shared-path, compressed) / consistency
    replay   -- deterministic replay of an evidence log
"""

from .hashing import RULESET_ID, RULESET_VERSION
from .model import EvidenceLog, EvidenceRecord, Fact, Rule, Step
from .tree import MerkleTree
from .proofs import (
    Checkpoint,
    ConsistencyProof,
    MembershipProof,
    MultiProof,
    verify_consistency,
    verify_membership,
    verify_multi,
)
from .replay import ReplayReport, replay_log

__all__ = [
    "RULESET_ID",
    "RULESET_VERSION",
    "EvidenceLog",
    "EvidenceRecord",
    "Fact",
    "Rule",
    "Step",
    "MerkleTree",
    "Checkpoint",
    "MembershipProof",
    "MultiProof",
    "ConsistencyProof",
    "verify_membership",
    "verify_multi",
    "verify_consistency",
    "ReplayReport",
    "replay_log",
]
