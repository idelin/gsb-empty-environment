"""确定性前向链推理引擎。

特性:
- 事实 / 规则 / 推导步骤均带稳定 ID 与版本号;
- 支持在不改动基线的前提下, 临时移除或替换事实并重新推导目标;
- 多个干预原子隔离: 全部校验通过才生效, 任一失败则整体不应用;
- 互相矛盾的干预(同一事实既移除又替换、被替换为两个不同值)预先拒绝;
- 替换事实时旧值不保留(存储按事实 ID 唯一);
- 推导步骤记录规则 ID+版本 与输入事实 ID+版本, 证据可重放;
- 干预结束后基线摘要不变。

仅使用 Python 3.11 标准库。
"""
from __future__ import annotations

import hashlib
import itertools
import json
from dataclasses import dataclass
from typing import Any, Iterable, Optional


class InterventionError(Exception):
    """干预校验或应用失败(矛盾干预、未知事实、未知操作类型)。"""


class ReplayError(Exception):
    """重放结果与已记录证据不一致。"""


# ---------------------------------------------------------------------------
# 数据模型: 事实 / 规则 / 步骤, 均带稳定 ID 与版本
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Fact:
    id: str
    assertion: str
    value: Any
    version: int = 1

    def ref(self) -> tuple[str, int]:
        """稳定引用: (事实ID, 版本)。"""
        return (self.id, self.version)


@dataclass(frozen=True)
class Condition:
    assertion: str
    value: Any


@dataclass(frozen=True)
class Rule:
    id: str
    version: int
    conditions: tuple[Condition, ...]
    conclusion: Condition


@dataclass(frozen=True)
class Step:
    """一步推导: 由某规则的某版本, 基于若干事实的某版本, 得出新事实。"""
    id: str
    rule_id: str
    rule_version: int
    inputs: tuple[tuple[str, int], ...]  # ((事实ID, 版本), ...) 已排序
    output: Fact

    def key(self) -> tuple:
        """用于跨推导比较的内容标识(与顺序无关的稳定身份)。"""
        return (
            self.rule_id,
            self.rule_version,
            self.inputs,
            self.output.assertion,
            _canonical(self.output.value),
        )


# ---------------------------------------------------------------------------
# 干预操作
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Remove:
    fact_id: str


@dataclass(frozen=True)
class Replace:
    fact_id: str
    new_value: Any


def _canonical(value: Any) -> str:
    return json.dumps(value, sort_keys=True, ensure_ascii=False, default=str)


def _normalize_ops(ops: Iterable[object]) -> tuple[dict[str, Remove], dict[str, Replace]]:
    """校验并归一化干预集合。

    - 同一事实既移除又替换            -> 拒绝
    - 同一事实被替换为两个不同的值    -> 拒绝
    - 完全相同的操作重复出现          -> 去重(幂等, 不视为矛盾)
    """
    removes: dict[str, Remove] = {}
    replaces: dict[str, Replace] = {}
    for op in ops:
        if isinstance(op, Remove):
            if op.fact_id in replaces:
                raise InterventionError(
                    f"矛盾干预: 事实 {op.fact_id} 同时被替换与移除"
                )
            removes.setdefault(op.fact_id, op)
        elif isinstance(op, Replace):
            if op.fact_id in removes:
                raise InterventionError(
                    f"矛盾干预: 事实 {op.fact_id} 同时被移除与替换"
                )
            prev = replaces.get(op.fact_id)
            if prev is not None and _canonical(prev.new_value) != _canonical(op.new_value):
                raise InterventionError(
                    f"矛盾干预: 事实 {op.fact_id} 被替换为两个不同的值 "
                    f"({prev.new_value!r} 与 {op.new_value!r})"
                )
            replaces[op.fact_id] = op
        else:
            raise InterventionError(f"未知干预类型: {type(op).__name__}")
    return removes, replaces


def apply_ops(base_facts: tuple[Fact, ...], ops: Iterable[object]) -> tuple[Fact, ...]:
    """原子应用干预: 先整体校验, 全部通过才返回新事实集; 失败则不产生任何变更。

    替换语义: 同一事实 ID 仅保留一个值, 旧值被覆盖且版本 +1, 不会同时保留旧值。
    """
    ops = list(ops)
    removes, replaces = _normalize_ops(ops)

    known_ids = {f.id for f in base_facts}
    for fid in sorted(set(removes) | set(replaces)):
        if fid not in known_ids:
            raise InterventionError(f"干预引用了不存在的事实: {fid}")

    result: list[Fact] = []
    for fact in base_facts:
        if fact.id in removes:
            continue
        if fact.id in replaces:
            result.append(
                Fact(
                    id=fact.id,
                    assertion=fact.assertion,
                    value=replaces[fact.id].new_value,
                    version=fact.version + 1,
                )
            )
        else:
            result.append(fact)
    return tuple(result)


# ---------------------------------------------------------------------------
# 推导
# ---------------------------------------------------------------------------

def _match_combos(rule: Rule, known: dict[str, Fact]):
    """按事实 ID 排序做确定性笛卡尔匹配。"""
    pools = []
    for cond in rule.conditions:
        pool = sorted(
            (
                f
                for f in known.values()
                if f.assertion == cond.assertion and f.value == cond.value
            ),
            key=lambda f: f.id,
        )
        if not pool:
            return
        pools.append(pool)
    yield from itertools.product(*pools)


def derive(base_facts: Iterable[Fact], rules: Iterable[Rule]) -> "Derivation":
    """确定性前向链推导至不动点。步骤 ID 按推导顺序稳定编号。"""
    rules = sorted(rules, key=lambda r: (r.id, r.version))
    known: dict[str, Fact] = {f.id: f for f in base_facts}
    steps: list[Step] = []
    counter = 0
    while True:
        fired = False
        for rule in rules:
            for combo in _match_combos(rule, known):
                concl = rule.conclusion
                if any(
                    f.assertion == concl.assertion and f.value == concl.value
                    for f in known.values()
                ):
                    continue
                counter += 1
                out = Fact(
                    id=f"D{counter:04d}",
                    assertion=concl.assertion,
                    value=concl.value,
                    version=1,
                )
                steps.append(
                    Step(
                        id=f"S{counter:04d}",
                        rule_id=rule.id,
                        rule_version=rule.version,
                        inputs=tuple(sorted(f.ref() for f in combo)),
                        output=out,
                    )
                )
                known[out.id] = out
                fired = True
        if not fired:
            break
    return Derivation(facts=known, steps=tuple(steps))


@dataclass(frozen=True)
class Derivation:
    """一次推导的完整结果: 全部已知事实 + 全部步骤(证据)。"""
    facts: dict[str, Fact]
    steps: tuple[Step, ...]

    def goal(self, assertion: str) -> Optional[Fact]:
        for fact in self.facts.values():
            if fact.assertion == assertion:
                return fact
        return None

    def support_chain(self, assertion: str) -> tuple[Step, ...]:
        """目标断言的支持链: 推导出该目标所需的全部步骤(拓扑序)。"""
        goal_fact = self.goal(assertion)
        if goal_fact is None:
            return ()
        by_output = {s.output.id: s for s in self.steps}
        chain: list[Step] = []
        seen: set[str] = set()

        def visit(fact_id: str) -> None:
            step = by_output.get(fact_id)
            if step is None or step.id in seen:
                return
            seen.add(step.id)
            for in_id, _ver in step.inputs:
                visit(in_id)
            chain.append(step)

        visit(goal_fact.id)
        return tuple(chain)

    def summary(self) -> str:
        """内容摘要(哈希): 事实与步骤的规范化指纹, 用于断言基线未变。"""
        payload = {
            "facts": sorted(
                (f.id, f.version, f.assertion, _canonical(f.value))
                for f in self.facts.values()
            ),
            "steps": [s.key() for s in self.steps],
        }
        blob = json.dumps(payload, sort_keys=True, ensure_ascii=False)
        return hashlib.sha256(blob.encode("utf-8")).hexdigest()


def diff_steps(before: Derivation, after: Derivation) -> tuple[list[Step], list[Step]]:
    """比较两次推导: 返回 (仅before有的步骤, 仅after有的步骤)。"""
    after_keys = {s.key() for s in after.steps}
    before_keys = {s.key() for s in before.steps}
    removed = [s for s in before.steps if s.key() not in after_keys]
    added = [s for s in after.steps if s.key() not in before_keys]
    return removed, added


def replay(base_facts: Iterable[Fact], rules: Iterable[Rule], recorded_steps: tuple[Step, ...]) -> Derivation:
    """重放证据: 用相同基线事实与规则重新推导, 逐步比对已记录步骤。"""
    actual = derive(base_facts, rules)
    if len(actual.steps) != len(recorded_steps):
        raise ReplayError(
            f"步骤数量不一致: 记录 {len(recorded_steps)} 步, 重放 {len(actual.steps)} 步"
        )
    for expected, got in zip(recorded_steps, actual.steps):
        if expected != got:
            raise ReplayError(
                f"步骤不一致: 记录 {expected.id}(规则 {expected.rule_id}v{expected.rule_version}) "
                f"!= 重放 {got.id}(规则 {got.rule_id}v{got.rule_version})"
            )
    return actual


# ---------------------------------------------------------------------------
# 引擎: 持有不可变基线, 干预在隔离副本上进行
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class ScenarioResult:
    """一次干预场景的结果(基线本身不被修改)。"""
    derivation: Derivation
    removed_steps: tuple[Step, ...]
    added_steps: tuple[Step, ...]
    baseline_summary_before: str
    baseline_summary_after: str

    @property
    def baseline_intact(self) -> bool:
        return self.baseline_summary_before == self.baseline_summary_after


class Engine:
    def __init__(self, facts: Iterable[Fact], rules: Iterable[Rule], goal_assertion: str):
        facts = tuple(facts)
        ids = [f.id for f in facts]
        if len(ids) != len(set(ids)):
            raise ValueError("基线事实 ID 必须唯一")
        self._facts = facts
        self._rules = tuple(rules)
        self.goal_assertion = goal_assertion

    @property
    def base_facts(self) -> tuple[Fact, ...]:
        return self._facts

    @property
    def rules(self) -> tuple[Rule, ...]:
        return self._rules

    def baseline(self) -> Derivation:
        return derive(self._facts, self._rules)

    def intervene(self, ops: Iterable[object]) -> ScenarioResult:
        """在隔离副本上原子应用干预并重新推导; 基线保持不变。

        校验失败时抛出 InterventionError, 且不产生任何部分变更。
        """
        before = self.baseline()
        summary_before = before.summary()
        new_facts = apply_ops(self._facts, ops)  # 先校验后应用, 原子生效
        after = derive(new_facts, self._rules)
        removed, added = diff_steps(before, after)
        return ScenarioResult(
            derivation=after,
            removed_steps=tuple(removed),
            added_steps=tuple(added),
            baseline_summary_before=summary_before,
            baseline_summary_after=self.baseline().summary(),
        )
