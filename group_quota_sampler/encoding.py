"""Canonical, deterministic encodings for typed values.

All encoding here is stable across processes and platforms (no use of the
builtin ``hash()`` for strings, no float coercion of integers).  ``None``
(SQL-style null) and float NaN are deliberately distinct values.
"""

from __future__ import annotations

import hashlib
import math

# Canonical sort keys are tuples whose first element is a type tag so that
# values of different types remain mutually comparable (total order).
TAG_NULL = 0
TAG_BOOL = 1
TAG_INT = 2
TAG_FLOAT = 3
TAG_STR = 4
TAG_BYTES = 5


def canonical_value(value):
    """Return a hashable, totally ordered canonical form of ``value``.

    null (None) and NaN map to different keys; ``-0.0`` normalises to ``0.0``;
    integers keep their exact value (never routed through float).
    """
    if value is None:
        return (TAG_NULL, 0)
    if isinstance(value, bool):
        return (TAG_BOOL, 1 if value else 0)
    if isinstance(value, int):
        return (TAG_INT, value)
    if isinstance(value, float):
        if math.isnan(value):
            return (TAG_FLOAT, 0)  # NaN: distinct from null and from numbers
        if value == 0.0:
            value = 0.0  # normalise -0.0
        return (TAG_FLOAT, 1, value)
    if isinstance(value, str):
        return (TAG_STR, value)
    if isinstance(value, bytes):
        return (TAG_BYTES, value)
    raise TypeError(f"unsupported value type: {type(value)!r}")


def canonical_group(values):
    """Canonical key for a (possibly multi-column) group tuple."""
    return tuple(canonical_value(v) for v in values)


def encode_value(value):
    """Encode a typed value as deterministic bytes (for hashing row ids)."""
    if value is None:
        return b"N"
    if isinstance(value, bool):
        return b"B1" if value else b"B0"
    if isinstance(value, int):
        return b"I" + str(value).encode("ascii")
    if isinstance(value, float):
        if math.isnan(value):
            return b"Fnan"
        if math.isinf(value):
            return b"F+inf" if value > 0 else b"F-inf"
        if value == 0.0:
            value = 0.0
        return b"F" + repr(value).encode("ascii")
    if isinstance(value, str):
        raw = value.encode("utf-8")
        return b"S" + str(len(raw)).encode("ascii") + b":" + raw
    if isinstance(value, bytes):
        return b"Y" + str(len(value)).encode("ascii") + b":" + value
    raise TypeError(f"unsupported value type: {type(value)!r}")


def normalize_seed(seed):
    """Normalise an explicit seed (int / str / bytes) to bytes."""
    if isinstance(seed, bytes):
        return seed
    if isinstance(seed, str):
        return b"seed:str:" + seed.encode("utf-8")
    if isinstance(seed, int) and not isinstance(seed, bool):
        return b"seed:int:" + str(seed).encode("ascii")
    raise TypeError(f"unsupported seed type: {type(seed)!r}")


def row_key(seed_bytes, row_id):
    """Deterministic 128-bit random key for a stable row id under a seed."""
    digest = hashlib.blake2b(digest_size=16)
    digest.update(len(seed_bytes).to_bytes(4, "big"))
    digest.update(seed_bytes)
    digest.update(encode_value(row_id))
    return int.from_bytes(digest.digest(), "big")


# --- JSON helpers (checkpointing) -------------------------------------------

def value_to_json(value):
    if value is None:
        return ["null"]
    if isinstance(value, bool):
        return ["bool", value]
    if isinstance(value, int):
        return ["int", value]  # Python ints serialise exactly in JSON
    if isinstance(value, float):
        if math.isnan(value):
            return ["float", "nan"]
        if math.isinf(value):
            return ["float", "inf" if value > 0 else "-inf"]
        return ["float", value]
    if isinstance(value, str):
        return ["str", value]
    if isinstance(value, bytes):
        return ["bytes", value.hex()]
    raise TypeError(f"unsupported value type: {type(value)!r}")


def value_from_json(tagged):
    tag = tagged[0]
    if tag == "null":
        return None
    if tag == "bool":
        return bool(tagged[1])
    if tag == "int":
        return int(tagged[1])
    if tag == "float":
        raw = tagged[1]
        if raw == "nan":
            return float("nan")
        if raw == "inf":
            return float("inf")
        if raw == "-inf":
            return float("-inf")
        return float(raw)
    if tag == "str":
        return str(tagged[1])
    if tag == "bytes":
        return bytes.fromhex(tagged[1])
    raise ValueError(f"unknown tag: {tag!r}")
