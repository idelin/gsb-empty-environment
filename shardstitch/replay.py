"""证据重放：从局部事实与边界假设出发，按步骤重新执行规则，
验证结论确实可被推导出来。重放是纯确定性的。"""
from __future__ import annotations

from .model import Proof, Rule

# 可重放的确定性算子表：输入 payload 列表 -> 输出 payload
OPS = {
    "join": lambda payloads: "|".join(sorted(payloads)),
    "concat": lambda payloads: "".join(payloads),
    "sum": lambda payloads: str(sum(int(p) for p in payloads)),
    "count": lambda payloads: str(len(payloads)),
}


class ReplayError(Exception):
    """证据无法重放时抛出。"""


def replay(proof: Proof, rules: tuple[Rule, ...]) -> str:
    """重放证明，返回结论事实的 payload；任何不一致都抛 ReplayError。"""
    rule_map = {(r.rule_id, r.version): r for r in rules}
    env: dict[tuple[str, int], str] = {}
    for fact in proof.facts:
        if fact.ref in env:
            raise ReplayError(f"事实 {fact.ref} 重复定义")
        env[fact.ref] = fact.payload
    for assumption in proof.assumptions:
        if assumption.ref in env:
            raise ReplayError(f"假设 {assumption.ref} 与局部事实冲突")
        env[assumption.ref] = assumption.claimed_payload
    for step in proof.steps:
        rule = rule_map.get((step.rule_id, step.rule_version))
        if rule is None:
            raise ReplayError(f"步骤 {step.step_id} 引用未知规则 {step.rule_id}@{step.rule_version}")
        if rule.output_ref != step.output:
            raise ReplayError(
                f"步骤 {step.step_id} 输出 {step.output} 与规则输出 {rule.output_ref} 不一致"
            )
        if step.output in env:
            raise ReplayError(f"步骤 {step.step_id} 重复推导 {step.output}")
        op = OPS.get(rule.op)
        if op is None:
            raise ReplayError(f"规则 {rule.rule_id}@{rule.version} 使用未知算子 {rule.op!r}")
        missing = [ref for ref in step.inputs if ref not in env]
        if missing:
            raise ReplayError(f"步骤 {step.step_id} 缺少输入 {missing}")
        env[step.output] = op([env[ref] for ref in step.inputs])
    if proof.conclusion not in env:
        raise ReplayError(f"结论 {proof.conclusion} 未被推导出来")
    return env[proof.conclusion]
