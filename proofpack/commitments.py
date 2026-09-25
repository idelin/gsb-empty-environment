"""Ordered commitment tree (Merkle) with covering-subtree range digests.

Leaves are committed in key order. The tree is padded to a power of two with
a public constant pad leaf so every subtree has a power-of-two size, which
makes range covering and root reconstruction unambiguous and replayable.
"""
from __future__ import annotations

import hashlib
import json

_LEAF_PREFIX = b"proofpack/leaf/v1:"
_NODE_PREFIX = b"proofpack/node/v1:"

PAD_LEAF_HEX = hashlib.sha256(b"proofpack/pad-leaf/v1").hexdigest()


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def canonical(obj) -> bytes:
    """Deterministic encoding used for every hashed object."""
    return json.dumps(obj, sort_keys=True, separators=(",", ":")).encode("utf-8")


def leaf_hash(record: dict) -> str:
    return _sha256(_LEAF_PREFIX + canonical(record))


def node_hash(left_hex: str, right_hex: str) -> str:
    return _sha256(_NODE_PREFIX + bytes.fromhex(left_hex) + bytes.fromhex(right_hex))


def next_pow2(n: int) -> int:
    p = 1
    while p < max(1, n):
        p <<= 1
    return p


class CommitmentTree:
    """Merkle tree over ordered leaf digests, padded with PAD_LEAF_HEX."""

    def __init__(self, leaves: list[str]):
        self.leaf_count = len(leaves)
        self.size = next_pow2(len(leaves))
        level = list(leaves) + [PAD_LEAF_HEX] * (self.size - len(leaves))
        self.levels = [level]
        while len(level) > 1:
            level = [node_hash(level[i], level[i + 1]) for i in range(0, len(level), 2)]
            self.levels.append(level)

    @property
    def root(self) -> str:
        return self.levels[-1][0]

    @property
    def height(self) -> int:
        return len(self.levels) - 1

    def covering_digests(self, start: int, end: int) -> list[dict]:
        """Minimal set of subtree digests exactly covering [start, end)."""
        out: list[dict] = []
        if start >= end:
            return out

        def rec(lo: int, hi: int, height: int) -> None:
            if start <= lo and hi <= end:
                out.append({
                    "kind": "digest",
                    "index": lo,
                    "height": height,
                    "hash": self.levels[height][lo >> height],
                })
                return
            mid = (lo + hi) // 2
            if start < mid:
                rec(lo, mid, height - 1)
            if mid < end:
                rec(mid, hi, height - 1)

        rec(0, self.size, self.height)
        return out


def reconstruct_root(items: list[dict], size: int) -> str:
    """Rebuild the root from items partitioning [0, size).

    Each item is {"index", "height", "hash"}; leaves have height 0.
    Raises ValueError on gaps, overlaps, or misalignment.
    """
    if size != next_pow2(size):
        raise ValueError("size must be a power of two")
    items = sorted(items, key=lambda it: it["index"])
    expect = 0
    stack: list[list] = []
    for it in items:
        idx, height = it["index"], it["height"]
        if idx != expect:
            raise ValueError(f"gap or overlap at index {idx}, expected {expect}")
        if idx % (1 << height) != 0:
            raise ValueError(f"misaligned subtree at index {idx} height {height}")
        expect += 1 << height
        stack.append([idx, height, it["hash"]])
        while len(stack) >= 2:
            i1, h1, d1 = stack[-2]
            i2, h2, d2 = stack[-1]
            if h1 == h2 and i2 == i1 + (1 << h1) and i1 % (1 << (h1 + 1)) == 0:
                stack.pop()
                stack.pop()
                stack.append([i1, h1 + 1, node_hash(d1, d2)])
            else:
                break
    if expect != size:
        raise ValueError(f"items cover [0,{expect}) but tree size is {size}")
    if len(stack) != 1 or stack[0][0] != 0:
        raise ValueError("items did not reduce to a single root")
    return stack[0][2]
