"""运行样例：不可变段 + 删除位图索引的完整演示。

运行：python3.11 demo.py
"""

import time

from index import Index
from tokenizer import tokenize


def show(title):
    print(f"\n=== {title} ===")


def show_stats(index, label="当前统计"):
    stats = index.stats()
    print(f"{label}: 可见文档={stats['visible_docs']}, 段数={stats['segments']}")
    for seg in stats["segment_details"]:
        print(
            f"  段{seg['seg_id']}: 文档={seg['docs']}, 墓碑={seg['tombstones']}"
        )


def main():
    show("1. 分词规则演示（本地合成 UTF-8 文本）")
    for text in ["Python3.11 发布了！", "苹果、香蕉和橙子", "Hello, 世界 123"]:
        print(f"  {text!r} -> {tokenize(text)}")

    index = Index(merge_threshold=4)

    show("2. 写入文档（每次写入生成一个不可变段）")
    index.put("d1", "苹果和香蕉是常见水果")
    index.put("d2", "Python 3.11 是优秀的编程语言")
    index.put("d3", "橙子富含维生素C")
    index.put("d4", "香蕉和橙子都是水果")
    print("  写入 d1..d4")
    show_stats(index)

    show("3. 查询（只命中可见文档）")
    print(f"  search('水果')   -> {index.search('水果')}")
    print(f"  search('python') -> {index.search('python')}")
    print(f"  search('橙')     -> {index.search('橙')}")
    print(f"  search('香蕉 水果')（AND）-> {index.search('香蕉 水果')}")

    show("4. 更新文档：新版本遮蔽旧版本")
    snap_before_update = index.snapshot()  # 旧读者快照
    index.put("d1", "苹果和葡萄是秋季水果")  # d1 的第二版
    print(f"  d1 更新后 get('d1') -> {index.get('d1')!r}")
    print(f"  新读者 search('葡萄') -> {index.search('葡萄')}")
    print(f"  新读者 search('香蕉') -> {index.search('香蕉')}  (d1 旧版本已遮蔽)")
    show_stats(index, "更新后统计（d1 仍只算 1 个可见文档）")

    show("5. 旧读者快照不受更新影响")
    print(f"  旧快照 get('d1') -> {snap_before_update.get('d1')!r}")
    print(f"  旧快照 search('香蕉') -> {snap_before_update.search('香蕉')}")
    print(f"  旧快照 stats -> 可见文档={snap_before_update.stats()['visible_docs']}")

    show("6. 删除文档：写入墓碑")
    index.delete("d3")
    print(f"  delete('d3') 后 get('d3') -> {index.get('d3')!r}")
    print(f"  search('橙子') -> {index.search('橙子')}")
    show_stats(index, "删除后统计（d3 不计入）")

    show("7. 删除后重新写入：更高版本不受旧墓碑影响")
    index.put("d3", "橙子重新上架")
    print(f"  get('d3') -> {index.get('d3')!r}")
    print(f"  search('橙子') -> {index.search('橙子')}")

    show("8. 墓碑保留：未覆盖所有旧段前不可丢")
    # 当前段（新->老）: [d3v2][tomb d3][d1v2][d4][d3v1][d2][d1v1]
    # 另起一个干净的索引演示部分合并时的墓碑保留
    idx2 = Index()
    idx2.put("a", "旧版本的甲文档")      # 段0: a@1
    idx2.put("b", "乙文档")             # 段1
    idx2.delete("a")                    # 段2: 墓碑 a@1
    merged = idx2.merge_newest(k=2)     # 合并段2+段1，段0（含 a@1）未参与
    print(f"  合并最新2段 -> {merged}")
    print(f"  合并后段列表: {list(idx2.snapshot().segments)}")
    print(f"  墓碑 a@1 被保留（段0 仍含 a 的旧条目）: "
          f"{merged.deleted == {'a': 1}}")
    print(f"  get('a') -> {idx2.get('a')!r}（墓碑仍然生效）")
    merged_all = idx2.merge_all()       # 合并全部段，墓碑覆盖所有旧段
    print(f"  全部合并后 -> {merged_all}（墓碑已安全丢弃）")
    print(f"  get('a') -> {idx2.get('a')!r}")

    show("9. 合并期间更新文档：新值不丢失，旧读者仍可查旧段")
    idx3 = Index()
    idx3.put("x", "初版内容")
    idx3.put("y", "另一个文档")
    old_reader = idx3.snapshot()

    def write_during_merge():
        idx3.put("x", "合并期间写入的新值")  # 在合并计算与交换之间写入

    idx3._merge_hook = write_during_merge
    idx3.merge_all()
    idx3._merge_hook = None
    print(f"  合并后 get('x') -> {idx3.get('x')!r}（新值未丢）")
    print(f"  旧读者 get('x') -> {old_reader.get('x')!r}（旧段仍可查）")
    print(f"  合并后段数 -> {idx3.segment_count}")

    show("10. 后台合并线程：段数保持在阈值附近")
    with Index(merge_threshold=4) as idx4:
        idx4.start_background_merge(interval=0.01)
        for i in range(30):
            idx4.put(f"doc{i}", f"第{i}号合成文档 水果")
        time.sleep(0.3)  # 等待后台合并
        print(f"  写入 30 篇文档后段数 -> {idx4.segment_count}（阈值 4）")
        print(f"  search('水果') 命中数 -> {len(idx4.search('水果'))}")
        show_stats(idx4, "后台合并后统计")

    show("11. 全部合并：段数收敛为 1，查询结果不变")
    before = index.search("水果")
    index.merge_all()
    after = index.search("水果")
    print(f"  合并前 search('水果') -> {before}")
    print(f"  合并后 search('水果') -> {after}")
    print(f"  结果一致: {before == after}")
    show_stats(index, "全部合并后统计")


if __name__ == "__main__":
    main()
