"""运行样例：不可变段 + 删除位图 + 读快照 + 后台合并。

用法: python3.11 run_sample.py
"""

from __future__ import annotations

from lsm_index import LsmTextIndex
from lsm_index.corpus import synth_documents


def hr(title: str) -> None:
    print(f"\n{'=' * 70}\n{title}\n{'=' * 70}")


def show(snap, query: str) -> None:
    st = snap.stats(query)
    print(f"查询 {query!r}:")
    for term, t in st["terms"].items():
        print(f"  term={term!r:12} doc_freq={t['doc_freq']} term_freq={t['term_freq']}")
    print(f"  AND 命中 {st['match_count']} 篇: {st['matching_docs']}")


def main() -> None:
    hr("1. 合成 UTF-8 语料，分批 flush 成多个不可变段")
    docs = synth_documents()
    print(f"合成文档 {len(docs)} 篇（中英混合，确定性随机种子）")

    idx = LsmTextIndex()
    # 三批写入，产生 3 个段
    for i, batch in enumerate((docs[:4], docs[4:8], docs[8:]), start=1):
        for doc_id, text in batch:
            idx.upsert(doc_id, text)
        seg = idx.flush()
        print(f"  flush #{i}: 新段 id={seg.seg_id}, 当前段 {idx.active_segments()}")

    snap = idx.open_snapshot()
    print(f"可见文档数: {snap.count_visible()}（12 篇全部可见）")

    hr("2. 跨段 AND 搜索（英文 / 中文逐字分词）")
    show(snap, "immutable segments")
    show(snap, "合 并")
    show(snap, "可 见 文 档")
    print("取一篇正文:")
    print(f"  doc-en-03 -> {snap.get_doc('doc-en-03')}")
    print(f"  doc-zh-04 -> {snap.get_doc('doc-zh-04')}")

    hr("3. 新版本遮蔽旧版本 + 删除（墓碑）；统计只算可见文档")
    idx.upsert("doc-en-03", "This document was rewritten, version two.")
    idx.delete("doc-zh-04")
    idx.flush()
    fresh = idx.open_snapshot()
    print(f"更新/删除后可见文档数: {fresh.count_visible()}")
    print(f"  doc-en-03 新正文 -> {fresh.get_doc('doc-en-03')}")
    print(f"  doc-zh-04（已删） -> {fresh.get_doc('doc-zh-04')}")
    print(f"  当前段 {idx.active_segments()}（注意旧段记录还在，仅位图遮蔽）")

    hr("4. 旧读者仍持有读快照，继续查旧段（MVCC）")
    print(f"旧快照读 doc-en-03 -> {snap.get_doc('doc-en-03')}")
    print(f"旧快照读 doc-zh-04 -> {snap.get_doc('doc-zh-04')}")
    print(f"旧快照可见数       -> {snap.count_visible()}")
    fresh.close()

    hr("5. 后台合并：构建期间并发更新，新值不能丢")
    before_segs = [s[0] for s in idx.active_segments()]
    print(f"合并前段: {before_segs}")

    def during_build(index: LsmTextIndex) -> None:
        print("  [后台线程正在锁外构建合并段; 主线程并发写入 doc-zh-01 并 flush]")
        index.upsert("doc-zh-01", "合并期间写入的新值，必须保留：并发安全。")
        index.flush()

    t = idx.merge_background(during_build=during_build)
    t.join()
    print(f"合并后段: {idx.active_segments()}")
    after = idx.open_snapshot()
    print(f"  doc-zh-01 -> {after.get_doc('doc-zh-01')}")
    print(f"  doc-en-03 -> {after.get_doc('doc-en-03')}")
    print(f"  doc-zh-04（仍删除）-> {after.get_doc('doc-zh-04')}")
    print(f"  可见文档数: {after.count_visible()}（12 篇，删除 1 篇）")
    assert after.get_doc("doc-zh-01") == "合并期间写入的新值，必须保留：并发安全。"
    assert after.count_visible() == 11
    after.close()

    hr("6. 墓碑安全：旧段未覆盖前不丢；旧读者释放后旧段才可回收")
    print(f"旧快照仍打开，被替换旧段保留数: {idx.retired_count()}")
    print(f"旧快照查旧段 doc-zh-04 仍为: {snap.get_doc('doc-zh-04')}")
    snap.close()
    print(f"旧快照关闭后保留旧段数: {idx.retired_count()}（无旧读者即可回收）")

    # 部分合并：s1 更旧、仍有 x 的活值；合并 s2+s3 时墓碑必须带入
    idx2 = LsmTextIndex()
    idx2.upsert("x", "旧版本 alpha")
    idx2.upsert("y", "beta")
    idx2.flush()  # s1
    idx2.delete("x")
    idx2.flush()  # s2
    idx2.upsert("z", "gamma")
    idx2.flush()  # s3
    ids = [s[0] for s in idx2.active_segments()]
    merged = idx2.merge([ids[1], ids[2]])  # 只合并 s2,s3，s1 留在更旧位置
    p2 = idx2.open_snapshot()
    print(f"部分合并 s2+s3 后: {idx2.active_segments()}")
    print(f"  合并段记录数={len(merged.records)}（含墓碑 x 与 z），x 可见? "
          f"{p2.get_doc('x') is not None}（应为 False，墓碑遮蔽了 s1）")
    p2.close()
    m = idx2.merge()  # 再全合并：范围外无更旧段 -> 墓碑可直接丢弃
    p3 = idx2.open_snapshot()
    print(f"全部合并后: {idx2.active_segments()}，合并结果 = {m}")
    print(f"  可见文档 = {p3.visible_doc_ids()}（y, z；墓碑 x 已随更旧段覆盖完毕而丢弃）")
    print(f"  查询 x = {p3.get_doc('x')}（墓碑已丢，旧活值也已不存在）")
    p3.close()

    print("\n样例运行完成。")


if __name__ == "__main__":
    main()
