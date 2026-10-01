"""运行样例：python3.11 demo.py

演示：增删维护统计、手算复核、块级剪枝与证据、失效上界重算、快照隔离。
"""

from bm25 import BM25Index, tokenize


def show(title):
    print("\n" + "=" * 64)
    print(title)
    print("=" * 64)


def show_hits(hits):
    for rank, (doc_id, score) in enumerate(hits, 1):
        print("  #%d  doc=%-3d score=%.6f" % (rank, doc_id, score))


show("1. 建索引：本地合成 UTF-8 文档（中英混合）")
idx = BM25Index(k1=1.5, b=0.75, block_size=2)
corpus = [
    "苹果 是 一种 水果, apple 很 新鲜",          # doc 0
    "香蕉 和 苹果 都 是 水果",                    # doc 1
    "BM25 是 经典 的 检索 公式, bm25 很 实用",     # doc 2
    "苹果 苹果 苹果, 我 最 爱 苹果",              # doc 3
    "！！！ 🎉",                                 # doc 4：空文档（无词）
    "检索 引擎 使用 倒排 索引 加速 查询",           # doc 5
]
for doc_id, text in enumerate(corpus):
    idx.add_document(doc_id, text)
    print("  add doc=%d len=%-2d %s" % (doc_id, idx.doc_len[doc_id], text))
print("  N=%d  total_len=%d  avgdl=%.4f  df(苹)=%d  df(bm25)=%d"
      % (idx.n_docs, idx.total_len, idx.avgdl,
         idx.df.get("苹", 0), idx.df.get("bm25", 0)))

show("2. 手算复核：重复查询词每次出现独立累加")
s1 = idx.score_document(tokenize("苹果"), 3)
s2 = idx.score_document(tokenize("苹果 苹果"), 3)
print("  score(doc3, '苹果')       = %.10f" % s1)
print("  score(doc3, '苹果 苹果')  = %.10f  == 2 x 单项: %s" % (s2, s2 == 2.0 * s1))

show("3. 块级 TopK 剪枝：最优项在最后一个块, 中间块凭证据跳过")
idx3 = BM25Index(block_size=2)
idx3.add_document(0, "apple apple apple")          # 块 0
idx3.add_document(1, "apple")                      # 块 0
idx3.add_document(2, "apple")                      # 块 1
idx3.add_document(3, "apple")                      # 块 1
idx3.add_document(4, "apple")                      # 块 2
idx3.add_document(5, "zebra zebra zebra apple")    # 块 2, 最优
query, k = "apple zebra", 1
hits, ev = idx3.search_debug(query, k)
full = idx3.search_full(query, k)
print("  query=%r k=%d  剪枝结果 == 全量参考: %s" % (query, k, hits == full))
show_hits(hits)
print("  候选文档 %d 篇, 实际评分 %d 篇" % (ev["docs_candidate"], ev["docs_scored"]))
print("  已评分块: %s" % ev["blocks_scored"])
for p in ev["blocks_pruned"]:
    print("  [未读块证据] 块%d: 上界=%.6f < 阈值=%.6f (阈值持有 doc=%d, 块内最小 doc=%d)"
          % (p["block"], p["bound"], p["threshold"],
             p["threshold_doc_id"], p["block_min_doc_id"]))
    print("      => 块内任何文档得分 <= 上界 < 阈值, 不可能进入 TopK")

show("4. 高频词更新：增删改变全局统计, 失效上界不得继续使用")
idx2 = BM25Index(block_size=4)
for doc_id in (0, 1, 4):
    idx2.add_document(doc_id, "apple")
print("  初始 N=%d avgdl=%.4f, 块1(apple)上界=%.6f (缓存于 version=%d)"
      % (idx2.n_docs, idx2.avgdl, idx2.block_bound("apple", 1), idx2.version))
stale = idx2.block_bound("apple", 1)
idx2.add_document(5, "apple apple apple apple apple apple")
print("  新增高频文档 doc5 后: N=%d avgdl=%.4f version=%d"
      % (idx2.n_docs, idx2.avgdl, idx2.version))
fresh = idx2.block_bound("apple", 1)
print("  旧上界 %.6f 已失效, 重算上界 %.6f" % (stale, fresh))
hits, ev = idx2.search_debug("apple", 1)
print("  Top1: doc=%d score=%.6f (旧上界 %.6f < 该得分, 若误用将丢解)"
      % (hits[0][0], hits[0][1], stale))
print("  已评分块: %s (块1被正确重新评估)" % ev["blocks_scored"])

show("5. 删除文档：平均长度与 df 同步更新")
before = idx.avgdl
idx.delete_document(3)   # 删除 len=4 的文档
idx.delete_document(4)   # 删除空文档
print("  删除 doc3(len=11) 与 doc4(len=0) 后: N=%d avgdl %.4f -> %.4f"
      % (idx.n_docs, before, idx.avgdl))
print("  df(苹)=%d" % idx.df.get("苹", 0))

show("6. 快照：固定统计与倒排版本, 同分按 ID")
snap = idx.snapshot()
snap_hits = snap.search("苹果", 3)
idx.add_document(6, "苹果 苹果 苹果 苹果 苹果")
idx.delete_document(0)
print("  快照 version=%d 冻结; 基索引 version=%d" % (snap.version, idx.version))
print("  基索引增删后, 快照结果不变: %s" % (snap.search("苹果", 3) == snap_hits))
print("  快照 Top3:")
show_hits(snap.search("苹果", 3))
print("  基索引 Top3 (统计已变):")
show_hits(idx.search("苹果", 3))
