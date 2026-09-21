#!/usr/bin/env python3.11
"""运行样例：展开并执行 examples/sample.macro。

输出三部分：程序运行结果、展开统计、以及宏展开节点的
（调用点, 定义点）双重源码映射（均为 UTF-8 字节区间）。
"""
from __future__ import annotations

import pathlib
import sys

from macrolang import (
    DepthLimitExceeded,
    Interpreter,
    describe,
    expand_source,
    run_source,
    walk,
)

SAMPLE = pathlib.Path(__file__).parent / "examples" / "sample.macro"
MAX_MAP_LINES = 14


def main() -> None:
    source = SAMPLE.read_text(encoding="utf-8")
    raw = source.encode("utf-8")

    forms, expander = expand_source(source)

    print("=== 程序输出 ===")
    Interpreter(out=sys.stdout).run(forms)

    print("\n=== 展开统计 ===")
    print(f"宏定义数量: {len(expander.macros)}")
    print(f"展开产生节点数: {expander.ctx.nodes_created}")

    print("\n=== 双重源码映射（调用点 <- 定义点，UTF-8 字节区间） ===")
    shown = 0
    for form in forms:
        for node in walk(form):
            if not node.provenance:
                continue
            record = node.provenance[-1]  # 最内层一次展开
            call_text = raw[record.call_span.start : record.call_span.end].decode()
            def_text = raw[record.def_span.start : record.def_span.end].decode()
            print(
                f"{describe(node):<14} 宏 {record.macro_name}: "
                f"调用[{record.call_span}] {call_text!r} "
                f"<- 定义[{record.def_span}] {def_text!r}"
            )
            shown += 1
            if shown >= MAX_MAP_LINES:
                print("...（其余映射从略）")
                break

    print("\n=== 递归宏超限演示（max_depth=6） ===")
    try:
        run_source("(defmacro inf (x) (inf x))\n(inf 1)", max_depth=6)
    except DepthLimitExceeded as exc:
        print(exc)


if __name__ == "__main__":
    main()
