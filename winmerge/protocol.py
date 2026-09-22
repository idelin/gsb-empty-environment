"""Input protocol for the windowed result merger (窗口结果汇合器).

Every inbound event is a plain JSON object with a ``type`` field:

- ``partial``  : a sharded partial result
- ``revoke``   : revocation of a previously sent partial
- ``handoff``  : scaling handoff manifest (split / merge of shards)
- ``ack``      : downstream acknowledgement of a published result
- ``tick``     : clock advance, drives window timeouts

Partial results carry four coordinates: shard epoch, local version,
input watermark and rule version.  Only slices that are compatible on
all four are ever merged into a window publication.
"""
from __future__ import annotations

from dataclasses import dataclass

PROTOCOL_VERSION = 1

EVENT_TYPES = ("partial", "revoke", "handoff", "ack", "tick")


class ProtocolError(ValueError):
    """Raised when an inbound event violates the wire protocol."""


@dataclass(frozen=True)
class Partial:
    window: str
    shard: str
    epoch: int
    local_version: int
    watermark: int
    rule_version: str
    value: float


@dataclass(frozen=True)
class Revoke:
    window: str
    shard: str
    epoch: int
    local_version: int
    reason: str


@dataclass(frozen=True)
class Handoff:
    manifest_id: str
    epoch: int
    splits: dict[str, tuple[str, ...]]  # old shard -> new sub-shards


@dataclass(frozen=True)
class Ack:
    result_id: str


@dataclass(frozen=True)
class Tick:
    now: int


def _req(event: dict, field: str, types: tuple[type, ...]):
    if field not in event:
        raise ProtocolError(f"missing field {field!r} in {event.get('type')!r} event")
    value = event[field]
    if not isinstance(value, types) or isinstance(value, bool):
        raise ProtocolError(
            f"field {field!r} must be {types}, got {type(value).__name__}"
        )
    return value


def parse(event: dict):
    """Validate a raw JSON object and return a typed event."""
    if not isinstance(event, dict):
        raise ProtocolError("event must be a JSON object")
    etype = event.get("type")
    if etype == "partial":
        epoch = _req(event, "epoch", (int,))
        local_version = _req(event, "local_version", (int,))
        if epoch < 1:
            raise ProtocolError("epoch must be >= 1")
        if local_version < 1:
            raise ProtocolError("local_version must be >= 1")
        return Partial(
            window=_req(event, "window", (str,)),
            shard=_req(event, "shard", (str,)),
            epoch=epoch,
            local_version=local_version,
            watermark=_req(event, "watermark", (int,)),
            rule_version=_req(event, "rule_version", (str,)),
            value=_req(event, "value", (int, float)),
        )
    if etype == "revoke":
        return Revoke(
            window=_req(event, "window", (str,)),
            shard=_req(event, "shard", (str,)),
            epoch=_req(event, "epoch", (int,)),
            local_version=_req(event, "local_version", (int,)),
            reason=event.get("reason", ""),
        )
    if etype == "handoff":
        raw = _req(event, "splits", (dict,))
        splits: dict[str, tuple[str, ...]] = {}
        children_seen: set[str] = set()
        for parent, children in raw.items():
            if not isinstance(parent, str) or not parent:
                raise ProtocolError("handoff parent shard must be a non-empty string")
            if (
                not isinstance(children, list)
                or not children
                or not all(isinstance(c, str) and c for c in children)
            ):
                raise ProtocolError("handoff children must be a non-empty string list")
            if parent in children:
                raise ProtocolError("shard cannot split into itself")
            dup = children_seen.intersection(children)
            if dup:
                raise ProtocolError(f"shard(s) {sorted(dup)} split from two parents")
            children_seen.update(children)
            splits[parent] = tuple(children)
        epoch = _req(event, "epoch", (int,))
        if epoch < 1:
            raise ProtocolError("epoch must be >= 1")
        return Handoff(
            manifest_id=_req(event, "manifest_id", (str,)),
            epoch=epoch,
            splits=splits,
        )
    if etype == "ack":
        return Ack(result_id=_req(event, "result_id", (str,)))
    if etype == "tick":
        return Tick(now=_req(event, "now", (int,)))
    raise ProtocolError(f"unknown event type {etype!r}")
