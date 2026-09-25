"""Run the synthetic sample: prune, export, verify, check necessity.

Usage: python3 run_sample.py
"""
from __future__ import annotations

import hashlib
import json

from proofpack import build_export, check_necessity, export_bytes, verify_package
from proofpack.sample import GOALS, build_sample_kb


def main() -> None:
    kb = build_sample_kb()
    print(f"Knowledge base: {len(kb.facts)} facts, {len(kb.rules)} rules")
    print(f"Root goals: {list(GOALS)}")
    print()

    export = build_export(kb, GOALS)
    result = export.result
    print(f"Search status : {result.status}")
    print(f"Cost          : public_facts={result.cost.public_facts}, size={result.cost.size}")
    print(f"Bounds        : lower={result.lower.as_tuple()}, upper={result.upper.as_tuple()}")
    print()

    blob = export.to_bytes()
    print("Export package (canonical JSON):")
    print(json.dumps(export.package, indent=2, sort_keys=True))
    print()

    report = verify_package(blob)
    print(f"Offline verification: ok={report.ok} errors={report.errors}")
    print(f"Derived goals: {sorted(report.derived_goals)}")

    necessity = check_necessity(blob)
    print(f"Necessity (remove-any-item fails): ok={necessity.ok} violations={necessity.violations}")

    again = export_bytes(kb, GOALS)
    print(f"Deterministic re-export identical: {blob == again}")
    print(f"Package sha256: {hashlib.sha256(blob).hexdigest()}")


if __name__ == "__main__":
    main()
