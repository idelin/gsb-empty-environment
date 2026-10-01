"""无用规则清理与等价简化。

两阶段顺序(先删不可生成、再删不可达)是保持语言不变的标准构造:
  1. 删去左部不可生成或右部含不可生成符号的规则(不影响 L(G),
     因为这些规则不可能出现在任何终结推导中);
  2. 在剩余文法上删去开始符不可达的规则。
若开始符本身不可生成,则 L(G) = ∅,返回空语言说明而非文法。
"""
from dataclasses import dataclass

from .generating import compute_generating
from .grammar import Grammar
from .reachability import compute_reachable


@dataclass(frozen=True)
class CleanupEntry:
    index: int
    production: object
    action: str   # 'keep' | 'drop'
    reason: str


@dataclass
class Simplification:
    entries: tuple
    mapping: dict        # 旧产生式编号 -> 新编号 或 None(已删除)
    grammar: object      # 简化后的等价文法;空语言时为 None
    empty_language: bool
    explanation: str


def simplify(grammar, cert=None, reachable=None):
    if cert is None:
        cert = compute_generating(grammar)
    if reachable is None:
        reachable = compute_reachable(grammar)
    gen = cert.generating
    entries = []
    kept = []
    for i, p in enumerate(grammar.productions):
        reason = None
        if p.lhs not in gen:
            reason = f"左部 {p.lhs} 不可生成终结串"
        else:
            bad = [s for s in p.rhs
                   if s in grammar.nonterminals and s not in gen]
            if bad:
                reason = f"右部含不可生成符号 {bad}"
            elif p.lhs not in reachable:
                reason = f"左部 {p.lhs} 自开始符 {grammar.start} 不可达"
        if reason is None:
            entries.append(CleanupEntry(i, p, "keep", "有用规则"))
            kept.append(i)
        else:
            entries.append(CleanupEntry(i, p, "drop", reason))
    if grammar.start not in gen:
        mapping = {i: None for i in range(len(grammar.productions))}
        return Simplification(
            entries=tuple(entries),
            mapping=mapping,
            grammar=None,
            empty_language=True,
            explanation=(
                f"L(G) = ∅: 开始符 {grammar.start} 不可生成任何终结串, "
                "所有推导都无法终结; 等价简化结果为空文法(无任何产生式)。"),
        )
    new_prods = [grammar.productions[i] for i in kept]
    used_nt = {grammar.start}
    used_t = set()
    for p in new_prods:
        used_nt.add(p.lhs)
        for s in p.rhs:
            if s in grammar.nonterminals:
                used_nt.add(s)
            else:
                used_t.add(s)
    mapping = {}
    for new_i, old_i in enumerate(kept):
        mapping[old_i] = new_i
    for e in entries:
        if e.action == "drop":
            mapping[e.index] = None
    simplified = Grammar(used_t, used_nt, grammar.start, new_prods)
    dropped = sum(1 for e in entries if e.action == "drop")
    return Simplification(
        entries=tuple(entries),
        mapping=mapping,
        grammar=simplified,
        empty_language=False,
        explanation=(
            f"删除 {dropped} 条无用规则, 保留 {len(kept)} 条; "
            "两阶段清理(先不可生成、后不可达)保持 L(G) 不变。"),
    )
