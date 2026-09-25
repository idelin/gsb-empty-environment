"""Checkpoint model: cursors, in-flight group, committed watermark.

A checkpoint captures everything needed to resume a merge without
re-emitting already committed groups:

* ``cursors`` -- per-stream ``(version, offset)`` pairs.  ``offset`` is the
  number of rows consumed from that stream; ``version`` must match the
  stream's current version at restore time or a
  ``CursorVersionMismatchError`` is raised.
* ``pending`` -- the incomplete group (key, partial aggregate states, and
  the ids of the streams already drained for that key).  This is what makes
  pausing in the middle of a group that spans several streams safe.
* ``committed_upto`` -- the greatest key already emitted.  Acts as a
  watermark so a recovered merger never re-outputs a committed group.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

from .types import decode_value, encode_value

CHECKPOINT_FORMAT = 1


@dataclass(frozen=True)
class CursorState:
    stream_id: str
    version: int
    offset: int

    def to_dict(self) -> dict:
        return {
            "stream_id": self.stream_id,
            "version": self.version,
            "offset": self.offset,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "CursorState":
        return cls(
            stream_id=data["stream_id"],
            version=data["version"],
            offset=data["offset"],
        )


@dataclass(frozen=True)
class PendingGroup:
    key: Any
    states: tuple  # tuple of aggregate-state json dicts
    drained: tuple  # tuple of stream ids already drained for ``key``

    def to_dict(self) -> dict:
        return {
            "key": encode_value(self.key),
            "states": list(self.states),
            "drained": list(self.drained),
        }

    @classmethod
    def from_dict(cls, data: dict) -> "PendingGroup":
        return cls(
            key=decode_value(data["key"]),
            states=tuple(data["states"]),
            drained=tuple(data["drained"]),
        )


@dataclass(frozen=True)
class Checkpoint:
    cursors: dict[str, CursorState]
    pending: "PendingGroup | None" = None
    committed_upto: Any = None
    has_committed: bool = False

    def to_dict(self) -> dict:
        return {
            "format": CHECKPOINT_FORMAT,
            "cursors": [c.to_dict() for c in self.cursors.values()],
            "pending": self.pending.to_dict() if self.pending is not None else None,
            "committed_upto": (
                encode_value(self.committed_upto) if self.has_committed else None
            ),
            "has_committed": self.has_committed,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Checkpoint":
        if data.get("format") != CHECKPOINT_FORMAT:
            raise ValueError(f"unsupported checkpoint format: {data.get('format')!r}")
        cursors = {}
        for raw in data["cursors"]:
            cursor = CursorState.from_dict(raw)
            cursors[cursor.stream_id] = cursor
        pending = (
            PendingGroup.from_dict(data["pending"])
            if data.get("pending") is not None
            else None
        )
        has_committed = data["has_committed"]
        committed_upto = (
            decode_value(data["committed_upto"]) if has_committed else None
        )
        return cls(
            cursors=cursors,
            pending=pending,
            committed_upto=committed_upto,
            has_committed=has_committed,
        )

    def to_json(self) -> str:
        return json.dumps(self.to_dict(), sort_keys=True)

    @classmethod
    def from_json(cls, payload: str) -> "Checkpoint":
        return cls.from_dict(json.loads(payload))
