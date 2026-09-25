"""Runnable sample on synthetic data.

Scenario:
  rules: A and B jointly imply C ; D alone implies C
  facts: fA=>A, fB=>B, fD=>D
  1) C holds with two minimal supports {fA,fB} and {fD}
  2) retract B: the conjunctive rule stops, but D keeps C alive
  3) retract D: C is finally withdrawn
Repeated assertions of the same fact ID change nothing.
An ungrounded cycle X->Y->X never establishes X or Y.
The whole run is replayed byte-for-byte from the event log at the end.
"""
from __future__ import annotations

import json

from .tms import TruthMaintenanceSystem


def _show(tms: TruthMaintenanceSystem, title: str) -> None:
    print(f"--- {title}")
    for atom in ("A", "B", "C", "D", "X", "Y"):
        envs = tms.supports(atom)
        status = "BELIEVED" if envs else "absent  "
        pretty = [sorted(e) for e in envs]
        print(f"  {atom}: {status} minimal_supports={pretty} "
              f"count={tms.support_count(atom)}")
    steps = ", ".join(
        f"{s.rule_id}:{'on' if s.active else 'off'}@v{s.version}"
        for s in sorted(tms.steps().values(), key=lambda x: x.rule_id))
    print(f"  steps: {steps}")


def run() -> None:
    tms = TruthMaintenanceSystem()
    tms.add_rule("r_ab_implies_c", ("A", "B"), "C")
    tms.add_rule("r_d_implies_c", ("D",), "C")
    tms.add_rule("r_x_implies_y", ("X",), "Y")
    tms.add_rule("r_y_implies_x", ("Y",), "X")

    tms.assert_fact("fA", "A")
    tms.assert_fact("fB", "B")
    tms.assert_fact("fD", "D")
    _show(tms, "after asserting A, B, D: C has two independent supports")

    print("  explain(C):")
    print("   ", json.dumps(tms.explain("C"), ensure_ascii=False, sort_keys=True))

    tms.assert_fact("fB", "B")  # repeated assertion: no change
    print("  repeat assert fB -> C support count:", tms.support_count("C"),
          "(still 2, fact fB version:", tms.fact("fB").version, ")")

    tms.retract_fact("fB")
    _show(tms, "retract B: D still supports C, conjunctive step stops")

    tms.retract_fact("fD")
    _show(tms, "retract D too: no support left, C withdrawn")

    print("--- ungrounded cycle X->Y->X: X =",
          tms.believed("X"), ", Y =", tms.believed("Y"),
          "(both must be False)")

    evidence = tms.to_evidence()
    print("--- evidence log (stable IDs + versions), JSON:")
    print(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True))

    replayed = TruthMaintenanceSystem.from_evidence(evidence)
    same = replayed.snapshot() == tms.snapshot()
    print("--- replay:",
          "snapshot identical ->", same,
          "| C believed after replay:", replayed.believed("C"))


if __name__ == "__main__":
    run()
