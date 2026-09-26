"""短语查询：同义位置图、可配置间隔（slop）、锚点跳跃匹配。

查询模型（同义位置图）：
    查询由若干“槽位”（slot）组成，每个槽位是一组同义备选；
    每个备选是一个或多个词组成的序列（支持多词同义，如 "new york" ~ "nyc"），
    因此槽位结构等价于一张按位置展开的同义词图。

匹配语义（有序、允许间隔）：
    为每个槽位 i 选定备选 a_i 与起始位置 p_i，要求：
      1. 词形匹配：位置 p_i + j 处的词等于 a_i[j]；
      2. 有序不重叠：p_{i+1} >= p_i + len(a_i)，因此同一个词位置
         永远不能同时充当两个查询项（重复词如 "a a" 必须占用两个位置）；
      3. 间隔约束：相邻槽位之间的空隙之和 <= slop。

为避免全笛卡尔积，匹配器选择“当前文档内最稀有的槽位”作为锚点，
向左右两侧按窗口 [lo, hi] 扩张，窗口内的位置通过分块跳跃枚举，
复杂度约为 O(锚点候选数 * 槽位数)，而不是各槽位位置数的乘积。
"""
from __future__ import annotations

from dataclasses import dataclass

from .postings import DecodeStats


def _norm_slot_alt(alt):
    if isinstance(alt, str):
        return (alt,)
    return tuple(alt)


class PhraseQuery:
    """同义位置图短语查询。

    slots: 槽位序列；每个槽位是备选序列，备选是词或词元组。
        例如 [["quick", "fast"], ["fox"]] 表示 (quick|fast) fox；
        [[("new", "york"), ("nyc",)]] 表示多词同义 new york ~ nyc。
    slop: 允许的总间隔（相邻槽位间空隙之和的上限），0 即精确短语。
    """

    def __init__(self, slots, slop=0):
        if slop < 0:
            raise ValueError("slop must be >= 0")
        self.slots = tuple(
            tuple(_norm_slot_alt(alt) for alt in slot) for slot in slots
        )
        if not self.slots:
            raise ValueError("query needs at least one slot")
        for slot in self.slots:
            if not slot:
                raise ValueError("slot needs at least one alternative")
        self.slop = slop

    @classmethod
    def of(cls, text, slop=0):
        """从空白分隔的词串构造精确/间隔短语查询。"""
        return cls([[w] for w in text.split()], slop=slop)

    def terms(self):
        seen = []
        for slot in self.slots:
            for alt in slot:
                for term in alt:
                    if term not in seen:
                        seen.append(term)
        return seen


@dataclass(frozen=True)
class SlotHit:
    """一个槽位的命中证据：选中的备选与占用的位置。"""

    slot_index: int
    terms: tuple
    start_pos: int
    positions: tuple


def _slot_data_for_field(query, get_field_postings):
    """取某个 (doc, field) 上每个槽位各备选的词位置表；缺词则备选不可用。"""
    slot_data = []
    for slot in query.slots:
        alts = []
        for alt in slot:
            fps = []
            for term in alt:
                fp = get_field_postings(term)
                if fp is None:
                    fps = None
                    break
                fps.append(fp)
            if fps is not None:
                alts.append((alt, fps))
        if not alts:
            return None
        slot_data.append(alts)
    return slot_data


def match_field(query, get_field_postings, stats=None):
    """在单个字段的位置空间内枚举全部匹配的槽位分配。

    get_field_postings(term) -> FieldPostings | None
    返回 tuple[SlotHit, ...] 列表（每个元素是一种完整分配）。
    """
    slot_data = _slot_data_for_field(query, get_field_postings)
    if slot_data is None:
        return []
    if stats is None:
        stats = DecodeStats()

    n = len(slot_data)

    def slot_cost(i):
        return min(fps[0].count for _, fps in slot_data[i])

    anchor = min(range(n), key=slot_cost)
    chosen = [None] * n
    results = []

    def expand_right(i, prev_end, slop_left):
        if i == n:
            results.append(
                tuple(
                    SlotHit(
                        slot_index=k,
                        terms=chosen[k][0],
                        start_pos=chosen[k][1],
                        positions=tuple(
                            range(chosen[k][1], chosen[k][1] + len(chosen[k][0]))
                        ),
                    )
                    for k in range(n)
                )
            )
            return
        for alt, fps in slot_data[i]:
            length = len(alt)
            hi = prev_end + slop_left
            for p in fps[0].iter_range(prev_end, hi, stats):
                ok = True
                for j in range(1, length):
                    if not fps[j].contains(p + j, stats):
                        ok = False
                        break
                if ok:
                    chosen[i] = (alt, p)
                    expand_right(i + 1, p + length, slop_left - (p - prev_end))

    def expand_left(i, next_start, slop_left):
        if i < 0:
            anchor_alt, anchor_p = chosen[anchor]
            expand_right(anchor + 1, anchor_p + len(anchor_alt), slop_left)
            return
        for alt, fps in slot_data[i]:
            length = len(alt)
            lo = max(0, next_start - slop_left - length)
            hi = next_start - length
            for p in fps[0].iter_range(lo, hi, stats):
                ok = True
                for j in range(1, length):
                    if not fps[j].contains(p + j, stats):
                        ok = False
                        break
                if ok:
                    chosen[i] = (alt, p)
                    expand_left(i - 1, p, slop_left - (next_start - (p + length)))

    for alt, fps in slot_data[anchor]:
        length = len(alt)
        for p in fps[0].iter_range(0, None, stats):
            ok = True
            for j in range(1, length):
                if not fps[j].contains(p + j, stats):
                    ok = False
                    break
            if ok:
                chosen[anchor] = (alt, p)
                expand_left(anchor - 1, p, query.slop)

    return list(dict.fromkeys(results))


def assignment_span(hits):
    """分配的覆盖位置区间 [start, end]（含端点）。"""
    start = hits[0].start_pos
    last = hits[-1]
    return (start, last.start_pos + len(last.terms) - 1)


def filter_minimal(assignments):
    """只保留最小覆盖范围：丢弃严格包含其他命中区间的分配。"""
    spans = [assignment_span(a) for a in assignments]
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

