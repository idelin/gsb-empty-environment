"""按时间桶维护词文档频率（DF）的增量索引。

核心性质：
- 增量维护：add/retract 只触碰文档所属的那一个桶，禁止每次扫描全部历史文档。
- 迟到文档：按文档自带时间戳落入对应桶，修订该桶统计。
- 时间桶撤回：retract_document 按文档身份从桶中移除。
- 滑窗推进：窗口 = 连续桶区间 [start, end)，查询时只聚合窗口内的桶。
- 频率分母：按「可见文档身份」维护，即窗口内去重后的 doc_id 数。
- 平滑比率：score = (df_recent + alpha) / (df_base + alpha)，
  0 -> 1 的变化得到有限分 (1+alpha)/alpha，而非无穷。
- 空窗保护：recent 窗口可见文档数为 0 时返回空排名，不伪报增长。
- TopK 剪枝：上界同时包含平滑项与最低支持数（见 top_trends 注释）。
"""

from __future__ import annotations

from dataclasses import dataclass

from .tokenizer import tokenize


@dataclass(frozen=True)
class TrendRecord:
    """一条趋势结果，含原始计数与平滑分。"""

    term: str
    df_recent: int      # 词在近期窗的文档频率（原始计数）
    df_base: int        # 词在基准窗的文档频率（原始计数）
    recent_docs: int    # 近期窗可见文档数（分母）
    base_docs: int      # 基准窗可见文档数（分母）
    score: float        # (df_recent + alpha) / (df_base + alpha)


class TrendIndex:
    def __init__(self, bucket_size: int = 3600, alpha: float = 1.0, min_support: int = 1):
        if bucket_size <= 0:
            raise ValueError("bucket_size 必须为正")
        if alpha <= 0:
            raise ValueError("alpha 必须为正，否则 0->1 会产生无穷")
        if min_support < 1:
            raise ValueError("min_support 至少为 1")
        self.bucket_size = bucket_size
        self.alpha = alpha
        self.min_support = min_support
        self._bucket_docs: dict[int, set[str]] = {}        # 桶 -> 可见 doc_id 集合
        self._bucket_terms: dict[int, dict[str, set[str]]] = {}  # 桶 -> 词 -> doc_id 集合
        self._docs: dict[str, tuple[int, frozenset[str]]] = {}   # doc_id -> (桶, 词集合)
        self.duplicate_events = 0   # 重复事件（同 id 同内容重复到达）计数
        self.revisions = 0          # 修订（同 id 不同内容/时间）计数
        self.pruned_candidates = 0  # 最近一次 top_trends 剪掉的候选数（观测用）

    # ---------- 桶与窗口 ----------

    def bucket_of(self, ts: int) -> int:
        return ts // self.bucket_size

    def _bucket_range(self, start: int, end: int) -> range:
        """窗口 [start, end) 覆盖的桶编号；空窗返回空 range。"""
        if end <= start:
            return range(0)
        return range(self.bucket_of(start), self.bucket_of(end - 1) + 1)

    # ---------- 增量写入（只触碰单个桶） ----------

    def add_document(self, doc_id: str, ts: int, text: str) -> bool:
        """写入文档。迟到文档按 ts 落入对应桶；重复事件幂等；内容变化视为修订。

        返回 True 表示索引发生变化。
        """
        terms = frozenset(tokenize(text))  # 重复词只计一次
        bucket = self.bucket_of(ts)
        old = self._docs.get(doc_id)
        if old is not None:
            if old == (bucket, terms):
                self.duplicate_events += 1  # 重复事件：幂等忽略
                return False
            self._remove_from_bucket(doc_id)  # 修订：先撤旧桶再入新桶
            self.revisions += 1
        self._docs[doc_id] = (bucket, terms)
        self._bucket_docs.setdefault(bucket, set()).add(doc_id)
        term_map = self._bucket_terms.setdefault(bucket, {})
        for term in terms:
            term_map.setdefault(term, set()).add(doc_id)
        return True

    def retract_document(self, doc_id: str) -> bool:
        """按文档身份撤回（时间桶撤回）。"""
        if doc_id not in self._docs:
            return False
        self._remove_from_bucket(doc_id)
        return True

    def _remove_from_bucket(self, doc_id: str) -> None:
        bucket, terms = self._docs.pop(doc_id)
        docs = self._bucket_docs.get(bucket)
        if docs is not None:
            docs.discard(doc_id)
            if not docs:
                del self._bucket_docs[bucket]
        term_map = self._bucket_terms.get(bucket)
        if term_map is not None:
            for term in terms:
                ids = term_map.get(term)
                if ids is not None:
                    ids.discard(doc_id)
                    if not ids:
                        del term_map[term]
            if not term_map:
                del self._bucket_terms[bucket]

    # ---------- 窗口聚合 ----------

    def window_stats(self, start: int, end: int) -> tuple[int, dict[str, int]]:
        """返回 (可见文档数分母, {词: DF})。只聚合窗口内的桶。

        每个文档只属于一个桶，因此跨桶 DF 直接相加不会重复计数。
        """
        df: dict[str, int] = {}
        doc_count = 0
        for bucket in self._bucket_range(start, end):
            doc_count += len(self._bucket_docs.get(bucket, ()))
            for term, ids in self._bucket_terms.get(bucket, {}).items():
                df[term] = df.get(term, 0) + len(ids)
        return doc_count, df

    # ---------- 趋势排名（TopK 界剪枝） ----------

    def top_trends(
        self,
        base: tuple[int, int],
        recent: tuple[int, int],
        k: int,
    ) -> list[TrendRecord]:
        """比较基准窗与近期窗，返回平滑比率 TopK。

        剪枝上界（平滑与最低支持数共同进入）：
        - 处理到某桶时，词 t 已有部分 DF=df，窗口剩余可见文档数为 rem；
          df 上界 = df + rem（每个剩余文档至多贡献 1）。
        - 支持数上界：df + rem < min_support => 永不可能达标，剪枝。
        - 分数上界：ub = (df + rem + alpha) / (df_base + alpha)；
          下界 lb = (df + alpha) / (df_base + alpha)（df 只会增长）。
          当前候选中第 k 大的 lb 是真实第 k 名的下界（每个候选终分 >= 其 lb），
          因此 ub < 该阈值的候选可安全剪枝。
        """
        self.pruned_candidates = 0
        if k <= 0:
            return []
        base_docs, base_df = self.window_stats(*base)
        recent_docs, _ = self.window_stats(*recent)
        if recent_docs == 0:
            return []  # 空窗不伪报增长

        alpha = self.alpha
        min_sup = self.min_support
        partial: dict[str, int] = {}
        remaining = recent_docs

        for bucket in self._bucket_range(*recent):
            for term, ids in self._bucket_terms.get(bucket, {}).items():
                partial[term] = partial.get(term, 0) + len(ids)
            remaining -= len(self._bucket_docs.get(bucket, ()))
            if not partial:
                continue
            threshold = None
            if len(partial) >= k:
                lbs = sorted(
                    ((df + alpha) / (base_df.get(t, 0) + alpha) for t, df in partial.items()),
                    reverse=True,
                )
                threshold = lbs[k - 1]
            for term in list(partial):
                df = partial[term]
                ub_df = df + remaining
                if ub_df < min_sup:
                    del partial[term]  # 支持数上界不达标
                    self.pruned_candidates += 1
                elif threshold is not None and (ub_df + alpha) / (base_df.get(term, 0) + alpha) < threshold:
                    del partial[term]  # 平滑分数上界进不了 TopK
                    self.pruned_candidates += 1

        records = [
            TrendRecord(
                term=t,
                df_recent=df,
                df_base=base_df.get(t, 0),
                recent_docs=recent_docs,
                base_docs=base_docs,
                score=(df + alpha) / (base_df.get(t, 0) + alpha),
            )
            for t, df in partial.items()
            if df >= min_sup
        ]
        records.sort(key=lambda r: (-r.score, -r.df_recent, r.term))
        return records[:k]
