"""独立参考实现：直接在 AST 上的朴素回溯匹配器。

与主引擎（epsilon-NFA + 懒惰 DFA + NFA 路径恢复）完全独立，用于
穷举小字符串交叉验证。语义规则与主引擎一致：最左、最长、优先级
（交替按书写顺序、星号贪婪）。

空串循环规则（与 NFA 简单路径语义一致）：星号的第一次迭代允许匹配
空串（此后循环必须结束）；非首次的迭代若为空，在 NFA 上必然重复
经过同一 (状态, 位置)，被简单路径约束排除。因此 (a*)*、(()*)* 等
嵌套星号不会死循环，且两侧语义一致。
"""
from __future__ import annotations

from .ast_nodes import (Alt, CharClass, Concat, EmptyLang, Epsilon, Group,
                        Literal, Star)


def iter_matches(node, text, pos, alphabet):
    """按优先级顺序产生 (end, caps)；caps 为 ((组号, 起点, 终点), ...)。"""
    if isinstance(node, EmptyLang):
        return
    if isinstance(node, Epsilon):
        yield (pos, ())
        return
    if isinstance(node, Literal):
        if pos < len(text) and text[pos] == node.char:
            yield (pos + 1, ())
        return
    if isinstance(node, CharClass):
        chars = (alphabet - node.chars) if node.negated else node.chars
        if pos < len(text) and text[pos] in chars:
            yield (pos + 1, ())
        return
    if isinstance(node, Concat):
        yield from _concat(node.parts, text, pos, alphabet)
        return
    if isinstance(node, Alt):
        for branch in node.branches:
            yield from iter_matches(branch, text, pos, alphabet)
        return
    if isinstance(node, Star):
        yield from _star(node.body, text, pos, alphabet)
        return
    if isinstance(node, Group):
        for end, caps in iter_matches(node.body, text, pos, alphabet):
            yield (end, caps + ((node.index, pos, end),))
        return
    raise TypeError(f"未知 AST 节点: {node!r}")  # pragma: no cover


def _concat(parts, text, pos, alphabet):
    if not parts:
        yield (pos, ())
        return
    for e1, c1 in iter_matches(parts[0], text, pos, alphabet):
        for e2, c2 in _concat(parts[1:], text, e1, alphabet):
            yield (e2, c1 + c2)


def _star(body, text, pos, alphabet, first=True):
    for e1, c1 in iter_matches(body, text, pos, alphabet):
        if e1 == pos:
            if first:
                # 首次空迭代：允许，但之后不能再迭代（NFA 回边会重复状态）
                yield (pos, c1)
        else:
            for e2, c2 in _star(body, text, e1, alphabet, first=False):
                yield (e2, c1 + c2)
    yield (pos, ())


def _normalize(caps, group_count):
    d = {i: None for i in range(1, group_count + 1)}
    for index, s, e in caps:  # 后写覆盖：循环组取最后一次迭代
        d[index] = (s, e)
    return d


def group_count_of(ast):
    counts = []

    def walk(node):
        if isinstance(node, Group):
            counts.append(node.index)
            walk(node.body)
        elif isinstance(node, (Concat, Alt)):
            for child in (node.parts if isinstance(node, Concat)
                          else node.branches):
                walk(child)
        elif isinstance(node, Star):
            walk(node.body)

    walk(ast)
    return max(counts, default=0)


def ref_findall(ast, text, alphabet):
    """返回 [(start, end, captures)]，与 Regex.findall 的语义对应。"""
    alphabet = frozenset(alphabet)
    ngroups = group_count_of(ast)
    out = []
    i = 0
    n = len(text)
    while i <= n:
        best = None
        for s in range(i, n + 1):
            best_end = -1
            best_caps = None
            for end, caps in iter_matches(ast, text, s, alphabet):
                if end > best_end:  # 同长度取优先级最高（最先产生）者
                    best_end = end
                    best_caps = caps
            if best_caps is not None:
                best = (s, best_end, best_caps)
                break
        if best is None:
            break
        s, e, caps = best
        out.append((s, e, _normalize(caps, ngroups)))
        i = e if e > s else e + 1
    return out
