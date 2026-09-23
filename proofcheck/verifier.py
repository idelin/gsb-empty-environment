"""证明验证器。

证明格式（纯 dict，可 JSON 序列化）：

    {
      "variable": "x",
      "steps": [
        {"rule": "expand", "domain": "...", "input": <expr>, "output": <expr>},
        {"rule": "combine", "domain": "...", "input": <expr>, "output": <expr>},
        {"rule": "differentiate", "domain": "...", "var": "x",
         "input": <expr>, "output": <expr>},
        {"rule": "substitute", "domain": "...", "var": "x", "value": "3/2",
         "input": <expr>, "output": <expr>},
      ],
      "result": <expr>,
    }

验证规则：
  1. 每步的 rule 必须是已知规则，未知规则直接拒绝；
  2. 每步必须携带非空 domain 适用域说明；
  3. 每步由对应检查器独立重算，不凭标签信任结论；
  4. 每步 input 必须等于上一步已验证的 output（证明链连续）；
  5. 最终结果 result 必须等于最后验证表达式。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .expr import ExprError, normalize
from .rules import (
    RULE_DOMAINS,
    RuleViolation,
    check_combine,
    check_differentiate,
    check_expand,
    check_substitute,
)

CHECKERS = {
    "expand": check_expand,
    "combine": check_combine,
    "differentiate": check_differentiate,
    "substitute": check_substitute,
}


@dataclass
class StepReport:
    index: int
    rule: str
    domain: str


@dataclass
class VerificationResult:
    ok: bool
    steps: list = field(default_factory=list)
    failed_step: int | None = None
    error: str | None = None


def _fail(index, message, reports=None):
    return VerificationResult(
        ok=False, steps=list(reports or []), failed_step=index, error=message
    )


def verify(proof):
    if not isinstance(proof, dict):
        return _fail(None, "证明必须是 dict")
    var = proof.get("variable")
    if not isinstance(var, str) or not var:
        return _fail(None, "证明缺少 variable 字段")
    steps = proof.get("steps")
    if not isinstance(steps, list):
        return _fail(None, "证明缺少 steps 列表")
    reports = []
    prev = None
    for index, step in enumerate(steps):
        if not isinstance(step, dict):
            return _fail(index, "步骤必须是 dict", reports)
        rule = step.get("rule")
        if rule not in CHECKERS:
            return _fail(index, f"未知规则 {rule!r}，拒绝验证", reports)
        domain = step.get("domain")
        if not isinstance(domain, str) or not domain.strip():
            return _fail(index, "步骤缺少适用域说明 domain", reports)
        try:
            pin, pout = CHECKERS[rule](step, var)
        except (ExprError, RuleViolation) as exc:
            return _fail(index, str(exc), reports)
        if prev is not None and pin != prev:
            return _fail(index, "步骤输入与上一已验证表达式不相等，证明链断裂", reports)
        reports.append(StepReport(index=index, rule=rule, domain=RULE_DOMAINS[rule]))
        prev = pout
    if "result" not in proof:
        return _fail(None, "证明缺少最终结果 result")
    try:
        final = normalize(proof["result"], var)
    except ExprError as exc:
        return _fail(None, f"最终结果无法规范化: {exc}")
    if prev is not None and final != prev:
        return _fail(len(steps) - 1, "最终结果不等于最后验证表达式", reports)
    return VerificationResult(ok=True, steps=reports)
