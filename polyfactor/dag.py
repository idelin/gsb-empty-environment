"""因式提取 DAG：hash-consing 共享子表达式，成本只计一次。

节点编码（元组即节点，全局 intern 表保证结构相同即同一对象）：
- ('atom', Fraction coeff, Monomial mono)：叶子，表示 coeff * x1^a1 * ...
- ('add', (child, ...))：n 元加法，成本 = 子节点数 - 1
- ('mul', (child, ...))：n 元乘法，成本 = 子节点数 - 1

成本模型（每个不同节点只计一次，共享自然成立）：
- add/mul 节点：子节点数 - 1 次二元运算；
- atom 叶子：构造该项所需乘法数 = (系数 != ±1 计 1) + 指数和，再减 1（下界 0）；
  常数项成本 0；幂按重复乘法计（x^2 = x*x 为 1 次乘法）。

说明：atom 是叶子，叶内部的幂次不在叶之间共享（模型限制，见 README）。
"""
from __future__ import annotations

from fractions import Fraction

from .poly import Polynomial, term_str

_intern: dict[tuple, tuple] = {}


def _node(key: tuple) -> tuple:
    hit = _intern.get(key)
    if hit is None:
        _intern[key] = hit = key
    return hit


def make_atom(coeff, mono) -> tuple:
    return _node(("atom", Fraction(coeff), tuple(mono)))


def make_add(children) -> tuple:
    flat = []
    for c in children:
        if c[0] == "add":
            flat.extend(c[1])
        else:
            flat.append(c)
    assert flat, "空加法"
    if len(flat) == 1:
        return flat[0]
    return _node(("add", tuple(sorted(flat))))


def make_mul(children) -> tuple:
    flat = []
    for c in children:
        if c[0] == "mul":
            flat.extend(c[1])
        else:
            flat.append(c)
    coeff = Fraction(1)
    mono = None
    others = []
    for c in flat:
        if c[0] == "atom":
            coeff *= c[1]
            mono = c[2] if mono is None else tuple(x + y for x, y in zip(mono, c[2]))
        else:
            others.append(c)
    kids = list(others)
    merged = None
    if mono is not None:
        merged = make_atom(coeff, mono)
        if not (coeff == 1 and not any(mono)):  # 乘法单位元直接丢弃
            kids.append(merged)
    if not kids:
        # 全部为原子：合并结果即所求（mono 的元数由输入原子保证一致）
        return merged
    if len(kids) == 1:
        return kids[0]
    return _node(("mul", tuple(sorted(kids))))


def atom_cost(coeff, mono) -> int:
    factors = (0 if coeff in (1, -1) else 1) + sum(mono)
    return max(0, factors - 1)


_cost_cache: dict[tuple, int] = {}


def dag_cost(root) -> int:
    """可达不同节点的运算总次数：共享子表达式只计一次。"""
    hit = _cost_cache.get(root)
    if hit is not None:
        return hit
    seen = set()
    stack = [root]
    total = 0
    while stack:
        n = stack.pop()
        if n in seen:
            continue
        seen.add(n)
        if n[0] == "atom":
            total += atom_cost(n[1], n[2])
        else:
            total += len(n[1]) - 1
            stack.extend(n[1])
    _cost_cache[root] = total
    return total


_expand_cache: dict[tuple, Polynomial] = {}


def expand(node, vars) -> Polynomial:
    """独立展开为规范多项式（验证乘回严格等于原式的唯一判据）。"""
    key = (node, vars)
    hit = _expand_cache.get(key)
    if hit is not None:
        return hit
    tag = node[0]
    if tag == "atom":
        result = Polynomial.build(vars, {node[2]: node[1]})
    elif tag == "add":
        result = Polynomial.zero(vars)
        for c in node[1]:
            result = result.add(expand(c, vars))
    elif tag == "mul":
        result = Polynomial.one(vars)
        for c in node[1]:
            result = result.mul(expand(c, vars))
    else:
        raise AssertionError(tag)
    _expand_cache[key] = result
    return result


def _display_key(node):
    if node[0] == "atom":
        return (0, tuple(-e for e in node[2]), node[1])
    return (1,)


def pretty(node, vars) -> str:
    tag = node[0]
    if tag == "atom":
        return term_str(vars, node[1], node[2])
    if tag == "add":
        parts = []
        for c in sorted(node[1], key=_display_key):
            if c[0] == "atom" and c[1] < 0:
                parts.append(("-", term_str(vars, -c[1], c[2])))
            else:
                parts.append(("+", pretty(c, vars)))
        text = ("-" if parts[0][0] == "-" else "") + parts[0][1]
        for sign, s in parts[1:]:
            text += (" - " if sign == "-" else " + ") + s
        return text
    if tag == "mul":
        factors = []
        for c in sorted(node[1], key=lambda c: c[0] != "atom"):
            s = pretty(c, vars)
            factors.append(f"({s})" if c[0] == "add" else s)
        return "*".join(factors)
    raise AssertionError(tag)
