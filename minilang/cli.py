"""Command-line interface: analyze and/or run a minilang source file."""
from __future__ import annotations

import argparse
from pathlib import Path

from .analysis import analyze
from .cfg import build_cfg
from .interpreter import RuntimeFailure, run
from .lexer import LexError
from .parser import ParseError, parse
from .source import line_col


def _pos(source: str, start: int, end: int) -> str:
    line, col = line_col(source, start)
    return f"bytes {start}..{end}, line {line} col {col}"


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        prog="minilang",
        description="CFG-based dataflow analysis and interpreter for minilang",
    )
    ap.add_argument("file", help="source file (UTF-8)")
    ap.add_argument("--run", action="store_true", help="interpret the program")
    ap.add_argument("--cfg", action="store_true", help="print CFG nodes and edges")
    ap.add_argument(
        "--sets", action="store_true", help="print dataflow sets per CFG node"
    )
    args = ap.parse_args(argv)

    source = Path(args.file).read_text(encoding="utf-8")
    try:
        program = parse(source)
    except (LexError, ParseError) as exc:
        print(f"error: {exc} ({_pos(source, exc.start, exc.end)})")
        return 1

    cfg = build_cfg(program)
    result = analyze(cfg)

    for diag in result.diagnostics:
        print(f"{diag.level}: {diag.message} ({_pos(source, diag.start, diag.end)})")

    if args.cfg:
        print("\nCFG:")
        for node in cfg.nodes:
            mark = "" if node.id in result.reachable else " [unreachable]"
            succs = " ".join(f"n{s.id}" for s in node.succs) or "-"
            print(f"  n{node.id} {node.label}{mark} -> {succs}")

    if args.sets:
        print("\nDataflow sets:")
        for node in cfg.nodes:
            if node.stmt is None:
                continue
            reaching = sorted(result.reaching_in[node.id])
            assigned = sorted(result.assigned_in[node.id])
            print(f"  n{node.id} {node.label}:")
            print(f"    reaching_in            = {reaching}")
            print(f"    definitely_assigned_in = {assigned}")

    if args.run:
        try:
            run(program)
        except RuntimeFailure as exc:
            print(f"runtime error: {exc} ({_pos(source, exc.start, exc.end)})")
            return 1

    return 1 if any(d.level == "error" for d in result.diagnostics) else 0
