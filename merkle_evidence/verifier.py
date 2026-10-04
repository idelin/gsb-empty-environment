"""Independent verifier.

The verifier knows only: the scheme version it expects, the claimed tree
size(s), the claimed leaf position(s) and record(s), the proof, and the
expected root(s).  It never sees the full record set, and it never trusts
the sender for padding material or internal bookkeeping: every padding
node is recomputed locally from (version, level, position) and every
sender-supplied node is consumed exactly once — leftover or missing
nodes both fail.
"""

from __future__ import annotations

from .hashing import (leaf_hash, node_hash, pad_subtree_root, empty_root, root_wrap)
from .ids import (
    RULE_MEMBERSHIP,
    RULE_BATCH,
    RULE_CONSISTENCY,
    PAD_MARKER,
)
from .proofs import MembershipProof, BatchProof, ConsistencyProof
from .tree import padded_size


class ProofError(Exception):
    """Raised whenever a proof fails verification."""


def _parse_key(key: str) -> tuple[int, int]:
    level, pos = key.split(":")
    return int(level), int(pos)


def verify_multiproof(
    version: str,
    tree_size: int,
    claims: list[tuple[int, int, bytes]],
    nodes: dict[tuple[int, int], bytes],
    expected_root: bytes,
) -> None:
    """Verify that ``claims`` (level, pos, hash) are committed under
    ``expected_root`` for a tree of ``tree_size`` real leaves.

    Raises ProofError on any inconsistency.
    """
    if tree_size < 1:
        raise ProofError("multiproof requires a non-empty tree")
    p = padded_size(tree_size)
    total_levels = p.bit_length() - 1

    known: dict[tuple[int, int], bytes] = {}
    for level, pos, digest in claims:
        if not (0 <= level <= total_levels and 0 <= pos < (p >> level)):
            raise ProofError(f"claim out of range: level={level} pos={pos}")
        key = (level, pos)
        if key in known and known[key] != digest:
            raise ProofError("conflicting duplicate claim")
        known[key] = digest

    remaining = dict(nodes)
    for level in range(total_levels):
        positions = sorted(pos for (cl, pos) in known if cl == level)
        for pos in positions:
            parent = pos >> 1

            def resolve(child: int) -> bytes:
                ckey = (level, child)
                if ckey in known:
                    return known[ckey]
                start = child << level
                if start >= tree_size:
                    # Fully-padding subtree: recompute locally; if the
                    # sender also supplied it, it must match exactly.
                    expected = pad_subtree_root(version, level, start)
                    supplied = remaining.pop(ckey, None)
                    if supplied is not None and supplied != expected:
                        raise ProofError("sender padding node mismatch")
                    return expected
                supplied = remaining.pop(ckey, None)
                if supplied is None:
                    raise ProofError(f"missing node at level={level} pos={child}")
                return supplied

            left = resolve(2 * parent)
            right = resolve(2 * parent + 1)
            digest = node_hash(version, left, right)
            pkey = (level + 1, parent)
            if pkey in known and known[pkey] != digest:
                raise ProofError("computed node conflicts with claimed node")
            known[pkey] = digest

    top = known.get((total_levels, 0))
    if top is None or root_wrap(version, tree_size, top) != expected_root:
        raise ProofError("root mismatch")
    if remaining:
        raise ProofError(f"unused proof nodes: {sorted(remaining)}")


def _check_version(proof_version: str, expected_version: str) -> None:
    if proof_version != expected_version:
        raise ProofError(
            f"version mismatch: proof={proof_version!r} expected={expected_version!r}"
        )


def verify_membership(
    proof: MembershipProof,
    expected_root: bytes,
    expected_version: str,
) -> None:
    _check_version(proof.version, expected_version)
    if proof.rule_id != RULE_MEMBERSHIP:
        raise ProofError("unexpected rule id")
    if not 0 <= proof.leaf_index < proof.tree_size:
        raise ProofError("leaf index out of range")
    p = padded_size(proof.tree_size)
    total_levels = p.bit_length() - 1
    if len(proof.siblings) != total_levels:
        raise ProofError("wrong number of sibling entries")
    nodes: dict[tuple[int, int], bytes] = {}
    for level, entry in enumerate(proof.siblings):
        if entry == PAD_MARKER:
            continue  # verifier recomputes padding locally
        sib = (proof.leaf_index >> level) ^ 1
        nodes[(level, sib)] = bytes.fromhex(entry)
    claim = (0, proof.leaf_index,
             leaf_hash(proof.version, proof.leaf_index, proof.record))
    verify_multiproof(proof.version, proof.tree_size, [claim], nodes,
                      expected_root)


def verify_batch(
    proof: BatchProof,
    expected_root: bytes,
    expected_version: str,
) -> None:
    _check_version(proof.version, expected_version)
    if proof.rule_id != RULE_BATCH:
        raise ProofError("unexpected rule id")
    if len(proof.indices) != len(proof.records) or not proof.indices:
        raise ProofError("indices/records mismatch")
    if proof.indices != sorted(set(proof.indices)):
        raise ProofError("indices must be sorted and unique")
    claims = []
    for index, record in zip(proof.indices, proof.records):
        if not 0 <= index < proof.tree_size:
            raise ProofError("leaf index out of range")
        claims.append((0, index, leaf_hash(proof.version, index, record)))
    nodes = {_parse_key(k): bytes.fromhex(v) for k, v in proof.nodes.items()}
    verify_multiproof(proof.version, proof.tree_size, claims, nodes,
                      expected_root)


def _check_decomposition(old_size: int,
                         subtrees: list[tuple[int, int, str]]) -> None:
    offset = 0
    prev_log = None
    for off, log, _hex_root in subtrees:
        if off != offset or log < 0 or (offset & ((1 << log) - 1)) != 0:
            raise ProofError("non-canonical subtree decomposition")
        if prev_log is not None and log >= prev_log:
            raise ProofError("decomposition must be strictly descending")
        offset += 1 << log
        prev_log = log
    if offset != old_size:
        raise ProofError("decomposition does not cover old_size")


def verify_consistency(
    proof: ConsistencyProof,
    old_root: bytes,
    new_root: bytes,
    expected_version: str,
) -> None:
    _check_version(proof.version, expected_version)
    if proof.rule_id != RULE_CONSISTENCY:
        raise ProofError("unexpected rule id")
    if not 0 <= proof.old_size <= proof.new_size:
        raise ProofError("invalid size range")
    if proof.old_size == 0:
        if proof.subtrees or proof.old_nodes or proof.new_nodes:
            raise ProofError("empty old tree must carry an empty proof")
        if old_root != empty_root(proof.version):
            raise ProofError("old root is not the empty root")
        return
    _check_decomposition(proof.old_size, proof.subtrees)
    claims = [(log, offset >> log, bytes.fromhex(hex_root))
              for offset, log, hex_root in proof.subtrees]
    old_nodes = {_parse_key(k): bytes.fromhex(v)
                 for k, v in proof.old_nodes.items()}
    new_nodes = {_parse_key(k): bytes.fromhex(v)
                 for k, v in proof.new_nodes.items()}
    # Same subtree roots, same positions, committed under both roots.
    verify_multiproof(proof.version, proof.old_size, claims, old_nodes,
                      old_root)
    verify_multiproof(proof.version, proof.new_size, claims, new_nodes,
                      new_root)


def is_valid(fn, *args) -> bool:
    """Boolean wrapper used by demos and tests."""
    try:
        fn(*args)
        return True
    except ProofError:
        return False
