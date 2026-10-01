"""无用规则清理与等价简化。

清理顺序（标准定理，保持语言不变）：
1. 删除含不可生成符号的规则；
2. 在剩余文法上删除自开始符不可达的规则。
另有可选的单位环消除（语言等价，可消除单位 ε-环带来的无限歧义，
但保留终结出口 —— 可空环另有终结出口时绝不全删）。
"""
from __future__ import annotations

from dataclasses import dataclass

from .chains import ProblemChain
from .grammar import Grammar, Production
from .reach import reachable_bfs


@dataclass
class RuleFate:
    index: int
    production: Production
    kept: bool
    new_index: int | None
    reasons: list[str]


@dataclass
class CleanupResult:
    simplified: Grammar
    fates: list[RuleFate]

    @property
    def removed_count(self) -> int:
        return sum(1 for f in self.fates if not f.kept)


def remove_useless(grammar: Grammar, generating: frozenset[str],
                   chains: dict[str, ProblemChain] | None = None) -> CleanupResult:
    chains = chains or {}
    gen = set(generating)

    def chain_hint(sym: str) -> str:
        ch = chains.get(sym)
        if ch is None:
            return ""
        if ch.root_kind == "no_rules":
            return f"（根因: {ch.root!r} 无产生式）"
        return f"（根因: 环 {' ⇒ '.join(ch.root_cycle)} 无终结出口）"

    # 第一遍：可生成性
    stage1_alive: list[bool] = []
    reasons: list[list[str]] = [[] for _ in grammar.productions]
    for i, prod in enumerate(grammar.productions):
        ok = True
        if prod.lhs not in gen:
            reasons[i].append(f"左部 {prod.lhs!r} 不可生成{chain_hint(prod.lhs)}")
            ok = False
        for s in prod.rhs:
            if s in grammar.nonterminals and s not in gen:
                reasons[i].append(f"右部符号 {s!r} 不可生成{chain_hint(s)}")
                ok = False
        stage1_alive.append(ok)
    g1_prods = [p for p, ok in zip(grammar.productions, stage1_alive) if ok]
    g1 = Grammar(grammar.terminals, grammar.nonterminals, grammar.start, tuple(g1_prods))

    # 第二遍：可达性（在剔除不可生成符号后的文法上）
    if grammar.start in gen:
        reach = reachable_bfs(g1)
    else:
        reach = frozenset()
    stage2_alive: list[bool] = []
    for i, prod in enumerate(grammar.productions):
        if not stage1_alive[i]:
            stage2_alive.append(False)
            continue
        ok = True
        if prod.lhs not in reach:
            reasons[i].append(f"左部 {prod.lhs!r} 自开始符 {grammar.start!r} 不可达")
            ok = False
        for s in prod.rhs:
            if s in grammar.nonterminals and s not in reach:
                reasons[i].append(f"右部符号 {s!r} 不可达")
                ok = False
        stage2_alive.append(ok)

    simplified_prods: list[Production] = []
    fates: list[RuleFate] = []
    for i, prod in enumerate(grammar.productions):
        if stage2_alive[i]:
            fates.append(RuleFate(i, prod, True, len(simplified_prods), []))
            simplified_prods.append(prod)
        else:
            fates.append(RuleFate(i, prod, False, None, reasons[i]))
    simplified = Grammar(grammar.terminals, grammar.nonterminals,
                         grammar.start, tuple(simplified_prods))
    return CleanupResult(simplified, fates)


def eliminate_unit_cycles(grammar: Grammar) -> tuple[Grammar, list[str]]:
    """单位产生式消除（语言等价）：对每个 A，把经单位链 A ⇒* B 可达的
    非单位产生式 B→α 直接挂到 A 上，然后删除全部单位产生式。
    单位 ε-环（如 A→B, B→A）随之消除，终结出口全部保留。"""
    notes: list[str] = []
    unit_adj: dict[str, set[str]] = {s: set() for s in grammar.nonterminals}
    non_unit: dict[str, list[tuple[str, ...]]] = {s: [] for s in grammar.nonterminals}
    unit_count = 0
    for prod in grammar.productions:
        if len(prod.rhs) == 1 and prod.rhs[0] in grammar.nonterminals:
            unit_adj[prod.lhs].add(prod.rhs[0])
            unit_count += 1
        else:
            non_unit[prod.lhs].append(prod.rhs)
    if unit_count == 0:
        return grammar, ["无单位产生式，无需消除"]
    # 单位闭包 U(A)
    closure: dict[str, set[str]] = {}
    for a in grammar.nonterminals:
        seen = {a}
        stack = [a]
        while stack:
            x = stack.pop()
            for y in unit_adj.get(x, ()):
                if y not in seen:
                    seen.add(y)
                    stack.append(y)
        closure[a] = seen
    new_prods: list[Production] = []
    seen_pairs: set[tuple[str, tuple[str, ...]]] = set()
    for a in sorted(grammar.nonterminals):
        for b in sorted(closure[a]):
            for rhs in non_unit[b]:
                if (a, rhs) not in seen_pairs:
                    seen_pairs.add((a, rhs))
                    new_prods.append(Production(a, rhs))
    notes.append(
        f"消除 {unit_count} 条单位产生式；对每个 A 补入单位链可达的非单位产生式（语言等价）"
    )
    result = Grammar(grammar.terminals, grammar.nonterminals, grammar.start, tuple(new_prods))
    return result, notes
