"""运行样例：构建本地合成 UTF-8 文档，执行布尔查询并展示命中证据。

用法：python3.11 demo.py
"""

from boolsearch import Index


def show(idx: Index, query: str) -> None:
    result = idx.search(query)
    print(f"\n查询: {query}")
    print(f"命中 {len(result.doc_ids)} 篇: {result.doc_ids}")
    for doc_id in result.doc_ids:
        evs = result.evidence[doc_id]
        if not evs:
            print(f"  {doc_id}: （由 NOT 否定命中，无正向证据）")
        for e in evs:
            print(f"  {doc_id}: {e.field}:{e.term} @[{e.start},{e.end}) {e.snippet}")


def main() -> None:
    idx = Index()
    idx.add_document("d1", {
        "title": "Python 入门",
        "body": "入门很简单，易学。",
        "tags": "python tutorial",
    })
    idx.add_document("d2", {
        "title": "Rust 指南",
        "body": "Rust is fast and safe. 没有 python。",
        "tags": "rust guide",
    })
    idx.add_document("d3", {
        "title": "Python vs Rust",
        "body": "对比 Python 与 Rust 的性能。",
        "tags": "python rust",
    })
    idx.add_document("d4", {
        "title": "空摘要示例",
        "body": "only body text",
        "summary": "",
    })
    print(f"文档全集: {sorted(idx.universe)}")

    show(idx, "python AND rust")          # 同时含两者
    show(idx, "NOT python OR rust")       # 按定义 = (NOT python) OR rust
    show(idx, "NOT (python OR rust)")     # 括号改变分组
    show(idx, "title:python")             # 字段限定
    show(idx, "summary:body")             # 缺失字段 / 空串字段均不命中
    show(idx, "NOT 入")                   # 纯否定查询（CJK 单字词项）

    print("\n--- 删除 d2 后 ---")
    idx.delete_document("d2")
    print(f"文档全集: {sorted(idx.universe)}")
    show(idx, "rust")
    show(idx, "NOT python")               # 补集基于删除后的全集


if __name__ == "__main__":
    main()
