"""Exact JSON handling.

* Duplicate keys are rejected.
* Integers stay Python ``int``; decimal literals become ``Decimal`` parsed
  straight from their source text, so a binary float never rounds a value.
* Keys preserve first-insertion order; array order is preserved.
"""

from __future__ import annotations

import json
from decimal import Decimal
from typing import Any

from .errors import RuleError

_BOM = b"\xef\xbb\xbf"


class DuplicateKeyError(ValueError):
    def __init__(self, key: str):
        super().__init__(f"duplicate JSON object key: {key!r}")
        self.key = key


class JSONDecodeError(ValueError):
    pass


def _number_exact(text: str) -> Any:
    if "." in text or "e" in text or "E" in text:
        return Decimal(text)
    return int(text)


def parse_json_bytes(data: bytes) -> Any:
    if data.startswith(_BOM):
        data = data[len(_BOM):]
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise JSONDecodeError(f"JSON payload is not valid UTF-8: {exc}") from None
    def reject_constant(text: str) -> Any:
        raise JSONDecodeError(f"invalid JSON constant: {text}")

    def hook(pairs: Any) -> Any:
        seen: set[str] = set()
        obj: dict[str, Any] = {}
        for key, value in pairs:
            if not isinstance(key, str):
                raise JSONDecodeError("JSON object key is not a string")
            if key in seen:
                raise DuplicateKeyError(key)
            seen.add(key)
            obj[key] = value
        return obj

    decoder = json.JSONDecoder(
        parse_int=int,
        parse_float=Decimal,
        parse_constant=reject_constant,
        object_pairs_hook=hook,
    )

    try:
        value, end = decoder.raw_decode(text)
    except DuplicateKeyError:
        raise
    except ValueError as exc:
        raise JSONDecodeError(str(exc)) from None
    if text[end:].strip():
        raise JSONDecodeError("trailing data after JSON document")
    return value


def type_name(value: Any) -> str:
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, Decimal):
        return "number"
    if isinstance(value, str):
        return "string"
    if value is None:
        return "null"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    raise RuleError(f"unsupported JSON value type: {type(value)!r}")


def type_matches(declared: str, value: Any) -> bool:
    actual = type_name(value)
    if declared == "any":
        return True
    if declared == "number":
        return actual in ("number", "integer")
    if declared == "scalar":
        return actual in ("number", "integer", "string", "bool", "null")
    return actual == declared


def resolve_path(value: Any, key_path: tuple[str, ...]) -> tuple[bool, Any]:
    cur = value
    for key in key_path:
        if isinstance(cur, dict) and key in cur:
            cur = cur[key]
            continue
        return False, None
    return True, cur


def canonical_value(value: Any) -> Any:
    """Deterministic, exact representation of a JSON value for evidence."""
    if isinstance(value, dict):
        return {key: canonical_value(val) for key, val in value.items()}
    if isinstance(value, list):
        return [canonical_value(item) for item in value]
    if isinstance(value, Decimal):
        return {"__decimal__": format(value, "f")}
    return value


_IGNORED_SENTINEL = object()


def residual(value: Any, ignored_trie: dict[str, Any]) -> Any:
    if ignored_trie.get("$") is _IGNORED_SENTINEL:
        return _IGNORED_SENTINEL

    if isinstance(value, dict):
        out: dict[str, Any] = {}
        for key in value:  # insertion order preserved
            sub = ignored_trie.get(key)
            if isinstance(sub, dict):
                sub_res = residual(value[key], sub)
                if sub_res is not _IGNORED_SENTINEL:
                    out[key] = sub_res
            else:
                out[key] = canonical_value(value[key])
        return out

    if isinstance(value, list):
        return [canonical_value(item) for item in value]

    return canonical_value(value)
