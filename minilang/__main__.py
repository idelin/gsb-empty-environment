"""命令行入口：python3.11 -m minilang <文件> [--analyze-only | --run-only]"""
from __future__ import annotations

import argparse
import sys

from .analysis import analyze
from .cfg import CFGBuilder
from .interpreter import InterpError, Interpreter
from .lexer import LexError, tokenize
from .parser import ParseError, Parser


def line_col(text: str, byte_off: int) -> tuple[int, int]:
    """把 UTF-8 字节偏移换算成 (行, 列)，仅用于展示。"""
    prefix = text.encode("utf-8")[:byte_off].decode("utf-8", errors="ignore")
    line = prefix.count("\n") + 1
    col = len(prefix.rsplit("\n", 1)[-1]) + 1
    return line, col


def where(text: str, span: tuple[int, int]) -> str:
    line, col = line_col(text, span[0])
    return f"行 {line}:{col} 字节[{span[0]},{span[1]})"


def print_report(text: str, result) -> None:
    cfg = result.cfg
    print("== CFG ==")
    for b in cfg.blocks:
        succs = " ".join(s.label() for s in b.succs) or "-"
        mark = "" if b.id in result.reachable else "  [不可达]"
        pos = f" 字节[{b.span[0]},{b.span[1]})" if b.span else ""
        print(f"  {b.label():<4} {b.kind:<7}{pos} -> {succs}{mark}")

    print("== 到达定义 (reaching definitions, may/并集) ==")
    for b in cfg.blocks:
        if b.id not in result.reachable:
            continue
        fmt = lambda defs: "{" + ", ".join(f"{v}@b{d}" for v, d in sorted(defs)) + "}"
        print(f"  {b.label():<4} in={fmt(result.reach_in[b.id])} "
              f"out={fmt(result.reach_out[b.id])}")

    print("== 一定已定义 (definitely defined, must/交集) ==")
    for b in cfg.blocks:
        if b.id not in result.reachable:
            continue
        din = "{" + ", ".join(sorted(result.def_in[b.id])) + "}"
        dout = "{" + ", ".join(sorted(result.def_out[b.id])) + "}"
        print(f"  {b.label():<4} in={din} out={dout}")

    print("== 诊断 ==")
    if not result.diagnostics:
        print("  （无）")
    for d in result.diagnostics:
        print(f"  [{d.kind}] {where(text, d.span)}: {d.message}")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="minilang")
    ap.add_argument("file", help="源文件（UTF-8）")
    group = ap.add_mutually_exclusive_group()
    group.add_argument("--analyze-only", action="store_true", help="只做静态分析")
    group.add_argument("--run-only", action="store_true", help="只解释执行")
    args = ap.parse_args(argv)

    with open(args.file, encoding="utf-8") as f:
        text = f.read()

    try:
        program = Parser(tokenize(text)).parse_program()
    except (LexError, ParseError) as exc:
        print(f"语法错误: {exc} @ 字节[{exc.start},{exc.end})", file=sys.stderr)
        return 1

    result = analyze(program, CFGBuilder().build(program))
    if not args.run_only:
        print_report(text, result)

    if not args.analyze_only:
        if not args.run_only:
            print("== 运行 ==")
        try:
            Interpreter().run(program)
        except InterpError as exc:
            print(f"运行时错误: {exc} @ {where(text, exc.span)}", file=sys.stderr)
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
