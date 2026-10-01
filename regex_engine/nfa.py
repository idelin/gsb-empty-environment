"""Thompson 构造：AST -> 带标签 epsilon-NFA。

转移分三类：
    EPS   epsilon 转移
    CHAR  字符转移，payload 为字母表子集（frozenset）
    TAG   捕获标签转移（也是 epsilon），payload 为 (组号, 是否开始标签)

每个状态的出边按“优先级顺序”加入：交替分支从左到右、星号优先进入
循环体（贪婪）。后续 NFA 路径恢复按此顺序做深度优先搜索，得到确定
的优先级语义。标签只挂在 NFA 边上；确定化把 NFA 状态合并成集合时
不携带标签，捕获通过在原 NFA 上重走匹配路径恢复，因此状态合并永远
不会丢失标签信息。
"""
from __future__ import annotations

from .ast_nodes import (Alt, CharClass, Concat, EmptyLang, Epsilon, Group,
                        Literal, Star)
from .errors import CompileError

EPS, CHAR, TAG = "eps", "char", "tag"


class NFA:
    def __init__(self):
        self.edges = []  # 每个状态: [(kind, target, payload), ...] 按优先级序

    def new_state(self):
        self.edges.append([])
        return len(self.edges) - 1

    def add(self, src, kind, dst, payload=None):
        self.edges[src].append((kind, dst, payload))

    @property
    def n_states(self):
        return len(self.edges)


def _resolve_class(node, alphabet):
    if node.negated:
        bad = node.chars - alphabet
        if bad:
            raise CompileError(f"字符类包含字母表外字符: {sorted(bad)!r}")
        return frozenset(alphabet - node.chars)
    bad = node.chars - alphabet
    if bad:
        raise CompileError(f"字符类包含字母表外字符: {sorted(bad)!r}")
    return node.chars


def build(ast, alphabet):
    """返回 (nfa, start_state, accept_state)。"""
    nfa = NFA()
    start, accept = _build(ast, nfa, frozenset(alphabet))
    return nfa, start, accept


def _build(node, nfa, alphabet):
    s = nfa.new_state()
    a = nfa.new_state()
    if isinstance(node, EmptyLang):
        pass  # 无通路：空语言
    elif isinstance(node, Epsilon):
        nfa.add(s, EPS, a)
    elif isinstance(node, Literal):
        if node.char not in alphabet:
            raise CompileError(f"字符 {node.char!r} 不在显式字母表中")
        nfa.add(s, CHAR, a, frozenset({node.char}))
    elif isinstance(node, CharClass):
        chars = _resolve_class(node, alphabet)
        if chars:
            nfa.add(s, CHAR, a, chars)
        # 解析后为空集等价于空语言：不加边
    elif isinstance(node, Concat):
        prev = s
        for part in node.parts:
            ps, pa = _build(part, nfa, alphabet)
            nfa.add(prev, EPS, ps)
            prev = pa
        nfa.add(prev, EPS, a)
    elif isinstance(node, Alt):
        for branch in node.branches:  # 按书写顺序 = 优先级顺序
            bs, ba = _build(branch, nfa, alphabet)
            nfa.add(s, EPS, bs)
            nfa.add(ba, EPS, a)
    elif isinstance(node, Star):
        bs, ba = _build(node.body, nfa, alphabet)
        nfa.add(s, EPS, bs)   # 优先进入循环体（贪婪）
        nfa.add(s, EPS, a)    # 其次旁路（零次迭代）
        nfa.add(ba, EPS, bs)  # 回边
        nfa.add(ba, EPS, a)
    elif isinstance(node, Group):
        bs, ba = _build(node.body, nfa, alphabet)
        nfa.add(s, TAG, bs, (node.index, True))
        nfa.add(ba, TAG, a, (node.index, False))
    else:  # pragma: no cover
        raise TypeError(f"未知 AST 节点: {node!r}")
    return s, a
