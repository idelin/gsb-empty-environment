"""Command line interface.

    python -m invcheck check MODEL.json --inv CANDIDATE.txt [options]
    python -m invcheck replay CERTIFICATE.json
    python -m invcheck enumerate MODEL.json --inv CANDIDATE.txt

Candidate file format (one invariant, optional incremental refinements):

    invariant: !a
    refine: !b        # stage 2 checks (!a) & (!b)
    refine: ...       # further stages

Exit codes: 0 = verified, 1 = counterexample found, 2 = undecided/error.
"""

from __future__ import annotations

import argparse
import json
import random
import sys

from . import expr as exprmod
from .model import load_model
from .checker import InvariantChecker
from .refcheck import enumerate_check, compare
from .certificate import (
    build_certificate,
    save_certificate,
    load_certificate,
    replay_certificate,
)

EXIT_VERIFIED = 0
EXIT_FAILED = 1
EXIT_UNDECIDED = 2


def load_candidate(path):
    with open(path, "r", encoding="utf-8") as fh:
        text = fh.read()
    invariant = None
    refinements = []
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("invariant:"):
            invariant = line[len("invariant:"):].strip()
        elif line.startswith("refine:"):
            refinements.append(line[len("refine:"):].strip())
        elif invariant is None:
            invariant = line
        else:
            raise SystemExit(f"unexpected extra line in candidate file: {line!r}")
    if not invariant:
        raise SystemExit("candidate file must contain an invariant expression")
    return invariant, refinements


def _parse_permutation(spec, variables):
    if spec in (None, "none"):
        return None
    perm = list(variables)
    if spec == "reverse":
        perm.reverse()
        return perm
    if spec.startswith("random:"):
        rng = random.Random(int(spec.split(":", 1)[1]))
        rng.shuffle(perm)
        return perm
    raise SystemExit(f"unknown --permute value {spec!r}")


def _fmt_state(s):
    return "{" + ", ".join(f"{k}={int(v)}" for k, v in s.items()) + "}"


def _print_result(res, out):
    p = lambda *a: print(*a, file=out)
    p(f"  verdict:           {res.verdict}")
    p(f"  base case:         {res.base_status}")
    if res.base_counterexample is not None:
        p(f"    counterexample initial state: {_fmt_state(res.base_counterexample)}")
    p(f"  inductive step:    {res.inductive_status}")
    if res.inductive_counterexample is not None:
        s, sp = res.inductive_counterexample
        p("    counterexample (lexicographically minimal under the variable order):")
        p(f"      premise   s  = {_fmt_state(s)}   (invariant holds)")
        p(f"      successor s' = {_fmt_state(sp)}   (invariant violated)")
        if res.cex_source_reachable is not None:
            reach = "yes" if res.cex_source_reachable else "NO (unreachable state)"
            p(f"      premise reachable from init: {reach}")
    if res.reachable_checked:
        p(f"  reachable states:  {res.reachable_count}; "
          f"invariant holds on all reachable states: {res.holds_on_reachable}")
    else:
        p("  reachability:      undecided")
    for note in res.notes:
        p(f"  note: {note}")
    p(f"  bdd nodes: {res.stats.get('bdd_nodes')}")


def cmd_check(args):
    model = load_model(args.model)
    inv_text, refine_texts = load_candidate(args.inv)
    perm = _parse_permutation(args.permute, model.variables)
    try:
        inv_ast = exprmod.parse(inv_text)
        refinements = [(exprmod.parse(r), r) for r in refine_texts]
    except exprmod.ParseError as exc:
        raise SystemExit(f"candidate parse error: {exc}")
    known = set(model.variables)
    for text, ast in [(inv_text, inv_ast)] + [(r, a) for a, r in refinements]:
        unknown = exprmod.ast_vars(ast) - known
        if unknown:
            raise SystemExit(
                f"candidate {text!r} references unknown variables: {sorted(unknown)}"
            )
    checker = InvariantChecker(
        model,
        order_style=args.order,
        permutation=perm,
        node_limit=args.node_limit,
        time_limit=args.time_limit,
        reachability=not args.no_reachability,
    )
    stages = []
    ast, text = inv_ast, inv_text
    stages.append((text, ast, checker.check(ast, text)))
    for r_ast, r_text in refinements:
        ast = ("and", ast, r_ast)
        text = f"({text}) & ({r_text})"
        stages.append((text, ast, checker.check(ast, text)))

    final = None
    for i, (text, ast, res) in enumerate(stages, 1):
        print(f"== stage {i}: candidate {text} ==")
        _print_result(res, sys.stdout)
        if args.reference:
            enum = enumerate_check(model, ast, max_states=args.max_states)
            if enum["status"] != "decided":
                print(f"  reference enumeration: undecided ({enum['reason']})")
            else:
                mism = compare(res, enum)
                if mism:
                    print("  reference enumeration: MISMATCH: " + "; ".join(mism))
                else:
                    print("  reference enumeration: agrees (full state space)")
        print()
        final = (text, res)
    final_text, final_res = final
    if args.cert:
        cert = build_certificate(model, final_text, final_res)
        save_certificate(cert, args.cert)
        print(f"certificate written to {args.cert}")
    return {
        "verified": EXIT_VERIFIED,
        "base_case_failed": EXIT_FAILED,
        "inductive_failed": EXIT_FAILED,
    }.get(final_res.verdict, EXIT_UNDECIDED)


def cmd_replay(args):
    cert = load_certificate(args.cert)
    report = replay_certificate(cert)
    for c in report["checks"]:
        print(f"  [{'ok' if c['ok'] else 'FAIL'}] {c['name']}")
    status = "succeeded" if report["ok"] else "FAILED"
    print(f"replay {status} (certificate verdict: {report['verdict']})")
    return 0 if report["ok"] else 1


def cmd_enumerate(args):
    model = load_model(args.model)
    inv_text, _ = load_candidate(args.inv)
    enum = enumerate_check(model, exprmod.parse(inv_text), max_states=args.max_states)
    print(json.dumps(enum, indent=2))
    if enum["status"] != "decided":
        return EXIT_UNDECIDED
    return EXIT_VERIFIED if enum["verdict"] == "verified" else EXIT_FAILED


def build_parser():
    parser = argparse.ArgumentParser(
        prog="invcheck",
        description="Symbolic (BDD-based) invariant checking for finite-state models",
    )
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_check = sub.add_parser("check", help="symbolic base-case and inductive-step check")
    p_check.add_argument("model", help="model JSON file")
    p_check.add_argument("--inv", required=True, help="candidate invariant file")
    p_check.add_argument("--order", choices=["interleave", "block"],
                         default="interleave", help="variable order style")
    p_check.add_argument("--permute", default="none",
                         help="variable permutation: none | reverse | random:SEED")
    p_check.add_argument("--node-limit", type=int, default=None,
                         help="BDD node budget; exceeding it yields 'undecided'")
    p_check.add_argument("--time-limit", type=float, default=None,
                         help="seconds; exceeding it yields 'undecided'")
    p_check.add_argument("--no-reachability", action="store_true",
                         help="skip the reachable-state fixpoint")
    p_check.add_argument("--reference", action="store_true",
                         help="cross-check by full enumeration (small domains)")
    p_check.add_argument("--max-states", type=int, default=1 << 16,
                         help="enumeration limit for --reference")
    p_check.add_argument("--cert", default=None, help="write certificate JSON here")
    p_check.set_defaults(func=cmd_check)

    p_replay = sub.add_parser("replay", help="independently re-verify a certificate")
    p_replay.add_argument("cert", help="certificate JSON file")
    p_replay.set_defaults(func=cmd_replay)

    p_enum = sub.add_parser("enumerate", help="brute-force reference check (small domains)")
    p_enum.add_argument("model", help="model JSON file")
    p_enum.add_argument("--inv", required=True, help="candidate invariant file")
    p_enum.add_argument("--max-states", type=int, default=1 << 16)
    p_enum.set_defaults(func=cmd_enumerate)
    return parser


def main(argv=None):
    args = build_parser().parse_args(argv)
    return args.func(args)
