"""可空性(nullable)与可生成性(generating)的最小不动点计算及证书。

可生成集 G 是算子 F(X) = {A | 存在 A->α, α ⊆ (X ∪ Σ)*} 的最小不动点。
证书携带每个符号的"轮次"(见证推导树高度)与见证产生式/见证终结串,
可被 verify.verify_generating_certificate 独立复核:
  (1) 秩严格下降  => 证书内符号确实都可生成(声音性);
  (2) 补集封闭    => 非生成符号的每条产生式右部都含非生成符号,
                     对推导长度归纳即知它们不可能生成终结串(完备性)。
"""
from collections import deque
from dataclasses import dataclass


@dataclass
class NullableInfo:
    nullable: frozenset
    witness_production: dict
    rounds: dict


def compute_nullable(grammar):
    """最小不动点:A 可空 当且仅当 存在 A->α 且 α 中符号全部可空。"""
    nts = grammar.nonterminals
    appears = {}
    remaining = []
    for i, p in enumerate(grammar.productions):
        if any(s in grammar.terminals for s in p.rhs):
            remaining.append(None)
            continue
        c = 0
        for s in p.rhs:
            if s in nts:
                c += 1
                appears.setdefault(s, set()).add(i)
        remaining.append(c)
    rounds = {}
    witness = {}
    queue = deque()
    for i, p in enumerate(grammar.productions):
        if remaining[i] == 0 and p.lhs not in rounds:
            rounds[p.lhs] = 1
            witness[p.lhs] = i
            queue.append(p.lhs)
    while queue:
        x = queue.popleft()
        for j in appears.get(x, ()):
            if remaining[j] is None:
                continue
            remaining[j] -= grammar.productions[j].rhs.count(x)
            if remaining[j] == 0:
                a = grammar.productions[j].lhs
                if a not in rounds:
                    rounds[a] = 1 + max(
                        (rounds[s] for s in grammar.productions[j].rhs
                         if s in nts),
                        default=0)
                    witness[a] = j
                    queue.append(a)
    return NullableInfo(frozenset(rounds), witness, dict(rounds))


@dataclass
class GeneratingCertificate:
    """可生成性最小不动点证书。

    rounds[A]          见证推导树高度(秩),沿见证产生式严格下降
    witness_production 每个可生成符号的见证产生式编号
    witness_string     每个可生成符号导出的最短终结串之一
    """
    rounds: dict
    witness_production: dict
    witness_string: dict
    generating: frozenset
    non_generating: frozenset


def compute_generating(grammar):
    """最小不动点:A 可生成 当且仅当 存在 A->α 且 α 中符号全部可生成。"""
    nts = grammar.nonterminals
    appears = {}
    remaining = []
    for i, p in enumerate(grammar.productions):
        c = 0
        for s in p.rhs:
            if s in nts:
                c += 1
                appears.setdefault(s, set()).add(i)
        remaining.append(c)
    rounds = {}
    witness_prod = {}
    witness_str = {}
    queue = deque()
    for i, p in enumerate(grammar.productions):
        if remaining[i] == 0 and p.lhs not in rounds:
            rounds[p.lhs] = 1
            witness_prod[p.lhs] = i
            witness_str[p.lhs] = tuple(p.rhs)
            queue.append(p.lhs)
    while queue:
        x = queue.popleft()
        for j in appears.get(x, ()):
            remaining[j] -= grammar.productions[j].rhs.count(x)
            if remaining[j] == 0:
                a = grammar.productions[j].lhs
                if a not in rounds:
                    deps = [s for s in grammar.productions[j].rhs if s in nts]
                    rounds[a] = 1 + max((rounds[s] for s in deps), default=0)
                    witness_prod[a] = j
                    ws = []
                    for s in grammar.productions[j].rhs:
                        if s in nts:
                            ws.extend(witness_str[s])
                        else:
                            ws.append(s)
                    witness_str[a] = tuple(ws)
                    queue.append(a)
    generating = frozenset(rounds)
    return GeneratingCertificate(
        rounds=dict(rounds),
        witness_production=witness_prod,
        witness_string=witness_str,
        generating=generating,
        non_generating=frozenset(nts - generating),
    )
