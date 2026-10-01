"""Shared data model and input loading (JSON, stdlib only)."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from decimal import Decimal
from pathlib import Path


@dataclass(frozen=True)
class Row:
    """One transaction row.

    id:     business identifier (unique within its side)
    side:   "L" or "R"
    keys:   candidate business-key set
    amount: exact decimal amount
    time:   integer timestamp (seconds)
    """

    id: str
    side: str
    keys: frozenset
    amount: Decimal
    time: int

    @property
    def abs_amount(self) -> Decimal:
        return abs(self.amount)


@dataclass
class Instance:
    window: int
    left: list = field(default_factory=list)
    right: list = field(default_factory=list)


def _parse_row(raw: dict, side: str) -> Row:
    keys = raw.get("keys", [])
    if not isinstance(keys, list) or not all(isinstance(k, str) for k in keys):
        raise ValueError(f"row {raw.get('id')!r}: 'keys' must be a list of strings")
    return Row(
        id=str(raw["id"]),
        side=side,
        keys=frozenset(keys),
        amount=Decimal(str(raw["amount"])),
        time=int(raw["time"]),
    )


def load_instance(source) -> Instance:
    """Load an instance from a JSON path, file object, or dict.

    Format:
      {
        "window": 300,
        "left":  [{"id": "a", "keys": ["x"], "amount": "100.00", "time": 1000}],
        "right": [{"id": "r1", "keys": ["x"], "amount": "100.00", "time": 1010}]
      }
    """
    if isinstance(source, (str, Path)):
        with open(source, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    elif hasattr(source, "read"):
        data = json.load(source)
    else:
        data = dict(source)

    left = [_parse_row(r, "L") for r in data.get("left", [])]
    right = [_parse_row(r, "R") for r in data.get("right", [])]
    for rows, name in ((left, "left"), (right, "right")):
        ids = [r.id for r in rows]
        if len(ids) != len(set(ids)):
            raise ValueError(f"duplicate row ids on side {name}")
    return Instance(window=int(data.get("window", 0)), left=left, right=right)
