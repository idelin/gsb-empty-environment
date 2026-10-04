"""Domain-separated, version-bound SHA-256 primitives.

Every digest mixes in the scheme version, so a digest computed under one
version can never validate under another.  Leaf hashes additionally bind
the leaf *position*, which gives duplicate contents a distinct positional
identity.
"""

from __future__ import annotations

import hashlib
from functools import lru_cache

from .ids import RULE_LEAF_HASH, RULE_NODE_HASH, RULE_PAD_LEAF, RULE_EMPTY_ROOT


def _u64(n: int) -> bytes:
    return n.to_bytes(8, "big")


def _version_bytes(version: str) -> bytes:
    raw = version.encode("utf-8")
    return len(raw).to_bytes(2, "big") + raw


def leaf_hash(version: str, index: int, data: bytes) -> bytes:
    """H_leaf(version, index, data) — binds content *and* position."""
    h = hashlib.sha256()
    h.update(b"LEAF\x00")
    h.update(_version_bytes(version))
    h.update(_u64(index))
    h.update(len(data).to_bytes(4, "big"))
    h.update(data)
    return h.digest()


def padding_leaf_hash(version: str, index: int) -> bytes:
    """Deterministic padding leaf; anyone can recompute it."""
    h = hashlib.sha256()
    h.update(b"PADL\x00")
    h.update(_version_bytes(version))
    h.update(_u64(index))
    return h.digest()


def node_hash(version: str, left: bytes, right: bytes) -> bytes:
    h = hashlib.sha256()
    h.update(b"NODE\x00")
    h.update(_version_bytes(version))
    h.update(left)
    h.update(right)
    return h.digest()


def root_wrap(version: str, tree_size: int, top: bytes) -> bytes:
    """Final root wrapper: binds the committed tree size into the root so
    a proof generated for one size can never verify under another."""
    h = hashlib.sha256()
    h.update(b"ROOT\x00")
    h.update(_version_bytes(version))
    h.update(_u64(tree_size))
    h.update(top)
    return h.digest()


def empty_root(version: str) -> bytes:
    h = hashlib.sha256()
    h.update(b"EMPT\x00")
    h.update(_version_bytes(version))
    return h.digest()


@lru_cache(maxsize=None)
def pad_subtree_root(version: str, level: int, start: int) -> bytes:
    """Root of a fully-padding subtree spanning ``2**level`` leaves at
    ``start``.  Pure function of (version, level, start): the verifier
    recomputes it and never trusts the sender for padding material."""
    if level == 0:
        return padding_leaf_hash(version, start)
    half = 1 << (level - 1)
    left = pad_subtree_root(version, level - 1, start)
    right = pad_subtree_root(version, level - 1, start + half)
    return node_hash(version, left, right)


# Referenced so static checkers see the rule IDs as used by this module.
RULE_IDS = (RULE_LEAF_HASH, RULE_PAD_LEAF, RULE_NODE_HASH, RULE_EMPTY_ROOT)
