"""运行样例：本地合成 UTF-8 文档上的 TopK 建议全流程。"""

from topk import StaleCursorError, TopKSuggester

DOC = """
Apple 发布了新的 apple 芯片，apple 股价应声上涨。
Python 开发者喜欢 python 的简洁，PYTHON 社区日益壮大。
苹果 和 苹果公司 都生产 苹果产品，苹果 很受欢迎。
café 的咖啡不错，CAFÉ 的甜点更好，café 是常用词。
program programming programmer 都与 program 相关，progress 除外。
state-of-the-art 的 tokenizer 会把 don't 切开。
"""


def show(title, page):
    print(f"\n== {title} ==")
    for i, s in enumerate(page.items, 1):
        print(f"  {i}. {s.display!r:24} norm={s.normalized!r:20} freq={s.freq}")
    print(f"  has_more={page.has_more}")


def main():
    s = TopKSuggester(base_bound=8, max_results=100)
    n = s.load_text(DOC)
    print(f"载入合成 UTF-8 文档：{n} 个词，{len(s)} 个规范词，stats={s.stats()}")

    # 1. 大小写别名聚合：显示形式 = 观测频次最高的表面形式
    show("空前缀 Top-5（受 max_results 上限约束）", s.suggest("", 5))
    print(f"apple 的显示形式: {s.display_of('APPLE')!r}（Apple/apple/APPLE 聚合）")

    # 2. 前缀建议：一个词是另一词的前缀时均可返回
    s.set("program", 50)
    s.set("programming", 30)
    s.set("programmer", 10)
    show("前缀 'program' Top-5（program 本身是 programming 的前缀）", s.suggest("program", 5))

    # 3. 排名分页：游标续页，不重复扫描子树
    print("\n== 排名分页（每页 2 条）==")
    cursor = None
    page_no = 1
    while True:
        page = s.suggest("program", 2, cursor=cursor)
        print(f"  第{page_no}页: {[i.normalized for i in page.items]}")
        if not page.has_more:
            break
        cursor = page.cursor
        page_no += 1

    # 4. 字典序分页
    print("\n== 字典序分页（每页 2 条）==")
    after = None
    while True:
        items, after = s.list_lex("program", limit=2, start_after=after)
        print(f"  {[i.normalized for i in items]}")
        if after is None:
            break

    # 5. 删除高频词后补入下一候选
    print("\n== 删除高频词 'program'(50) 后补入下一候选 ==")
    before = [i.normalized for i in s.suggest("program", 3).items]
    s.remove("program")
    after_del = [i.normalized for i in s.suggest("program", 3).items]
    print(f"  删除前: {before} -> 删除后: {after_del}")

    # 6. 词频下降同样补回候选
    s.set("programming", 1)
    print(f"programming 降为 1 后: {[(i.normalized, i.freq) for i in s.suggest('program', 3).items]}")

    # 7. 内存压缩重定位：旧游标明确失效
    print("\n== 内存压缩重定位 ==")
    stale_cursor = s.suggest("", 2).cursor  # 全局首页游标（还有大量后续页）
    print(f"  compact 前: {s.stats()}")
    s.compact()
    print(f"  compact 后: {s.stats()}")
    try:
        s.suggest("", 2, cursor=stale_cursor)
    except StaleCursorError as e:
        print(f"  旧游标按预期失效: {e}")
    print(f"  重查结果不变: {[i.normalized for i in s.suggest('program', 3).items]}")


if __name__ == "__main__":
    main()
