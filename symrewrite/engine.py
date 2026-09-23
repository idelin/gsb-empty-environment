"""重写引擎。

每步在规范形上按确定顺序（位置先序遍历，位置内按优先级、名称排序）
选择第一条模式匹配且条件满足的规则，在确定位置做局部重写。
终止状态有三种：
- normal_form：无可用规则，达到范式；
- cycle：规范状态重复出现（如 a→b→a），报告循环区间；
- step_limit：步数耗尽。
每一步都记录位置、绑定、适用域与被条件拦下的规则，可精确回放。
"""
from __future__ import annotations

from dataclasses import dataclass

from .expr import Expr, canonical, canonical_key, children, get_at, replace_at, to_str
from .pattern import match

NORMAL_FORM = "normal_form"
CYCLE = "cycle"
STEP_LIMIT = "step_limit"


@dataclass(frozen=True)
class Step:
    """变换链中的一步。"""

    index: int            # 从 1 开始的步号
    rule: str             # 规则名
    position: tuple       # 局部重写位置（子节点下标路径）
    bindings: tuple       # ((变量名, 值字符串), ...)，按变量名排序
    before: Expr          # 重写前的规范形
    after: Expr           # 重写后的规范形
    domain: str           # 该步的适用域说明
    blocked: tuple        # 同位置上模式匹配但条件不满足而不可用的规则名


@dataclass
class RewriteResult:
    initial: Expr
    final: Expr
    steps: list
    status: str
    cycle: tuple | None = None  # (首次出现的步号, 重复出现的步号)；0 表示初始状态


def _iter_positions(e: Expr, prefix: tuple = ()):
    """先序遍历所有位置，顺序确定。"""
    yield prefix
    for i, c in enumerate(children(e)):
        yield from _iter_positions(c, prefix + (i,))


def _select(state: Expr, rules: list):
    """选出本步的 (位置, 规则, 绑定, 被条件拦下的规则名)；无可用规则返回 None。"""
    ordered = sorted(rules, key=lambda r: (-r.priority, r.name))
    for pos in _iter_positions(state):
        sub = get_at(state, pos)
        blocked = []
        for rule in ordered:
            bindings = match(rule.pattern, sub)
            if bindings is None:
                continue
            if not rule.applies(bindings):
                blocked.append(rule.name)
                continue
            return pos, rule, bindings, tuple(blocked)
    return None


def rewrite(expr: Expr, rules: list, max_steps: int = 100) -> RewriteResult:
    state = canonical(expr)
    seen = {canonical_key(state): 0}
    steps = []
    for index in range(1, max_steps + 1):
        chosen = _select(state, rules)
        if chosen is None:
            return RewriteResult(expr, state, steps, NORMAL_FORM)
        pos, rule, bindings, blocked = chosen
        new_state = canonical(replace_at(state, pos, rule.fire(bindings)))
        steps.append(Step(
            index=index,
            rule=rule.name,
            position=pos,
            bindings=tuple(sorted((k, to_str(v)) for k, v in bindings.items())),
            before=state,
            after=new_state,
            domain=rule.domain,
            blocked=blocked,
        ))
        key = canonical_key(new_state)
        if key in seen:
            return RewriteResult(expr, new_state, steps, CYCLE,
                                 cycle=(seen[key], index))
        seen[key] = index
        state = new_state
    return RewriteResult(expr, state, steps, STEP_LIMIT)


class ReplayError(Exception):
    """回放与记录的变换链不一致时抛出。"""


def replay(initial: Expr, steps: list, rules: list) -> Expr:
    """按记录的位置与规则逐步重放，校验每步结果，返回最终表达式。"""
    by_name = {r.name: r for r in rules}
    state = canonical(initial)
    for step in steps:
        if step.rule not in by_name:
            raise ReplayError(f"步骤 {step.index}: 未知规则 {step.rule}")
        rule = by_name[step.rule]
        target = get_at(state, step.position)
        bindings = match(rule.pattern, target)
        if bindings is None:
            raise ReplayError(
                f"步骤 {step.index}: 规则 {rule.name} 在位置 {step.position} 不匹配")
        if not rule.applies(bindings):
            raise ReplayError(
                f"步骤 {step.index}: 规则 {rule.name} 条件不满足，不可用")
        state = canonical(replace_at(state, step.position, rule.fire(bindings)))
        if state != step.after:
            raise ReplayError(f"步骤 {step.index}: 回放结果与记录不一致")
    return state


def find_blocked(state: Expr, rules: list) -> list:
    """列出当前状态下所有模式匹配但条件不满足（不可用）的 (位置, 规则名)。"""
    out = []
    for pos in _iter_positions(state):
        sub = get_at(state, pos)
        for rule in rules:
            bindings = match(rule.pattern, sub)
            if bindings is not None and not rule.applies(bindings):
                out.append((pos, rule.name))
    return out
