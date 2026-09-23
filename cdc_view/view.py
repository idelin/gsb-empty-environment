"""物化视图：按表聚合计数与金额，幂等应用 CDC 事件。

应用不变式：事件必须按 commit_seq 升序应用到同一视图。
满足该不变式时：
  - commit_seq <= watermark 的事件已被快照或先前应用覆盖，直接跳过；
  - dedup_key 重复的事件是重投（at-least-once），直接跳过。
因此切换前后不会双计。
"""

from __future__ import annotations

from .events import ChangeEvent


class FrozenViewError(RuntimeError):
    """视图已冻结（被切换下线），拒绝继续写入。"""


class MaterializedView:
    def __init__(self, version: int, watermark: int) -> None:
        self.version = version
        self.watermark = watermark  # 已反映到的最大提交序号
        self._stats: dict[str, dict[str, int]] = {}
        self._seen: set[tuple] = set()
        self._frozen = False

    @classmethod
    def from_snapshot(
        cls, version: int, tables: dict[str, list[dict]], watermark: int
    ) -> "MaterializedView":
        """从固定快照构建视图，快照水位即视图水位。"""
        view = cls(version, watermark)
        for table, rows in tables.items():
            stat = view._stats.setdefault(table, {"count": 0, "amount": 0})
            for row in rows:
                stat["count"] += 1
                stat["amount"] += int(row.get("amount", 0))
        return view

    @property
    def frozen(self) -> bool:
        return self._frozen

    def freeze(self) -> None:
        self._frozen = True

    def apply(self, event: ChangeEvent) -> bool:
        """应用一条事件；重复或已被覆盖的事件返回 False（幂等）。"""
        if self._frozen:
            raise FrozenViewError(f"view v{self.version} is frozen")
        if event.dedup_key in self._seen or event.commit_seq <= self.watermark:
            return False
        stat = self._stats.setdefault(event.table, {"count": 0, "amount": 0})
        if event.op == "insert":
            stat["count"] += 1
            stat["amount"] += int(event.after.get("amount", 0))
        elif event.op == "delete":
            stat["count"] -= 1
            stat["amount"] -= int(event.before.get("amount", 0))
        else:  # update：计数不变，金额按前后值差额调整
            stat["amount"] += int(event.after.get("amount", 0)) - int(
                event.before.get("amount", 0)
            )
        self._seen.add(event.dedup_key)
        self.watermark = max(self.watermark, event.commit_seq)
        return True

    def stats(self) -> dict[str, dict[str, int]]:
        """读取聚合结果（深拷贝，读者不受后续写入影响）。"""
        return {table: dict(stat) for table, stat in self._stats.items()}
