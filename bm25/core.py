"""BM25 核心：统计、评分、全量参考检索与块级 TopK 剪枝。

公式（明确写出，可手算复核）：

    score(q, d) = sum_{t 的每次出现 in q} IDF(t) * f(t,d)*(k1+1) / (f(t,d) + K(d))
    K(d)      = k1 * (1 - b + b * |d| / avgdl)
    IDF(t)    = ln(1 + (N - df(t) + 0.5) / (df(t) + 0.5))      （非负 IDF 变体）

其中：
    f(t,d)  = 词 t 在文档 d 中的词频（tf）
    df(t)   = 包含词 t 的文档数
    N       = 文档总数
    |d|     = 文档 d 的词数（长度）
    avgdl   = 全部文档的平均长度；N=0 时定义为 0.0

重复查询词规则：查询先分词，每个查询词的【每次出现】都独立累加上面的一项，
即 "apple apple" 的得分恰为 "apple" 的两倍（对同一文档），可手算复核。

空文档不除零：|d|=0 的文档没有任何词，不会出现在倒排中；若整个语料 avgdl==0，
归一化分母退化为 k1*(1-b)，不做除法。

排名规则：得分降序；同分按文档 ID 升序（ID 小者排前）。
"""

import heapq
import math

from .tokenizer import tokenize


class BM25Core:
    """某一版本的全局统计 + 倒排，提供评分与检索。子类决定可变/不可变语义。"""

    def __init__(self, k1=1.5, b=0.75, block_size=8):
        self.k1 = float(k1)
        self.b = float(b)
        self.block_size = int(block_size)
        self.postings = {}      # term -> {doc_id: tf}
        self.doc_len = {}       # doc_id -> 词数
        self.df = {}            # term -> 文档频率
        self.n_docs = 0         # N
        self.total_len = 0      # sum(|d|)
        self.version = 0        # 统计/倒排版本号，任何增删都会 +1
        self._block_bounds = {}  # (term, block_no) -> (version, upper_bound)

    # ---- 全局统计 ----

    @property
    def avgdl(self):
        return self.total_len / self.n_docs if self.n_docs else 0.0

    def idf(self, df):
        return math.log(1.0 + (self.n_docs - df + 0.5) / (df + 0.5))

    def _norm(self, dl):
        # K(d)；avgdl==0（空语料或全部空文档）时不做除法，避免除零
        if self.avgdl <= 0.0:
            return self.k1 * (1.0 - self.b)
        return self.k1 * (1.0 - self.b + self.b * dl / self.avgdl)

    # ---- 评分 ----

    def term_score(self, term, doc_id):
        """词 term 对文档 doc_id 的一项 BM25 贡献（tf=0 时为 0.0）。"""
        tf = self.postings.get(term, {}).get(doc_id)
        if not tf:
            return 0.0
        idf = self.idf(self.df[term])
        return idf * tf * (self.k1 + 1.0) / (tf + self._norm(self.doc_len[doc_id]))

    def score_document(self, query_terms, doc_id):
        """按“重复查询词每次出现独立累加”的规则对文档评分。"""
        return sum(self.term_score(t, doc_id) for t in query_terms)

    # ---- 块结构上界 ----

    def _block_no(self, doc_id):
        return doc_id // self.block_size

    def block_bound(self, term, block_no):
        """(term, block) 的得分上界：块内所有文档该词贡献的真实最大值。

        上界带有版本号：只有当缓存版本 == 当前统计版本时才可使用；
        文档增删会推进版本号，使旧上界立即失效并被重算，
        因此绝不会使用失效上界做剪枝。
        """
        key = (term, block_no)
        cached = self._block_bounds.get(key)
        if cached is not None and cached[0] == self.version:
            return cached[1]
        bound = 0.0
        idf = self.idf(self.df.get(term, 0))
        for doc_id, tf in self.postings.get(term, {}).items():
            if self._block_no(doc_id) != block_no:
                continue
            contrib = idf * tf * (self.k1 + 1.0) / (tf + self._norm(self.doc_len[doc_id]))
            if contrib > bound:
                bound = contrib
        self._block_bounds[key] = (self.version, bound)
        return bound

    def _query_block_bound(self, query_terms, block_no):
        # 与 score_document 相同的求和顺序（IEEE 加法单调），
        # 保证任一文档的真实得分 <= 该上界（浮点意义下同样成立）。
        bound = 0.0
        for t in query_terms:
            bound += self.block_bound(t, block_no)
        return bound

    # ---- 全量评分参考实现（无剪枝，作为正确性对照） ----

    def _candidates(self, query_terms):
        cand = set()
        for t in set(query_terms):
            cand.update(self.postings.get(t, ()))
        return cand

    def search_full(self, query, k):
        """全量评分参考：对所有候选文档逐一评分后取 TopK。同分按 ID 升序。"""
        terms = tokenize(query)
        if k <= 0 or not terms:
            return []
        scored = [(d, self.score_document(terms, d)) for d in self._candidates(terms)]
        scored.sort(key=lambda x: (-x[1], x[0]))
        return scored[:k]

    # ---- 块级 TopK 剪枝 ----

    def search(self, query, k):
        """块级剪枝 TopK，结果与 search_full 完全一致。"""
        hits, _evidence = self.search_debug(query, k)
        return hits

    def search_debug(self, query, k):
        """同 search，但额外返回剪枝证据。

        证据中每个被跳过的块都记录：上界、当时阈值、阈值持有文档 ID、
        块内最小文档 ID —— 足以核验“未读块不可能胜过阈值”。
        """
        terms = tokenize(query)
        evidence = {
            "query_terms": terms,
            "blocks_scored": [],
            "blocks_pruned": [],
            "docs_scored": 0,
            "docs_candidate": 0,
        }
        if k <= 0 or not terms:
            return [], evidence

        blocks = {}
        for d in self._candidates(terms):
            blocks.setdefault(self._block_no(d), []).append(d)
        evidence["docs_candidate"] = sum(len(v) for v in blocks.values())

        # 堆元素 (score, -doc_id)：堆顶是当前保留集合中的“最差项”
        # （分数最低；同分时 ID 最大），阈值即堆顶。
        best = []
        for block_no in sorted(blocks):
            doc_ids = sorted(blocks[block_no])
            if len(best) == k:
                thr_score, thr_neg_id = best[0]
                bound = self._query_block_bound(terms, block_no)
                # 剪枝判据（对“同分按 ID 升序”严格正确）：
                #   bound < 阈值：块内任何文档得分都严格低于阈值；
                #   bound == 阈值 且块内最小 ID > 阈值文档 ID：
                #   块内任何文档至多同分且 ID 更大，必排在其后。
                if bound < thr_score or (bound == thr_score and doc_ids[0] > -thr_neg_id):
                    evidence["blocks_pruned"].append({
                        "block": block_no,
                        "bound": bound,
                        "threshold": thr_score,
                        "threshold_doc_id": -thr_neg_id,
                        "block_min_doc_id": doc_ids[0],
                    })
                    continue
            evidence["blocks_scored"].append(block_no)
            for d in doc_ids:
                evidence["docs_scored"] += 1
                item = (self.score_document(terms, d), -d)
                if len(best) < k:
                    heapq.heappush(best, item)
                elif item > best[0]:
                    heapq.heapreplace(best, item)

        hits = sorted(((-neg, s) for s, neg in best), key=lambda x: (-x[1], x[0]))
        return hits, evidence
