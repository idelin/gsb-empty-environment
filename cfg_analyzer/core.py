"""最小不动点分析：可生成性 / 可空性证书、最短终结串、推导回放引擎。

可生成性证书是最小不动点的分层证明：第 r 轮加入的符号只引用更早轮次
的符号（或终结符），因此任何独立校验器都能逐步重放并验证完备性
（不存在被遗漏的可加入符号 ⇒ 确为最小不动点）。
"""
from __future__ import annotations

from dataclasses import dataclass

from .grammar import Grammar


class DerivationError(ValueError):
    """推导回放失败。"""


@dataclass
class FixpointCertificate:
    """最小不动点证书（可生成性或可空性）。

    rounds[r] = [(符号, 见证产生式下标), ...]：第 r 轮新加入的符号。
    support[符号] = 见证产生式下标；rank[符号] = 加入轮次。
    """

    kind: str  # "generating" 或 "nullable"
    rounds: list[list[tuple[str, int]]]
    support: dict[str, int]
    rank: dict[str, int]
    result: frozenset[str]


def _least_fixpoint(grammar: Grammar, nullable_mode: bool) -> FixpointCertificate:
    result: set[str] = set()
    support: dict[str, int] = {}
    rank: dict[str, int] = {}
    rounds: list[list[tuple[str, int]]] = []
    while True:
        new: list[tuple[str, int]] = []
        new_syms: set[str] = set()
        for idx, prod in enumerate(grammar.productions):
            if prod.lhs in result or prod.lhs in new_syms:
                continue
            ok = True
            for sym in prod.rhs:
                if nullable_mode:
                    # 可空性：右部每个符号都必须已证可空（终结符永不可空）
                    if sym not in result and sym not in new_syms:
                        ok = False
                        break
                else:
                    # 可生成性：终结符视为已证；非终结符需已证可生成
                    if sym not in grammar.terminals and sym not in result and sym not in new_syms:
                        ok = False
                        break
            if ok:
                new.append((prod.lhs, idx))
                new_syms.add(prod.lhs)
        if not new:
            break
        for sym, idx in new:
            result.add(sym)
            support[sym] = idx
            rank[sym] = len(rounds)
        rounds.append(new)
    kind = "nullable" if nullable_mode else "generating"
    return FixpointCertificate(kind, rounds, support, rank, frozenset(result))


def generating_certificate(grammar: Grammar) -> FixpointCertificate:
    return _least_fixpoint(grammar, nullable_mode=False)


def nullable_certificate(grammar: Grammar) -> FixpointCertificate:
    return _least_fixpoint(grammar, nullable_mode=True)


def verify_certificate(grammar: Grammar, cert: FixpointCertificate) -> list[str]:
    """独立校验证书：返回问题列表，空列表表示证书有效。

    校验三条性质：逐轮可重放（每步只引用更早轮次）、无重复、
    完备性（不存在证书外仍可加入的符号 ⇒ 确为最小不动点）。
    """
    problems: list[str] = []
    nullable_mode = cert.kind == "nullable"
    prods = grammar.productions
    seen: set[str] = set()
    for r, entries in enumerate(cert.rounds):
        for sym, idx in entries:
            if sym in seen:
                problems.append(f"符号 {sym!r} 在第 {r} 轮重复加入")
            if not (0 <= idx < len(prods)):
                problems.append(f"第 {r} 轮符号 {sym!r} 的产生式下标 {idx} 越界")
                continue
            prod = prods[idx]
            if prod.lhs != sym:
                problems.append(f"第 {r} 轮：产生式 #{idx} 左部为 {prod.lhs!r}，非 {sym!r}")
            for s in prod.rhs:
                if nullable_mode:
                    ok = s in seen
                else:
                    ok = s in grammar.terminals or s in seen
                if not ok:
                    problems.append(
                        f"第 {r} 轮：{sym!r} 的见证产生式 #{idx} 引用了未证符号 {s!r}"
                    )
            seen.add(sym)
    # 完备性：证书外不存在仍可加入的符号
    for idx, prod in enumerate(prods):
        if prod.lhs in seen:
            continue
        if nullable_mode:
            ok = all(s in seen for s in prod.rhs)
        else:
            ok = all(s in grammar.terminals or s in seen for s in prod.rhs)
        if ok:
            problems.append(f"证书非不动点：产生式 #{idx}（{prod}）表明 {prod.lhs!r} 也应加入")
    if frozenset(seen) != cert.result:
        problems.append("result 字段与轮次内容不一致")
    if set(cert.support) != seen or set(cert.rank) != seen:
        problems.append("support/rank 字段与轮次内容不一致")
    for sym, idx in cert.support.items():
        if 0 <= idx < len(prods) and prods[idx].lhs != sym:
            problems.append(f"support[{sym!r}] 指向的产生式左部不符")
    for r, entries in enumerate(cert.rounds):
        for sym, _ in entries:
            if cert.rank.get(sym) != r:
                problems.append(f"rank[{sym!r}] 与轮次不符")
    return problems


# ---------------------------------------------------------------- 最短终结串

@dataclass
class YieldInfo:
    """每个可生成非终结符的最短终结串见证。

    best_len/best_height：最短串长度与最短串推导树中的最小高度
    （(长度, 高度) 字典序最小化，保证沿 chosen 展开必终止）。
    """

    best_len: dict[str, int]
    best_height: dict[str, int]
    best_str: dict[str, str]
    chosen: dict[str, int]  # 符号 -> 取得 (长度,高度) 最小的产生式下标


def shortest_yields(grammar: Grammar, generating: frozenset[str]) -> YieldInfo:
    inf = float("inf")
    best_len = {s: inf for s in generating}
    best_height = {s: inf for s in generating}
    best_str: dict[str, str] = {}

    def prod_key(prod) -> tuple[int, int] | None:
        total, hmax, has_nt = 0, 0, False
        for s in prod.rhs:
            if s in grammar.terminals:
                total += 1
            elif s in generating and best_len[s] < inf:
                total += best_len[s]
                hmax = max(hmax, best_height[s])
                has_nt = True
            else:
                return None
        return (total, 1 + hmax if has_nt else 1)

    changed = True
    while changed:
        changed = False
        for prod in grammar.productions:
            if prod.lhs not in generating:
                continue
            key = prod_key(prod)
            if key is None:
                continue
            if key < (best_len[prod.lhs], best_height[prod.lhs]):
                best_len[prod.lhs], best_height[prod.lhs] = key
                changed = True
    # 第二遍按收敛后的 (长度,高度) 重建最短串与见证产生式
    chosen: dict[str, int] = {}
    for sym in generating:
        best_idx, best_k = None, None
        for idx, prod in enumerate(grammar.productions):
            if prod.lhs != sym:
                continue
            key = prod_key(prod)
            if key is None:
                continue
            if best_k is None or key < best_k:
                best_k, best_idx = key, idx
        chosen[sym] = best_idx
    for sym in generating:
        # 由 chosen 自底向上拼出最短串（沿 (长度,高度) 严格下降，必终止）
        best_str[sym] = _expand_string(grammar, sym, chosen, best_str)
    return YieldInfo(best_len, best_height, best_str, chosen)


def _expand_string(grammar: Grammar, sym: str, chosen: dict[str, int],
                   memo: dict[str, str]) -> str:
    if sym in memo:
        return memo[sym]
    memo[sym] = ""  # 占位防环（正常不会触发：chosen 沿 (长度,高度) 下降）
    prod = grammar.productions[chosen[sym]]
    parts = []
    for s in prod.rhs:
        parts.append(s if s in grammar.terminals else _expand_string(grammar, s, chosen, memo))
    memo[sym] = "".join(parts)
    return memo[sym]


# ---------------------------------------------------------------- 推导回放

def nt_positions(grammar: Grammar, form) -> list[int]:
    """句型中所有非终结符出现的绝对下标。"""
    return [i for i, s in enumerate(form) if s in grammar.nonterminals]


def apply_step(grammar: Grammar, form: list[str], step: tuple[int, int]) -> list[str]:
    """应用一步推导：step = (产生式下标, 第几个非终结符出现位置, 0 起)。"""
    pidx, nt_pos = step
    if not (0 <= pidx < len(grammar.productions)):
        raise DerivationError(f"产生式下标 {pidx} 越界")
    prod = grammar.productions[pidx]
    positions = nt_positions(grammar, form)
    if nt_pos >= len(positions):
        raise DerivationError(f"句型 {' '.join(form)} 中不存在第 {nt_pos} 个非终结符")
    i = positions[nt_pos]
    if form[i] != prod.lhs:
        raise DerivationError(
            f"第 {nt_pos} 个非终结符是 {form[i]!r}，与产生式 #{pidx} 左部 {prod.lhs!r} 不符"
        )
    return form[:i] + list(prod.rhs) + form[i + 1:]


def replay(grammar: Grammar, start_form, steps) -> list[str]:
    """从 start_form 出发重放推导步序列，返回最终句型。"""
    form = list(start_form)
    for step in steps:
        form = apply_step(grammar, form, step)
    return form


def expand_all_to_terminals(grammar: Grammar, form, chosen: dict[str, int]):
    """最左展开句型中所有非终结符（用 chosen 见证产生式），返回 (步骤, 终结串)。"""
    steps: list[tuple[int, int]] = []
    current = list(form)
    while True:
        positions = nt_positions(grammar, current)
        if not positions:
            break
        sym = current[positions[0]]
        if sym not in chosen:
            raise DerivationError(f"符号 {sym!r} 没有可用的终结化产生式")
        step = (chosen[sym], 0)  # 最左展开 ⇒ 位置恒为 0
        steps.append(step)
        current = apply_step(grammar, current, step)
    return steps, "".join(current)
