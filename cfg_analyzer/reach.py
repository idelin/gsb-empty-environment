"""可达性分析：两种相互独立的实现（BFS 与集合格不动点迭代）用于交叉核验；
另提供"有用符号"（可生成上下文中的可达）与可达见证（具体推导步）。"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .core import apply_step, nt_positions
from .grammar import Grammar


def reachable_bfs(grammar: Grammar, only_generating: frozenset[str] | None = None) -> frozenset[str]:
    """从开始符出发的图搜索（队列实现）。only_generating 给定时，
    只沿右部符号全部可生成的产生式扩展（用于"有用符号"计算）。"""
    if grammar.start not in grammar.nonterminals:
        return frozenset()
    if only_generating is not None and grammar.start not in only_generating:
        return frozenset()
    seen = {grammar.start}
    queue = deque([grammar.start])
    while queue:
        sym = queue.popleft()
        for prod in grammar.productions:
            if prod.lhs != sym:
                continue
            if only_generating is not None and not _all_generating(grammar, prod, only_generating):
                continue
            for s in prod.rhs:
                if s in grammar.nonterminals and s not in seen:
                    seen.add(s)
                    queue.append(s)
    return frozenset(seen)


def reachable_fixpoint(grammar: Grammar, only_generating: frozenset[str] | None = None) -> frozenset[str]:
    """独立实现二：R_{i+1} = R_i ∪ {右部符号 | 左部 ∈ R_i}，迭代至不动点。"""
    if grammar.start not in grammar.nonterminals:
        return frozenset()
    if only_generating is not None and grammar.start not in only_generating:
        return frozenset()
    reach = {grammar.start}
    while True:
        new = set(reach)
        for prod in grammar.productions:
            if prod.lhs not in reach:
                continue
            if only_generating is not None and not _all_generating(grammar, prod, only_generating):
                continue
            for s in prod.rhs:
                if s in grammar.nonterminals:
                    new.add(s)
        if new == reach:
            return frozenset(reach)
        reach = new


def _all_generating(grammar: Grammar, prod, generating: frozenset[str]) -> bool:
    return all(s in grammar.terminals or s in generating for s in prod.rhs)


def useful_symbols(grammar: Grammar, generating: frozenset[str]) -> frozenset[str]:
    """有用符号：既可达（经由全可生成的产生式）又自身可生成，
    即存在 S ⇒* uXv ⇒* 终结串 的完整推导。"""
    return reachable_bfs(grammar, only_generating=generating) & generating


@dataclass
class ReachWitness:
    """S ⇒* α target β 的具体见证（α、β 中符号均可生成）。"""

    target: str
    steps: list[tuple[int, int]]      # 推导步（产生式下标, 非终结符位置）
    left_context: tuple[str, ...]     # α
    right_context: tuple[str, ...]    # β


def reach_witness(grammar: Grammar, generating: frozenset[str], target: str) -> ReachWitness | None:
    """BFS 求 S ⇒* α target β 的最短（边数）见证，仅经过全可生成产生式。"""
    if target not in grammar.nonterminals:
        return None
    if grammar.start not in generating or target not in generating:
        return None
    # parent[符号] = (前驱符号, 产生式下标, 该符号在右部的下标)
    parent: dict[str, tuple[str, int, int]] = {}
    if target == grammar.start:
        return ReachWitness(target, [], (), ())
    seen = {grammar.start}
    queue = deque([grammar.start])
    found = False
    while queue and not found:
        sym = queue.popleft()
        for idx, prod in enumerate(grammar.productions):
            if prod.lhs != sym or not _all_generating(grammar, prod, generating):
                continue
            for j, s in enumerate(prod.rhs):
                if s in grammar.nonterminals and s not in seen:
                    parent[s] = (sym, idx, j)
                    if s == target:
                        found = True
                        break
                    seen.add(s)
                    queue.append(s)
            if found:
                break
    if target not in parent:
        return None
    # 回溯路径 start = X0 → X1 → ... → Xk = target
    path: list[tuple[str, int, int]] = []  # (当前符号, 产生式, 右部下标)
    node = target
    while node != grammar.start:
        entry = parent[node]
        path.append(entry)
        node = entry[0]
    path.reverse()
    # 模拟推导，跟踪"目标出现位置"（第几个非终结符）
    form = [grammar.start]
    steps: list[tuple[int, int]] = []
    dist_nt_pos = 0
    for _sym, pidx, j in path:
        steps.append((pidx, dist_nt_pos))
        form = apply_step(grammar, form, steps[-1])
        prod = grammar.productions[pidx]
        dist_nt_pos += sum(1 for s in prod.rhs[:j] if s in grammar.nonterminals)
    abs_pos = nt_positions(grammar, form)[dist_nt_pos]
    return ReachWitness(
        target=target,
        steps=steps,
        left_context=tuple(form[:abs_pos]),
        right_context=tuple(form[abs_pos + 1:]),
    )
