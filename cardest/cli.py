"""Command line interface.

  python -m cardest run SPEC.json [--budget N] [--max-cells N]
                        [--resume STATE.json] [--save-state OUT.json] [--out OUT.json]
  python -m cardest verify SPEC.json RESULT.json
  python -m cardest estimate SPEC.json
  python -m cardest consistency SPEC.json
"""
from __future__ import annotations

import argparse
import json
import sys

from .constraints import check_consistency, find_minimal_conflict
from .engine import analyze
from .estimate import point_estimate
from .search import StaleStateError
from .spec import SpecError, load_spec, spec_fingerprint
from .witness import verify


def _dump(obj, path=None):
    text = json.dumps(obj, indent=2, sort_keys=False)
    if path:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    else:
        print(text)


def cmd_run(args):
    spec = load_spec(args.spec)
    resume_state = None
    if args.resume:
        with open(args.resume, "r", encoding="utf-8") as fh:
            resume_state = json.load(fh)
    try:
        result = analyze(
            spec,
            node_budget=args.budget,
            max_cells=args.max_cells,
            resume_state=resume_state,
        )
    except StaleStateError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    if args.save_state and "resume_state" in result:
        _dump(result["resume_state"], args.save_state)
    _dump(result, args.out)
    return 0


def cmd_verify(args):
    spec = load_spec(args.spec)
    with open(args.result, "r", encoding="utf-8") as fh:
        result = json.load(fh)
    fingerprint = result.get("spec_fingerprint")
    overall = {"spec_fingerprint_ok": True, "witnesses": {}}
    if fingerprint != spec_fingerprint(spec):
        overall["spec_fingerprint_ok"] = False
        overall["stale"] = True
        overall["detail"] = (
            "specification changed since this result was issued; "
            "all witnesses and bounds are invalid and must be re-derived"
        )
        _dump(overall)
        return 1
    ok = True
    for key in ("min", "max"):
        entry = result.get(key)
        if not entry:
            continue
        report = verify(spec, entry["witness"], entry["value"], fingerprint)
        overall["witnesses"][key] = report
        ok = ok and report["valid"]
    overall["valid"] = ok
    _dump(overall)
    return 0 if ok else 1


def cmd_estimate(args):
    spec = load_spec(args.spec)
    _dump(point_estimate(spec))
    return 0


def cmd_consistency(args):
    spec = load_spec(args.spec)
    budget = args.budget or 100000
    cons = check_consistency(spec, budget=budget)
    out = {"consistency": cons}
    if cons["status"] == "inconsistent":
        out["conflict"] = find_minimal_conflict(spec, budget=budget)
    _dump(out)
    return 0 if cons["status"] == "consistent" else 1


def main(argv=None):
    parser = argparse.ArgumentParser(prog="cardest")
    sub = parser.add_subparsers(dest="command", required=True)

    p_run = sub.add_parser("run", help="analyze a spec and emit certified bounds")
    p_run.add_argument("spec")
    p_run.add_argument("--budget", type=int, default=None)
    p_run.add_argument("--max-cells", type=int, default=None)
    p_run.add_argument("--resume", default=None)
    p_run.add_argument("--save-state", default=None)
    p_run.add_argument("--out", default=None)
    p_run.set_defaults(func=cmd_run)

    p_ver = sub.add_parser("verify", help="verify witnesses of a previous run")
    p_ver.add_argument("spec")
    p_ver.add_argument("result")
    p_ver.set_defaults(func=cmd_verify)

    p_est = sub.add_parser("estimate", help="heuristic point estimate (not a bound)")
    p_est.add_argument("spec")
    p_est.set_defaults(func=cmd_estimate)

    p_con = sub.add_parser("consistency", help="check statistic consistency")
    p_con.add_argument("spec")
    p_con.add_argument("--budget", type=int, default=None)
    p_con.set_defaults(func=cmd_consistency)

    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except SpecError as exc:
        print(f"spec error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
