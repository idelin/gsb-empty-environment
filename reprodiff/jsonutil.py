"""Strict JSON parsing helpers (thin wrappers over the stdlib json module).

Parsing never routes numbers through binary floating point: integers stay
Python ints of arbitrary precision and non-integral numbers become
``decimal.Decimal``.  Duplicate object keys and non-standard constants are
rejected.
"""

from __future__ import annotations

import json
from decimal import Decimal

from .model import MISSING


class JsonError(Exception):
    """Raised when a document is not acceptable strict JSON."""


def strict_loads(data: bytes):
    """Parse JSON bytes, rejecting duplicate keys and non-standard input."""
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise JsonError(f"not valid UTF-8: {exc}") from exc

    def _pairs(pairs):
        obj = {}
        for key, value in pairs:
            if key in obj:
                raise JsonError(f"duplicate object key {key!r}")
            obj[key] = value
        return obj

    def _constant(name):
        raise JsonError(f"non-standard constant {name}")

    try:
        return json.loads(
            text,
            object_pairs_hook=_pairs,
            parse_int=int,
            parse_float=Decimal,
            parse_constant=_constant,
        )
    except json.JSONDecodeError as exc:
        raise JsonError(str(exc)) from exc


class JsonDiff:
    """One leaf-level difference between two JSON documents."""

    __slots__ = ("key_path", "kind", "old", "new")

    def __init__(self, key_path, kind, old, new):
        self.key_path = key_path
        self.kind = kind
        self.old = old
        self.new = new


def _is_number(value) -> bool:
    return isinstance(value, (int, Decimal)) and not isinstance(value, bool)


def json_diffs(old, new, path=()):
    """Yield leaf differences between two parsed documents.

    Array order is significant: elements are compared positionally.
    """
    if _is_number(old) and _is_number(new):
        if old != new:
            yield JsonDiff(path, "value-changed", old, new)
        return
    if type(old) is not type(new):
        yield JsonDiff(path, "type-changed", old, new)
        return
    if isinstance(old, dict):
        for key in old:
            if key not in new:
                yield JsonDiff(path + (key,), "key-removed", old[key], MISSING)
        for key in new:
            if key not in old:
                yield JsonDiff(path + (key,), "key-added", MISSING, new[key])
        for key in old:
            if key in new:
                yield from json_diffs(old[key], new[key], path + (key,))
    elif isinstance(old, list):
        common = min(len(old), len(new))
        for index in range(common):
            yield from json_diffs(old[index], new[index], path + (index,))
        for index in range(common, len(old)):
            yield JsonDiff(path + (index,), "element-removed", old[index], MISSING)
        for index in range(common, len(new)):
            yield JsonDiff(path + (index,), "element-added", MISSING, new[index])
    elif old != new:
        yield JsonDiff(path, "value-changed", old, new)
