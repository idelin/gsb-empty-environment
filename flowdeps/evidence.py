"""Replayable evidence.

Every mutation, execution, commit and ensure call is appended to
``Engine.log`` as a plain tuple.  ``replay`` rebuilds a fresh engine from
those entries plus a step registry ``{(step_id, version): func}`` and
verifies that the regenerated log is byte-identical -- i.e. the evidence
fully determines the outcome.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Optional

from .engine import Engine


@dataclass
class ReplayReport:
    ok: bool
    total_entries: int
    first_divergence: Optional[int]
    log: list


def replay(entries, registry: dict) -> ReplayReport:
    eng = Engine()
    for entry in entries:
        kind = entry[0]
        if kind == "define_step":
            func: Callable = registry[(entry[1], entry[2])]
            eng.define_step(entry[1], entry[2], func)
        elif kind == "upsert":
            eng.upsert_fact(entry[1], dict(entry[3]))
        elif kind == "remove":
            eng.remove_fact(entry[1])
        elif kind == "rule_set":
            eng.set_rule(entry[1], entry[3])
        elif kind == "ensure":
            eng.ensure(entry[1])
        elif kind == "commit":
            eng.commit()
        elif kind == "run":
            pass                      # reproduced by execution itself
        else:
            raise ValueError(f"unknown evidence event {kind!r}")

    new_log = eng.log
    divergence: Optional[int] = None
    for i, (expected, actual) in enumerate(zip(entries, new_log)):
        if expected != actual:
            divergence = i
            break
    if divergence is None and len(entries) != len(new_log):
        divergence = min(len(entries), len(new_log))
    return ReplayReport(divergence is None, len(entries), divergence, new_log)
