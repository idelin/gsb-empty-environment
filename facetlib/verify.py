"""逐文档布尔参考实现与全组合核对。

参考实现直接对每份文档做布尔求值（与位图引擎完全独立），用于核对
**所有选择组合**下引擎的计数是否一致。参考实现只在核对阶段运行；
位图引擎本身从不逐文档扫描。
"""

from __future__ import annotations

import itertools

from .documents import Doc
from .query import MISSING, Query, evaluate
from .tokenizer import tokenize


class _RefDoc:
    __slots__ = ("doc_id", "tokens", "facets")

    def __init__(self, doc: Doc) -> None:
        self.doc_id = doc.doc_id
        self.tokens = frozenset(tokenize(doc.text))
        self.facets = {f: frozenset(vs) for f, vs in doc.facets.items()}


def _matches(ref: _RefDoc, terms: frozenset, filters, skip_facet) -> bool:
    if not terms <= ref.tokens:
        return False
    for facet, selection in filters.items():
        if facet == skip_facet or not selection:
            continue
        values = ref.facets.get(facet)
        if values is None:  # 分面缺失
            if MISSING not in selection:
                return False
        else:  # 分面存在（可能为空集合）：须命中某个具体值
            concrete = {v for v in selection if v is not MISSING}
            if not values & concrete:
                return False
    return True


def reference_counts(ref_docs, terms, filters, facet, self_exclude=True):
    """返回 (基线文档数, {值: 计数}, 缺失计数)。同文档同值经 frozenset 去重。"""
    skip = facet if self_exclude else None
    counts: dict[str, int] = {}
    base = 0
    missing = 0
    for ref in ref_docs:
        if not _matches(ref, terms, filters, skip):
            continue
        base += 1
        values = ref.facets.get(facet)
        if values is None:
            missing += 1
        else:
            for value in values:
                counts[value] = counts.get(value, 0) + 1
    return base, counts, missing


def _all_selections(values):
    """某分面的全部可能选择：值域 ∪ {MISSING} 的所有子集（空集 = 不约束）。"""
    options = list(values) + [MISSING]
    for r in range(len(options) + 1):
        for combo in itertools.combinations(options, r):
            yield frozenset(combo)


def verify_all_combinations(docs, store, domains, term_queries=((),),
                            self_exclude=True):
    """穷举所有分面选择组合，逐项比对引擎与逐文档布尔参考。

    返回 (组合数, 核对项数, 不一致列表)。
    """
    ref_docs = [_RefDoc(d) for d in docs]
    facet_names = sorted(domains)
    selection_lists = [list(_all_selections(domains[f])) for f in facet_names]
    combos = 0
    checks = 0
    mismatches = []
    for terms in term_queries:
        term_set = frozenset(terms)
        for combo in itertools.product(*selection_lists):
            filters = dict(zip(facet_names, combo))
            query = Query(terms=tuple(terms), filters=filters)
            results, _ = evaluate(store, query, count_facets=facet_names,
                                  self_exclude=self_exclude)
            combos += 1
            for facet in facet_names:
                base, counts, missing = reference_counts(
                    ref_docs, term_set, filters, facet, self_exclude)
                got = results[facet]
                checks += 1
                same_counts = all(
                    got.counts.get(v, 0) == counts.get(v, 0)
                    for v in set(counts) | set(got.counts)
                )
                if got.base_size != base or got.missing != missing or not same_counts:
                    mismatches.append((terms, filters, facet))
    return combos, checks, mismatches
