"""Command line interface for the (hierarchical) DRR schedulers.

Usage:
  python3.11 cli.py run   --config CFG.json [--rounds N] [--out OUT.jsonl]
  python3.11 cli.py hrun  --config CFG.json [--rounds N] [--out OUT.jsonl]
                          [--snapshot-in SNAP.json] [--snapshot-out SNAP.json]
  python3.11 cli.py check --config CFG.json [--max-steps N] [--out OUT.json]

All output is JSON Lines (run/hrun) or a single JSON object (check) on
stdout unless --out is given.  See README.md for the config schema.
"""
from __future__ import annotations

import argparse
import json
import sys

from drr_core import DRRScheduler
from hdr_core import HierDRR
from starvation import check_starvation


def _emit(lines, out_path):
    text = "\n".join(json.dumps(x, ensure_ascii=False) for x in lines) + "\n"
    if out_path:
        with open(out_path, "w") as f:
            f.write(text)
    else:
        sys.stdout.write(text)


def main(argv=None) -> int:
    p = argparse.ArgumentParser(prog="drr", description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)

    def common(sp):
        sp.add_argument("--config", required=True, help="JSON config file")
        sp.add_argument("--out", help="output file (default: stdout)")

    sp = sub.add_parser("run", help="single-level DRR simulation")
    common(sp)
    sp.add_argument("--rounds", type=int, help="override config 'rounds'")

    sp = sub.add_parser("hrun", help="hierarchical (tree) DRR simulation")
    common(sp)
    sp.add_argument("--rounds", type=int, help="override config 'rounds'")
    sp.add_argument("--snapshot-in", help="restore scheduler state from snapshot JSON")
    sp.add_argument("--snapshot-out", help="write final scheduler state snapshot JSON")

    sp = sub.add_parser("check", help="bounded exhaustive starvation check (hierarchical)")
    common(sp)
    sp.add_argument("--max-steps", type=int, default=10000)

    args = p.parse_args(argv)

    if args.cmd == "run":
        cfg = json.load(open(args.config))
        sched = DRRScheduler.from_config(cfg)
        lines = sched.run(args.rounds or cfg.get("rounds", 10), cfg.get("events"))
        _emit(lines, args.out)
        return 0

    if args.cmd == "hrun":
        if args.snapshot_in:
            sched = HierDRR.from_snapshot(json.load(open(args.snapshot_in)))
            cfg = json.load(open(args.config)) if args.config else {}
        else:
            cfg = json.load(open(args.config))
            sched = HierDRR.from_config(cfg)
        lines = sched.run(args.rounds or cfg.get("rounds", 10), cfg.get("events"))
        _emit(lines, args.out)
        if args.snapshot_out:
            with open(args.snapshot_out, "w") as f:
                json.dump(sched.to_snapshot(), f, indent=2)
        return 0

    if args.cmd == "check":
        cfg = json.load(open(args.config))
        sched = HierDRR.from_config(cfg)
        result = check_starvation(sched, max_steps=args.max_steps)
        # keep stdout readable: full counterexample trace only via --out
        summary = {k: v for k, v in result.items() if k != "counterexample"}
        if result.get("counterexample"):
            summary["counterexample_rounds"] = len(result["counterexample"]["cycle_trace"])
        print(json.dumps(summary, ensure_ascii=False))
        if args.out:
            with open(args.out, "w") as f:
                json.dump(result, f, indent=2)
        return 1 if result["result"] == "STARVATION" else 0

    return 2


if __name__ == "__main__":
    raise SystemExit(main())
