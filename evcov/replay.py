"""Stable digests and step records so every computation is replayable."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any


def _default(obj: Any) -> Any:
    if hasattr(obj, "__dataclass_fields__"):
        return {k: getattr(obj, k) for k in obj.__dataclass_fields__}
    if isinstance(obj, (set, frozenset)):
        return sorted(obj)
    if isinstance(obj, tuple):
        return list(obj)
    raise TypeError(f"not serializable: {type(obj)!r}")


def stable_digest(obj: Any) -> str:
    payload = json.dumps(obj, sort_keys=True, default=_default, separators=(",", ":"))
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


@dataclass(frozen=True)
class StepRecord:
    """One computation step: stable ID, version, and input/output digests."""

    step_id: str
    version: int
    kind: str
    input_digest: str
    output_digest: str
