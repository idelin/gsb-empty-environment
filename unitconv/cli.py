"""Executable command-line interface for the unit conversion graph.

Run ``python3.11 -m unitconv --help`` for the command overview.  State lives
in a JSON store (``unitconv_store.json`` by default); all mutating commands
are atomic -- a rejected batch leaves the file untouched.
"""

from __future__ import annotations

import argparse
import json
import sys
from fractions import Fraction
from typing import List, Optional, Sequence

from . import __version__
from .core import (
    ContradictionError,
    DimensionConflictError,
    DimensionMismatchError,
    NoConversionPathError,
    StatementError,
    UnknownUnitError,
    UnitConvError,
    VersionedGraph,
    format_factor,
    parse_factor,
)
from .store import (
    DEFAULT_STORE_PATH,
    append_batch,
    init_store,
    load_store,
)

# Exit codes -- stable and machine-checkable.
EXIT_OK = 0
EXIT_INVALID = 2
EXIT_CONTRADICTION = 3
EXIT_DIMENSION_MISMATCH = 4
EXIT_NO_PATH = 5
EXIT_UNKNOWN_UNIT = 6
EXIT_NO_STORE = 7
EXIT_STORE_EXISTS = 8


def _parse_amount(text: str) -> Fraction:
    token = text.strip()
    if token == "0":
        return Fraction(0)
    value = parse_factor(token)
    return value


def _path_json(path):
    return [
        {"unit": unit, "edge": None if edge is None else format_factor(edge)}
        for unit, edge in path
    ]


def _build_parser() -> argparse.ArgumentParser:
    # SUPPRESS defaults: a value given *before* the subcommand is kept,
    # while the real defaults live on the top-level parser below.
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("-f", "--file", default=argparse.SUPPRESS,
                        help="path to the JSON store "
                             "(default: %(default)s)")
    common.add_argument("--json", action="store_true",
                        default=argparse.SUPPRESS,
                        help="emit machine-readable JSON")

    parser = argparse.ArgumentParser(
        prog="python -m unitconv",
        description="Transactional exact-rational unit conversions.")
    parser.add_argument("-f", "--file", default=DEFAULT_STORE_PATH,
                        help="path to the JSON store "
                             "(default: %(default)s)")
    parser.add_argument("--json", action="store_true",
                        help="emit machine-readable JSON")
    parser.add_argument("--version", action="version",
                        version=f"unitconv {__version__}")
    sub = parser.add_subparsers(dest="command", required=True)

    p_init = sub.add_parser("init", parents=[common],
                            help="create an empty store file")
    p_init.set_defaults(handler=_cmd_init)

    p_batch = sub.add_parser(
        "batch", parents=[common],
        help="atomically add one batch of statements")
    p_batch.add_argument("statements", nargs="*",
                         help="statements, e.g. 'A = 2 B' or 'dim: LEN m'")
    p_batch.add_argument("--from-file", metavar="PATH",
                         help="read statements (one per line; # comments "
                              "allowed) in addition to positional ones")
    p_batch.set_defaults(handler=_cmd_batch)

    p_query = sub.add_parser(
        "query", parents=[common],
        help="convert an amount between two units")
    p_query.add_argument("source")
    p_query.add_argument("target")
    p_query.add_argument("amount", nargs="?", default="1",
                         help="amount in source units (exact, default 1)")
    p_query.add_argument("-v", "--at-version", type=int, default=None,
                         help="answer against a retained historical version")
    p_query.set_defaults(handler=_cmd_query)

    p_versions = sub.add_parser(
        "versions", parents=[common],
        help="list retained versions (old queries can be replayed)")
    p_versions.set_defaults(handler=_cmd_versions)

    p_rules = sub.add_parser(
        "rules", parents=[common],
        help="list committed statement strings of a version")
    p_rules.add_argument("-v", "--at-version", type=int, default=None)
    p_rules.set_defaults(handler=_cmd_rules)

    p_units = sub.add_parser(
        "units", parents=[common],
        help="list known units and their dimension labels")
    p_units.add_argument("-v", "--at-version", type=int, default=None)
    p_units.set_defaults(handler=_cmd_units)

    return parser


# ---------------------------------------------------------------------------
# Command handlers
# ---------------------------------------------------------------------------


def _cmd_init(args) -> int:
    init_store(args.file)
    if args.json:
        print(json.dumps({"status": "initialised", "file": args.file},
                         ensure_ascii=False))
    else:
        print(f"initialised empty store at {args.file}")
    return EXIT_OK


def _collect_statements(args) -> List[str]:
    statements: List[str] = list(args.statements)
    if getattr(args, "from_file", None):
        with open(args.from_file, "r", encoding="utf-8") as handle:
            for line in handle:
                line = line.strip()
                if line and not line.startswith("#"):
                    statements.append(line)
    if not statements:
        raise StatementError(
            "batch requires at least one statement "
            "(positional args or --from-file)")
    return statements


def _cmd_batch(args) -> int:
    statements = _collect_statements(args)
    _, info = append_batch(args.file, statements)
    if args.json:
        print(json.dumps({
            "status": "committed",
            "version": info.version,
            "timestamp": info.timestamp,
            "statement_count": info.statement_count,
            "statements": statements,
        }, ensure_ascii=False, indent=2))
    else:
        print(f"committed {info.statement_count} statement(s) "
              f"-> version {info.version}"
              + (f" at {info.timestamp}" if info.timestamp else ""))
    return EXIT_OK


def _cmd_query(args) -> int:
    amount = _parse_amount(args.amount)
    graph: VersionedGraph = load_store(args.file)
    result = graph.convert(args.source, args.target, amount,
                           version=args.at_version)
    used_version = args.at_version if args.at_version is not None \
        else graph.version
    if args.json:
        print(json.dumps({
            "source": result.source,
            "target": result.target,
            "amount": format_factor(result.amount),
            "factor": format_factor(result.factor),
            "result": format_factor(result.result),
            "path": _path_json(result.path),
            "version": used_version,
        }, ensure_ascii=False, indent=2))
    else:
        print(f"{format_factor(result.amount)} {result.source} = "
              f"{format_factor(result.result)} {result.target}")
        print(f"factor: 1 {result.source} = {format_factor(result.factor)} "
              f"{result.target}")
        print(f"path: {result.render_path()}")
        print(f"version: {used_version}")
    return EXIT_OK


def _cmd_versions(args) -> int:
    graph = load_store(args.file)
    if args.json:
        print(json.dumps([
            {"version": 0, "timestamp": None, "statement_count": 0,
             "note": "empty initial graph"}
        ] + [
            {"version": info.version, "timestamp": info.timestamp,
             "statement_count": info.statement_count}
            for info in graph.commits()
        ], ensure_ascii=False, indent=2))
    else:
        print("version  timestamp                  statements")
        print("0        (initial empty graph)      0")
        for info in graph.commits():
            ts = info.timestamp or "-"
            print(f"{info.version:<8} {ts:<26} {info.statement_count}")
    return EXIT_OK


def _cmd_rules(args) -> int:
    graph = load_store(args.file)
    state = graph.state(args.at_version)
    statements = [_statement_text(stmt) for stmt in state.rule_statements()]
    used_version = args.at_version if args.at_version is not None \
        else graph.version
    if args.json:
        print(json.dumps({"version": used_version,
                          "statements": statements},
                         ensure_ascii=False, indent=2))
    else:
        print(f"# rules at version {used_version}")
        for index, text in enumerate(statements, start=1):
            print(f"{index:>3}. {text}")
    return EXIT_OK


def _statement_text(stmt) -> str:
    if stmt[0] == "dim":
        _, dimension, unit = stmt
        return f"dim: {dimension} {unit}"
    _, unit_a, unit_b, factor = stmt
    return f"{unit_a} = {format_factor(factor)} {unit_b}"


def _cmd_units(args) -> int:
    graph = load_store(args.file)
    state = graph.state(args.at_version)
    rows = [(unit, state.dimension_of(unit) or "-") for unit in state.units()]
    used_version = args.at_version if args.at_version is not None \
        else graph.version
    if args.json:
        print(json.dumps({
            "version": used_version,
            "units": [{"unit": unit, "dimension": dim} for unit, dim in rows],
        }, ensure_ascii=False, indent=2))
    else:
        print(f"# units at version {used_version}")
        for unit, dim in rows:
            print(f"{unit:<12} {dim}")
    return EXIT_OK


# ---------------------------------------------------------------------------
# Entry point with stable exit codes
# ---------------------------------------------------------------------------


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = _build_parser()
    args = parser.parse_args(argv)
    try:
        return args.handler(args)
    except ContradictionError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_CONTRADICTION
    except DimensionMismatchError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_DIMENSION_MISMATCH
    except DimensionConflictError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_INVALID
    except NoConversionPathError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_NO_PATH
    except UnknownUnitError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_UNKNOWN_UNIT
    except StatementError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_INVALID
    except FileNotFoundError as exc:
        print(f"error: {exc} (run 'init' first)", file=sys.stderr)
        return EXIT_NO_STORE
    except FileExistsError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_STORE_EXISTS
    except (ValueError, UnitConvError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return EXIT_INVALID


if __name__ == "__main__":
    raise SystemExit(main())
