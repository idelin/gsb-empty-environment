#!/usr/bin/env python3
"""运行样例：分段索引多字段 TopK 归并。

直接运行：python3.11 demo.py
"""

from __future__ import annotations

from minisearch import SearchHit, SortField, NULLS_FIRST, NULLS_LAST, tokenize
from minisearch.corpus import build_demo_index


def _print_hits(title: str, hits: list[SearchHit]) -> None:
    print(f"\n=== {title} ===")
    for rank, hit in enumerate(hits, start=1):
        fields = hit.document.fields
        print(
            f"{rank:2d}. id={hit.doc_id:2d} seg={hit.segment_id} "
            f"score={hit.score:7.4f} price={fields['price']:5d} "
            f"rating={str(fields['rating']):>4} promo={int(fields['promo'])} "
            f"flagship={int(fields['flagship'])}  {hit.document.text}"
        )


def naive_topk_per_segment(index, limit: int):
    """错误对照：每段只取排序后前 K 个原始项，再在归并后过滤。"""
    field = SortField("price")
    picked: list[SearchHit] = []
    for segment in index.segments:
        stream = segment.iter_ranked(None, (field,))
        for _ in range(limit):
            try:
                _key, doc, score = next(stream)
            except StopIteration:
                break
            if not doc.fields["promo"]:
                picked.append(SearchHit(doc.doc_id, score, doc, segment.segment_id))
    picked.sort(key=lambda h: h.document.fields["price"])
    return picked[:limit]


def main() -> None:
    index = build_demo_index()

    print("分段索引信息：")
    for segment in index.segments:
        print(
            f"  段 {segment.segment_id}: {len(segment.doc_store)} 条文档, "
            f"可见 {segment.live_count} 条, 词表 {len(segment.postings)} 项"
        )

    print("\n=== 分词规则样例 ===")
    sample = "Wireless 机械键盘 keyboard_42 ßtraße ＵＳＢ"
    print(f"  原文: {sample!r}")
    print(f"  tokens: {tokenize(sample)}")

    _print_hits(
        "1) 多字段归并 Top2：price 升序（过滤促销后自动补足）",
        index.search(
            None,
            sort_by=[SortField("price")],
            limit=2,
            doc_filter=lambda doc: not doc.fields["promo"],
        ),
    )
    wrong = naive_topk_per_segment(index, 2)
    print(
        "   [错误对照] 每段只取前 2 个原始项再过滤，得到 "
        f"{[hit.doc_id for hit in wrong]}（段头促销文档之后的 doc 6 被漏掉，正确应为 [1, 6]）"
    )

    _print_hits(
        "2) null 位置：rating 降序 NULLS LAST + price 升序（query=keyboard）",
        index.search(
            "keyboard",
            sort_by=[
                SortField("rating", descending=True, nulls=NULLS_LAST),
                SortField("price"),
            ],
            limit=10,
        ),
    )

    _print_hits(
        "3) rating 升序 NULLS FIRST（同样的 keyboard 查询）",
        index.search(
            "keyboard",
            sort_by=[SortField("rating", nulls=NULLS_FIRST), SortField("price")],
            limit=10,
        ),
    )

    _print_hits(
        "4) 默认按相关度 _score 降序：query='4k monitor 旗舰'",
        index.search("4k monitor 旗舰", limit=10),
    )

    _print_hits(
        "5) 多字段排序：mechanical keyboard，rating 降序(NULLS LAST) + price 升序",
        index.search(
            "mechanical keyboard",
            sort_by=[
                SortField("rating", descending=True, nulls=NULLS_LAST),
                SortField("price"),
            ],
            limit=10,
        ),
    )

    print("\n=== 6) 删除位图：删除 doc 10 与 doc 23 后重新检索 ===")
    index.delete(10)
    index.delete(23)
    print("  已删除: 10 (mechanical keyboard 红轴), 23 (wireless earbuds)")
    _print_hits(
        "删除后 query='keyboard' Top3，仅在有货文档中取（10 不可见，继续补足）",
        index.search(
            "keyboard",
            sort_by=[SortField("price")],
            limit=3,
            doc_filter=lambda doc: doc.fields["in_stock"],
        ),
    )
    seg1 = index.segments[1]
    print(
        f"  段 1 可见 {seg1.live_count}/{len(seg1.doc_store)}，"
        f"doc 10 is_deleted={seg1.is_deleted(10)}"
    )

    _print_hits(
        "7) 段头被过滤后继续读取：query='usb'，排除促销（段 1 段头 doc14 被过滤）",
        index.search(
            "usb",
            sort_by=[SortField("price")],
            limit=5,
            doc_filter=lambda doc: not doc.fields["promo"],
        ),
    )

    empty_hits = index.search(
        "usb",
        sort_by=[SortField("price")],
        limit=5,
        doc_filter=lambda doc: doc.fields["flagship"] and not doc.fields["in_stock"],
    )
    print("\n=== 8) 候选确实耗尽：usb 且旗舰且无货 ===")
    print(f"   结果数: {len(empty_hits)}（空结果：没有候选被丢弃，只是无满足条件的可见文档）")


if __name__ == "__main__":
    main()
