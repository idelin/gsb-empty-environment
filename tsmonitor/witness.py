"""Violation witnesses (counterexamples).

A witness is the *shortest* evidence for a FALSE verdict: the trigger
events plus, for a forbidden-event violation, the single offending event.
For a missing-requirement violation the witness is the trigger together
with a completeness token (watermark or termination) attesting that no
more events inside the window can arrive.

Witnesses are independently executable: ``to_script`` emits a
self-contained Python program (standard library only, no import of this
package) that re-checks the violation and exits non-zero if it does not
hold.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

from .events import Event
from .rules import RuleSpec

KIND_FORBIDDEN = "forbidden-event"
KIND_MISSING = "missing-requirement"

COMPLETENESS_OPEN = "open"
COMPLETENESS_WATERMARK = "watermark"
COMPLETENESS_TERMINATION = "termination"


@dataclass(frozen=True)
class Witness:
    rule: RuleSpec
    kind: str  # KIND_FORBIDDEN | KIND_MISSING
    events: tuple[Event, ...]  # trigger events, then the offending event if any
    window_end: float
    completeness: str  # COMPLETENESS_* constant

    def verify(self) -> bool:
        """Independently re-check that this witness proves a violation."""
        rule = self.rule
        evs = sorted(self.events, key=lambda e: (e.ts, e.id))
        k = len(rule.prefix)
        if len(evs) < k:
            return False
        trigger, extra = evs[:k], evs[k:]
        if tuple(e.symbol for e in trigger) != tuple(rule.prefix):
            return False
        t0 = trigger[-1].ts
        if abs(self.window_end - (t0 + rule.window)) > 1e-9:
            return False
        if self.kind == KIND_FORBIDDEN:
            if len(extra) != 1:
                return False
            bad = extra[0]
            return bad.symbol in rule.forbid_set and t0 < bad.ts <= self.window_end
        if self.kind == KIND_MISSING:
            if rule.require is None:
                return False
            if self.completeness not in (
                COMPLETENESS_WATERMARK,
                COMPLETENESS_TERMINATION,
            ):
                return False
            return not any(
                e.symbol == rule.require and t0 < e.ts <= self.window_end for e in evs
            )
        return False

    def to_script(self) -> str:
        """Render a self-contained, independently executable checker."""
        payload = {
            "rule": {
                "name": self.rule.name,
                "prefix": list(self.rule.prefix),
                "require": self.rule.require,
                "forbid": sorted(self.rule.forbid),
                "window": self.rule.window,
            },
            "kind": self.kind,
            "window_end": self.window_end,
            "completeness": self.completeness,
            "events": [
                {"id": e.id, "ts": e.ts, "symbol": e.symbol} for e in self.events
            ],
        }
        return _SCRIPT_TEMPLATE.replace("__PAYLOAD__", repr(json.dumps(payload)))


_SCRIPT_TEMPLATE = '''\
"""Self-contained counterexample checker (auto-generated).

Run: python3 <this file>.  Prints VIOLATION CONFIRMED and exits 0 when the
embedded witness proves the violation, otherwise exits 1.
"""

import json
import sys

DATA = json.loads(__PAYLOAD__)


def main():
    rule = DATA["rule"]
    evs = sorted(DATA["events"], key=lambda e: (e["ts"], e["id"]))
    k = len(rule["prefix"])
    ok = len(evs) >= k
    trigger = extra = None
    if ok:
        trigger, extra = evs[:k], evs[k:]
        ok = tuple(e["symbol"] for e in trigger) == tuple(rule["prefix"])
    if ok:
        t0 = trigger[-1]["ts"]
        ok = abs(DATA["window_end"] - (t0 + rule["window"])) <= 1e-9
    if ok and DATA["kind"] == "forbidden-event":
        ok = (
            len(extra) == 1
            and extra[0]["symbol"] in rule["forbid"]
            and t0 < extra[0]["ts"] <= DATA["window_end"]
        )
    elif ok and DATA["kind"] == "missing-requirement":
        req = rule["require"]
        ok = DATA["completeness"] in ("watermark", "termination") and not any(
            e["symbol"] == req and t0 < e["ts"] <= DATA["window_end"] for e in evs
        )
    else:
        ok = False
    print("VIOLATION CONFIRMED" if ok else "WITNESS REJECTED")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
'''
