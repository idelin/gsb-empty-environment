"""变换步骤记录：每步变换都说明其适用域。"""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Step:
    action: str   # 变换名称
    domain: str   # 适用域说明（该变换在何种前提下合法）
    detail: str = ""

    def __str__(self) -> str:
        base = f"[{self.action}] {self.domain}"
        return f"{base} -- {self.detail}" if self.detail else base


@dataclass
class StepLog:
    steps: list[Step] = field(default_factory=list)

    def record(self, action: str, domain: str, detail: str = "") -> None:
        self.steps.append(Step(action, domain, detail))

    def __iter__(self):
        return iter(self.steps)

    def __len__(self) -> int:
        return len(self.steps)
