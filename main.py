"""运行样例：合成本地 UTF-8 文档 -> 建位图索引 -> 自排除分面计数 ->
增量更新与历史快照 -> 全选择组合逐文档布尔核对。

用法：python3.11 main.py
"""

from __future__ import annotations

from pathlib import Path

from facetlib import (
    MISSING,
    Doc,
    Query,
    build_index,
    evaluate,
    generate_docs,
    load_docs,
    text_query,
    verify_all_combinations,
    write_docs,
)

DOCS_DIR = Path("data/docs")


def show(title, results):
    print(f"\n== {title} ==")
    for facet, res in results.items():
        parts = [f"{v}={c}" for v, c in sorted(res.counts.items())]
        parts.append(f"缺失={res.missing}")
        print(f"  [{facet}] 基线文档数={res.base_size}: " + ", ".join(parts))


def main() -> None:
    # 1) 本地合成 UTF-8 文本文档并落盘，再从磁盘读回（不依赖任何搜索服务）。
    docs = generate_docs(n=240, seed=7)
    write_docs(DOCS_DIR, docs)
    loaded = load_docs(DOCS_DIR)
    index = build_index(loaded)
    print(f"已生成并索引 {len(loaded)} 份 UTF-8 文本文档（目录 {DOCS_DIR}/）")

    # 2) 主查询 + 同时选中两个分面（分面内 OR、分面间 AND、各自自排除）。
    query = Query(
        terms=text_query("苹果"),
        filters={"颜色": frozenset({"红", "绿"}), "作者": frozenset({"alice"})},
    )
    results, stats = evaluate(index, query)
    print("\n查询: 主查询='苹果'(词项 AND), 颜色∈{红,绿}(OR), 作者∈{alice}; "
          "分面间 AND；各分面计数自排除")
    show("自排除分面计数（各分面忽略自身过滤，尊重其它过滤）", results)
    print("  说明: [颜色] 的基线含 作者 过滤但不含 颜色 过滤；"
          "[作者] 反之；[年份]/[来源] 的基线含两者过滤。")

    # 3) 位图复用与访问量报告。
    k = stats.filter_bitmaps_built
    print("\n== 位图复用与访问量 ==")
    print(f"  主查询位图构建: {stats.main_bitmap_built} 次（被 {stats.facets_counted} 个分面计数共享）")
    print(f"  分面过滤位图构建: {k} 个（每个仅 1 次，跨分面复用）")
    print(f"  基线合取 AND: {stats.conjunction_ands} 次（前缀+后缀共享 = 2k；"
          f"朴素逐分面重算需 k(k-1) = {k * (k - 1)}）")
    print(f"  词项位图读取: {stats.term_bitmap_reads}, 值位图读取: {stats.value_bitmap_reads}")
    print(f"  逐文档扫描: {stats.docs_scanned}（引擎只做位图运算，不为分面重扫全库）")

    # 4) 历史快照 + 增量更新。
    snap = index.snapshot()
    index.add_or_update(Doc("doc-9000", "苹果，手机，新品。", {"颜色": ("红", "红"), "作者": ("alice",), "年份": ("2026",), "来源": ("web",)}))
    index.add_or_update(Doc("doc-9001", "苹果，平板。", {"作者": ("alice",), "来源": ("app",)}))  # 颜色缺失
    index.add_or_update(Doc("doc-0000", "苹果，电脑。", {"颜色": ("红",), "作者": ("alice",), "年份": ("2024",), "来源": ("web",)}))  # 更新
    index.remove("doc-0001")
    print(f"\n== 增量更新后（version {snap.version} -> {index.version}）==")
    show("历史快照上的同一查询（保持不变）", evaluate(snap, query)[0])
    show("当前索引上的同一查询（反映增删改）", evaluate(index, query)[0])

    # 5) 全选择组合穷举核对（逐文档布尔参考）。
    sample = loaded[:120]
    sample_index = build_index(sample)
    domains = {
        "颜色": ("红", "绿", "蓝"),
        "作者": ("alice", "bob", "carol"),
        "年份": ("2023", "2024", "2025"),
    }
    term_queries = [(), text_query("苹果")]
    combos, checks, mismatches = verify_all_combinations(
        sample, sample_index, domains, term_queries=term_queries)
    print("\n== 全组合核对（3 分面 × (值∪{缺失}) 的全部子集 × 2 组主查询）==")
    print(f"  选择组合数: {combos}, 核对计数项: {checks}, 不一致: {len(mismatches)}")
    if mismatches:
        for m in mismatches[:5]:
            print("  不一致:", m)
        raise SystemExit(1)
    print("  全部一致 ✔（4 分面全组合核对见 unittest）")


if __name__ == "__main__":
    main()
