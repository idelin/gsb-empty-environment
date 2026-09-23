"""命令行演示：运行样例并打印因子 DAG、成本、上下界与验证证书。"""
from __future__ import annotations

from .certificate import build_certificate
from .dag import pretty
from .parser import parse
from .search import optimize

EXAMPLES = [
    ("6*x^2*y + 9*x*y^2", 10_000, "两项提公因子 3xy"),
    ("2*x + 2*y + 3*x*z + 3*y*z", 10_000, "共享子表达式 (x+y) 只计费一次"),
    ("1/2*x + 1/2*y + 1/2*z", 10_000, "有理系数公因子 1/2"),
    ("0", 10_000, "零多项式（特殊处理）"),
    (
        "2*x + 2*y + 4*x^2 + 4*x*y + 6*y^2 + 6*x*z + 2*z + 4*y*z",
        400,
        "8 项 + 小预算：中断并保留上下界",
    ),
]


def main() -> None:
    for text, budget, note in EXAMPLES:
        poly = parse(text)
        res = optimize(poly, budget=budget)
        cert = build_certificate(poly, res.dag, res.complete)
        print("=" * 66)
        print(f"样例：{note}")
        print(f"输入：{poly}   （变量：{', '.join(poly.vars) or '无'}）")
        print("-" * 66)
        print(f"因子提取 DAG：{pretty(res.dag, poly.vars)}")
        if res.complete:
            print(
                f"运算次数：{res.cost}"
                f"（候选空间已穷尽，确为本成本模型下全局最优；上界=下界={res.cost}）"
            )
        else:
            print(f"运算次数：上界 {res.upper_bound}（当前最优完整候选），下界 {res.lower_bound}")
            print("搜索因预算中断：不承诺全局最优。")
        print(f"已评估候选数：{res.explored}（预算 {res.budget}）")
        print("验证证书：")
        for line in cert.render().splitlines():
            print("  " + line)
        print()


if __name__ == "__main__":
    main()
