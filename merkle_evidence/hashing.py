"""Domain-separated SHA-256 hashing rules for the evidence Merkle tree.

Every hashing rule carries a stable rule ID and a version.  Changing any
rule changes every derived hash, so proofs can never be spliced across
rule versions.

Rules (stable IDs):
    RULE-HASH-LEAF   v1  leaf  = SHA256(0x00 | u64be(index) | ustr(log_version) | record_digest)
    RULE-HASH-NODE   v1  node  = SHA256(0x01 | left | right)
    RULE-HASH-RECORD v1  rec   = SHA256(0x02 | canonical_json(record))
    RULE-HASH-EMPTY  v1  empty = SHA256("")

The leaf hash binds the *position* (index) and the *log version* into
every leaf, so identical record content at different positions yields
different leaves and proofs can never be swapped across positions or
across versions.
"""

from __future__ import annotations

import hashlib

RULESET_ID = "merkle-evidence-rules"
RULESET_VERSION = 1

RULE_LEAF = ("RULE-HASH-LEAF", 1)
RULE_NODE = ("RULE-HASH-NODE", 1)
RULE_RECORD = ("RULE-HASH-RECORD", 1)
RULE_EMPTY = ("RULE-HASH-EMPTY", 1)

_TAG_LEAF = b"\x00"
_TAG_NODE = b"\x01"
_TAG_RECORD = b"\x02"


def sha256(data: bytes) -> bytes:
    return hashlib.sha256(data).digest()


def _u64(value: int) -> bytes:
    if value < 0:
        raise ValueError("index must be non-negative")
    return value.to_bytes(8, "big")


def _ustr(text: str) -> bytes:
    data = text.encode("utf-8")
    return len(data).to_bytes(4, "big") + data


def leaf_hash(index: int, log_version: str, record_digest: bytes) -> bytes:
    """RULE-HASH-LEAF v1: binds position, log version and record digest."""
    if len(record_digest) != 32:
        raise ValueError("record digest must be 32 bytes")
    return sha256(_TAG_LEAF + _u64(index) + _ustr(log_version) + record_digest)


def node_hash(left: bytes, right: bytes) -> bytes:
    """RULE-HASH-NODE v1: ordered concatenation of child hashes."""
    if len(left) != 32 or len(right) != 32:
        raise ValueError("child hashes must be 32 bytes")
    return sha256(_TAG_NODE + left + right)


def record_digest(canonical_record: bytes) -> bytes:
    """RULE-HASH-RECORD v1: digest of the canonical record encoding."""
    return sha256(_TAG_RECORD + canonical_record)


# RULE-HASH-EMPTY v1: root of an empty tree.
EMPTY_ROOT = sha256(b"")
