"""Synthetic end-to-end demo (all data is fabricated).

Scenario: is claim "server-room.temperature.safe" true?
  sensor-low v1 (level 1) says true,  valid the whole month
  audit-a  v1 (level 3) says false, valid Jan 01 - Jan 10
  audit-b  v1 (level 3) says true,  valid Jan 01 - Jan 10
  sensor-low v0 is a stale version, superseded by v1

Expected:
  Jan 05: two level-3 audits conflict -> UNDECIDED, sensor outranked
  Jan 20: audits expired -> level-1 sensor decides TRUE again

Run: python3.11 sample.py
"""

from __future__ import annotations

import itertools
import json
from datetime import datetime, timezone

from fact_engine import (
    DEFAULT_RULES,
    Fact,
    build_evidence,
    replay,
    resolve,
    verify,
)
from fact_engine.replay import dumps

UTC = timezone.utc
CLAIM = "server-room.temperature.safe"


def make_facts() -> list[Fact]:
    return [
        Fact("sensor-low", 0, CLAIM, True, 1,
             datetime(2026, 1, 1, tzinfo=UTC), None, "temp-sensor-7"),
        Fact("sensor-low", 1, CLAIM, True, 1,
             datetime(2026, 1, 1, tzinfo=UTC), None, "temp-sensor-7"),
        Fact("audit-a", 1, CLAIM, False, 3,
             datetime(2026, 1, 1, tzinfo=UTC),
             datetime(2026, 1, 10, tzinfo=UTC), "auditor-alice"),
        Fact("audit-b", 1, CLAIM, True, 3,
             datetime(2026, 1, 1, tzinfo=UTC),
             datetime(2026, 1, 10, tzinfo=UTC), "auditor-bob"),
    ]


def show(decision) -> None:
    print(f"  claim      : {decision.claim}")
    print(f"  at         : {decision.at.isoformat()}")
    print(f"  conclusion : {decision.value.value.upper()}")
    if decision.winners:
        print(f"  decided by : {', '.join(decision.winners)}")
    if decision.contesting:
        print(f"  contesting : {', '.join(decision.contesting)} (kept, not picked)")
    print("  suppressed evidence:")
    for item in decision.suppressed:
        print(f"    - {item.fact_ref}: {item.reason.value} ({item.detail})")
    print("  steps:")
    for step in decision.steps:
        print(f"    - {step.step_id} v{step.version} "
              f"[{step.rule_id} v{step.rule_version}] {step.summary}")
    print(f"  digest     : {decision.digest()}")


def main() -> None:
    facts = make_facts()
    t_inside = datetime(2026, 1, 5, tzinfo=UTC)
    t_after = datetime(2026, 1, 20, tzinfo=UTC)

    print("=== Query 1: inside the audit window (2026-01-05) ===")
    d1 = resolve(CLAIM, facts, t_inside)
    show(d1)

    print("\n=== Query 2: after the audits expired (2026-01-20) ===")
    d2 = resolve(CLAIM, facts, t_after)
    show(d2)

    print("\n=== Order independence: all input permutations ===")
    perms = list(itertools.permutations(facts))
    digests = {resolve(CLAIM, list(perm), t_inside).digest() for perm in perms}
    print(f"  distinct digests over {len(perms)} permutations: "
          f"{len(digests)} (expect 1)")

    print("\n=== Replayable evidence bundle (query 1) ===")
    evidence = build_evidence(CLAIM, facts, t_inside)
    payload = dumps(evidence)
    print(f"  bundle size: {len(payload)} bytes, format: {evidence['format']}")
    restored = json.loads(payload)
    print(f"  replay verify: {verify(restored)}")
    replayed = replay(restored)
    print(f"  replayed conclusion: {replayed.value.value.upper()}, "
          f"digest match: {replayed.digest() == evidence['digest']}")

    print("\n=== Tamper check: flip a fact value inside the bundle ===")
    tampered = json.loads(payload)
    tampered["facts"][2]["value"] = not tampered["facts"][2]["value"]
    print(f"  verify(tampered): {verify(tampered)} (expect False)")

    print("\n=== Ruleset applied ===")
    for rule in DEFAULT_RULES:
        print(f"  - {rule.rule_id} v{rule.version}: {rule.description}")


if __name__ == "__main__":
    main()
