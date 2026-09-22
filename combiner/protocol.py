"""Input protocol for the window result combiner.

Every local (shard) result is described by a :class:`ShardReport`. A report
is uniquely identified by ``(window_id, shard_id, local_version)`` and
carries the shard epoch, the input watermark and the rule version it was
produced with. Scale in/out is described by a :class:`HandoverManifest`.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import asdict, dataclass

UPSERT = "upsert"
REVOKE = "revoke"


@dataclass(frozen=True)
class ShardReport:
    """One local result (or a revocation) reported by a shard.

    - ``epoch``: shard epoch; a handover manifest retires a parent shard at
      a given epoch, and later reports from it are rejected.
    - ``local_version``: per-shard monotonic version; only the newest version
      of a shard may contribute to a window.
    - ``watermark``: input watermark the result was computed at.
    - ``rule_version``: rule set the result was computed with; only slices
      sharing one rule version are mutually compatible.
    - ``kind``: ``upsert`` (default) or ``revoke`` (withdraw the slice).
    """

    window_id: str
    shard_id: str
    epoch: int
    local_version: int
    watermark: int
    rule_version: str
    value: float = 0.0
    kind: str = UPSERT

    def to_dict(self) -> dict:
        return asdict(self)

    @staticmethod
    def from_dict(data: dict) -> "ShardReport":
        return ShardReport(**data)


@dataclass(frozen=True)
class HandoverManifest:
    """Declares that ``parent`` is replaced by ``children`` at shard ``epoch``.

    Once a manifest is applied to a window, the parent's slice is dropped and
    any further report from the parent is rejected, so an old shard and its
    new sub-shards can never be counted twice.
    """

    window_id: str
    parent: str
    children: tuple[str, ...]
    epoch: int

    def to_dict(self) -> dict:
        data = asdict(self)
        data["children"] = list(self.children)
        return data

    @staticmethod
    def from_dict(data: dict) -> "HandoverManifest":
        data = dict(data)
        data["children"] = tuple(data["children"])
        return HandoverManifest(**data)


def result_id_for(payload: dict) -> str:
    """Deterministic result id.

    The same logical result always hashes to the same id, which is what makes
    re-delivery after a crash idempotent for the downstream.
    """
    blob = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(blob).hexdigest()[:24]
