"""内置样例文法：覆盖题目要求的各特殊情形。"""
from __future__ import annotations

from .grammar import Grammar


def _g(terminals, nonterminals, start, prods) -> Grammar:
    return Grammar.build(terminals, nonterminals, start,
                         [(lhs, tuple(rhs.split())) for lhs, rhs in prods])


SAMPLES: dict[str, tuple[str, Grammar]] = {}


def _add(name: str, desc: str, grammar: Grammar):
    SAMPLES[name] = (desc, grammar)


_add(
    "cycle_no_exit",
    "仅 A→B、B→A：互相依赖、无终结出口 ⇒ 均不可生成；开始符无用 ⇒ L(G)=∅",
    _g(["a"], ["A", "B"], "A", [("A", "B"), ("B", "A")]),
)

_add(
    "nullable_cycle_with_exit",
    "可空环 A⇔B 另有终结出口 A→a：清理时绝不能全删；"
    "无限歧义（'a' 与 '' 均有无限推导）但语言有限 {ε, a}",
    _g(["a"], ["S", "A", "B"], "S",
       [("S", "A"), ("A", "B"), ("B", "A"), ("B", ""), ("A", "a")]),
)

_add(
    "unreachable",
    "X、Y 可生成但自 S 不可达 ⇒ 无用规则被清理，语言不变",
    _g(["a"], ["S", "A", "X", "Y"], "S",
       [("S", "A a"), ("A", "a"), ("A", ""), ("X", "a"), ("Y", "X a")]),
)

_add(
    "growth_cycle",
    "A→Aa|a：语言无限 {a, aa, ...}，但无 ε-环 ⇒ 非无限歧义",
    _g(["a"], ["S", "A"], "S",
       [("S", "A"), ("A", "A a"), ("A", "a")]),
)

_add(
    "epsilon_context_cycle",
    "A→BA, B→ε, A→a：非单位产生式构成的 ε-环；'a' 有无限推导，语言 {a} 有限",
    _g(["a"], ["S", "A", "B"], "S",
       [("S", "A"), ("A", "B A"), ("A", "a"), ("B", "")]),
)

_add(
    "deep_witness",
    "A0→A1→…→A9→x：见证链深 10。浅层枚举（深度≤3）找不到任何终结串，"
    "但不可生成性只能由不动点证书判定 —— 枚举没找到 ≠ 不可生成",
    _g(["x"], [f"A{i}" for i in range(10)], "A0",
       [(f"A{i}", f"A{i+1}") for i in range(9)] + [("A9", "x")]),
)

_add(
    "mixed",
    "综合：可生成/不可生成/不可达/ε-环/增长环并存",
    _g(["a", "b"], ["S", "A", "B", "C", "D", "E", "F"], "S",
       [("S", "A b"), ("A", "a"), ("A", "B"), ("B", "A"),   # A⇔B 单位环，A→a 出口
        ("C", "D"), ("D", "C"),                              # 无出口环 ⇒ 不可生成
        ("E", "a"), ("F", "E"),                              # 不可达
        ("S", "C")]),
)
