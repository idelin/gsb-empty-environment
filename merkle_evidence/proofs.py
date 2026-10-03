"""Proof objects and independent verifiers.

The verifier never accesses the full record list.  It is given:
    * the proof object (which binds log version, tree size, leaf
      position(s)),
    * the digest(s) of the record(s) under inspection,
    * a trusted Checkpoint (log_version, tree_size, root), e.g. signed.

The claimed size/version inside a proof is NEVER trusted: it must equal
the checkpoint's size/version before any hash is recomputed.  This
matters because the promotion padding rule (RULE-PAD-PROMOTE v1) makes
the root of an n-leaf tree reproducible under a different claimed size
with a crafted path length -- only the checkpoint binds the size.

All tree structure is recomputed by the verifier from the claimed size
alone (RULE-PAD-PROMOTE v1 / RULE-SPLIT v1); any prover-supplied hash
that does not fit the derived shape -- including extra "padding" hashes
or wrong counts -- makes verification fail.
"""

from __future__ import annotations

from dataclasses import dataclass

from .hashing import EMPTY_ROOT, leaf_hash, node_hash
from .tree import split_point


class _Malformed(Exception):
    """Internal: proof structure does not match the derived tree shape."""


@dataclass(frozen=True)
class Checkpoint:
    """Trusted commitment to a log state: (version, size, root)."""

    log_version: str
    tree_size: int
    root: bytes


@dataclass(frozen=True)
class MembershipProof:
    log_version: str
    tree_size: int
    leaf_index: int
    record_digest: bytes
    path: tuple[bytes, ...]  # sibling hashes, leaf-to-root


@dataclass(frozen=True)
class MultiProof:
    """Compressed batch proof: shared path hashes appear exactly once."""

    log_version: str
    tree_size: int
    leaf_indices: tuple[int, ...]        # sorted, unique
    record_digests: tuple[bytes, ...]    # aligned with leaf_indices
    hashes: tuple[bytes, ...]            # verifier-consumption order


@dataclass(frozen=True)
class ConsistencyProof:
    log_version: str
    old_size: int
    new_size: int
    hashes: tuple[bytes, ...]


def verify_membership(proof: MembershipProof, checkpoint: Checkpoint) -> bool:
    """Verify a single-leaf membership proof against a trusted checkpoint."""
    if proof.log_version != checkpoint.log_version:
        return False
    if proof.tree_size != checkpoint.tree_size or proof.tree_size < 1:
        return False
    if not 0 <= proof.leaf_index < proof.tree_size:
        return False
    try:
        leaf = leaf_hash(proof.leaf_index, proof.log_version,
                         proof.record_digest)
    except (ValueError, TypeError):
        return False
    pos = 0

    def rec(start: int, length: int, index: int) -> bytes:
        nonlocal pos
        if length == 1:
            return leaf
        k = split_point(length)
        if index < start + k:
            h = rec(start, k, index)
            if pos >= len(proof.path):
                raise _Malformed
            sib = proof.path[pos]
            pos += 1
            return node_hash(h, sib)
        h = rec(start + k, length - k, index)
        if pos >= len(proof.path):
            raise _Malformed
        sib = proof.path[pos]
        pos += 1
        return node_hash(sib, h)

    try:
        root = rec(0, proof.tree_size, proof.leaf_index)
    except (_Malformed, ValueError, TypeError):
        return False
    return pos == len(proof.path) and root == checkpoint.root


def verify_multi(proof: MultiProof, checkpoint: Checkpoint) -> bool:
    """Verify a compressed shared-path proof for several members."""
    if proof.log_version != checkpoint.log_version:
        return False
    if proof.tree_size != checkpoint.tree_size:
        return False
    n = proof.tree_size
    idxs = list(proof.leaf_indices)
    if n < 1 or not idxs:
        return False
    if len(idxs) != len(proof.record_digests):
        return False
    if idxs != sorted(idxs) or len(set(idxs)) != len(idxs):
        return False
    if idxs[0] < 0 or idxs[-1] >= n:
        return False
    try:
        leaves = {
            i: leaf_hash(i, proof.log_version, d)
            for i, d in zip(idxs, proof.record_digests)
        }
    except (ValueError, TypeError):
        return False
    pos = 0

    def rec(start: int, length: int, sub: list[int]) -> bytes:
        nonlocal pos
        if not sub:
            if pos >= len(proof.hashes):
                raise _Malformed
            h = proof.hashes[pos]
            pos += 1
            return h
        if length == 1:
            if sub != [start]:
                raise _Malformed
            return leaves[start]
        k = split_point(length)
        left = [i for i in sub if i < start + k]
        right = [i for i in sub if i >= start + k]
        lh = rec(start, k, left)
        rh = rec(start + k, length - k, right)
        return node_hash(lh, rh)

    try:
        root = rec(0, n, idxs)
    except (_Malformed, ValueError, TypeError, KeyError):
        return False
    return pos == len(proof.hashes) and root == checkpoint.root


def verify_consistency(proof: ConsistencyProof, old_checkpoint: Checkpoint,
                       new_checkpoint: Checkpoint) -> bool:
    """Verify that the tree of `old_size` (rooted at `old_root`) is an
    append-only prefix of the tree of `new_size` (rooted at `new_root`)."""
    if not (proof.log_version == old_checkpoint.log_version
            == new_checkpoint.log_version):
        return False
    if proof.old_size != old_checkpoint.tree_size:
        return False
    if proof.new_size != new_checkpoint.tree_size:
        return False
    old_root, new_root = old_checkpoint.root, new_checkpoint.root
    m, n = proof.old_size, proof.new_size
    if m < 0 or n < 0 or m > n:
        return False
    if m == 0:
        return not proof.hashes and old_root == EMPTY_ROOT
    if m == n:
        return not proof.hashes and old_root == new_root
    pos = 0

    def rec(mm: int, nn: int, known: bytes | None) -> tuple[bytes, bytes]:
        nonlocal pos
        if mm == nn:
            if known is not None:
                return known, known
            if pos >= len(proof.hashes):
                raise _Malformed
            h = proof.hashes[pos]
            pos += 1
            return h, h
        k = split_point(nn)
        if mm <= k:
            h1_left, h2_left = rec(mm, k, known)
            if pos >= len(proof.hashes):
                raise _Malformed
            right = proof.hashes[pos]
            pos += 1
            return h1_left, node_hash(h2_left, right)
        h1_right, h2_right = rec(mm - k, nn - k, None)
        if pos >= len(proof.hashes):
            raise _Malformed
        left = proof.hashes[pos]
        pos += 1
        return node_hash(left, h1_right), node_hash(left, h2_right)

    try:
        h_old, h_new = rec(m, n, old_root)
    except (_Malformed, ValueError, TypeError):
        return False
    return (pos == len(proof.hashes)
            and h_old == old_root
            and h_new == new_root)
