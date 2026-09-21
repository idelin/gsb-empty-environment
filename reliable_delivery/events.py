"""Event model and wraparound sequence-number arithmetic."""
from __future__ import annotations

from dataclasses import dataclass, replace

DEFAULT_MODULUS = 256


def forward_distance(base: int, seq: int, modulus: int) -> int:
    """Steps from ``base`` to ``seq`` moving forward in the ring."""
    return (seq - base) % modulus


def is_ahead(seq: int, base: int, modulus: int) -> bool:
    """True when ``seq`` is strictly ahead of ``base`` within half the ring."""
    dist = (seq - base) % modulus
    return 0 < dist < modulus // 2


def range_covers(start: int, end: int, seq: int, modulus: int) -> bool:
    """True when ``seq`` lies in the forward range [start, end] (wrap-aware)."""
    return (seq - start) % modulus <= (end - start) % modulus


@dataclass(frozen=True)
class Event:
    partition: str
    epoch: int
    seq: int
    payload: str
    # Ring cycle within the epoch, assigned by the layer when the event is
    # accepted. Seq wraps; the cycle keeps the delivery id stable for the
    # same event yet distinct across wrap turns.
    cycle: int = 0

    @property
    def delivery_id(self) -> str:
        return f"{self.partition}#{self.epoch}:{self.cycle}:{self.seq}"

    def with_cycle(self, cycle: int) -> "Event":
        return replace(self, cycle=cycle)

    def to_dict(self) -> dict:
        return {
            "partition": self.partition,
            "epoch": self.epoch,
            "seq": self.seq,
            "payload": self.payload,
            "cycle": self.cycle,
        }

    @staticmethod
    def from_dict(data: dict) -> "Event":
        return Event(
            partition=data["partition"],
            epoch=data["epoch"],
            seq=data["seq"],
            payload=data["payload"],
            cycle=data.get("cycle", 0),
        )
