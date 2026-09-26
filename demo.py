"""运行样例：本地合成 UTF-8 文档上的布尔检索演示。"""

from search_engine import SearchEngine


def show(engine: SearchEngine, query: str) -> None:
    print(f"\n查询: {query}")
    results = engine.search(query)
    if not results:
        print("  (无命中)")
    for result in results:
        print(f"  命中 {result.doc_id}")
        for ev in result.evidence:
            print(f"    证据: 字段={ev.field} 词元={ev.term!r} 次数={ev.count}")
        if not result.evidence:
            print("    证据: (纯否定分支命中，无正向证据)")


def main() -> None:
    engine = SearchEngine()
    engine.add_document("d1", {
        "title": "Python 编程入门",
        "body": "Python 是一种编程语言，适合初学者。",
        "author": "张三",
    })
    engine.add_document("d2", {
        "title": "Java 与 Python 对比",
        "body": "Java 和 Python 都是流行语言。",
        "author": "李四",
    })
    engine.add_document("d3", {
        "title": "数据结构",
        "body": "介绍链表、树与图。",
        "author": "张三",
    })
    engine.add_document("d4", {"title": "空正文文档", "body": ""})
    engine.add_document("d5", {"title": "缺少 body 与 author 字段的文档"})

    print(f"文档全集: {sorted(engine.universe)}")

    show(engine, "python AND 编程")
    show(engine, "NOT python OR java")      # 按 (NOT python) OR java 求值
    show(engine, "NOT (python OR java)")    # 括号改变优先级
    show(engine, "author:张三 AND NOT title:数据")
    show(engine, "title:(java OR 数据)")     # 字段限定作用于括号组
    show(engine, "NOT body:python")          # 纯否定：空串字段(d4)与缺失字段(d5)都命中
    show(engine, "NOT python")               # 纯否定查询

    print("\n--- 删除 d3 后，全集收缩，NOT 补集不再包含 d3 ---")
    engine.delete_document("d3")
    print(f"文档全集: {sorted(engine.universe)}")
    show(engine, "NOT python")
    show(engine, "链表")


if __name__ == "__main__":
    main()
