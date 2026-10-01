"""Shared data model and input loading for the reconciliation interpreter."""
from __future__ import annotations

import json
from dataclasses import dataclass
from decimal import Decimal
from pathlib import Path


@dataclass(frozen=True)
class Row:
    """One transaction row.

    id:     business identifier of the row (unique within its table)
    keys:   candidate business-key set
    amount: transaction amount (exact Decimal)
    time:   transaction timestamp in epoch seconds (exact Decimal)
    """

    id: str
    keys: frozenset
    amount: Decimal
    time: Decimal


def _to_decimal(value) -> Decimal:
    if isinstance(value, Decimal):
        return value
    return Decimal(str(value))


def load_table(path) -> list:
    """Load a transaction table from a JSON file.

    Expected format: a JSON array of objects, each with
      "id":     string
      "keys":   array of strings (candidate business keys)
      "amount": number or numeric string
      "time":   number (epoch seconds) or numeric string
    """
    data = json.loads(Path(path).read_text(encoding="utf-8"), parse_float=Decimal)
    if not isinstance(data, list):
        raise ValueError(f"{path}: top level must be a JSON array")
    rows = []
    seen = set()
    for pos, item in enumerate(data):
        row = Row(
            id=str(item["id"]),
            keys=frozenset(str(k) for k in item.get("keys", [])),
            amount=_to_decimal(item["amount"]),
            time=_to_decimal(item["time"]),
        )
        if row.id in seen:
            raise ValueError(f"{path}: duplicate row id {row.id!r} at index {pos}")
        seen.add(row.id)
        rows.append(row)
    return rows
