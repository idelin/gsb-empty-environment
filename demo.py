"""运行样例：固定索引版本 + 键集分页 + 游标绑定查询摘要。

运行方式：python3.11 demo.py
"""

from search_engine import (
    CursorIndexExpiredError,
    CursorQueryMismatchError,
    SearchEngine,
    UpdatePolicy,
)

DOCS = {
    "d01": "苹果 苹果 手机 发布会",
    "d02": "苹果手机 评测 续航",
    "d03": "苹果 香蕉 苹果 水果",
    "d04": "手机 手机 手机 快充",
    "d05": "苹果 果园 采摘",
    "d06": "香蕉 苹果 手机 苹果 对比",
    "d07": "完全无关的 烹饪 教程",
    "d08": "Apple Watch 与 iPhone 生态",
}


def show(page, label):
    hits = ", ".join(f"{h.doc_id}(score={h.score})" for h in page.hits) or "(空)"
    print(f"  {label}: v{page.index_version} [{hits}] 下一页游标={'有' if page.next_cursor else '无'}")
    return page.next_cursor


def main():
    print("== 1. 键集分页遍历（页大小=3，固定索引版本内不重复不漏项）==")
    engine = SearchEngine(on_update=UpdatePolicy.SNAPSHOT)
    for doc_id, text in DOCS.items():
        engine.add_document(doc_id, text)
    cursor, page_no, seen = None, 1, []
    while True:
        page = engine.search("苹果 手机", page_size=3, cursor=cursor)
        cursor = show(page, f"第{page_no}页")
        seen.extend(h.doc_id for h in page.hits)
        page_no += 1
        if cursor is None:
            break
    print(f"  汇总: {seen}  去重后数量一致={len(seen) == len(set(seen))}")

    print("\n== 2. 中途改变页大小（2 -> 2 -> 5），不重复已读文档 ==")
    p1 = engine.search("苹果", page_size=2)
    c = show(p1, "页大小2")
    p2 = engine.search("苹果", page_size=2, cursor=c)
    c = show(p2, "页大小2")
    p3 = engine.search("苹果", page_size=5, cursor=c)
    show(p3, "页大小5")
    seen = [h.doc_id for h in (*p1.hits, *p2.hits, *p3.hits)]
    full = [h.doc_id for h in engine.search("苹果", page_size=100).hits]
    print(f"  拼接结果 == 一次性全量结果: {seen == full}")

    print("\n== 3. 换查询复用游标被拒绝 ==")
    cursor = engine.search("苹果", page_size=3).next_cursor
    try:
        engine.search("手机", page_size=3, cursor=cursor)
    except CursorQueryMismatchError as exc:
        print(f"  拒绝: {exc}")

    print("\n== 4. 更新策略=明确失效（INVALIDATE）：旧游标报错 ==")
    strict = SearchEngine(on_update=UpdatePolicy.INVALIDATE)
    for doc_id, text in DOCS.items():
        strict.add_document(doc_id, text)
    cursor = strict.search("苹果", page_size=3).next_cursor
    strict.add_document("d09", "苹果 苹果 苹果 新品 手机")
    try:
        strict.search("苹果", page_size=3, cursor=cursor)
    except CursorIndexExpiredError as exc:
        print(f"  拒绝: {exc}")

    print("\n== 5. 更新策略=快照保持（SNAPSHOT）：旧游标继续读旧版本 ==")
    page1 = engine.search("苹果", page_size=3)
    old_cursor = show(page1, "更新前第1页")
    engine.add_document("d09", "苹果 苹果 苹果 新品 手机")
    print(f"  （索引已更新至 v{engine.version}，新增 d09）")
    page2 = engine.search("苹果", page_size=3, cursor=old_cursor)
    show(page2, "旧游标续翻")
    print(f"  旧快照结果不含 d09: {'d09' not in [h.doc_id for h in page2.hits]}")
    fresh = engine.search("苹果", page_size=3)
    show(fresh, "新查询第1页")
    print(f"  新查询榜首为 d09: {fresh.hits[0].doc_id == 'd09'}")


if __name__ == "__main__":
    main()
