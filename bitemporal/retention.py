"""旧版本保留与回收（归档）条件。

历史版本为什么必须保留
----------------------
查询输入是 ``(业务时间 t, 观察时刻 as_of)``。旧的观察时刻会重放“当时
我们知道什么”，因此被更正、撤销、补录覆盖掉的记录不能就地删除——它们
仍是旧查询的答案来源。索引采用只追加账本，任何历史记录默认永久保留。

什么时候可以回收
----------------
设归档后系统只需继续服务观察时刻 ``as_of >= horizon`` 的查询（更早的
历史重放需求已过保留期，可将数据转冷/离线）。一条记录可物理回收，当且
仅当它被某条**已确认存在**的后继直接取代，且后继的 ``recorded_at <=
horizon``：

* 对所有 ``as_of >= horizon``，后继在每个旧记录曾可见的观察时刻都已
  可见，且同区间、同对象，旧记录永远是“被取代的链中节点”，不会成为
  链头，因此不可能影响任何查询结果或时间轴分段；
* 未来版本（``recorded_at > horizon``）的前驱还不能回收——旧观察时刻
  仍只看得到前驱；独立重叠记录（无取代关系）永远不能按此规则回收。

回收是“逻辑保留、物理归档”：可把记录导出冷存储后再从在线索引删除。
为保证替换链可校验，建议在冷存储中保留墓碑元数据。
"""

from __future__ import annotations

from .index import VersionIndex
from .model import Record

__all__ = ["reclaimable_rids", "purge", "RetentionReport"]


class RetentionReport:
    def __init__(self, reclaimed: list[str], horizon) -> None:
        self.reclaimed = reclaimed
        self.horizon = horizon

    def __repr__(self) -> str:
        return (f"RetentionReport(horizon={self.horizon!r}, "
                f"reclaimed={self.reclaimed!r})")


def reclaimable_rids(idx: VersionIndex, horizon) -> list[str]:
    """返回在给定保留边界下可安全物理回收的 rid 列表。"""
    # rid -> 直接后继记录（按 replaces_id 反查）
    successor: dict[str, Record] = {}
    all_recs = [idx.get_record(r) for r in _all_rids(idx)]
    for rec in all_recs:
        if rec.replaces_id is not None:
            successor.setdefault(rec.replaces_id, rec)
    return sorted(
        rec.rid for rec in all_recs
        if rec.rid in successor
        and successor[rec.rid].recorded_at <= horizon
    )


def _all_rids(idx: VersionIndex) -> list[str]:
    # 通过公开接口枚举：objects + 全量扫描（演示规模足够）。
    rids: set[str] = set()
    # 索引没有公开全量 rid 枚举，借助内部映射的快照（回收本就是维护操作）。
    for rec in idx._records.values():  # noqa: SLF001
        rids.add(rec.rid)
    return sorted(rids)


def purge(idx: VersionIndex, horizon) -> RetentionReport:
    """回收所有满足条件的记录，返回报告。幂等。"""
    victims = reclaimable_rids(idx, horizon)
    for rid in victims:
        idx.remove(rid)
    return RetentionReport(victims, horizon)
