"""命令行入口。

用法：
  python -m cfg_analyzer                      # 运行全部内置样例
  python -m cfg_analyzer --sample NAME        # 运行指定样例
  python -m cfg_analyzer --json FILE          # 诊断 JSON 文法文件
  python -m cfg_analyzer --save-witnesses DIR # 导出可独立执行的见证脚本
"""
from __future__ import annotations

import argparse
import os
import sys

from .grammar import Grammar, GrammarError
from .report import analyze, format_report
from .samples import SAMPLES
from .witness import emit_all


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="cfg_analyzer", description="CFG 诊断工具")
    parser.add_argument("--sample", choices=sorted(SAMPLES), help="运行指定内置样例")
    parser.add_argument("--json", metavar="FILE", help="诊断 JSON 文法文件")
    parser.add_argument("--enum-depth", type=int, default=8, help="有界枚举深度（默认 8）")
    parser.add_argument("--save-witnesses", metavar="DIR",
                        help="把可独立执行的见证脚本写入目录 DIR")
    args = parser.parse_args(argv)

    targets: list[tuple[str, str, Grammar]] = []
    if args.json:
        try:
            targets.append((args.json, "来自 JSON 文件", Grammar.from_json(args.json)))
        except (GrammarError, OSError, ValueError) as exc:
            print(f"输入错误: {exc}", file=sys.stderr)
            return 2
    elif args.sample:
        desc, g = SAMPLES[args.sample]
        targets.append((args.sample, desc, g))
    else:
        for name, (desc, g) in SAMPLES.items():
            targets.append((name, desc, g))

    for name, desc, g in targets:
        print(f"\n{'#' * 60}")
        print(f"# 样例: {name}")
        print(f"# {desc}")
        print(f"{'#' * 60}")
        print(g)
        print()
        diag = analyze(g, enum_depth=args.enum_depth)
        print(format_report(diag))
        if args.save_witnesses:
            os.makedirs(args.save_witnesses, exist_ok=True)
            for fname, text in emit_all(diag).items():
                path = os.path.join(args.save_witnesses, f"{name.replace('/', '_')}__{fname}")
                with open(path, "w", encoding="utf-8") as fh:
                    fh.write(text)
                print(f"  [见证脚本已导出] {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
