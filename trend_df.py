# -*- coding: utf-8 -*-
"""按时间桶维护词文档频率（DF）的趋势引擎。

分词规则（唯一权威定义，见 tokenize）：
  1. 文本先做 Unicode 小写化（str.lower()）。
  2. 连续的 [0-9a-z] 字符构成一个词元。
  3. 每个 CJK 统一表意文字（U+4E00 至 U+9FFF）单独构成一个词元。
  4. 其余一切字符（空白、标点、全角符号等）均为分隔符。
  5. 文档频率按文档内去重统计：同一词元在同一文档中出现多次只计一次。

平滑比率（比较两个窗口）：
  freq(w, t) = (df(w, t) + alpha) / (N(w) + alpha)
  ratio(t)   = freq(当前窗口, t) / freq(上一窗口, t)
其中 N(w) 是窗口 w 内“可见文档身份”的去重数量（分母按文档身份维护，
迟到文档修订其时间戳所属桶，撤回文档立即从分母与计数中移除）。
0 -> 1 的变化得到 (1 + alpha) / alpha，是受平滑的有限值而非无穷。

空窗规则：任一窗口 N == 0 时，比较结果为“退化”（degenerate），
不输出任何排名，空窗不可伪报增长。

最低支持数：df(当前窗口, t) < min_support 的词不进入排名。

TopK 剪枝：候选词按上界 UB(t) 降序处理，
  UB(t) = ((df_cur + alpha) / (N_cur + alpha)) / (alpha / (N_prev + alpha))
即假设上一窗口计数为 0 的最乐观情形——平滑常数 alpha 进入上界；
低于 min_support 的词在界计算之前即被排除——最低支持数同样进入上界。
当剩余候选的 UB 不超过当前第 k 名的精确分值时停止，其余候选被剪枝。

增量约束：add/retract 只触及文档所属时间桶，禁止每次更新扫描全部历史文档。
verify() 提供独立的全量重算校验（从事件日志重放），检查分母变化、
零窗与重复事件，并对每处排名更正输出贡献证据；它只用于审计与测试，
不参与引擎的增量维护路径。
"""
from __future__ import annotations

import heapq
import re
from collections import Counter
from dataclasses import dataclass, field

_TOKEN_RE = re.compile(r"[0-9a-z]+|[一-鿿]")


def tokenize(text: str):
    """按模块 docstring 中的分词规则把 UTF-8 文本切成词元列表。"""
    return _TOKEN_RE.findall(text.lower())


@dataclass
class Bucket:
    docs: set = field(default_factory=set)            # 可见文档身份
    df: Counter = field(default_factory=Counter)          # 词 -> 文档频率
    doc_tokens: dict = field(default_factory=dict)    # doc_id -> frozenset(词)


@dataclass
class WindowStats:
    start: int
    end: int
    df: Counter
    docs: frozenset

    @property
    def n(self) -> int:
        return len(self.docs)


@dataclass
class RankingEntry:
    term: str
    df_prev: int
    df_cur: int
    n_prev: int
    n_cur: int
    score: float


@dataclass
class Ranking:
    entries: list
    degenerate: str | None   # 非 None 表示空窗退化原因
    n_prev: int
    n_cur: int
    candidates: int
    evaluated: int
    pruned: int


@dataclass
class CorrectionEvidence:
    kind: str                # "denominator" | "df"
    window: str              # "prev" | "cur"
    term: str | None
    incremental: object      # 增量引擎的值
    recomputed: object       # 独立重算的值
    contributing_docs: tuple # 造成差异的贡献证据（相关文档身份）


@dataclass
class CheckReport:
    ok: bool
    corrections: list
    duplicates_ignored: int
    prev_empty: bool
    cur_empty: bool
    recomputed_n_prev: int
    recomputed_n_cur: int


class TrendEngine:
    def __init__(self, bucket_span: int, window_buckets: int,
                 alpha: float = 1.0, min_support: int = 2):
        if bucket_span <= 0:
            raise ValueError("bucket_span 必须为正")
        if window_buckets <= 0:
            raise ValueError("window_buckets 必须为正")
        if alpha <= 0:
            raise ValueError("alpha 必须为正")
        if min_support < 1:
            raise ValueError("min_support 必须 >= 1")
        self.bucket_span = bucket_span
        self.window_buckets = window_buckets
        self.alpha = float(alpha)
        self.min_support = int(min_support)
        self.buckets: dict[int, Bucket] = {}
        self.doc_index: dict[str, int] = {}   # doc_id -> bucket_id
        self.event_log: list[tuple] = []      # 供独立校验器重放
        self.duplicate_events = 0

    # ---------------- 增量维护（只触及单个桶） ----------------

    def bucket_of(self, ts: int) -> int:
        return ts // self.bucket_span

    def add_document(self, doc_id: str, ts: int, text: str) -> bool:
        """登记文档；迟到文档修订其时间戳所属的历史桶。

        返回 False 表示这是重复事件（doc_id 已可见），按幂等处理忽略。
        """
        tokens = frozenset(tokenize(text))
        bucket_id = self.bucket_of(ts)
        self.event_log.append(("add", doc_id, bucket_id, tokens))
        if doc_id in self.doc_index:
            self.duplicate_events += 1
            return False
        bucket = self.buckets.setdefault(bucket_id, Bucket())
        bucket.docs.add(doc_id)
        bucket.doc_tokens[doc_id] = tokens
        bucket.df.update(tokens)
        self.doc_index[doc_id] = bucket_id
        return True

    def retract_document(self, doc_id: str) -> bool:
        """按时间桶撤回文档；不存在的 doc_id 为无害空操作。"""
        self.event_log.append(("retract", doc_id))
        bucket_id = self.doc_index.pop(doc_id, None)
        if bucket_id is None:
            return False
        bucket = self.buckets[bucket_id]
        tokens = bucket.doc_tokens.pop(doc_id)
        bucket.docs.discard(doc_id)
        for term in tokens:
            bucket.df[term] -= 1
            if bucket.df[term] <= 0:
                del bucket.df[term]
        return True

    # ---------------- 窗口统计与比较 ----------------

    def window_stats(self, end_bucket: int) -> WindowStats:
        """汇总 [end-w+1, end] 共 w 个桶；只访问窗口内的桶，不扫全史。"""
        start = end_bucket - self.window_buckets + 1
        df = Counter()
        docs = set()
        for b in range(start, end_bucket + 1):
            bucket = self.buckets.get(b)
            if bucket is None:
                continue
            docs |= bucket.docs
            df.update(bucket.df)
        return WindowStats(start, end_bucket, df, frozenset(docs))

    def window_pair(self, ts: int) -> tuple[int, int]:
        """滑窗推进：给定时刻返回 (上一窗口末桶, 当前窗口末桶)。"""
        cur_end = self.bucket_of(ts)
        return cur_end - self.window_buckets, cur_end

    def smoothed_ratio(self, df_cur: int, n_cur: int,
                       df_prev: int, n_prev: int) -> float:
        a = self.alpha
        return ((df_cur + a) / (n_cur + a)) / ((df_prev + a) / (n_prev + a))

    def _upper_bound(self, df_cur: int, n_cur: int, n_prev: int) -> float:
        # 最乐观情形 df_prev = 0；alpha 与 min_support（前置过滤）共同进入上界
        a = self.alpha
        return ((df_cur + a) / (n_cur + a)) / (a / (n_prev + a))

    def compare(self, prev_end: int, cur_end: int, k: int = 10) -> Ranking:
        prev = self.window_stats(prev_end)
        cur = self.window_stats(cur_end)
        if prev.n == 0 or cur.n == 0:
            which = "上一窗口" if prev.n == 0 else "当前窗口"
            if prev.n == 0 and cur.n == 0:
                which = "两个窗口"
            return Ranking([], f"{which}为空（N=0），不报告增长",
                           prev.n, cur.n, 0, 0, 0)
        candidates = [(t, c) for t, c in cur.df.items()
                      if c >= self.min_support]
        order = sorted(candidates,
                       key=lambda tc: (-self._upper_bound(tc[1], cur.n, prev.n), tc[0]))
        heap = []          # (score, term)，容量 k 的小顶堆
        by_term = {}
        evaluated = 0
        pruned = 0
        kth = None
        for idx, (term, c) in enumerate(order):
            if kth is not None and self._upper_bound(c, cur.n, prev.n) <= kth:
                pruned = len(order) - idx
                break
            p = prev.df.get(term, 0)
            score = self.smoothed_ratio(c, cur.n, p, prev.n)
            evaluated += 1
            by_term[term] = RankingEntry(term, p, c, prev.n, cur.n, score)
            if len(heap) < k:
                heapq.heappush(heap, (score, term))
            elif (score, term) > heap[0]:
                heapq.heapreplace(heap, (score, term))
            if len(heap) == k:
                kth = heap[0][0]
        entries = sorted((by_term[term] for _, term in heap),
                         key=lambda e: (-e.score, e.term))
        return Ranking(entries, None, prev.n, cur.n,
                       len(candidates), evaluated, pruned)

    # ---------------- 独立全量重算校验器 ----------------

    @staticmethod
    def replay(event_log):
        """从事件日志独立重放可见文档集合；重复 add 事件只计一次。"""
        docs = {}   # doc_id -> (bucket_id, tokens)
        duplicates = 0
        for ev in event_log:
            if ev[0] == "add":
                _, doc_id, bucket_id, tokens = ev
                if doc_id in docs:
                    duplicates += 1
                    continue
                docs[doc_id] = (bucket_id, tokens)
            else:  # retract
                docs.pop(ev[1], None)
        return docs, duplicates

    @staticmethod
    def _window_from_docs(docs, start, end):
        df = Counter()
        ids = set()
        for doc_id, (b, tokens) in docs.items():
            if start <= b <= end:
                ids.add(doc_id)
                df.update(tokens)
        return df, ids

    def verify(self, prev_end: int, cur_end: int) -> CheckReport:
        """独立全量重算，与增量状态对账；输出每处差异的贡献证据。"""
        docs, duplicates = self.replay(self.event_log)
        corrections = []
        recomputed = {}
        for label, end in (("prev", prev_end), ("cur", cur_end)):
            start = end - self.window_buckets + 1
            rdf, rids = self._window_from_docs(docs, start, end)
            recomputed[label] = (rdf, rids)
            inc = self.window_stats(end)
            if set(inc.docs) != rids:  # 分母（可见文档身份）不一致
                corrections.append(CorrectionEvidence(
                    "denominator", label, None,
                    sorted(inc.docs), sorted(rids),
                    tuple(sorted(set(inc.docs) ^ rids))))
            for term in sorted(set(inc.df) | set(rdf)):
                if inc.df.get(term, 0) != rdf.get(term, 0):
                    contrib = tuple(sorted(d for d in rids if term in docs[d][1]))
                    corrections.append(CorrectionEvidence(
                        "df", label, term,
                        inc.df.get(term, 0), rdf.get(term, 0), contrib))
        n_prev = len(recomputed["prev"][1])
        n_cur = len(recomputed["cur"][1])
        return CheckReport(
            ok=not corrections,
            corrections=corrections,
            duplicates_ignored=duplicates,
            prev_empty=n_prev == 0,
            cur_empty=n_cur == 0,
            recomputed_n_prev=n_prev,
            recomputed_n_cur=n_cur,
        )


# ---------------- 运行样例 ----------------

def _print_ranking(title, ranking):
    print(f"\n== {title} ==")
    if ranking.degenerate:
        print(f"  [退化] {ranking.degenerate}")
        return
    print(f"  分母: N_prev={ranking.n_prev} N_cur={ranking.n_cur}  "
          f"候选={ranking.candidates} 精确求值={ranking.evaluated} "
          f"剪枝={ranking.pruned}")
    print(f"  {'词':<8}{'df_prev':>8}{'df_cur':>8}{'平滑比率':>10}")
    for e in ranking.entries:
        print(f"  {e.term:<8}{e.df_prev:>8}{e.df_cur:>8}{e.score:>10.3f}")


def main():
    eng = TrendEngine(bucket_span=1, window_buckets=3, alpha=1.0, min_support=2)
    docs = [
        ("d1", 0, "苹果 发布 新款 手机 apple phone 手机"),
        ("d2", 1, "苹果 手机 销量 上升 apple sales"),
        ("d3", 2, "香蕉 和 苹果 水果 价格"),
        ("d4", 3, "手机 电池 续航 评测 phone battery"),
        ("d5", 4, "手机 屏幕 电池 评测"),
        ("d6", 5, "手机 电池 快充 评测"),
    ]
    for doc_id, ts, text in docs:
        eng.add_document(doc_id, ts, text)

    # 1) 空窗：上一窗口没有任何文档，不可伪报增长
    prev_end, cur_end = eng.window_pair(2)
    _print_ranking("t=2 滑窗比较（上一窗口为空）",
                   eng.compare(prev_end, cur_end, k=5))

    # 2) 正常滑窗比较
    prev_end, cur_end = eng.window_pair(5)
    _print_ranking("t=5 滑窗比较（窗口 [0,2] vs [3,5]）",
                   eng.compare(prev_end, cur_end, k=5))

    # 3) 迟到文档：时间戳 t=4，修订历史桶 4
    print("\n-- 迟到文档 d7 (ts=4) 到达，修订桶 4 --")
    eng.add_document("d7", 4, "手机 评测 市场 增长")
    _print_ranking("迟到修订后 t=5 滑窗比较",
                   eng.compare(prev_end, cur_end, k=5))

    # 4) 时间桶撤回：d3 从桶 2 撤下，分母随之变化
    print("\n-- 撤回文档 d3 (ts=2) --")
    eng.retract_document("d3")
    _print_ranking("撤回后 t=5 滑窗比较",
                   eng.compare(prev_end, cur_end, k=5))

    # 5) 重复事件幂等
    print("\n-- 重复提交 d4（应被忽略） --")
    accepted = eng.add_document("d4", 3, "手机 电池 续航 评测 phone battery")
    print(f"  重复事件被接受? {accepted}")

    # 6) TopK 上界剪枝演示
    print("\n== TopK 上界剪枝演示 ==")
    eng2 = TrendEngine(bucket_span=1, window_buckets=2, alpha=1.0, min_support=2)
    eng2.add_document("p0", 0, "filler0")
    eng2.add_document("p1", 1, "filler1")
    for i in range(3):
        for j in range(3):
            eng2.add_document(f"s{i}_{j}", 2 + j % 2, f"strong{i}")
    for i in range(20):
        for j in range(2):
            eng2.add_document(f"w{i}_{j}", 2 + j % 2, f"weak{i}")
    _print_ranking("k=3（20 个弱候选应被上界剪枝）", eng2.compare(1, 3, k=3))

    # 7) 独立全量重算校验
    report = eng.verify(prev_end, cur_end)
    print("\n== 独立全量重算校验 ==")
    print(f"  一致: {report.ok}  更正数: {len(report.corrections)}  "
          f"重复事件忽略: {report.duplicates_ignored}")
    print(f"  重算分母: N_prev={report.recomputed_n_prev} "
          f"N_cur={report.recomputed_n_cur}  "
          f"零窗: prev={report.prev_empty} cur={report.cur_empty}")
    for c in report.corrections:
        print(f"  [更正] {c.kind} {c.window} 词={c.term} "
              f"增量={c.incremental} 重算={c.recomputed} 证据={c.contributing_docs}")


if __name__ == "__main__":
    main()
