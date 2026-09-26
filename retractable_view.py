"""按主体撤回的聚合物化视图（仅依赖 Python 3.11 标准库）。

语义：
- 事件（upsert）携带：主体 subject、事件 event_id、分组 group、整数值 value、
  修订号 revision、同意代次 generation。
- 同一 (subject, event_id) 上，更高 revision 替换旧贡献；<= 旧 revision 被忽略。
- retract 删除主体全部贡献并留下墓碑（记录代次）；此后代次 <= 墓碑代次的事件
  一律忽略，无法复活旧贡献。
- consent 必须携带严格更高的代次才能使主体重新生效。
- 查询支持：总和 sum、计数 count、精确中位数 median（可按分组过滤）。
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass, field


@dataclass
class Contribution:
    revision: int
    group: str
    value: int


@dataclass
class SubjectState:
    generation: int = 0
    active: bool = True
    events: dict = field(default_factory=dict)  # event_id -> Contribution


class Aggregate:
    """增量聚合：维护总和、计数与有序值列表（用于精确中位数）。"""

    __slots__ = ("total", "count", "_values")

    def __init__(self) -> None:
        self.total = 0
        self.count = 0
        self._values: list[int] = []

    def add(self, value: int) -> None:
        self.total += value
        self.count += 1
        bisect.insort(self._values, value)

    def remove(self, value: int) -> None:
        idx = bisect.bisect_left(self._values, value)
        if idx >= len(self._values) or self._values[idx] != value:
            raise ValueError(f"值 {value} 不在聚合中，增量状态不一致")
        self._values.pop(idx)
        self.total -= value
        self.count -= 1

    def median(self):
        if not self._values:
            return None
        n = len(self._values)
        mid = n // 2
        if n % 2 == 1:
            return self._values[mid]
        med = (self._values[mid - 1] + self._values[mid]) / 2
        return int(med) if med == int(med) else med

    def result(self) -> dict:
        return {"sum": self.total, "count": self.count, "median": self.median()}


class RetractableView:
    """按主体撤回的聚合物化视图。"""

    def __init__(self) -> None:
        self._subjects: dict[str, SubjectState] = {}
        self._global = Aggregate()
        self._groups: dict[str, Aggregate] = {}

    # ---- 内部工具 ----

    def _subject(self, subject: str) -> SubjectState:
        return self._subjects.setdefault(subject, SubjectState())

    def _group_agg(self, group: str) -> Aggregate:
        return self._groups.setdefault(group, Aggregate())

    def _add_contribution(self, group: str, value: int) -> None:
        self._global.add(value)
        self._group_agg(group).add(value)

    def _remove_contribution(self, group: str, value: int) -> None:
        self._global.remove(value)
        self._group_agg(group).remove(value)

    # ---- 命令 ----

    def upsert(self, subject, event_id, group, value, revision, generation):
        """写入/修订事件贡献。返回 (是否生效, 原因)。"""
        value = int(value)
        revision = int(revision)
        generation = int(generation)
        st = self._subject(subject)
        if generation < st.generation:
            return False, "stale_generation"
        if not st.active:
            return False, "subject_retracted"
        old = st.events.get(event_id)
        if old is not None and revision <= old.revision:
            return False, "stale_revision"
        if old is not None:
            self._remove_contribution(old.group, old.value)
        st.events[event_id] = Contribution(revision, group, value)
        self._add_contribution(group, value)
        if generation > st.generation:
            st.generation = generation
        return True, "applied"

    def retract(self, subject, generation):
        """撤回主体：删除全部贡献并留下墓碑。返回 (是否生效, 原因)。"""
        generation = int(generation)
        st = self._subject(subject)
        if generation < st.generation:
            return False, "stale_generation"
        for contrib in st.events.values():
            self._remove_contribution(contrib.group, contrib.value)
        st.events.clear()
        st.active = False
        st.generation = generation
        return True, "applied"

    def consent(self, subject, generation):
        """重新同意：必须携带严格更高的代次。返回 (是否生效, 原因)。"""
        generation = int(generation)
        st = self._subject(subject)
        if generation <= st.generation and not st.active:
            return False, "generation_not_higher"
        if st.active and generation <= st.generation:
            return False, "generation_not_higher"
        st.active = True
        st.generation = generation
        return True, "applied"

    def apply_command(self, cmd: dict):
        ctype = cmd.get("type")
        if ctype == "upsert":
            return self.upsert(cmd["subject"], cmd["event_id"], cmd["group"],
                               cmd["value"], cmd["revision"], cmd["generation"])
        if ctype == "retract":
            return self.retract(cmd["subject"], cmd["generation"])
        if ctype == "consent":
            return self.consent(cmd["subject"], cmd["generation"])
        raise ValueError(f"未知命令类型: {ctype!r}")

    # ---- 查询 ----

    def query(self, group: str | None = None) -> dict:
        if group is None:
            return self._global.result()
        agg = self._groups.get(group)
        return agg.result() if agg is not None else Aggregate().result()

    # ---- 全量重算对照 ----

    def full_recompute(self, group: str | None = None) -> dict:
        """从存储的贡献记录全量重算聚合结果（用于与增量结果对照）。"""
        agg = Aggregate()
        for st in self._subjects.values():
            if not st.active:
                continue
            for contrib in st.events.values():
                if group is None or contrib.group == group:
                    agg.add(contrib.value)
        return agg.result()

    def verify(self) -> dict:
        """对照增量结果与全量重算结果（全局及每个分组）。"""
        groups = {None} | set(self._groups)
        report = {}
        ok = True
        for g in sorted(groups, key=lambda x: (x is not None, x)):
            inc = self.query(g)
            full = self.full_recompute(g)
            match = inc == full
            ok = ok and match
            report[g if g is not None else "__global__"] = {
                "incremental": inc, "recomputed": full, "match": match,
            }
        return {"ok": ok, "groups": report}

    # ---- 持久化（快照） ----

    def dump_state(self) -> dict:
        return {
            "subjects": {
                s: {
                    "generation": st.generation,
                    "active": st.active,
                    "events": {
                        eid: {"revision": c.revision, "group": c.group, "value": c.value}
                        for eid, c in st.events.items()
                    },
                }
                for s, st in self._subjects.items()
            }
        }

    @classmethod
    def load_state(cls, state: dict) -> "RetractableView":
        view = cls()
        for subject, sdata in state.get("subjects", {}).items():
            st = view._subject(subject)
            st.generation = sdata["generation"]
            st.active = sdata["active"]
            for eid, c in sdata["events"].items():
                st.events[eid] = Contribution(c["revision"], c["group"], c["value"])
                if st.active:
                    view._add_contribution(c["group"], c["value"])
        return view
