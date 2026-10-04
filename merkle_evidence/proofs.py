"""Proof objects: membership, batch (shared-path) and append-consistency.

All proofs bind: scheme version, tree size, leaf position(s).  All proof
objects carry the stable ID of the rule that produced them and support
JSON-safe round-tripping so evidence is replayable from serialized form.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .ids import RULE_MEMBERSHIP, RULE_BATCH, RULE_CONSISTENCY, PAD_MARKER


def _hex(b: bytes) -> str:
    return b.hex()


def _unhex(s: str) -> bytes:
    return bytes.fromhex(s)


@dataclass
class MembershipProof:
    """Ordered sibling list, one entry per level (bottom-up).

    Each entry is either a hex hash or the literal ``PAD`` marker meaning
    "the verifier must recompute this padding node itself".  Order is
    significant: swapping two entries must invalidate the proof.
    """

    version: str
    tree_size: int
    leaf_index: int
    record: bytes
    siblings: list[str] = field(default_factory=list)
    rule_id: str = RULE_MEMBERSHIP

    def to_dict(self) -> dict:
        return {
            "rule_id": self.rule_id,
            "version": self.version,
            "tree_size": self.tree_size,
            "leaf_index": self.leaf_index,
            "record": _hex(self.record),
            "siblings": list(self.siblings),
        }

    @classmethod
    def from_dict(cls, d: dict) -> "MembershipProof":
        return cls(
            version=d["version"],
            tree_size=d["tree_size"],
            leaf_index=d["leaf_index"],
            record=_unhex(d["record"]),
            siblings=list(d["siblings"]),
            rule_id=d["rule_id"],
        )


@dataclass
class BatchProof:
    """Multi-member proof with shared paths sent exactly once.

    ``nodes`` maps "level:pos" -> hex hash for every sibling node the
    verifier cannot derive itself.  Nodes shared by several members are
    transmitted a single time (compression).  Padding-derivable nodes are
    never required from the sender.
    """

    version: str
    tree_size: int
    indices: list[int]
    records: list[bytes]  # aligned with ``indices``
    nodes: dict[str, str] = field(default_factory=dict)
    rule_id: str = RULE_BATCH

    def to_dict(self) -> dict:
        return {
            "rule_id": self.rule_id,
            "version": self.version,
            "tree_size": self.tree_size,
            "indices": list(self.indices),
            "records": [_hex(r) for r in self.records],
            "nodes": dict(self.nodes),
        }

    @classmethod
    def from_dict(cls, d: dict) -> "BatchProof":
        return cls(
            version=d["version"],
            tree_size=d["tree_size"],
            indices=list(d["indices"]),
            records=[_unhex(r) for r in d["records"]],
            nodes=dict(d["nodes"]),
            rule_id=d["rule_id"],
        )


@dataclass
class ConsistencyProof:
    """Append-only consistency between an old and a new tree size.

    The old tree's canonical subtree decomposition (maximal aligned
    power-of-two subtrees covering [0, old_size)) is proven against both
    the old root and the new root, showing the first ``old_size`` leaves
    are unchanged at identical positions.
    """

    version: str
    old_size: int
    new_size: int
    # Canonical decomposition of old_size: (offset, log2(size), hex root).
    subtrees: list[tuple[int, int, str]] = field(default_factory=list)
    old_nodes: dict[str, str] = field(default_factory=dict)
    new_nodes: dict[str, str] = field(default_factory=dict)
    rule_id: str = RULE_CONSISTENCY

    def to_dict(self) -> dict:
        return {
            "rule_id": self.rule_id,
            "version": self.version,
            "old_size": self.old_size,
            "new_size": self.new_size,
            "subtrees": [list(s) for s in self.subtrees],
            "old_nodes": dict(self.old_nodes),
            "new_nodes": dict(self.new_nodes),
        }

    @classmethod
    def from_dict(cls, d: dict) -> "ConsistencyProof":
        return cls(
            version=d["version"],
            old_size=d["old_size"],
            new_size=d["new_size"],
            subtrees=[tuple(s) for s in d["subtrees"]],
            old_nodes=dict(d["old_nodes"]),
            new_nodes=dict(d["new_nodes"]),
            rule_id=d["rule_id"],
        )


__all__ = [
    "MembershipProof",
    "BatchProof",
    "ConsistencyProof",
    "PAD_MARKER",
]
