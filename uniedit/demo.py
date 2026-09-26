"""运行样例：python3 -m uniedit.demo

在本地合成的 UTF-8 文本上构建索引，演示：
1. 度量成本（禁用换位）下的 BK 树阈值查询与 TopK 查询；
2. 换位被支配（trans=2）时 BK 树仍可用；
3. 换位过便宜（trans=1）破坏三角不等式时自动回退安全全扫描，
   且结果与参考全扫描逐条一致。
"""
from __future__ import annotations

from .costs import CostModel
from .index import WordIndex

DOC = (
    "Apple apple APPLE apply apples applet café CAFE caffe café-au-lait "
    "中文 中文测试 中问 中问讯 Straße strasse STRASSE "
    "naïve naive résumé resume 北京 背景"
)


def show(title, result):
    print(f"\n== {title} ==")
    print(f"mode={result.mode} metric_ok={result.metric_ok} "
          f"visited={result.visited}/{result.total}")
    for h in result.hits:
        print(f"  {h.surface:<12} norm={h.word:<10} dist={h.distance:<4} pos={h.position}")
    if not result.hits:
        print("  (无命中)")


def main() -> None:
    print(f"文档（合成 UTF-8）：{DOC}")

    metric_costs = CostModel.uniform()  # 单位成本，换位禁用 => 度量
    idx = WordIndex.from_text(DOC, metric_costs)
    print(f"\n索引词条数（规范词去重后）：{len(idx)}")

    show("阈值查询 q='apple' max_dist=1（度量，BK 树）",
         idx.search("apple", max_dist=1))
    show("TopK 查询 q='café' k=3（等距候选完整）",
         idx.search("café", top_k=3))
    show("阈值查询 q='中文' max_dist=1（CJK）",
         idx.search("中文", max_dist=1))
    show("TopK 查询 q='strasse' k=2（casefold 命中 Straße）",
         idx.search("strasse", top_k=2))

    dominated = CostModel.uniform(trans=2.0)  # trans 被支配 => 仍为度量
    idx_dom = WordIndex.from_text(DOC, dominated)
    show("换位被支配 trans=2：q='café' max_dist=2（仍走 BK 树）",
         idx_dom.search("café", max_dist=2))

    broken = CostModel.uniform(trans=1.0)  # 破坏三角不等式
    idx_bad = WordIndex.from_text(DOC, broken)
    res = idx_bad.search("café", max_dist=2)
    show("换位过便宜 trans=1：q='café' max_dist=2（自动回退全扫描）", res)
    report = idx_bad.last_report
    print(f"  校验报告：{len(report.violations)} 条违规，示例：{report.violations[0]}")

    ref = idx_bad._full_scan(res.query, 2.0, None)[0]
    got = sorted((h.word, h.distance) for h in res.hits)
    want = sorted((w, d) for w, d in ref if d <= 2.0)
    print(f"  与全扫描参考一致：{got == want}（不漏词）")


if __name__ == "__main__":
    main()
