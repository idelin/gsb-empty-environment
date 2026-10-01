"""按需（懒惰）确定化：子集构造 + 状态预算 + LRU 转移缓存。

资源额度语义：
    * max_states 限制整个运行期出现过的“不同 NFA 状态集合”的数量，
      超过即抛 BudgetExceeded —— 调用方标记未完成，绝不返回错误自动机。
    * cache_size 限制转移缓存 (dfa状态, 字符) -> dfa状态 的条目数，
      LRU 驱逐。缓存只是纯函数的记忆化：驱逐后按需重算，结果完全
      相同，因此驱逐不改变语义。
"""
from __future__ import annotations

from collections import OrderedDict

from .errors import BudgetExceeded
from .nfa import CHAR, EPS, TAG


def closure(nfa, states):
    """epsilon 闭包（TAG 边也是 epsilon）。带访问集合，空串环不会死循环。"""
    seen = set()
    order = []

    def visit(s):
        if s in seen:
            return
        seen.add(s)
        order.append(s)
        for kind, dst, _ in nfa.edges[s]:
            if kind in (EPS, TAG):
                visit(dst)

    for s0 in states:
        visit(s0)
    return frozenset(order)


class Determinizer:
    def __init__(self, nfa, start, accept, alphabet, max_states=4096,
                 cache_size=4096):
        self.nfa = nfa
        self.accept_nfa = accept
        self.alphabet = tuple(sorted(alphabet))
        self.max_states = max_states
        self.cache_size = max(1, cache_size)
        self._ids = {}    # frozenset(nfa状态) -> dfa id；不驱逐，计入预算
        self._sets = []   # dfa id -> frozenset(nfa状态)
        self._cache = OrderedDict()  # (dfa id, char) -> dfa id | None
        self.start_id = self._intern(closure(nfa, {start}))

    def _intern(self, nfa_set):
        sid = self._ids.get(nfa_set)
        if sid is None:
            if len(self._sets) >= self.max_states:
                raise BudgetExceeded(
                    f"确定化状态数超过预算 {self.max_states}")
            sid = len(self._sets)
            self._ids[nfa_set] = sid
            self._sets.append(nfa_set)
        return sid

    @property
    def n_states(self):
        return len(self._sets)

    def is_accept(self, sid):
        return self.accept_nfa in self._sets[sid]

    def step(self, sid, ch):
        """返回后继 dfa id；无转移返回 None（死状态不占用预算）。"""
        key = (sid, ch)
        if key in self._cache:
            self._cache.move_to_end(key)
            return self._cache[key]
        targets = set()
        for s in self._sets[sid]:
            for kind, dst, payload in self.nfa.edges[s]:
                if kind == CHAR and ch in payload:
                    targets.add(dst)
        nxt = self._intern(closure(self.nfa, targets)) if targets else None
        self._cache[key] = nxt
        if len(self._cache) > self.cache_size:
            self._cache.popitem(last=False)  # LRU 驱逐，语义不变
        return nxt
