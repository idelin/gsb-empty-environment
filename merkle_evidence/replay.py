"""Deterministic replay of an evidence log.

Replaying recomputes every record digest from the canonical encoding,
rebuilds the Merkle tree, and compares the resulting root against the
published (trusted) root.  Any tampering with facts, rules, steps, IDs,
versions, ordering or the log version is detected.
"""

from __future__ import annotations

from dataclasses import dataclass

from .model import EvidenceLog
from .tree import MerkleTree


@dataclass(frozen=True)
class ReplayReport:
    log_id: str
    log_version: str
    size: int
    root: bytes
    record_digests: tuple[bytes, ...]
    expected_root: bytes | None
    ok: bool


def replay_log(log: EvidenceLog, expected_root: bytes | None = None
               ) -> ReplayReport:
    """Recompute everything from the log itself; optionally check a root."""
    for rec in log.records:
        rec.validate()
    digests = log.digests()
    tree = MerkleTree(digests, log.log_version)
    root = tree.root()
    ok = expected_root is None or root == expected_root
    return ReplayReport(
        log_id=log.log_id,
        log_version=log.log_version,
        size=tree.size,
        root=root,
        record_digests=tuple(digests),
        expected_root=expected_root,
        ok=ok,
    )
