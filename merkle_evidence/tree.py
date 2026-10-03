"""Merkle tree over ordered evidence record digests.

Missing-leaf padding rule (RULE-PAD-PROMOTE v1):
    The tree is built over exactly `size` leaves; it is never padded with
    transmitted padding leaves.  When a level has an odd number of nodes,
    the final node is *promoted unchanged* to the next level (equivalently,
    the missing sibling is defined as transparent).  All tree structure is
    a pure function of (size, log_version), so an independent verifier
    derives the full shape itself and never has to trust prover-supplied
    padding leaves or internal node counts.

Split rule (RULE-SPLIT v1):
    A subtree covering `n` leaves (n > 1) splits at k, the largest power
    of two strictly smaller than n.
"""

from __future__ import annotations

from .hashing import EMPTY_ROOT, leaf_hash, node_hash

RULE_PAD = ("RULE-PAD-PROMOTE", 1)
RULE_SPLIT = ("RULE-SPLIT", 1)


def split_point(n: int) -> int:
    """Largest power of two strictly smaller than n (RULE-SPLIT v1)."""
    if n < 2:
        raise ValueError("cannot split a subtree of size < 2")
    k = 1 << (n.bit_length() - 1)
    if k == n:
        k >>= 1
    return k


class MerkleTree:
    """Merkle tree over an ordered list of 32-byte record digests."""

    def __init__(self, record_digests: list[bytes] | tuple[bytes, ...],
                 log_version: str):
        self.log_version = log_version
        self.size = len(record_digests)
        self._leaves = [
            leaf_hash(i, log_version, d) for i, d in enumerate(record_digests)
        ]
        self._cache: dict[tuple[int, int], bytes] = {}

    # ------------------------------------------------------------------ #
    # core hashing                                                        #
    # ------------------------------------------------------------------ #
    def subtree_hash(self, start: int, length: int) -> bytes:
        """Hash of the subtree covering leaves [start, start+length)."""
        if length < 1 or start < 0 or start + length > self.size:
            raise ValueError("subtree range outside the tree")
        key = (start, length)
        if key not in self._cache:
            if length == 1:
                value = self._leaves[start]
            else:
                k = split_point(length)
                value = node_hash(
                    self.subtree_hash(start, k),
                    self.subtree_hash(start + k, length - k),
                )
            self._cache[key] = value
        return self._cache[key]

    def root(self) -> bytes:
        if self.size == 0:
            return EMPTY_ROOT
        return self.subtree_hash(0, self.size)

    # ------------------------------------------------------------------ #
    # membership proof (single leaf)                                      #
    # ------------------------------------------------------------------ #
    def audit_path(self, index: int) -> list[bytes]:
        """Sibling hashes for `index`, ordered leaf-to-root."""
        if not 0 <= index < self.size:
            raise IndexError(f"leaf index {index} outside size {self.size}")
        path: list[bytes] = []

        def rec(start: int, length: int) -> None:
            if length == 1:
                return
            k = split_point(length)
            if index < start + k:
                rec(start, k)
                path.append(self.subtree_hash(start + k, length - k))
            else:
                rec(start + k, length - k)
                path.append(self.subtree_hash(start, k))

        rec(0, self.size)
        return path

    # ------------------------------------------------------------------ #
    # multi proof (batch, shared paths compressed)                        #
    # ------------------------------------------------------------------ #
    def multi_proof_hashes(self, indices: list[int] | tuple[int, ...]
                           ) -> list[bytes]:
        """Minimal set of sibling hashes proving all `indices` together.

        Hashes shared by several members (shared paths) are emitted once.
        The emission order is the exact order the verifier consumes them:
        depth-first, left subtree before right subtree.
        """
        idxs = sorted(indices)
        if not idxs:
            raise ValueError("multi proof needs at least one index")
        if len(set(idxs)) != len(idxs):
            raise ValueError("duplicate indices")
        if idxs[0] < 0 or idxs[-1] >= self.size:
            raise IndexError("leaf index outside tree")
        out: list[bytes] = []

        def rec(start: int, length: int, sub: list[int]) -> None:
            if length == 1:
                return  # the proven leaf itself; verifier recomputes it
            k = split_point(length)
            left = [i for i in sub if i < start + k]
            right = [i for i in sub if i >= start + k]
            if left:
                rec(start, k, left)
            else:
                out.append(self.subtree_hash(start, k))
            if right:
                rec(start + k, length - k, right)
            else:
                out.append(self.subtree_hash(start + k, length - k))

        rec(0, self.size, idxs)
        return out

    # ------------------------------------------------------------------ #
    # append-only consistency proof                                       #
    # ------------------------------------------------------------------ #
    def consistency_proof(self, old_size: int) -> list[bytes]:
        """Proof that the tree of `old_size` leaves is a prefix of this
        (larger) tree.  Hashes are emitted in the exact order the verifier
        consumes them."""
        n = self.size
        if not 0 <= old_size <= n:
            raise ValueError("old_size outside [0, tree size]")
        if old_size == 0 or old_size == n:
            return []
        out: list[bytes] = []

        def rec(m: int, nn: int, start: int, complete: bool) -> None:
            if m == nn:
                if not complete:
                    out.append(self.subtree_hash(start, nn))
                return
            k = split_point(nn)
            if m <= k:
                rec(m, k, start, complete)
                out.append(self.subtree_hash(start + k, nn - k))
            else:
                rec(m - k, nn - k, start + k, False)
                out.append(self.subtree_hash(start, k))

        rec(old_size, n, 0, True)
        return out
