"""Proof generation.  The prover holds the full records; the verifier
(see verifier.py) never needs them."""

from __future__ import annotations

from .hashing import leaf_hash
from .ids import PAD_MARKER
from .proofs import MembershipProof, BatchProof, ConsistencyProof
from .tree import MerkleTree, padded_size


def _key(level: int, pos: int) -> str:
    return f"{level}:{pos}"


def decompose(size: int) -> list[tuple[int, int]]:
    """Canonical decomposition of ``size`` into aligned power-of-two
    subtrees: returns [(offset, log2_size), ...], largest first."""
    out: list[tuple[int, int]] = []
    offset = 0
    remaining = size
    while remaining > 0:
        log = remaining.bit_length() - 1
        out.append((offset, log))
        offset += 1 << log
        remaining -= 1 << log
    return out


def build_multiproof_nodes(
    tree: MerkleTree, claims: list[tuple[int, int]]
) -> dict[str, bytes]:
    """Sibling nodes needed to verify ``claims`` (each ``(level, pos)``)
    against the tree root.  Nodes shared between claims appear once;
    padding-derivable nodes are omitted entirely."""
    n = tree.size
    if n == 0:
        raise ValueError("cannot prove against an empty tree")
    levels = tree.levels()
    total_levels = len(levels) - 1
    known: set[tuple[int, int]] = set(claims)
    nodes: dict[str, bytes] = {}
    for level in range(total_levels):
        parents: set[int] = set()
        for (cl, pos) in sorted(known):
            if cl != level:
                continue
            sib = pos ^ 1
            if (level, sib) not in known:
                start = sib << level
                if start < n:  # not padding-derivable: must be supplied
                    nodes[_key(level, sib)] = levels[level][sib]
            parents.add(pos >> 1)
        for parent in parents:
            known.add((level + 1, parent))
    return nodes


def membership_proof(tree: MerkleTree, index: int) -> MembershipProof:
    if not 0 <= index < tree.size:
        raise ValueError("leaf index out of range")
    levels = tree.levels()
    total_levels = len(levels) - 1
    nodes = build_multiproof_nodes(tree, [(0, index)])
    siblings: list[str] = []
    for level in range(total_levels):
        sib = (index >> level) ^ 1
        start = sib << level
        if start >= tree.size:
            siblings.append(PAD_MARKER)  # verifier recomputes, never trusts us
        else:
            siblings.append(nodes[_key(level, sib)].hex())
    return MembershipProof(
        version=tree.version,
        tree_size=tree.size,
        leaf_index=index,
        record=tree.records[index],
        siblings=siblings,
    )


def batch_proof(tree: MerkleTree, indices: list[int]) -> BatchProof:
    ordered = sorted(set(indices))
    if not ordered:
        raise ValueError("batch proof needs at least one index")
    for i in ordered:
        if not 0 <= i < tree.size:
            raise ValueError("leaf index out of range")
    claims = [(0, i) for i in ordered]
    nodes = build_multiproof_nodes(tree, claims)
    return BatchProof(
        version=tree.version,
        tree_size=tree.size,
        indices=ordered,
        records=[tree.records[i] for i in ordered],
        nodes={k: v.hex() for k, v in nodes.items()},
    )


def consistency_proof(tree: MerkleTree, old_size: int) -> ConsistencyProof:
    """Prove the current tree (size ``tree.size``) is an append-only
    extension of the tree as it was at ``old_size``."""
    new_size = tree.size
    if not 0 <= old_size <= new_size:
        raise ValueError("old_size must satisfy 0 <= old_size <= new_size")
    subtrees: list[tuple[int, int, str]] = []
    old_nodes: dict[str, bytes] = {}
    new_nodes: dict[str, bytes] = {}
    if old_size > 0:
        old_tree = MerkleTree(tree.version)
        old_tree.records = list(tree.records[:old_size])
        parts = decompose(old_size)
        claims = [(log, offset >> log) for offset, log in parts]
        old_levels = old_tree.levels()
        subtrees = [
            (offset, log, old_levels[log][offset >> log].hex())
            for offset, log in parts
        ]
        old_nodes = build_multiproof_nodes(old_tree, claims)
        new_nodes = build_multiproof_nodes(tree, claims)
    return ConsistencyProof(
        version=tree.version,
        old_size=old_size,
        new_size=new_size,
        subtrees=subtrees,
        old_nodes={k: v.hex() for k, v in old_nodes.items()},
        new_nodes={k: v.hex() for k, v in new_nodes.items()},
    )


# Re-export for convenience of callers that only import the prover.
__all__ = [
    "membership_proof",
    "batch_proof",
    "consistency_proof",
    "build_multiproof_nodes",
    "decompose",
    "leaf_hash",
    "padded_size",
]
