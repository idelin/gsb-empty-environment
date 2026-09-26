"""Runnable demo: synthetic UTF-8 corpus, self-excluding facets,
shared-bitmap metrics, incremental updates and historical snapshots.

Run:  python3 demo.py
"""

from __future__ import annotations

from facetlib import (EMPTY, MISSING, Document, DocumentStore, FacetEngine,
                      TextQuery, reference_facets)

DOCS = [
    Document("P01", "红色 苹果 Apple 手机 支持5G 双卡",
             {"brand": ("Apple",), "color": ("red",),
              "tags": ("phone", "5g", "phone")}),          # duplicate tag
    Document("P02", "蓝色 苹果 Apple 平板 办公 利器",
             {"brand": ("Apple",), "color": ("blue",),
              "tags": ("tablet",)}),
    Document("P03", "红色 三星 Samsung 手机 长续航",
             {"brand": ("Samsung",), "color": ("red",),
              "tags": ()}),                                # EMPTY tags
    Document("P04", "三星 Samsung 耳机 降噪 蓝牙",
             {"brand": ("Samsung",),
              "tags": ("audio",)}),                        # MISSING color
    Document("P05", "绿色 索尼 Sony 耳机 高解析 音质",
             {"brand": ("Sony",), "color": ("green",),
              "tags": ("audio", "hires")}),
    Document("P06", "黑色 索尼 Sony 相机 全画幅",
             {"brand": ("Sony",), "color": ("black",),
              "tags": ("camera",)}),
    Document("P07", "红色 小米 Xiaomi 手机 快充",
             {"brand": ("Xiaomi",), "color": ("red",),
              "tags": ("phone",)}),
    Document("P08", "香蕉 苹果 水果 新鲜 上市",
             {"tags": ("food", "food")}),                  # MISSING brand+color
]


def show(title, report):
    print(f"\n== {title} ==")
    print(f"  主查询命中文档数 base_count = {report.base_count}")
    for dim in sorted(report.dimensions):
        d = report.dimensions[dim]
        counts = ", ".join(f"{k}={v}" for k, v in d.counts.items()) or "-"
        print(f"  [{dim}] 计数范围 context={d.context_size} 篇")
        print(f"      值: {counts}")
        print(f"      缺失 MISSING={d.missing}  空集合 EMPTY={d.empty}")
    m = report.metrics
    print(f"  指标: 位图取数={m.bitmap_lookups} 复用命中={m.cache_hits} "
          f"位图运算={m.bitmap_ops} 文档访问={m.docs_examined}")


def main():
    store = DocumentStore()
    for doc in DOCS:
        store.upsert(doc)
    v1 = store.snapshot()
    print(f"语料: {len(DOCS)} 篇合成 UTF-8 文档, 索引版本 v{v1.version}")
    print("分词规则: casefold; 字母/数字连续成词; 汉字逐字成词; 其余为分隔符")

    query = TextQuery(all_of=("手机",))
    show("A. 主查询 all_of=[手机], 无分面选择",
         FacetEngine(v1).compute(query))

    sel = {"brand": ("Apple", "Samsung"), "color": ("red",)}
    show("B. 主查询 all_of=[手机], 选中 brand=(Apple OR Samsung) AND color=red\n"
         "   自排除: brand 计数只看 color 过滤, color 计数只看 brand 过滤",
         FacetEngine(v1).compute(query, sel))

    show("C. 同主查询, 选中 tags=(EMPTY) — 空集合可选且与缺失不同",
         FacetEngine(v1).compute(query, {"tags": (EMPTY,)}))

    # ---- incremental updates -> v2, historical snapshot stays frozen ----
    store.upsert(Document("P02", "红色 苹果 Apple 平板 办公 利器",
                          {"brand": ("Apple",), "color": ("red",),
                           "tags": ("tablet",)}))           # blue -> red
    store.upsert(Document("P09", "红色 华为 Huawei 手机 卫星通信",
                          {"brand": ("Huawei",), "color": ("red",),
                           "tags": ("phone",)}))            # new doc
    store.delete("P06")                                     # removed doc
    v2 = store.snapshot()
    print(f"\n-- 增量更新: 改 P02, 增 P09, 删 P06 -> 版本 v{v2.version} --")
    show("D. 同一查询 B 在新快照 v2 上", FacetEngine(v2).compute(query, sel))
    show("E. 同一查询 B 在历史快照 v1 上(结果与 B 完全一致)",
         FacetEngine(v1).compute(query, sel))

    # ---- cross-check the demo scenarios against the boolean oracle ----
    print("\n== F. 逐文档布尔参考核对 ==")
    for name, q, s in [("A", query, {}), ("B", query, sel),
                       ("C", query, {"tags": (EMPTY,)})]:
        got = FacetEngine(v1).compute(q, s)
        want = reference_facets(v1.docs.values(), q, s)
        ok = (got.base_count == want.base_count and all(
            got.dimensions[k] == want.dimensions[k] for k in want.dimensions))
        print(f"  场景 {name}: 引擎 == 参考 -> {ok} "
              f"(参考实现文档访问量={want.metrics.docs_examined}, "
              f"引擎=0)")


if __name__ == "__main__":
    main()
