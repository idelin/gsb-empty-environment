"""运行样例: python3 examples/demo.py"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from productpath import (
    ForbiddenRule,
    Graph,
    RuleSet,
    StaleCursorError,
    brute_force_paths,
    find_paths,
    shortest_path,
)


def show(title, res):
    print(f"\n== {title} ==")
    if not res.paths:
        print("  (无合法路径)")
    for i, p in enumerate(res.paths, 1):
        print(
            f"  #{i} cost={p.cost:g} hops={len(p.edges)} "
            f"edges={p.edges} vertices={p.vertices} states={p.states}"
        )
    print(f"  候选扩展量={res.expansions} 搜索完备={res.exhausted}")
    if res.pending:
        print(f"  预算未决状态={res.pending_states}")


def main():
    g = Graph()
    g.add_vertex("孤岛站")  # 孤立点被保留
    g.add_edge("e1", "家", "地铁站", 1.0, "步行", "乘车")
    g.add_edge("e2", "地铁站", "公司", 2.0, "乘车", "步行")
    g.add_edge("e3", "家", "公司", 4.0, "步行", "步行")
    g.add_edge("e4", "家", "公司", 4.0, "步行", "步行")  # 多重边, 边ID参与决胜
    g.add_edge("e5", "家", "公园", 0.0, "步行", "步行")  # 零权边
    g.add_edge("e6", "公园", "家", 0.0, "步行", "休息过")  # 零权环 + 状态翻转
    g.add_edge("e7", "家", "公司", 3.5, "休息过", "步行")

    rules = RuleSet()

    show("1. 最短路(三级决胜: 成本/边数/边ID)", shortest_path(g, rules, "家", "步行", "公司"))

    no_metro = RuleSet()
    no_metro.add_rule(ForbiddenRule("地铁站", "乘车"))  # 永久禁行, 无时间区间
    show(
        "2. 地铁站禁行后: 零权环(家->公园->家)翻转状态, 同一顶点以不同状态再次经过",
        shortest_path(g, no_metro, "家", "步行", "公司"),
    )

    # 时间区间左闭右开: 到达地铁站的时刻 t=1, [0,1) 不含 1 故不拦截
    tmp = RuleSet()
    tmp.add_rule(ForbiddenRule("地铁站", "乘车", start=0.0, end=1.0))
    show("3. 右开区间 [0,1): t=1 不在区间内, 路径仍合法",
         shortest_path(g, tmp, "家", "步行", "公司"))

    # 禁行规则: 版本 1, [1,3) 含 t=1, 较短的地铁路径变为非法
    rules.add_rule(ForbiddenRule("地铁站", "乘车", start=1.0, end=3.0))
    show(
        f"4. 新增禁行规则(版本={rules.version}): 地铁站@乘车 t∈[1,3) 禁行, 较短非法路径让位给较长合法路径",
        shortest_path(g, rules, "家", "步行", "公司"),
    )

    page1 = find_paths(g, rules, "家", "步行", "公司", k=2)
    show("5. 前K条合法路径 第1页(k=2)", page1)
    page2 = find_paths(g, rules, "家", "步行", "公司", k=2, cursor=page1.cursor)
    show(f"6. 第2页(游标绑定版本={page1.cursor.version})", page2)

    rules.add_rule(ForbiddenRule("公园", "步行"))
    try:
        find_paths(g, rules, "家", "步行", "公司", k=2, cursor=page1.cursor)
    except StaleCursorError as exc:
        print(f"\n== 7. 规则更新到版本={rules.version} 后旧游标被拒绝 ==\n  {exc}")

    show(
        "8. 预算=2 的受限搜索(记录候选扩展量与预算未决状态)",
        find_paths(g, rules, "家", "步行", "公司", k=5, budget=2),
    )

    brute = brute_force_paths(g, rules, "家", "步行", "公司")
    fast = find_paths(g, rules, "家", "步行", "公司", k=len(brute) or 1)
    ok = [p.key for p in fast.paths] == [p.key for p in brute]
    print(f"\n== 9. 与独立穷举校验一致性: {'OK' if ok else 'MISMATCH'} (共{len(brute)}条) ==")


if __name__ == "__main__":
    main()
