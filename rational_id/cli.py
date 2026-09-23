"""命令行入口：python -m rational_id "左式" "右式" """
from __future__ import annotations

import argparse

from .checker import IDENTICAL, NOT_IDENTICAL, UNKNOWN, VERDICT_ZH, check


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="rational_id",
        description="有理多项式表达式恒等判定：精确有理数计算，不依赖符号代数库")
    parser.add_argument("left", help="左表达式")
    parser.add_argument("right", help="右表达式")
    parser.add_argument("--term-budget", type=int, default=2000,
                        help="展开项数预算，超限返回未知（默认 2000）")
    parser.add_argument("--eval-budget", type=int, default=20000,
                        help="反例搜索的最大求值点数（默认 20000）")
    parser.add_argument("-q", "--quiet", action="store_true",
                        help="只输出结论与原因")
    args = parser.parse_args(argv)

    result = check(args.left, args.right,
                   term_budget=args.term_budget,
                   eval_budget=args.eval_budget)

    print(f"结论：{VERDICT_ZH[result.verdict]}")
    print(f"原因：{result.reason}")
    if not args.quiet:
        if result.forms:
            names = result.forms["variables"]
            print(f"变量：{', '.join(names) if names else '（无）'}")
            for label, key in (("左式", "left"), ("右式", "right")):
                form = result.forms[key]
                print(f"{label}规范化：({form['num']}) / ({form['den']})，"
                      f"定义域分母：{form['domain_den']}")
        if result.counterexample:
            cex = result.counterexample
            point = ", ".join(f"{k} = {v}" for k, v in cex["point"].items())
            if not point:
                point = "（无变量）"
            print(f"反例：{point}")
            print(f"  左式 = {cex['left']}")
            print(f"  右式 = {cex['right']}")
            print("  （反例已用有理数精确求值验证）")
        print("变换日志（每步注明适用域）：")
        for i, entry in enumerate(result.log, 1):
            print(f"  {i}. {entry}")

    return {IDENTICAL: 0, NOT_IDENTICAL: 1, UNKNOWN: 2}[result.verdict]
