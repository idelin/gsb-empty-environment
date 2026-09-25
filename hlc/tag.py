"""Hybrid logical clock tags.

A tag carries three explicit components:

* ``physical`` -- calibrated wall-clock time in microseconds;
* ``logical``  -- tie-breaker strictly increasing within one physical value;
* ``epoch``    -- node incarnation counter, bumped once per recovered
                  process, so restarted nodes never reuse an old tag.

``node`` identifies the minting node and only acts as a deterministic final
tie-breaker when every ordering component is equal (which cannot happen
between a node's own successive tags, but can in principle be observed on a
validator over independent origins).
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True, eq=True)
class Tag:
    physical: int
    logical: int
    epoch: int
    node: str

    def to_wire(self) -> str:
        return f"hlc/{self.physical:x}.{self.logical:x}.{self.epoch:x}/{self.node}"

    @classmethod
    def from_wire(cls, text: str) -> "Tag":
        if not isinstance(text, str):
            raise ValueError("wire tag must be a string")
        try:
            prefix, rest = text.split("/", 1)
            parts, node = rest.rsplit("/", 1)
            p, l, e = parts.split(".")
            if prefix != "hlc" or not node:
                raise ValueError
            tag = cls(int(p, 16), int(l, 16), int(e, 16), node)
        except (ValueError, IndexError) as exc:
            raise ValueError(f"malformed wire tag: {text!r}") from exc
        if tag.physical < 0 or tag.logical < 0 or tag.epoch < 0:
            raise ValueError(f"negative component in wire tag: {text!r}")
        return tag

    def to_dict(self) -> dict:
        return {
            "physical": self.physical,
            "logical": self.logical,
            "epoch": self.epoch,
            "node": self.node,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Tag":
        return cls(
            int(data["physical"]),
            int(data["logical"]),
            int(data["epoch"]),
            str(data["node"]),
        )

    def key(self) -> tuple[int, int, int, str]:
        # Global ordering is (physical, epoch, logical, node): a newer node
        # incarnation dominates any same-physical tag from an older epoch,
        # which is what keeps restarts strictly monotonic after the logical
        # counter resets.
        return self.physical, self.epoch, self.logical, self.node

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, Tag):
            return NotImplemented
        return self.key() < other.key()

    def __le__(self, other: object) -> bool:
        if not isinstance(other, Tag):
            return NotImplemented
        return self.key() <= other.key()

    def __gt__(self, other: object) -> bool:
        if not isinstance(other, Tag):
            return NotImplemented
        return self.key() > other.key()

    def __ge__(self, other: object) -> bool:
        if not isinstance(other, Tag):
            return NotImplemented
        return self.key() >= other.key()
