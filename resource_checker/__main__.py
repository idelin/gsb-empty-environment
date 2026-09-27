"""CLI: run the checker over the bundled example models.

Usage:  python3.11 -m resource_checker
"""
from __future__ import annotations

from .analyzer import Analyzer
from .examples import CROSS_CHECKED, EXAMPLES
from .exhaustive import cross_check, enumerate_paths, ground_truth


def _print_finding(prefix: str, f) -> None:
    where = f.node
    if f.kind == "leak":
        where = f"{f.exit_kind} exit {f.node!r}" if f.exit_kind != "overwrite" else f.node
    res = f" of {f.resource}" if f.resource else ""
    print(f"  [{f.status}] {f.kind}{res} at {where}")
    print(f"      path: {f.path_str()}")
    if f.evidence:
        print(f"      evidence: {f.evidence}")


def main() -> int:
    for name, (factory, desc) in EXAMPLES.items():
        program = factory()
        report = Analyzer(program).run()
        print(f"=== {name}: {desc}")
        print(f"    verdict: {report.status} "
              f"(states explored: {report.states_explored}, complete: {report.complete})")
        for f in report.findings:
            _print_finding("  ", f)
        for f in report.filtered:
            print(f"  [filtered] {f.kind} of {f.resource} at {f.node}")
            print(f"      evidence: {f.evidence}")
        if not report.findings and not report.filtered:
            print("    (no findings)")
        print()

    print("=== exhaustive cross-check (analyzer vs. concrete ground truth)")
    ok_all = True
    for name in CROSS_CHECKED:
        program = EXAMPLES[name][0]()
        result = cross_check(program)
        truth = ground_truth(program)
        paths = len(enumerate_paths(program))
        status = "OK" if result.program_ok else "MISMATCH"
        ok_all = ok_all and result.program_ok
        print(f"  {name}: {status} "
              f"({paths} paths, {truth.runs} concrete runs, "
              f"{len(truth.errors)} errors, {len(truth.leaks)} leaks)")
        if not result.program_ok:
            print(f"      missed: errors={result.missed_errors} leaks={result.missed_leaks}")
            print(f"      spurious: errors={result.spurious_errors} leaks={result.spurious_leaks}")
    print(f"cross-check: {'all OK' if ok_all else 'FAILURES'}")
    return 0 if ok_all else 1


if __name__ == "__main__":
    raise SystemExit(main())
