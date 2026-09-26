"""独立核验：逐文档暴力枚举，与索引匹配器的结果对照。

brute_force_field 不经过倒排表与分块跳跃，直接在词条序列上枚举
每个槽位的起始位置（显式检查位置不复用），并自带一份独立的
覆盖区间/最小化实现，作为索引结果的参照；
compare_document 用于逐文档比对两侧输出。
"""
from __future__ import annotations

from .tokenize import tokenize


def brute_force_field(tokens, query):
    """在单个字段的词条序列上暴力枚举全部匹配分配。"""
    terms_at = [t.term for t in tokens]
    n_tokens = len(tokens)
    n_slots = len(query.slots)
    results = []

    def rec(i, prev_end, slop_left, chosen, used):
        if i == n_slots:
            results.append(tuple(chosen))
            return
        for alt in query.slots[i]:
            length = len(alt)
            for p in range(prev_end, n_tokens - length + 1):
                gap = 0 if i == 0 else p - prev_end
                if gap > slop_left:
                    break
                positions = tuple(range(p, p + length))
                if any(pos in used for pos in positions):
                    continue
                if all(terms_at[p + j] == alt[j] for j in range(length)):
                    chosen.append((i, tuple(alt), p, positions))
                    rec(
                        i + 1,
                        p + length,
                        slop_left - gap,
                        chosen,
                        used | set(positions),
                    )
                    chosen.pop()

    rec(0, 0, query.slop, [], frozenset())
    return results


def _span_of(hits):
    """独立的覆盖区间实现：hits 为 (slot, terms, start, positions) 元组。"""
    start = hits[0][2]
    last = hits[-1]
    return (start, last[2] + len(last[1]) - 1)


def _filter_minimal(assignments):
    """独立的最小覆盖过滤：丢弃严格包含其他命中区间的分配。"""
    spans = [_span_of(a) for a in assignments]
    kept = []
    for i, (s, e) in enumerate(spans):
        dominated = False
        for j, (s2, e2) in enumerate(spans):
            if i == j:
                continue
            if s2 >= s and e2 <= e and (s2 > s or e2 < e):
                dominated = True
                break
        if not dominated:
            kept.append(assignments[i])
    return kept


def brute_force_document(fields, query):
    """逐字段暴力枚举，返回可比较的键集合。"""
    out = set()
    for field_name, text in fields.items():
        tokens = tokenize(text)
        assignments = _filter_minimal(brute_force_field(tokens, query))
        for hits in assignments:
            span = _span_of(hits)
            key = tuple((h[0], h[1], h[2]) for h in hits)
            out.add((field_name, span, key))
    return out


def index_side_document(matches_for_doc):
    """把索引侧某文档的 Match 列表规整成可比较的键集合。"""
    out = set()
    for m in matches_for_doc:
        key = tuple((h.slot_index, tuple(h.terms), h.start_pos) for h in m.evidence)
        out.add((m.field, (m.pos_start, m.pos_end), key))
    return out


def compare_document(fields, matches_for_doc, query):
    """对照单个文档的索引结果与暴力枚举结果。"""
    expected = brute_force_document(fields, query)
    actual = index_side_document(matches_for_doc)
    return actual == expected, actual, expected
