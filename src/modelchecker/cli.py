"""Command-line interface: generate certificates and independently verify them."""

from __future__ import annotations

import argparse
import json
import sys
from typing import Optional, Sequence

from .checker import CheckOutcome, check_safety
from .models import list_models, load_model
from .verifier import VerificationVerdict, verify_certificate

EXIT_OK = 0
EXIT_INVALID = 1
EXIT_UNSAFE = 2
EXIT_UNKNOWN = 3


def _cmd_check(args: argparse.Namespace) -> int:
    model = load_model(args.model)
    result = check_safety(model, budget=args.budget)
    summary = {
        "model": model.name,
        "fingerprint": model.fingerprint,
        "outcome": result.outcome.value,
        "explored": len(result.explored),
        "expansions": result.expansions,
        "budget": args.budget,
    }
    if result.outcome is CheckOutcome.UNKNOWN:
        summary["reason"] = result.reason
        print(json.dumps(summary, indent=2))
        print(
            f"UNKNOWN: only {len(result.explored)} state(s) explored within "
            f"budget {args.budget}; no certificate emitted",
            file=sys.stderr,
        )
        return EXIT_UNKNOWN

    assert result.certificate is not None
    text = json.dumps(result.certificate, indent=2)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as handle:
            handle.write(text + "\n")
        summary["certificate_path"] = args.output
    print(json.dumps(summary, indent=2))
    if result.outcome is CheckOutcome.UNSAFE:
        print(f"UNSAFE: counterexample path {list(result.counterexample)}",
              file=sys.stderr)
        return EXIT_UNSAFE
    print("SAFE: reachability closure certificate", file=sys.stderr)
    return EXIT_OK


def _cmd_verify(args: argparse.Namespace) -> int:
    model = load_model(args.model)
    with open(args.certificate, "r", encoding="utf-8") as handle:
        certificate = json.load(handle)
    verdict, findings = verify_certificate(model, certificate)
    report = {
        "model": model.name,
        "fingerprint": model.fingerprint,
        "verdict": verdict.value,
        "certificate_kind": certificate.get("kind"),
        "findings": [
            {"code": f.code, "message": f.message, "detail": f.detail}
            for f in findings
        ],
    }
    print(json.dumps(report, indent=2))
    if verdict is VerificationVerdict.VALID:
        kind = certificate.get("kind")
        if kind == "counterexample":
            print("VALID counterexample: property is violated by the replayed path",
                  file=sys.stderr)
        else:
            print("VALID closure: initial-covered, successor-closed, property holds",
                  file=sys.stderr)
        return EXIT_OK
    print(f"{verdict.value.upper()}: certificate rejected", file=sys.stderr)
    return EXIT_INVALID


def _cmd_list(_args: argparse.Namespace) -> int:
    for name in list_models():
        print(name)
    return EXIT_OK


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="modelchecker",
        description="Finite-state safety model checker with verifiable certificates.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    check = sub.add_parser("check", help="run BFS and emit a certificate")
    check.add_argument("model", help="registered model name or path to model JSON")
    check.add_argument(
        "--budget",
        type=int,
        default=None,
        help="maximum successor expansions (default: unlimited)",
    )
    check.add_argument("-o", "--output", help="write certificate JSON to this path")
    check.set_defaults(func=_cmd_check)

    verify = sub.add_parser("verify", help="independently verify a certificate")
    verify.add_argument("model", help="registered model name or path to model JSON")
    verify.add_argument("certificate", help="path to certificate JSON")
    verify.set_defaults(func=_cmd_verify)

    listing = sub.add_parser("list", help="list registered models")
    listing.set_defaults(func=_cmd_list)
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
