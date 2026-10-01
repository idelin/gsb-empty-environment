"""有界推导树枚举（最左推导 ⇔ 推导树一一对应）。

仅用于交叉核验与反例展示：
- 证书声称的见证串必须能在足够深度内被枚举发现（正向核验）；
- "枚举没找到"绝不作为不可生成的证明 —— 不可生成性只由
  最小不动点证书证明。
"""
from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass

from .grammar import Grammar


@dataclass
class EnumerationResult:
    start: str
    max_depth: int
    yields: dict[str, int]          # 终结串 -> 推导（树）数量（可能被封顶）
    truncated: bool                 # 是否因资源上限被截断
    expanded_forms: int             # 展开的句型数

    def count(self, string: str) -> int:
        return self.yields.get(string, 0)


def bounded_enumerate(grammar: Grammar, start: str | None = None,
                      max_depth: int = 8, form_cap: int = 200_000,
                      count_cap: int = 1_000_000) -> EnumerationResult:
    """枚举 start（默认为开始符）出发、深度 ≤ max_depth 的全部最左推导，
    汇总每个终结串的推导数量。深度 = 产生式应用次数。"""
    start = grammar.start if start is None else start
    # 每个非终结符的产生式下标
    prods_of: dict[str, list[int]] = defaultdict(list)
    for idx, prod in enumerate(grammar.productions):
        prods_of[prod.lhs].append(idx)

    yields: dict[str, int] = {}
    forms: dict[tuple[str, ...], int] = {(start,): 1}
    truncated = False
    expanded = 0
    for _depth in range(max_depth):
        next_forms: dict[tuple[str, ...], int] = defaultdict(int)
        for form, cnt in forms.items():
            # 找最左非终结符
            left = None
            for i, s in enumerate(form):
                if s in grammar.nonterminals:
                    left = i
                    break
            if left is None:
                yields["".join(form)] = min(count_cap, yields.get("".join(form), 0) + cnt)
                continue
            expanded += 1
            if expanded > form_cap:
                truncated = True
                break
            sym = form[left]
            for pidx in prods_of.get(sym, ()):
                rhs = grammar.productions[pidx].rhs
                new_form = form[:left] + rhs + form[left + 1:]
                if any(s in grammar.nonterminals for s in new_form):
                    next_forms[new_form] = min(count_cap, next_forms[new_form] + cnt)
                else:
                    key = "".join(new_form)
                    yields[key] = min(count_cap, yields.get(key, 0) + cnt)
        if truncated:
            break
        forms = dict(next_forms)
        if not forms:
            break
    return EnumerationResult(start, max_depth, yields, truncated, expanded)
