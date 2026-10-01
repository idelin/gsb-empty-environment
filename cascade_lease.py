"""资源租约的级联撤销（cascading revocation of resource leases）。

模型：
- 租约构成一棵树。根租约无父租约，其余租约从父租约借出。
- 子租约的时间区间必须包含在父租约时间区间内（半开区间 [start, end)）。
- 对任意租约，任意时刻其所有活跃（未撤销）子租约的配额总和不得超过其自身配额。
- 父租约缩短时间区间或降低配额时，需要撤销一部分子孙租约以恢复约束，
  撤销集合使"损失"最小：损失 = 被撤销租约的权重之和。
- 撤销会递归影响子孙（级联），同一租约的损失只计一次。
- 损失相同时，按被撤销租约 ID 排序序列的字典序取最小者。

最优性：对每个受影响父租约的直接子租约做子集穷举，精确求得最小损失，
因此结果保证最优（指数复杂度，适用于子租约数量有限的场景）。
"""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field


class LeaseError(Exception):
    """租约操作违反约束时抛出。"""


@dataclass
class Lease:
    lease_id: str
    parent_id: str | None
    quota: int
    start: int
    end: int
    weight: int
    revoked: bool = False
    children: list[str] = field(default_factory=list)


def _validate_interval(start: int, end: int) -> None:
    if not isinstance(start, int) or not isinstance(end, int) or not start < end:
        raise LeaseError(f"invalid interval: [{start}, {end})")


def _validate_quota(quota: int) -> None:
    if not isinstance(quota, int) or quota < 0:
        raise LeaseError(f"invalid quota: {quota}")


def _validate_weight(weight: int) -> None:
    if not isinstance(weight, int) or weight < 0:
        raise LeaseError(f"invalid weight: {weight}")


class LeaseSystem:
    """租约系统：维护租约树并在父租约变更时计算最小损失撤销集合。"""

    def __init__(self) -> None:
        self.leases: dict[str, Lease] = {}

    # ------------------------------------------------------------------ #
    # 基本操作
    # ------------------------------------------------------------------ #
    def add_root(self, lease_id: str, quota: int, start: int, end: int,
                 weight: int = 0) -> None:
        if lease_id in self.leases:
            raise LeaseError(f"duplicate lease id: {lease_id}")
        _validate_interval(start, end)
        _validate_quota(quota)
        _validate_weight(weight)
        self.leases[lease_id] = Lease(lease_id, None, quota, start, end, weight)

    def add_lease(self, lease_id: str, parent_id: str, quota: int,
                  start: int, end: int, weight: int = 0) -> None:
        if lease_id in self.leases:
            raise LeaseError(f"duplicate lease id: {lease_id}")
        _validate_interval(start, end)
        _validate_quota(quota)
        _validate_weight(weight)
        parent = self.leases.get(parent_id)
        if parent is None:
            raise LeaseError(f"unknown parent lease: {parent_id}")
        if parent.revoked:
            raise LeaseError(f"parent lease revoked: {parent_id}")
        if not (parent.start <= start and end <= parent.end):
            raise LeaseError(
                f"child interval [{start}, {end}) not contained in parent "
                f"[{parent.start}, {parent.end})"
            )
        probe = Lease(lease_id, parent_id, quota, start, end, weight)
        siblings = self._active_children(parent)
        if not self._fits_capacity(parent.quota, siblings + [probe]):
            raise LeaseError(
                f"concurrent child quota exceeds parent quota {parent.quota}"
            )
        self.leases[lease_id] = probe
        parent.children.append(lease_id)

    def resize(self, lease_id: str, quota: int | None = None,
               start: int | None = None, end: int | None = None) -> dict:
        """缩短/减额租约，返回最小损失撤销结果。

        返回 {"revoked": [...按字典序排序的被撤销 ID...], "loss": 总损失}。
        """
        lease = self.leases.get(lease_id)
        if lease is None:
            raise LeaseError(f"unknown lease: {lease_id}")
        if lease.revoked:
            raise LeaseError(f"lease revoked: {lease_id}")

        new_quota = lease.quota if quota is None else quota
        new_start = lease.start if start is None else start
        new_end = lease.end if end is None else end
        _validate_interval(new_start, new_end)
        _validate_quota(new_quota)

        # 变更后仍须满足该租约自身父租约的约束。
        if lease.parent_id is not None:
            parent = self.leases[lease.parent_id]
            if not (parent.start <= new_start and new_end <= parent.end):
                raise LeaseError("resized interval escapes parent interval")
            siblings = [self.leases[c] for c in parent.children
                        if c != lease_id and not self.leases[c].revoked]
            probe = Lease(lease_id, lease.parent_id, new_quota,
                          new_start, new_end, 0)
            if not self._fits_capacity(parent.quota, siblings + [probe]):
                raise LeaseError("resize violates parent capacity")

        lease.quota, lease.start, lease.end = new_quota, new_start, new_end

        revoked_ids, loss = self._minimal_revocations(lease)
        for rid in revoked_ids:
            self._revoke(rid)
        return {"revoked": revoked_ids, "loss": loss}

    # ------------------------------------------------------------------ #
    # 内部工具
    # ------------------------------------------------------------------ #
    def _active_children(self, lease: Lease) -> list[Lease]:
        return [self.leases[c] for c in lease.children
                if not self.leases[c].revoked]

    @staticmethod
    def _fits_capacity(quota: int, children: list[Lease]) -> bool:
        """检查一组并发子租约是否在所有时刻都不超过配额（半开区间）。"""
        for t in sorted({c.start for c in children}):
            load = sum(c.quota for c in children if c.start <= t < c.end)
            if load > quota:
                return False
        return True

    def _collect_subtree(self, lease_id: str, out: set[str]) -> None:
        out.add(lease_id)
        for child_id in self.leases[lease_id].children:
            self._collect_subtree(child_id, out)

    def _revoke(self, lease_id: str) -> None:
        lease = self.leases[lease_id]
        if lease.revoked:
            return
        lease.revoked = True
        for child_id in lease.children:
            self._revoke(child_id)

    def _minimal_revocations(self, lease: Lease) -> tuple[list[str], int]:
        """对 lease 的直接子租约穷举撤销子集，返回 (排序后的撤销 ID 列表, 损失)。

        - 区间不再被包含的子租约为强制撤销。
        - 其余子租约枚举所有子集，取满足并发配额约束且损失最小者；
          损失相同取撤销 ID 排序序列字典序最小者。
        - 撤销某子租约会级联撤销其整个子树，损失按子树并集计，不重复计数。
        """
        forced: list[Lease] = []
        candidates: list[Lease] = []
        for child in self._active_children(lease):
            if lease.start <= child.start and child.end <= lease.end:
                candidates.append(child)
            else:
                forced.append(child)

        forced_ids: set[str] = set()
        for child in forced:
            self._collect_subtree(child.lease_id, forced_ids)

        best_loss: int | None = None
        best_ids: list[str] = []
        n = len(candidates)
        for mask in range(1 << n):
            chosen = [candidates[i] for i in range(n) if mask >> i & 1]
            kept = [candidates[i] for i in range(n) if not mask >> i & 1]
            if not self._fits_capacity(lease.quota, kept):
                continue
            ids = set(forced_ids)
            for child in chosen:
                self._collect_subtree(child.lease_id, ids)
            loss = sum(self.leases[i].weight for i in ids)
            sorted_ids = sorted(ids)
            if best_loss is None or (loss, sorted_ids) < (best_loss, best_ids):
                best_loss, best_ids = loss, sorted_ids
        return best_ids, best_loss if best_loss is not None else 0


# ---------------------------------------------------------------------- #
# 命令行接口
# ---------------------------------------------------------------------- #
def run(spec: dict) -> dict:
    """执行操作序列，返回每个操作的结果。"""
    system = LeaseSystem()
    results = []
    for op in spec.get("operations", []):
        kind = op.get("op")
        entry: dict = {"op": kind, "id": op.get("id")}
        try:
            if kind == "add_root":
                system.add_root(op["id"], op["quota"], op["start"], op["end"],
                                op.get("weight", 0))
                entry["status"] = "ok"
            elif kind == "add_lease":
                system.add_lease(op["id"], op["parent"], op["quota"],
                                 op["start"], op["end"], op.get("weight", 0))
                entry["status"] = "ok"
            elif kind == "resize":
                outcome = system.resize(op["id"], op.get("quota"),
                                        op.get("start"), op.get("end"))
                entry["status"] = "ok"
                entry["revoked"] = outcome["revoked"]
                entry["loss"] = outcome["loss"]
            else:
                entry["status"] = "error"
                entry["error"] = f"unknown op: {kind}"
        except LeaseError as exc:
            entry["status"] = "error"
            entry["error"] = str(exc)
        results.append(entry)
    return {"results": results}


def main(argv: list[str]) -> int:
    if len(argv) > 2:
        print("usage: python3 cascade_lease.py [input.json]", file=sys.stderr)
        return 2
    if len(argv) == 2:
        with open(argv[1], "r", encoding="utf-8") as fh:
            spec = json.load(fh)
    else:
        spec = json.load(sys.stdin)
    output = run(spec)
    json.dump(output, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
