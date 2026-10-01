"""可达性主分析:从开始符出发的 BFS(独立复核见 verify.independent_reachable)。"""
from collections import deque


def compute_reachable(grammar):
    seen = {grammar.start}
    dq = deque([grammar.start])
    while dq:
        a = dq.popleft()
        for p in grammar.productions_of(a):
            for s in p.rhs:
                if s in grammar.nonterminals and s not in seen:
                    seen.add(s)
                    dq.append(s)
    return frozenset(seen)
