"""BM25 运行样例：本地合成 UTF-8 语料，演示增删、快照、剪枝证据与全量对照。

运行：python3.11 demo.py
"""

from bm25 import BM25Index

# 本地合成 UTF-8 语料（中文词之间用空格分开，因为分词器不做 CJK 切分）
DOCS = {
    1: "机器 学习 是 人工智能 的 核心 方法",
    2: "深度 学习 属于 机器 学习 的 分支",
    3: "苹果 香蕉 与 橙子 都是 水果",
    4: "机器 机器 机器 学习",          # 高频词文档
    5: "自然语言 处理 使用 机器 学习 技术",
    6: "banana apple 水果 fruit",
    7: "",                             # 空文档：验证不除零
    8: "学习 学习 学习 学习",
}


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("1. 建索引与全局统计（文档长度 / 词频 / 文档频率）")
    idx = BM25Index(k1=1.5, b=0.75, block_size=3)
    for doc_id, text in DOCS.items():
        idx.add_document(doc_id, text)
    print(f"N={idx.num_docs}  avgdl={idx.avgdl:.4f}  version={idx.version}")
    print(f"df(机器)={idx.document_frequency('机器')}  "
          f"df(学习)={idx.document_frequency('学习')}  "
          f"df(水果)={idx.document_frequency('水果')}")

    show("2. 快照 S1 + 块级剪枝 top-3（含逐块证据）")
    snap1 = idx.snapshot()
    q = "机器 学习"
    results, audit = snap1.topk_pruned(q, 3)
    print(f"查询 {q!r} 的 top-3: {[(d, round(s, 4)) for d, s in results]}")
    for e in audit:
        thr = "None(top-k 未满)" if e.threshold is None else (
            f"({e.threshold[0]:.4f}, id={e.threshold[1]})")
        print(f"  块{e.block_index} ids={e.doc_ids}  UB={e.upper_bound:.4f}  "
              f"决策时阈值={thr}  -> {e.decision}")
    skipped = [e for e in audit if e.decision == "skipped"]
    for e in skipped:
        print(f"  证据: 块{e.block_index} UB={e.upper_bound:.4f} <= 阈值"
              f" {e.threshold[0]:.4f}，块内最小 id={e.doc_ids[0]}，不可能进入 top-3")
    assert results == snap1.topk_full(q, 3)
    print("剪枝结果 == 全量评分结果 ✓")

    show("3. 增删改变全局统计：旧快照冻结，新快照用新版本上界")
    idx.delete_document(4)             # 删掉高频词文档
    idx.add_document(9, "机器 视觉 与 机器 学习 应用")
    print(f"删除 doc4 + 新增 doc9 后: N={idx.num_docs}  avgdl={idx.avgdl:.4f}  "
          f"df(机器)={idx.document_frequency('机器')}  version={idx.version}")
    snap2 = idx.snapshot()
    r1 = snap1.topk_full(q, 3)
    r2 = snap2.topk_full(q, 3)
    print(f"S1(v{snap1.version}) top-3: {[(d, round(s, 4)) for d, s in r1]}  （冻结不变）")
    print(f"S2(v{snap2.version}) top-3: {[(d, round(s, 4)) for d, s in r2]}  （统计已更新）")
    snap1.topk_pruned(q, 3)
    snap2.topk_pruned(q, 3)
    v1 = {k[0] for k in snap1._bounds_cache}
    v2 = {k[0] for k in snap2._bounds_cache}
    print(f"上界缓存版本: S1={v1}  S2={v2}  （键不同 ⇒ 不会复用失效上界）")

    show("4. 重复查询词：score([t, t]) = 2 × score([t])，可手算复核")
    s_one = snap2.score_document(2, ["学习"])
    s_two = snap2.score_document(2, ["学习", "学习"])
    print(f"doc2  score([学习])={s_one:.6f}  score([学习, 学习])={s_two:.6f}  "
          f"比值={s_two / s_one:.1f}")

    show("5. 空文档与删除后平均长度")
    print(f"空文档 doc7 得分={snap2.score_document(7, ['学习'])}（不除零）")
    idx.delete_document(7)
    print(f"删除空文档后: N={idx.num_docs}  avgdl={idx.avgdl:.4f}")


if __name__ == "__main__":
    main()
