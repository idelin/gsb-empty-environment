"""Command line interface: check / verify / replay / strengthen / demo."""

from __future__ import annotations

import argparse
import json
import os
import sys

from .certificate import (build_certificate, replay_certificate,
                          save_certificate, verify_certificate)
from .checker import InvariantChecker
from .incremental import IncrementalSession
from .model import load_model
from .reference import cross_check

_EXAMPLES_DIR = os.path.join(os.path.dirname(os.path.dirname(
    os.path.abspath(__file__))), "examples")


def _fmt_cube(cube):
    if not cube:
        return "(true)"
    return " & ".join(("%s" if v else "~%s") % k for k, v in sorted(cube.items()))


def _print_result(result):
    undecided = result.verdict == "UNKNOWN"
    print("verdict: %s" % result.verdict)
    print("  invariant:      %s" % result.invariant)
    print("  init empty:     %s" % result.init_empty)
    base = "undecided" if undecided else ("ok" if result.base_ok else "FAILED")
    step = "undecided" if undecided else ("ok" if result.step_ok else "FAILED")
    print("  base case:      %s" % base)
    if result.base_cex:
        print("  base cex:       %s" % _fmt_cube(result.base_cex))
    print("  inductive step: %s" % step)
    if result.step_cex:
        print("  step cex src:   %s" % _fmt_cube(result.step_cex.state))
        print("  step cex dst:   %s" % _fmt_cube(result.step_cex.next_state))
        print("  cex source is:  %s" % result.step_cex.reachability)
    if result.reachable_complete:
        print("  reachable:      %s states (complete)" % result.reachable_states)
        print("  holds on reachable: %s" % result.holds_on_reachable)
    else:
        print("  reachable:      undecided (search incomplete)")
    if result.unknown_reason:
        print("  undecided:      %s" % result.unknown_reason)
    for note in result.notes:
        print("  note: %s" % note)
    print("  stats: %s" % result.stats)


def cmd_check(args):
    model = load_model(args.model)
    order = args.order.split(",") if args.order else None
    checker = InvariantChecker(model, variable_order=order,
                               max_nodes=args.max_nodes, max_ops=args.max_ops,
                               max_reach_iter=args.max_reach_iter)
    invariant = args.invariant if args.invariant else None
    result = checker.check(invariant)
    _print_result(result)
    if args.reference:
        mismatches = cross_check(model, result, invariant=invariant)
        if mismatches:
            print("reference cross-check MISMATCH:")
            for m in mismatches:
                print("  - %s" % m)
            return 2
        print("reference cross-check: agrees with exhaustive enumeration")
    if args.cert:
        cert = build_certificate(model, result)
        save_certificate(cert, args.cert)
        print("certificate written to %s" % args.cert)
    return 0 if result.verdict == "PASS" else 1


def cmd_verify(args):
    report = verify_certificate(args.cert)
    for c in report["checks"]:
        mark = "ok " if c["ok"] else "FAIL"
        detail = (" -- " + c["detail"]) if c["detail"] else ""
        print("[%s] %s%s" % (mark, c["check"], detail))
    print("certificate %s" % ("VALID" if report["ok"] else "INVALID"))
    return 0 if report["ok"] else 1


def cmd_replay(args):
    for line in replay_certificate(args.cert):
        print(line)
    return 0


def cmd_strengthen(args):
    model = load_model(args.model)
    session = IncrementalSession(model, max_nodes=args.max_nodes,
                                 max_ops=args.max_ops)
    if args.invariant:
        session.invariant = args.invariant
    final = session.auto_strengthen(max_rounds=args.rounds)
    for i, (inv, res) in enumerate(session.history):
        print("round %d: %-40s -> %s" % (i, inv, res.verdict))
    print("final candidate: %s" % session.invariant)
    print("final verdict:   %s" % (final.verdict if final else "none"))
    return 0 if final and final.verdict == "PASS" else 1


def cmd_demo(args):
    examples = sorted(f for f in os.listdir(_EXAMPLES_DIR) if f.endswith(".json"))
    rc = 0
    for name in examples:
        path = os.path.join(_EXAMPLES_DIR, name)
        print("=" * 70)
        print("example: %s" % name)
        model = load_model(path)
        result = InvariantChecker(model).check()
        _print_result(result)
        mismatches = cross_check(model, result)
        print("  reference: %s" % ("agrees" if not mismatches else mismatches))
        if mismatches:
            rc = 2
    return rc


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="invcheck",
        description="Symbolic (BDD) candidate invariant checker")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("check", help="check the candidate invariant of a model")
    p.add_argument("model")
    p.add_argument("--invariant", help="override the candidate invariant")
    p.add_argument("--order", help="comma-separated BDD variable order")
    p.add_argument("--max-nodes", type=int, default=None)
    p.add_argument("--max-ops", type=int, default=None)
    p.add_argument("--max-reach-iter", type=int, default=100000)
    p.add_argument("--reference", action="store_true",
                   help="cross-check against exhaustive enumeration (small domains)")
    p.add_argument("--cert", help="write an independently checkable certificate")
    p.set_defaults(fn=cmd_check)

    p = sub.add_parser("verify", help="independently verify a certificate")
    p.add_argument("cert")
    p.set_defaults(fn=cmd_verify)

    p = sub.add_parser("replay", help="replay the counterexample in a certificate")
    p.add_argument("cert")
    p.set_defaults(fn=cmd_replay)

    p = sub.add_parser("strengthen", help="incrementally strengthen the candidate")
    p.add_argument("model")
    p.add_argument("--invariant", help="starting candidate (default: model's)")
    p.add_argument("--rounds", type=int, default=16)
    p.add_argument("--max-nodes", type=int, default=None)
    p.add_argument("--max-ops", type=int, default=None)
    p.set_defaults(fn=cmd_strengthen)

    p = sub.add_parser("demo", help="run all bundled examples")
    p.set_defaults(fn=cmd_demo)

    args = parser.parse_args(argv)
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main())
