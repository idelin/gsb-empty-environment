"""运行样例：从 sample_docs 装载 UTF-8 文档，演示三种索引查询与更新删旧项。

运行：python3.11 demo.py
"""

from __future__ import annotations

from interval_index import (
    CONTAINED_BY,
    CONTAINS,
    INTERSECTS,
    MISSING,
    Catalog,
    Interval,
    tokenize,
)

LABEL = {
    INTERSECTS: "相交 intersects",
    CONTAINS: "查询包含文档 contains",
    CONTAINED_BY: "查询被文档包含 contained_by",
}


def show(catalog: Catalog, query: str) -> None:
    print(f"查询 {query}")
    for mode in (INTERSECTS, CONTAINS, CONTAINED_BY):
        indexed = catalog.query(query, mode)
        scanned = catalog.full_scan(query, mode)
        assert indexed == scanned, f"索引与全扫描不一致: {mode}"
        assert len(indexed) == len(set(indexed)), "结果存在重复 id"
        print(f"  {LABEL[mode]:<32} -> {indexed}")
    print()


def main() -> None:
    catalog = Catalog.from_directory("sample_docs")
    print(f"已装载 {len(catalog)} 篇文档；索引统计: {catalog.index_stats()}")
    print(f"属性缺失文档: {catalog.query(None, MISSING)}")
    print(f"空区间文档  : {catalog.empty_ids()}")
    print()

    show(catalog, "[0,5)")
    show(catalog, "[5,8)")
    show(catalog, "[5,5]")
    show(catalog, "(,)")

    print("分词示例 tokenize('区间[0,5) 与 ABC_42'):")
    print(" ", tokenize("区间[0,5) 与 ABC_42"))
    print()

    # 更新演示：把 doc-a 从 [0,5) 改成 [100,200]，旧索引项必须被删除。
    print("更新 doc-a: [0,5) -> [100,200]")
    doc_a = catalog.get("doc-a")
    new_doc = type(doc_a)(doc_a.doc_id, Interval.parse("[100,200]"),
                          doc_a.meta, doc_a.body)
    catalog.upsert_document(new_doc)
    print(f"  更新后索引统计: {catalog.index_stats()}（文档数与行数均不增加）")
    print(f"  [0,5) 相交 -> {catalog.query('[0,5)', INTERSECTS)}（不再含 doc-a）")
    print(f"  [150,150] 相交 -> {catalog.query('[150,150]', INTERSECTS)}")
    print()

    # 再改成缺失，验证区间行被摘除并进入 missing 集合。
    from interval_index import parse_document

    missing_doc = parse_document("id: doc-a\nrange:\n\n属性改为缺失",
                                 doc_id="doc-a")
    catalog.upsert_document(missing_doc)
    print("更新 doc-a: 属性缺失")
    print(f"  (,) 相交 -> {catalog.query('(,)', INTERSECTS)}（不含 doc-a）")
    print(f"  缺失集合 -> {catalog.query(None, MISSING)}")


if __name__ == "__main__":
    main()
