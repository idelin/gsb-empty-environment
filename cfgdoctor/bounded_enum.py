"""有界推导枚举 + 交叉核验。

原则:枚举找不到 ≠ 不可生成。负向结论(不可生成)只能由最小不动点
证书证明;有界枚举对负向结果只做一致性观察,并在报告中显式标注。
正向(可生成)则可用精确界(见证推导步数)做到必然复现。
"""
from collections import deque
from dataclasses import dataclass

from .verify import independent_reachable


def enumerate_terminal_strings(grammar, start_symbol, max_depth):
    """枚举 max_depth 步推导内可达的全部终结串(含可能的 ε)。"""
    start_form = (start_symbol,)
    visited = {start_form}
    found = set()
    dq = deque([(start_form, 0)])
    while dq:
        form, depth = dq.popleft()
        if all(s in grammar.terminals for s in form):
            found.add(form)
        if depth == max_depth:
            continue
        for pos, s in enumerate(form):
            if s not in grammar.nonterminals:
                continue
            for j in grammar.production_indices_of(s):
                nxt = (form[:pos] + grammar.productions[j].rhs
                       + form[pos + 1:])
                if nxt not in visited:
                    visited.add(nxt)
                    dq.append((nxt, depth + 1))
    return found


@dataclass
class CrossCheckReport:
    witness_confirmed: dict       # 可生成符号 -> 见证串是否在精确界内复现
    negative_consistent: dict     # 不可生成符号 -> 界内是否确实一无所获
    negative_bound: int
    reachability_agrees: bool
    notes: tuple

    @property
    def ok(self):
        return (all(self.witness_confirmed.values())
                and all(self.negative_consistent.values())
                and self.reachability_agrees)


def _witness_step_count(grammar, cert, symbol, memo):
    if symbol in memo:
        return memo[symbol]
    memo[symbol] = 0
    p = grammar.productions[cert.witness_production[symbol]]
    total = 1
    for s in p.rhs:
        if s in grammar.nonterminals:
            total += _witness_step_count(grammar, cert, s, memo)
    memo[symbol] = total
    return total


def cross_check(grammar, cert, reachable, negative_bound=None):
    memo = {}
    confirmed = {}
    for a in sorted(cert.generating):
        depth = _witness_step_count(grammar, cert, a, memo)
        found = enumerate_terminal_strings(grammar, a, depth)
        confirmed[a] = cert.witness_string[a] in found
    if negative_bound is None:
        negative_bound = max(cert.rounds.values(), default=0) + 2
    consistent = {}
    for a in sorted(cert.non_generating):
        consistent[a] = not enumerate_terminal_strings(
            grammar, a, negative_bound)
    agrees = independent_reachable(grammar) == reachable
    notes = (
        "正向: 每个可生成符号的证书见证串在界=见证推导步数的有界枚举中复现。",
        "负向: 不可生成符号在界内未枚举出终结串, 仅为一致性观察; "
        "不可生成性的证明来自最小不动点证书, 枚举找不到不构成证明。",
        ("可达性: 独立 DFS 复核与主分析一致。" if agrees
         else "可达性: 独立复核不一致!"),
    )
    return CrossCheckReport(confirmed, consistent, negative_bound,
                            agrees, notes)
