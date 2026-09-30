"""BM25（Okapi）检索：显式公式、可增删的全局统计、带正确上界的块级 TopK 剪枝。

仅使用 Python 3.11 标准库；不使用任何搜索服务或全文检索库。

==================== 1. 分词规则（明确、可手算复核） ====================
见 tokenize() 的 docstring。查询与文档使用同一分词器。

==================== 2. 打分公式（显式 BM25） ====================
对查询词列表 q = [t1, t2, ...]（允许重复）与文档 d：

    score(q, d) = Σ_{t ∈ q} idf(t) · tf(t,d)·(k1+1) / (tf(t,d) + k1·(1−b+b·|d|/avgdl))
    idf(t)      = ln(1 + (N − df(t) + 0.5) / (df(t) + 0.5))     （Robertson IDF，恒 ≥ 0）

* 重复查询词规则：q 是多重集合，t 出现 m 次就累加 m 次，
  即 score([t, t], d) = 2 · score([t], d)，可直接手算复核。
* |d| 为文档 token 数，avgdl 为全库平均文档长度，N 为文档数，df 为文档频率。
* 空文档 |d| = 0 时 tf 恒为 0，贡献为 0；若全库皆空（avgdl = 0），长度归一项
  退化为 k1·(1−b)，不会除零。

==================== 3. 排名规则 ====================
按 (-score, doc_id) 排序：分数高者在前，同分按 doc_id 升序。
（doc_id 必须是可相互比较的类型，例如全用 int 或全用 str。）

==================== 4. 快照与版本 ====================
BM25Index 每次增删都把 version +1。snapshot() 把当前统计（N、avgdl、df）与
倒排数据（每文档词频、文档长度）整体复制固化；之后的增删不影响旧快照。
块上界缓存以 (version, 查询多重集合, 块号) 为键：统计一旦变化 ⇒ version 变化
⇒ 旧上界键永远不会再被命中，从机制上保证“文档增删改变全局统计时，不能继续
使用失效的得分上界”。

==================== 5. 块级 TopK 剪枝（上界正确性） ====================
快照把按 doc_id 升序排列的文档切成定长块。对块 B 与查询多重集合 q：

    UB(B) = Σ_{t ∈ q} m_q(t) · max_{d ∈ B} contrib(t, d)

任意 d ∈ B 的真实得分 = Σ m_q(t)·contrib(t,d) ≤ Σ m_q(t)·max contrib = UB(B)，
故 UB(B) 是块内真实得分的正确上界（逐项取精确最大值再求和，非近似）。

剪枝规则（阈值 = 当前第 k 名的 (score, doc_id)）：若
    UB(B) < 阈值分数，
    或 UB(B) == 阈值分数 且 块内最小 doc_id ≥ 阈值 doc_id（同分按 ID 升序，
    ID 更大者必排在其后），
则块内任何文档都不可能进入 top-k，整块跳过。topk_pruned 返回的审计轨迹
逐块记录 UB 与决策时刻的阈值，即“未读块不可能胜过阈值”的证据。
"""

from __future__ import annotations

import math
import re
from collections import Counter
from dataclasses import dataclass

__all__ = ["tokenize", "BM25Index", "Snapshot", "BlockAudit"]

# token = 极大的 Unicode 字母/数字序列；下划线、空白、标点均为分隔符。
TOKEN_PATTERN = re.compile(r"[^\W_]+", re.UNICODE)


def tokenize(text: str) -> list[str]:
    """分词规则（明确、可手算复核）：

    1. 先对整段文本做 Unicode casefold（大小写折叠，比 lower 更彻底）。
    2. token = 极大连续的 Unicode 字母/数字序列（即正则 [^\\W_]+：
       str.isalnum() 为真的字符）；下划线、空白、标点都是分隔符。
    3. 不做词干化、不去停用词、不做 CJK 切分：
       "我爱北京" 是 1 个 token，"我爱 北京" 是 2 个 token。

    例：tokenize("Hello, WORLD! foo_bar 我爱 北京42")
        == ["hello", "world", "foo", "bar", "我爱", "北京42"]
    """
    return TOKEN_PATTERN.findall(text.casefold())


def _normalize_query(query) -> list[str]:
    """查询可以是字符串（先分词）或已分好的 token 列表。"""
    if isinstance(query, str):
        return tokenize(query)
    return list(query)


def _qkey_of(mult: Counter) -> tuple:
    """查询多重集合的规范键：按词排序的 (term, 次数) 元组。

    打分与上界都按此固定顺序累加，保证同一文档的真实得分与“可达上界”
    的浮点结果逐位一致（== 比较安全）。
    """
    return tuple(sorted(mult.items()))


@dataclass(frozen=True)
class BlockAudit:
    """单块审计记录：upper_bound 与决策时刻的 threshold 即为剪枝证据。"""

    block_index: int
    doc_ids: tuple
    upper_bound: float
    decision: str            # "scored"（精确评分）| "skipped"（整块跳过）
    threshold: tuple | None  # 决策时第 k 名的 (score, doc_id)；top-k 未满为 None


class Snapshot:
    """某一 version 的固化视图：统计与倒排在快照之后不再变化。"""

    def __init__(self, *, version, k1, b, block_size, doc_tf, doc_len, df, total_len):
        self.version = version
        self.k1 = float(k1)
        self.b = float(b)
        self.block_size = int(block_size)
        # 全部复制，快照后与 BM25Index 的后续增删彻底隔离。
        self._doc_tf = {d: dict(tf) for d, tf in doc_tf.items()}
        self._doc_len = dict(doc_len)
        self._df = dict(df)
        self._total_len = int(total_len)
        self.num_docs = len(self._doc_len)
        self.avgdl = self._total_len / self.num_docs if self.num_docs else 0.0
        self._doc_ids = sorted(self._doc_len)
        self._blocks = [
            tuple(self._doc_ids[i:i + self.block_size])
            for i in range(0, len(self._doc_ids), self.block_size)
        ]
        # 键 = (version, 查询多重集合, 块号)。version 固定于本快照；全局统计一旦
        # 变化，新快照的 version 不同，旧键永远不会被命中（不会复用失效上界）。
        self._bounds_cache: dict = {}

    # ---------------- 统计 ----------------

    def document_frequency(self, term: str) -> int:
        return self._df.get(term, 0)

    def document_length(self, doc_id) -> int:
        return self._doc_len[doc_id]

    def idf(self, term: str) -> float:
        df = self._df.get(term, 0)
        if df == 0:
            return 0.0
        return math.log(1.0 + (self.num_docs - df + 0.5) / (df + 0.5))

    # ---------------- 打分 ----------------

    def _term_contrib(self, term: str, doc_id) -> float:
        tf = self._doc_tf[doc_id].get(term, 0)
        if tf == 0:
            return 0.0
        idf = self.idf(term)
        dl = self._doc_len[doc_id]
        if self.avgdl > 0.0:
            norm = self.k1 * (1.0 - self.b + self.b * dl / self.avgdl)
        else:
            norm = self.k1 * (1.0 - self.b)  # 全库文档皆空（avgdl=0）：避免除零
        return idf * (tf * (self.k1 + 1.0)) / (tf + norm)

    def _score_with_qkey(self, doc_id, qkey) -> float:
        return sum(m * self._term_contrib(t, doc_id) for t, m in qkey)

    def score_document(self, doc_id, query) -> float:
        """单文档精确得分；query 为字符串或 token 列表（可重复）。"""
        qkey = _qkey_of(Counter(_normalize_query(query)))
        return self._score_with_qkey(doc_id, qkey)

    # ---------------- TopK ----------------

    def topk_full(self, query, k: int):
        """全量评分参考：对每篇文档精确打分后排序。返回 [(doc_id, score), ...]。"""
        if k <= 0 or not self._doc_ids:
            return []
        qkey = _qkey_of(Counter(_normalize_query(query)))
        scored = [(d, self._score_with_qkey(d, qkey)) for d in self._doc_ids]
        scored.sort(key=lambda ds: (-ds[1], ds[0]))
        return scored[:k]

    def topk_pruned(self, query, k: int):
        """块级剪枝 top-k。返回 ([(doc_id, score), ...], [BlockAudit, ...])。

        结果与 topk_full 完全一致；audit 逐块给出未读块不可能胜过阈值的证据。
        """
        audit: list[BlockAudit] = []
        if k <= 0 or not self._doc_ids:
            return [], audit
        qkey = _qkey_of(Counter(_normalize_query(query)))
        ubs = [self._block_upper_bound(bi, qkey) for bi in range(len(self._blocks))]
        # 上界大的块先精确评分，阈值尽快收紧（块号次序保证确定性）。
        order = sorted(range(len(self._blocks)), key=lambda bi: (-ubs[bi], bi))
        best: list = []  # [(doc_id, score)]，按 (-score, doc_id) 排序，至多 k 个
        for bi in order:
            block = self._blocks[bi]
            threshold = None
            if len(best) == k:
                thr_id, thr_score = best[-1]
                threshold = (thr_score, thr_id)
            if threshold is not None:
                thr_score, thr_id = threshold
                ub = ubs[bi]
                # 块内任意文档得分 <= ub；同分按 doc_id 升序，块内最小 id 若已
                # 不优于当前第 k 名，则整块不可能进入 top-k。
                if ub < thr_score or (ub == thr_score and block[0] >= thr_id):
                    audit.append(BlockAudit(bi, block, ub, "skipped", threshold))
                    continue
            for doc_id in block:
                best.append((doc_id, self._score_with_qkey(doc_id, qkey)))
            best.sort(key=lambda ds: (-ds[1], ds[0]))
            del best[k:]
            audit.append(BlockAudit(bi, block, ubs[bi], "scored", threshold))
        return best, audit

    # ---------------- 块上界 ----------------

    def _block_upper_bound(self, block_index: int, qkey: tuple) -> float:
        cache_key = (self.version, qkey, block_index)
        cached = self._bounds_cache.get(cache_key)
        if cached is not None:
            return cached
        block = self._blocks[block_index]
        ub = 0.0
        for term, m in qkey:
            best_contrib = 0.0
            for doc_id in block:
                contrib = self._term_contrib(term, doc_id)
                if contrib > best_contrib:
                    best_contrib = contrib
            ub += m * best_contrib
        self._bounds_cache[cache_key] = ub
        return ub


class BM25Index:
    """可增删的 BM25 索引：维护文档长度、词频、文档频率与全局统计。"""

    def __init__(self, k1: float = 1.5, b: float = 0.75, block_size: int = 8):
        if k1 <= 0:
            raise ValueError("k1 必须为正数")
        if not 0.0 <= b <= 1.0:
            raise ValueError("b 必须在 [0, 1] 内")
        if block_size < 1:
            raise ValueError("block_size 必须 >= 1")
        self.k1 = float(k1)
        self.b = float(b)
        self.block_size = int(block_size)
        self._doc_tf: dict = {}    # doc_id -> {term: tf}
        self._doc_len: dict = {}   # doc_id -> token 数
        self._df: dict = {}        # term -> 文档频率
        self._total_len = 0        # 全库 token 总数
        self._version = 0          # 每次增删 +1

    # ---------------- 统计（只读） ----------------

    @property
    def version(self) -> int:
        return self._version

    @property
    def num_docs(self) -> int:
        return len(self._doc_len)

    @property
    def avgdl(self) -> float:
        return self._total_len / self.num_docs if self.num_docs else 0.0

    def document_frequency(self, term: str) -> int:
        return self._df.get(term, 0)

    def document_length(self, doc_id) -> int:
        return self._doc_len[doc_id]

    # ---------------- 增删 ----------------

    def add_document(self, doc_id, text: str) -> None:
        if doc_id in self._doc_len:
            raise ValueError(f"doc_id 已存在: {doc_id!r}（请先 delete_document）")
        tf = Counter(tokenize(text))
        self._doc_tf[doc_id] = tf
        self._doc_len[doc_id] = sum(tf.values())
        self._total_len += self._doc_len[doc_id]
        for term in tf:
            self._df[term] = self._df.get(term, 0) + 1
        self._version += 1

    def delete_document(self, doc_id) -> None:
        try:
            tf = self._doc_tf.pop(doc_id)
        except KeyError:
            raise KeyError(f"doc_id 不存在: {doc_id!r}") from None
        self._total_len -= self._doc_len.pop(doc_id)
        for term in tf:
            new_df = self._df[term] - 1
            if new_df:
                self._df[term] = new_df
            else:
                del self._df[term]
        self._version += 1

    # ---------------- 快照与查询 ----------------

    def snapshot(self) -> Snapshot:
        """固化当前统计与倒排版本；之后的增删不影响返回的快照。"""
        return Snapshot(
            version=self._version,
            k1=self.k1,
            b=self.b,
            block_size=self.block_size,
            doc_tf=self._doc_tf,
            doc_len=self._doc_len,
            df=self._df,
            total_len=self._total_len,
        )

    def query(self, query, k: int):
        """在即时快照上执行剪枝 top-k，返回 [(doc_id, score), ...]。"""
        results, _ = self.snapshot().topk_pruned(query, k)
        return results
