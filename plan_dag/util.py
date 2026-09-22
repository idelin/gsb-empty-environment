"""Small strict-decoding helpers shared by expressions, nodes and codec."""

from __future__ import annotations

from typing import Any, Iterable

from .errors import DocumentFormatError


def as_type(obj: Any, expected: type | tuple[type, ...], where: str) -> Any:
    if not isinstance(obj, expected):
        names = (
            ", ".join(t.__name__ for t in expected)
            if isinstance(expected, tuple)
            else expected.__name__
        )
        raise DocumentFormatError(
            f"{where}: expected {names}, got {type(obj).__name__}"
        )
    return obj


def parse_object(
    obj: Any,
    where: str,
    *,
    required: Iterable[str] = (),
    optional: Iterable[str] = (),
) -> dict[str, Any]:
    as_type(obj, dict, where)
    required_set = set(required)
    allowed = required_set | set(optional)
    unknown = [key for key in obj if key not in allowed]
    if unknown:
        raise DocumentFormatError(
            f"{where}: unknown field(s) {sorted(unknown)!r}"
        )
    missing = [key for key in required_set if key not in obj]
    if missing:
        raise DocumentFormatError(f"{where}: missing field(s) {sorted(missing)!r}")
    return obj


def as_str(obj: Any, where: str) -> str:
    return as_type(obj, str, where)
