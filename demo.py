"""运行样例：固定索引版本 + 排序键分页 + 游标绑定查询摘要。"""

from paginated_search import CursorInvalidated, CursorQueryMismatch, SearchEngine

DOCS = [
    ("doc-01", "游标分页 基于排序键 的 分页 方案，分页 稳定 不重复"),
    ("doc-02", "固定索引版本 与 排序键分页"),
    ("doc-03", "游标绑定查询摘要 分页 防止重复漏项"),
    ("doc-04", "更新索引时 选择快照保持 或明确失效 分页"),
    ("doc-05", "同分文档按ID形成全序 分页"),
    ("doc-06", "页大小改变 不重复已读文档 分页"),
]


def show(page, label):
    ids = [(h.doc_id, h.score) for h in page.items]
    tail = "有" if page.next_cursor else "无"
    print(f"{label}: v{page.index_version} -> {ids}  next_cursor={tail}")
    return page


def main():
    engine = SearchEngine()
    engine.update(DOCS)
    print(f"索引版本: v{engine.version}\n")

    print("== 1. 排序键分页（页大小 2，doc-01 评分最高，同分按 ID 全序） ==")
    p1 = show(engine.search("分页", page_size=2), "第1页")
    p2 = show(engine.search("分页", page_size=2, cursor=p1.next_cursor), "第2页")
    show(engine.search("分页", page_size=2, cursor=p2.next_cursor), "第3页")

    print("\n== 2. 页大小中途改变（2 -> 3 -> 1），不重读已读文档 ==")
    seen = []
    p = show(engine.search("分页", page_size=2), "页大小2")
    seen += [h.doc_id for h in p.items]
    p = show(engine.search("分页", page_size=3, cursor=p.next_cursor), "改页大小3")
    seen += [h.doc_id for h in p.items]
    p = show(engine.search("分页", page_size=1, cursor=p.next_cursor), "改页大小1")
    seen += [h.doc_id for h in p.items]
    print(f"已读 {len(seen)} 条，去重后 {len(set(seen))} 条 -> 无重复无漏项")

    print("\n== 3. 换查询复用游标 -> 拒绝 ==")
    try:
        engine.search("快照", page_size=2, cursor=p1.next_cursor)
    except CursorQueryMismatch as e:
        print(f"CursorQueryMismatch: {e}")

    print("\n== 4. 更新索引：默认保留快照，旧游标继续读旧版本 ==")
    old_cursor = p1.next_cursor  # 绑定 v1 的游标
    engine.update(DOCS + [("doc-07", "分页 新增文档")])
    print(f"索引版本: v{engine.version}（新增 doc-07）")
    show(engine.search("分页", page_size=2, cursor=old_cursor), "旧游标续读(v1快照)")
    show(engine.search("分页", page_size=10), "新查询(v2)")

    print("\n== 5. 更新索引：明确失效，旧游标被拒绝 ==")
    p = engine.search("分页", page_size=2)
    engine.update(DOCS, invalidate_previous=True)
    print(f"索引版本: v{engine.version}（invalidate_previous=True）")
    try:
        engine.search("分页", page_size=2, cursor=p.next_cursor)
    except CursorInvalidated as e:
        print(f"CursorInvalidated: {e}")


if __name__ == "__main__":
    main()
