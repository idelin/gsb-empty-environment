"""Checkpoint serialisation for the append-only ledger.

A checkpoint is the full, ordered revision log.  Loading rebuilds state by
replaying each invertible effect, so recovered ledgers answer exactly the
same queries (including old snapshots).  Endpoints serialise losslessly:
``fractions.Fraction`` is stored as ``["fraction", num, den]``.
"""

from __future__ import annotations

import json
from fractions import Fraction
from typing import Any, Dict, List

from .ledger import Ledger, Revision


def _encode(value: Any) -> Any:
    if isinstance(value, Fraction):
        return ["fraction", value.numerator, value.denominator]
    if isinstance(value, tuple):
        return [_encode(item) for item in value]
    if isinstance(value, list):
        return [_encode(item) for item in value]
    if isinstance(value, dict):
        return {key: _encode(item) for key, item in value.items()}
    return value


def _decode(value: Any) -> Any:
    if isinstance(value, list):
        if len(value) == 3 and value[0] == "fraction":
            return Fraction(value[1], value[2])
        return [_decode(item) for item in value]
    if isinstance(value, dict):
        return {key: _decode(item) for key, item in value.items()}
    return value


def to_json(ledger: Ledger) -> str:
    payload: Dict[str, Any] = {
        "format": "coverage-ledger-checkpoint/1",
        "head": ledger.head,
        "revisions": [
            {
                "seq": revision.seq,
                "kind": revision.kind,
                "action": revision.action,
                "effect": _encode(revision.effect),
                "summary": revision.summary,
            }
            for revision in ledger.revisions
        ],
    }
    return json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True)


def save_checkpoint(ledger: Ledger, path: str) -> None:
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(to_json(ledger))


def from_json(text: str) -> Ledger:
    payload = json.loads(text)
    if payload.get("format") != "coverage-ledger-checkpoint/1":
        raise ValueError("unknown checkpoint format")
    revisions: List[Revision] = []
    for raw in payload["revisions"]:
        revisions.append(Revision(
            seq=raw["seq"], kind=raw["kind"], action=raw["action"],
            effect=_decode(raw["effect"]), summary=raw.get("summary", ""),
        ))
    ledger = Ledger()
    ledger.replay(revisions)
    return ledger


def load_checkpoint(path: str) -> Ledger:
    with open(path, encoding="utf-8") as handle:
        return from_json(handle.read())
