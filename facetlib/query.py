"""分面查询与自排除（disjunctive）计数引擎。

语义约定（显式、无歧义）
------------------------
* 主查询：若干词项，文档须**同时包含全部词项**（AND）才进入计数范围；
* 同一分面内多个选中值之间为 **OR**（含哨兵 ``MISSING`` 表示“选缺失”）；
* 不同分面之间为 **AND**；
* 空选择（空 frozenset）= 该分面**不约束**，与“选中 MISSING”是两回事；
* 自排除模式：分面 F 的计数基线 = 主查询 AND 除 F 外所有分面的过滤，
  即 F 的计数**忽略 F 自身的选中值**，但尊重其它分面的过滤；
* 同时选中两个分面 A、B 时：A 的计数范围含 B 的过滤、B 的计数范围含 A 的过滤，
  两者各自只排除自己；
* 计数绝不包含不匹配主查询的文档（基线总是 AND 主查询位图）。

共享中间结果
------------
主查询位图只算一次，被所有分面计数复用；每个分面的过滤位图只算一次；
对 k 个受约束分面用**前缀积 + 后缀积**求各自基线，合取次数为 2k，
而非朴素逐分面重算的 k(k-1)。整个过程只做位图运算，不为任何分面重扫文档。
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .tokenizer import tokenize


class _Missing:
    """“选中缺失值”的哨兵；与空选择（不约束）严格区分。"""

    def __repr__(self) -> str:  # pragma: no cover - 仅用于展示
        return "<缺失>"


MISSING = _Missing()


@dataclass
class Query:
    terms: tuple[str, ...] = ()
    filters: dict[str, frozenset] = field(default_factory=dict)


def text_query(text: str) -> tuple[str, ...]:
    """把用户输入的主查询文本按同一套分词规则转为词项元组。"""
    return tuple(tokenize(text))


@dataclass
class EvalStats:
    """单次求值的位图访问量与复用情况。"""

    main_bitmap_built: int = 0  # 主查询位图构建次数（恒为 1，被所有分面复用）
    filter_bitmaps_built: int = 0  # 分面过滤位图构建次数（每个受约束分面仅 1 次）
    conjunction_ands: int = 0  # 基线合取次数（前缀+后缀共享 => 2k）
    term_bitmap_reads: int = 0
    value_bitmap_reads: int = 0
    facets_counted: int = 0
    docs_scanned: int = 0  # 恒为 0：引擎从不逐文档扫描


@dataclass
class FacetResult:
    facet: str
    base_size: int  # 该分面计数基线中的文档数
    counts: dict[str, int]  # 值 -> 文档数（仅列出 >0 的值）
    missing: int  # 基线中缺失该分面的文档数（缺失单列）


def evaluate(store, query: Query, count_facets=None, self_exclude: bool = True,
             stats: EvalStats | None = None):
    """在索引或快照 ``store`` 上求值，返回 ``(results, stats)``。

    ``count_facets`` 为 None 时对库中全部分面计数。
    """
    stats = stats if stats is not None else EvalStats()

    # 1) 主查询位图：只构建一次，之后被所有分面计数共享。
    main = store.alive
    stats.main_bitmap_built += 1
    for term in query.terms:
        stats.term_bitmap_reads += 1
        main &= store.terms.get(term, 0)

    # 2) 每个受约束分面的过滤位图（分面内 OR）：各只构建一次。
    filt: dict[str, int] = {}
    for facet in sorted(query.filters):
        selection = query.filters[facet]
        if not selection:  # 空选择 = 不约束
            continue
        bits = 0
        for value in sorted(selection, key=repr):
            stats.value_bitmap_reads += 1
            if value is MISSING:
                bits |= store.missing.get(facet, 0)
            else:
                bits |= store.facets.get(facet, {}).get(value, 0)
        filt[facet] = bits
        stats.filter_bitmaps_built += 1

    # 3) 前缀积 / 后缀积：k 个过滤分面只用 2k 次合取即得全部自排除基线。
    keys = sorted(filt)
    n = len(keys)
    prefix = [main]
    for key in keys:
        prefix.append(prefix[-1] & filt[key])
        stats.conjunction_ands += 1
    suffix = [store.full_mask] * (n + 1)
    for i in range(n - 1, -1, -1):
        suffix[i] = suffix[i + 1] & filt[keys[i]]
        stats.conjunction_ands += 1

    if count_facets is None:
        count_facets = sorted(set(store.facets) | set(store.missing))

    results: dict[str, FacetResult] = {}
    for facet in count_facets:
        if self_exclude and facet in filt:
            i = keys.index(facet)
            base = prefix[i] & suffix[i + 1]  # 主查询 AND 其它分面过滤
        else:
            base = prefix[n]  # 主查询 AND 全部分面过滤
        counts: dict[str, int] = {}
        for value, bitmap in store.facets.get(facet, {}).items():
            stats.value_bitmap_reads += 1
            count = (base & bitmap).bit_count()
            if count:
                counts[value] = count
        stats.value_bitmap_reads += 1
        missing = (base & store.missing.get(facet, 0)).bit_count()
        results[facet] = FacetResult(facet, base.bit_count(), counts, missing)
        stats.facets_counted += 1
    return results, stats
