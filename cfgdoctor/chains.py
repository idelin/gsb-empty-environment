"""最短问题链:把每个问题符号连接到最近的缺陷核心。

不可生成符号的缺陷核心 = 非生成依赖子图中的汇 SCC:
  - 没有任何产生式的符号;
  - 仅自循环的符号;
  - 互相依赖、无任何终结出口的死循环。
对不可达符号,给出引用者诊断(不可达意味着没有任何来自开始符的链)。
"""
from collections import deque
from dataclasses import dataclass

from .cycles import _tarjan


@dataclass
class ProblemChain:
    symbol: str
    kind: str    # 'non-generating' | 'unreachable'
    chain: tuple  # ((from_symbol, production_index), ...) 末步进入缺陷核心
    core: str     # 缺陷核心描述


def _sink_cores(grammar, non_gen, dep):
    succ = {a: [t for t, _i in dep.get(a, ())] for a in non_gen}
    cores = []
    for scc in _tarjan(sorted(non_gen), succ):
        members = set(scc)
        if all(t in members for a in scc for t in succ.get(a, ())):
            cores.append(scc)
    return cores


def _core_description(grammar, comp):
    if len(comp) == 1:
        a = comp[0]
        if not grammar.production_indices_of(a):
            return f"{a} 没有任何产生式(无终结出口)"
        return f"{a} 仅自循环, 无终结出口"
    return (f"死循环 {{{', '.join(sorted(comp))}}}: "
            "环上符号互相依赖且无任何终结出口")


def _bfs_to_core(start, dep, core_of):
    prev = {}
    dq = deque([start])
    seen = {start}
    while dq:
        a = dq.popleft()
        for t, i in dep.get(a, ()):
            if t in seen:
                continue
            seen.add(t)
            prev[t] = (a, i)
            if t in core_of:
                steps = []
                cur = t
                while cur != start:
                    frm, pi = prev[cur]
                    steps.append((frm, pi))
                    cur = frm
                steps.reverse()
                return steps, t
            dq.append(t)
    return None


def shortest_problem_chains(grammar, cert, reachable):
    nts = grammar.nonterminals
    non_gen = set(cert.non_generating)
    dep = {}
    for a in non_gen:
        for i in grammar.production_indices_of(a):
            for s in grammar.productions[i].rhs:
                if s in non_gen:
                    dep.setdefault(a, []).append((s, i))
    core_of = {}
    for comp in _sink_cores(grammar, non_gen, dep):
        desc = _core_description(grammar, comp)
        for m in comp:
            core_of[m] = desc
    chains = {}
    for a in sorted(non_gen):
        if a in core_of:
            chains[a] = ProblemChain(a, "non-generating", (), core_of[a])
            continue
        found = _bfs_to_core(a, dep, core_of)
        if found is None:
            chains[a] = ProblemChain(a, "non-generating", (),
                                     "缺陷核心不可定位(内部错误)")
        else:
            steps, core_sym = found
            chains[a] = ProblemChain(a, "non-generating", tuple(steps),
                                     core_of[core_sym])
    for a in sorted(nts - set(reachable)):
        refs = sorted({p.lhs for p in grammar.productions if a in p.rhs})
        if not refs:
            desc = f"{a} 从未出现在任何产生式右部"
        else:
            desc = f"{a} 仅被不可达符号引用: {refs}"
        chains[a] = ProblemChain(a, "unreachable", (), desc)
    return chains
