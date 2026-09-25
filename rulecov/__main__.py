"""CLI: analyze a rule spec, or verify (replay) a previously emitted report.

    python -m rulecov analyze SPEC.json [-o REPORT.json] [--budget N]
    python -m rulecov verify  SPEC.json REPORT.json
"""
from __future__ import annotations

import argparse
import json
import sys

from .analyze import analyze
from .evidence import verify_report
from .model import SpecError, load_spec


def _load_json(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def _summarize(report, out):
    spec = report["spec"]
    budget = report["budget"]
    print(f"spec: {spec['id']} v{spec['version']} (sha256: {spec['sha256'][:12]}...)",
          file=out)
    print(f"budget: used {budget['used']} / {budget['limit']} "
          f"(exhausted: {budget['exhausted']})", file=out)
    phases = " ".join(f"{k}={v}" for k, v in report["phases"].items())
    print(f"phases: {phases}", file=out)
    print(f"complete: {report['complete']}", file=out)
    print(f"findings: {len(report['findings'])}", file=out)
    for finding in report["findings"]:
        kind = finding["kind"]
        if kind == "unreachable_rule":
            covered = ", ".join(r["id"] for r in finding["covered_by"])
            print(f"  {finding['finding_id']}: rule {finding['rule']['id']} "
                  f"fully covered by [{covered}]", file=out)
        elif kind == "partial_overlap":
            ce = finding["counterexample"]["input"]
            print(f"  {finding['finding_id']}: {finding['low_rule']['id']} "
                  f"shadowed by {finding['high_rule']['id']} e.g. {ce}",
                  file=out)
        else:
            ce = finding["counterexample"]["input"]
            print(f"  {finding['finding_id']}: {finding['rules'][0]['id']} vs "
                  f"{finding['rules'][1]['id']} at priority "
                  f"{finding['priority']} e.g. {ce}", file=out)


def main(argv=None):
    parser = argparse.ArgumentParser(prog="rulecov", description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)

    p_an = sub.add_parser("analyze", help="analyze a rule spec")
    p_an.add_argument("spec")
    p_an.add_argument("-o", "--out", help="write the JSON report here")
    p_an.add_argument("--budget", type=int, default=100000,
                      help="max number of set operations (default 100000)")

    p_ve = sub.add_parser("verify", help="replay and check a report")
    p_ve.add_argument("spec")
    p_ve.add_argument("report")

    args = parser.parse_args(argv)
    try:
        spec = load_spec(_load_json(args.spec))
    except (SpecError, KeyError, TypeError) as exc:
        print(f"error: invalid spec: {exc}", file=sys.stderr)
        return 2

    if args.command == "analyze":
        report = analyze(spec, args.budget)
        _summarize(report, sys.stdout)
        text = json.dumps(report, indent=2, ensure_ascii=False,
                          sort_keys=True)
        if args.out:
            with open(args.out, "w", encoding="utf-8") as fh:
                fh.write(text + "\n")
            print(f"report written to {args.out}", file=sys.stderr)
        return 0 if report["complete"] else 1

    report = _load_json(args.report)
    problems = verify_report(spec, report)
    if problems:
        print("verification FAILED:", file=sys.stderr)
        for problem in problems:
            print(f"  - {problem}", file=sys.stderr)
        return 1
    print(f"verification OK: report {report['report_id']} replays "
          f"({report['budget']['used']} steps)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
