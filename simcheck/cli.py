"""Command line interface: check / replay / enumerate."""

from __future__ import annotations

import argparse
import json
import sys

from .enumerate import enumerate_greatest_simulation
from .fixpoint import Budget, FAIL, PASS, UNDECIDED, greatest_simulation
from .lts import LTS
from .strategy import build_strategy, verify_witness, witness_dict

EXIT_PASS = 0
EXIT_FAIL = 1
EXIT_UNDECIDED = 2
EXIT_ERROR = 3


def _load_pair(args: argparse.Namespace) -> tuple[LTS, LTS]:
    return LTS.load(args.impl), LTS.load(args.spec)


def _budget_from_args(args: argparse.Namespace) -> Budget:
    return Budget(max_rounds=args.max_rounds, max_pair_checks=args.max_pair_checks)


def cmd_check(args: argparse.Namespace) -> int:
    impl, spec = _load_pair(args)
    result = greatest_simulation(
        impl,
        spec,
        mode=args.mode,
        divergence_sensitive=args.divergence_sensitive,
        budget=_budget_from_args(args),
    )
    print(f"mode={result.mode} divergence_sensitive={result.divergence_sensitive}")
    print(f"status={result.status} rounds={result.rounds} pair_checks={result.pair_checks}")
    print(f"reason: {result.reason}")
    if result.status == FAIL:
        graph = build_strategy(
            impl,
            spec,
            result.mode,
            result.divergence_sensitive,
            result.relation,
            result.rank,
            result.rounds,
        )
        document = witness_dict(graph)
        if args.witness:
            with open(args.witness, "w", encoding="utf-8") as handle:
                json.dump(document, handle, ensure_ascii=False, indent=2)
                handle.write("\n")
            print(f"counterexample strategy graph written to {args.witness}")
        else:
            print(json.dumps(document, ensure_ascii=False, indent=2))
        return EXIT_FAIL
    if result.status == UNDECIDED:
        return EXIT_UNDECIDED
    return EXIT_PASS


def cmd_replay(args: argparse.Namespace) -> int:
    impl, spec = _load_pair(args)
    with open(args.witness, "r", encoding="utf-8") as handle:
        witness = json.load(handle)
    errors = verify_witness(impl, spec, witness)
    if errors:
        print("replay FAILED:")
        for error in errors:
            print(f"  - {error}")
        return EXIT_FAIL
    print("replay OK: witness is a valid counterexample for the initial pair")
    return EXIT_PASS


def cmd_enumerate(args: argparse.Namespace) -> int:
    impl, spec = _load_pair(args)
    result = enumerate_greatest_simulation(
        impl,
        spec,
        mode=args.mode,
        divergence_sensitive=args.divergence_sensitive,
        max_relations=args.max_relations,
    )
    print(f"mode={result.mode} divergence_sensitive={result.divergence_sensitive}")
    print(f"status={result.status} universe={result.universe_size} "
          f"relations_checked={result.relations_checked} postfixed={result.postfixed_count}")
    print(f"reason: {result.reason}")
    if result.status == "ok":
        print(f"initial pair simulated: {result.initial_simulated}")
        print(f"greatest relation ({len(result.greatest)} pairs):")
        for pair in sorted(result.greatest):
            print(f"  {pair[0]}  <=  {pair[1]}")
        return EXIT_PASS if result.initial_simulated else EXIT_FAIL
    return EXIT_UNDECIDED


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="simcheck",
        description="Weak/branching simulation checker for finite LTS models "
        "(greatest fixpoint, replayable counterexample strategy graphs).",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    def add_common(p: argparse.ArgumentParser) -> None:
        p.add_argument("impl", help="implementation LTS (JSON)")
        p.add_argument("spec", help="specification LTS (JSON)")
        p.add_argument("--mode", choices=["weak", "branching"], default="weak")
        p.add_argument(
            "--divergence-sensitive",
            action="store_true",
            help="fail when the implementation may diverge (infinite tau) "
            "while the specification cannot",
        )

    check = sub.add_parser("check", help="run the greatest-fixpoint simulation check")
    add_common(check)
    check.add_argument("--max-rounds", type=int, default=None, help="iteration budget")
    check.add_argument("--max-pair-checks", type=int, default=None, help="pair-check budget")
    check.add_argument("--witness", default=None, help="write counterexample JSON here")
    check.set_defaults(func=cmd_check)

    replay = sub.add_parser("replay", help="replay/verify a counterexample witness")
    replay.add_argument("impl", help="implementation LTS (JSON)")
    replay.add_argument("spec", help="specification LTS (JSON)")
    replay.add_argument("witness", help="counterexample JSON produced by 'check'")
    replay.set_defaults(func=cmd_replay)

    enum = sub.add_parser("enumerate", help="exhaustive enumeration oracle (small models)")
    add_common(enum)
    enum.add_argument("--max-relations", type=int, default=1 << 20)
    enum.set_defaults(func=cmd_enumerate)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except (ValueError, OSError, json.JSONDecodeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_ERROR


if __name__ == "__main__":
    sys.exit(main())
