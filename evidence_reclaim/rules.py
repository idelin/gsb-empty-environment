"""规则注册表。

每条规则带稳定 ID 与版本号，供阻塞链步骤引用，并随证书重放。
保留期是“法定无关的自定义保留期”（组织自定义，不是法定义务）。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import List

from .ids import stable_id

RULE_SCHEMA = "rule.v1"


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    title: str
    description: str

    def to_dict(self):
        return {
            "schema": RULE_SCHEMA,
            "rule_id": self.rule_id,
            "version": self.version,
            "title": self.title,
            "description": self.description,
        }

    def stable_id(self) -> str:
        return stable_id("rule", self.to_dict())


_RULES: List[Rule] = [
    Rule(
        "R-RETENTION-ROOT",
        1,
        "未过自定义保留期即为 GC 根",
        "节点当前时间未超过其法定无关的自定义保留期时不可回收。",
    ),
    Rule(
        "R-LIVE-REF-ROOT",
        1,
        "有效引用来自活结论",
        "存活节点（含所有 GC 根）发出的有效（未撤销）引用使目标节点不可回收。",
    ),
    Rule(
        "R-FREEZE-ROOT",
        1,
        "显式冻结标记",
        "带活动冻结标记的节点不可回收，无论是否过期。",
    ),
    Rule(
        "R-FREEZE-INHERIT",
        1,
        "冻结可继承",
        "冻结保护沿有效引用边传播：被冻结的存活节点引用的节点同样受保护。",
    ),
    Rule(
        "R-FREEZE-REVOKABLE",
        1,
        "冻结可撤回",
        "冻结标记可通过解冻事件撤回；撤回后重新计算最小不可回收闭包。",
    ),
    Rule(
        "R-EXPIRED-LIVE-REF",
        1,
        "过期但被活结论引用不可回收",
        "已过保留期的节点，只要仍被存活结论的有效引用闭包覆盖，即不可回收。",
    ),
    Rule(
        "R-NO-DANGLING",
        1,
        "禁止悬空引用",
        "新引用的源与目标必须为现存、未回收的节点版本；回收提交后须验证无悬空依赖。",
    ),
    Rule(
        "R-CAS-NEW-REF",
        1,
        "新引用必须阻止已计划删除",
        "试算计划后若有新引用覆盖计划删除目标，提交必须拒绝并要求以新代际重算。",
    ),
    Rule(
        "R-VERSION-CHECK",
        1,
        "应用前验证版本",
        "回收提交必须验证存储版本与计划基线一致、候选节点版本精确匹配。",
    ),
    Rule(
        "R-GENERATION",
        1,
        "标记代际",
        "每次试算产生单调递增的标记代际；提交携带其代际，跨代际变更须重新标记。",
    ),
    Rule(
        "R-CERT-REPLAY",
        1,
        "删除证书可重放",
        "证书记录候选集、代际、状态根与规则版本；重放时重新计算并逐项比对。",
    ),
]

RULES = {r.rule_id: r for r in _RULES}


def rules_digest() -> str:
    from .ids import canonical, digest

    return digest(sorted((r.to_dict() for r in _RULES), key=canonical))
