"""独立账本校验状态机。

只消费公开事件日志（events），不依赖 BudgetLedger 内部实现，
逐步重放并核对：
1. 预算守恒：任意时刻 reserved + spent <= total（取消返还、发布不退还）。
2. 最多一次发布：同一 query_id 的 PUBLISH 事件至多出现一次，
   且发布后不得再出现该 ID 的任何状态迁移。
3. 合法迁移：NONE -> RESERVED -> {PUBLISHED, CANCELLED}。
4. 秘密卫生：事件 payload 不得包含密钥 / 随机状态字段。
"""

from __future__ import annotations

from .errors import InvariantViolation

# 公开日志允许出现的 payload 字段（白名单）。
PUBLIC_PAYLOAD_KEYS = frozenset(
    {"query_id", "content_hash", "epsilon", "noisy_value"}
)
# 明确禁止出现的秘密字段（黑名单，双重防护）。
SECRET_PAYLOAD_KEYS = frozenset(
    {"seed", "rng", "rng_state", "random_state", "secret", "key", "noise_draw",
     "true_value"}
)

_TRANSITIONS = {
    "NONE": {"RESERVE": "RESERVED"},
    "RESERVED": {"PUBLISH": "PUBLISHED", "CANCEL": "CANCELLED"},
    "PUBLISHED": {},
    "CANCELLED": {},
}


class LedgerChecker:
    """重放公开日志的独立状态机（与账本实现解耦）。""" 

    def __init__(self, total_epsilon: float):
        self.total = float(total_epsilon)
        self.states: dict[str, str] = {}
        self.epsilons: dict[str, float] = {}
        self.reserved = 0.0
        self.spent = 0.0
        self.published_ids: set[str] = set()

    def _check_payload_hygiene(self, event: dict) -> None:
        keys = set(event["payload"].keys())
        leaked = keys & SECRET_PAYLOAD_KEYS
        if leaked:
            raise InvariantViolation(
                f"event #{event['seq']} leaks secret fields: {sorted(leaked)}"
            )
        unknown = keys - PUBLIC_PAYLOAD_KEYS
        if unknown:
            raise InvariantViolation(
                f"event #{event['seq']} has non-public fields: {sorted(unknown)}"
            )

    def step(self, event: dict) -> None:
        self._check_payload_hygiene(event)
        qid = event["query_id"]
        etype = event["type"]
        state = self.states.get(qid, "NONE")
        nxt = _TRANSITIONS[state].get(etype)
        if nxt is None:
            raise InvariantViolation(
                f"event #{event['seq']}: illegal transition {state} --{etype}-->"
                f" for query {qid!r}"
            )
        eps = float(event["payload"]["epsilon"])
        if etype == "RESERVE":
            self.reserved += eps
            self.epsilons[qid] = eps
        elif etype == "PUBLISH":
            if qid in self.published_ids:
                raise InvariantViolation(
                    f"query {qid!r} published more than once"
                )
            self.published_ids.add(qid)
            self.reserved -= eps
            self.spent += eps
        elif etype == "CANCEL":
            self.reserved -= eps  # 取消返还预算；已发布永远到不了这里
        self.states[qid] = nxt
        # 预算守恒不变量（含浮点容差）。
        if self.reserved + self.spent > self.total + 1e-9:
            raise InvariantViolation(
                f"budget conservation violated after event #{event['seq']}:"
                f" reserved={self.reserved} spent={self.spent}"
                f" total={self.total}"
            )
        if self.reserved < -1e-9 or self.spent < -1e-9:
            raise InvariantViolation(
                f"negative budget component after event #{event['seq']}"
            )

    def replay(self, events: list) -> dict:
        for event in events:
            self.step(event)
        return {
            "total": self.total,
            "reserved": self.reserved,
            "spent": self.spent,
            "available": self.total - self.reserved - self.spent,
            "published": sorted(self.published_ids),
            "states": dict(self.states),
        }


def verify(events: list, total_epsilon: float) -> dict:
    """便捷入口：重放全部事件并返回核对报告。"""
    return LedgerChecker(total_epsilon).replay(events)
