"""独立全量重算校验器。

与增量索引完全分离地重算两个窗口的统计量，用于发现增量维护中的错误。
重算只扫描「两个窗口覆盖的桶内的文档」，不扫描全部历史文档。

检查内容：
1. 分母变化：两个窗口的可见文档数（按文档身份去重）是否与增量结果一致。
2. 零窗：近期窗可见文档为 0 时，增量排名必须为空（不伪报增长）。
3. 重复事件：同一 doc_id 在文档存储中只出现一次；重复到达事件数如实上报。
4. 排名更正：逐词对比 DF，排名不一致时输出每个词的贡献证据
   （该词在两个窗口中分别由哪些 doc_id 贡献）。
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .index import TrendIndex, TrendRecord


@dataclass
class Correction:
    """一次排名/统计更正及其贡献证据。"""

    kind: str               # "denominator" | "df" | "ranking" | "zero_window"
    term: str | None
    incremental: object     # 增量索引给出的值
    recomputed: object      # 全量重算给出的值
    evidence: dict = field(default_factory=dict)  # 贡献证据，如 {"recent": [doc_id...], "base": [...]}


@dataclass
class CheckReport:
    ok: bool
    corrections: list[Correction]
    docs_checked: int        # 参与重算的文档数（仅限两个窗口）
    duplicate_events: int    # 索引记录的重复事件数
    expected_ranking: list[TrendRecord]  # 重算得到的正确排名


def _window_df(docs: dict[str, tuple[int, frozenset[str]]],
               buckets: set[int]) -> tuple[int, dict[str, int], dict[str, list[str]]]:
    """从文档存储重算某窗口的 (分母, DF, 词->贡献doc_id列表)。"""
    doc_count = 0
    df: dict[str, int] = {}
    contrib: dict[str, list[str]] = {}
    for doc_id, (bucket, terms) in docs.items():
        if bucket not in buckets:
            continue
        doc_count += 1
        for term in terms:
            df[term] = df.get(term, 0) + 1
            contrib.setdefault(term, []).append(doc_id)
    return doc_count, df, contrib


def full_recompute_check(
    index: TrendIndex,
    base: tuple[int, int],
    recent: tuple[int, int],
    k: int,
) -> CheckReport:
    """对 index 的 top_trends 结果做独立全量重算校验。"""
    base_buckets = set(index._bucket_range(*base))
    recent_buckets = set(index._bucket_range(*recent))
    scope = base_buckets | recent_buckets
    # 只取两个窗口覆盖的桶内的文档，不触碰其余历史
    scoped_docs = {d: v for d, v in index._docs.items() if v[0] in scope}

    base_docs, base_df, base_contrib = _window_df(scoped_docs, base_buckets)
    recent_docs, recent_df, recent_contrib = _window_df(scoped_docs, recent_buckets)

    # 用与索引相同的打分规则重算排名
    alpha, min_sup = index.alpha, index.min_support
    expected: list[TrendRecord] = []
    if recent_docs > 0 and k > 0:
        for term, df in recent_df.items():
            if df < min_sup:
                continue
            bdf = base_df.get(term, 0)
            expected.append(TrendRecord(
                term=term, df_recent=df, df_base=bdf,
                recent_docs=recent_docs, base_docs=base_docs,
                score=(df + alpha) / (bdf + alpha),
            ))
        expected.sort(key=lambda r: (-r.score, -r.df_recent, r.term))
        expected = expected[:k]

    corrections: list[Correction] = []

    # 1. 分母检查
    inc_base_docs, _ = index.window_stats(*base)
    inc_recent_docs, _ = index.window_stats(*recent)
    if inc_base_docs != base_docs:
        corrections.append(Correction(
            kind="denominator", term=None,
            incremental=inc_base_docs, recomputed=base_docs,
            evidence={"window": "base", "doc_ids": sorted(
                d for d, (b, _) in scoped_docs.items() if b in base_buckets)},
        ))
    if inc_recent_docs != recent_docs:
        corrections.append(Correction(
            kind="denominator", term=None,
            incremental=inc_recent_docs, recomputed=recent_docs,
            evidence={"window": "recent", "doc_ids": sorted(
                d for d, (b, _) in scoped_docs.items() if b in recent_buckets)},
        ))

    # 2. 零窗检查
    actual = index.top_trends(base, recent, k)
    if recent_docs == 0 and actual:
        corrections.append(Correction(
            kind="zero_window", term=None,
            incremental=[r.term for r in actual], recomputed=[],
            evidence={"recent_docs": 0},
        ))

    # 3. 逐词 DF 检查（贡献证据：该词在两个窗口中的 doc_id 列表）
    inc_recent_doc_count, inc_recent_df = index.window_stats(*recent)
    inc_base_doc_count, inc_base_df = index.window_stats(*base)
    for term in sorted(set(inc_recent_df) | set(recent_df)):
        if inc_recent_df.get(term, 0) != recent_df.get(term, 0):
            corrections.append(Correction(
                kind="df", term=term,
                incremental=inc_recent_df.get(term, 0),
                recomputed=recent_df.get(term, 0),
                evidence={"window": "recent", "doc_ids": sorted(recent_contrib.get(term, []))},
            ))
    for term in sorted(set(inc_base_df) | set(base_df)):
        if inc_base_df.get(term, 0) != base_df.get(term, 0):
            corrections.append(Correction(
                kind="df", term=term,
                incremental=inc_base_df.get(term, 0),
                recomputed=base_df.get(term, 0),
                evidence={"window": "base", "doc_ids": sorted(base_contrib.get(term, []))},
            ))

    # 4. 排名检查：不一致时给出涉及词的贡献证据
    if [r.term for r in actual] != [r.term for r in expected]:
        involved = sorted({r.term for r in actual} | {r.term for r in expected})
        corrections.append(Correction(
            kind="ranking", term=None,
            incremental=[r.term for r in actual],
            recomputed=[r.term for r in expected],
            evidence={
                t: {
                    "recent_doc_ids": sorted(recent_contrib.get(t, [])),
                    "base_doc_ids": sorted(base_contrib.get(t, [])),
                }
                for t in involved
            },
        ))

    return CheckReport(
        ok=not corrections,
        corrections=corrections,
        docs_checked=len(scoped_docs),
        duplicate_events=index.duplicate_events,
        expected_ranking=expected,
    )
