"""分组 distinct 计数引擎。

语义要点：
- 支持计数：每个 (组, distinct值) 记录支持者行数；distinct 数 = 计数>0 的值个数。
- 同值两行删一仍计 1；删最后一条才减；计数将变负 => 缺事件，拒绝且不改动状态。
- null 策略可配置：null_equal=True 时同列所有 null 折叠为同一个值；
  False 时每行的 null 是独立 distinct 值（按主键区分）。
- 多列组合 distinct：distinct_cols 组成复合键。
- 条件过滤：不满足过滤条件的行不产生贡献（前/后镜像分别判定）。
- 跨组更新：原子地撤销旧贡献再加入新贡献，失败回滚。
- 输入去重：按 (txn_id, table, pk, commit_seq) 去重，重复投递忽略。
- checkpoint 持久化支持计数、主键贡献、已见事件集合与提交序号——
  仅输出计数不足以恢复状态。
"""
from __future__ import annotations

import json
import os
import sqlite3
from typing import Iterable, Optional

from .events import ChangeEvent
from .state import SpillMap

NULL_TOKEN = "\x00NULL"           # null_equal=True 时所有 null 共用
NULL_TOKEN_FMT = "\x00NULL:{pk}"  # null_equal=False 时每行独立
_KEY_SEP = "\x01"                 # JSON 串内不会出现裸控制符

_OPS = {
    "eq": lambda a, b: a == b,
    "ne": lambda a, b: a != b,
    "lt": lambda a, b: a is not None and a < b,
    "le": lambda a, b: a is not None and a <= b,
    "gt": lambda a, b: a is not None and a > b,
    "ge": lambda a, b: a is not None and a >= b,
    "in": lambda a, b: a in b,
    "not_in": lambda a, b: a not in b,
    "is_null": lambda a, b: a is None,
    "not_null": lambda a, b: a is not None,
}


class MissingEventError(RuntimeError):
    """事件流不完整（计数将变负、前镜像不符、序号回退等），拒绝应用。"""


def passes_filter(row: dict, filters: Optional[list]) -> bool:
    for col, op, arg in filters or []:
        if op not in _OPS:
            raise ValueError(f"unknown filter op: {op}")
        if not _OPS[op](row.get(col), arg):
            return False
    return True


def normalize_value(value, pk: str, null_equal: bool):
    if value is None:
        return NULL_TOKEN if null_equal else NULL_TOKEN_FMT.format(pk=pk)
    return value


def group_key_of(row: dict, group_cols: list) -> str:
    return json.dumps([row.get(c) for c in group_cols], ensure_ascii=False)


def value_key_of(row: dict, distinct_cols: list, pk: str, null_equal: bool) -> str:
    return json.dumps(
        [normalize_value(row.get(c), pk, null_equal) for c in distinct_cols],
        ensure_ascii=False,
    )


def pair_of(row: Optional[dict], pk: str, group_cols, distinct_cols,
            null_equal: bool, filters) -> Optional[tuple]:
    """行 -> (组键, 值键)；被过滤或行为 None 时返回 None。"""
    if row is None or not passes_filter(row, filters):
        return None
    return (group_key_of(row, group_cols),
            value_key_of(row, distinct_cols, pk, null_equal))


class DistinctCountEngine:
    def __init__(self, group_cols, distinct_cols, *, null_equal=True,
                 filters=None, store_path=":memory:", max_cached=10000,
                 _conn=None):
        self.group_cols = list(group_cols)
        self.distinct_cols = list(distinct_cols)
        self.null_equal = null_equal
        self.filters = [tuple(f) for f in (filters or [])]
        self._conn = _conn or sqlite3.connect(store_path)
        self._support = SpillMap(self._conn, "support", max_cached)
        self._contrib = SpillMap(self._conn, "contrib", max_cached)
        self._seen = SpillMap(self._conn, "seen", max_cached)
        self._conn.execute(
            "CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT)")
        self._conn.commit()
        self._last_seq = int(self._meta_get("last_seq", "0"))

    def close(self) -> None:
        self._conn.close()

    def __del__(self):
        try:
            self._conn.close()
        except Exception:
            pass

    # ---- 内部工具 ----
    def _meta_get(self, key, default=None):
        row = self._conn.execute(
            "SELECT v FROM meta WHERE k = ?", (key,)).fetchone()
        return row[0] if row else default

    def _meta_put(self, key, value):
        self._conn.execute(
            "INSERT OR REPLACE INTO meta (k, v) VALUES (?, ?)", (key, value))

    def _support_key(self, pair) -> str:
        return pair[0] + _KEY_SEP + pair[1]

    def _inc_support(self, pair) -> None:
        key = self._support_key(pair)
        cur = self._support.get(key)
        self._support.put(key, str(int(cur) + 1 if cur is not None else 1))

    def _dec_support(self, pair) -> None:
        key = self._support_key(pair)
        cur = self._support.get(key)
        cur = int(cur) if cur is not None else 0
        if cur <= 0:
            raise MissingEventError(
                f"support count would go negative for {pair}: "
                "delete/update without matching insert (missing event)")
        if cur == 1:
            self._support.delete(key)
        else:
            self._support.put(key, str(cur - 1))

    def _pair_for(self, row, pk):
        return pair_of(row, pk, self.group_cols, self.distinct_cols,
                       self.null_equal, self.filters)

    # ---- 主入口 ----
    def apply(self, event: ChangeEvent) -> str:
        eid = json.dumps(event.event_id, ensure_ascii=False)
        if self._seen.get(eid) is not None:
            return "duplicate"
        if event.commit_seq <= self._last_seq:
            raise MissingEventError(
                f"commit_seq {event.commit_seq} <= last {self._last_seq}: "
                "missing or out-of-order events")

        stored = self._contrib.get(event.pk)
        old_pair = json.loads(stored) if stored is not None else None
        old_pair = tuple(old_pair) if old_pair else None

        if event.before is None and old_pair is not None:
            raise MissingEventError(
                f"INSERT for pk={event.pk} but a contribution already exists")
        if event.before is not None:
            before_pair = self._pair_for(event.before, event.pk)
            if before_pair != old_pair:
                raise MissingEventError(
                    f"{event.kind} for pk={event.pk} inconsistent with "
                    f"stored state (missing event?): "
                    f"stored {old_pair} != event {before_pair}")

        new_pair = self._pair_for(event.after, event.pk)

        # 原子迁移：先校验并撤销旧贡献，再加入新贡献；失败回滚。
        removed = None
        try:
            if old_pair is not None:
                self._dec_support(old_pair)
                removed = old_pair
            if new_pair is not None:
                self._inc_support(new_pair)
        except Exception:
            if removed is not None:
                self._inc_support(removed)
            raise

        if new_pair is not None:
            self._contrib.put(event.pk, json.dumps(new_pair, ensure_ascii=False))
        else:
            self._contrib.delete(event.pk)
        self._seen.put(eid, "1")
        self._last_seq = event.commit_seq
        self._meta_put("last_seq", str(event.commit_seq))
        return "applied"

    def apply_all(self, events: Iterable[ChangeEvent]) -> dict:
        stats = {"applied": 0, "duplicate": 0}
        for e in events:
            stats[self.apply(e)] += 1
        return stats

    # ---- 查询 ----
    def snapshot_counts(self) -> dict:
        """组键(JSON) -> distinct 数（支持计数 > 0 的值个数）。"""
        counts: dict[str, int] = {}
        for key, cnt in self._support.items():
            if int(cnt) <= 0:
                continue
            group = key.split(_KEY_SEP, 1)[0]
            counts[group] = counts.get(group, 0) + 1
        return counts

    def support_count(self, group_key: str, value_key: str) -> int:
        raw = self._support.get(group_key + _KEY_SEP + value_key)
        return int(raw) if raw is not None else 0

    # ---- checkpoint / 恢复 ----
    def checkpoint(self, path: str) -> None:
        """保留支持计数、主键贡献、输入去重集合与提交序号。"""
        self._support.flush()
        self._contrib.flush()
        self._seen.flush()
        self._meta_put("config", json.dumps({
            "group_cols": self.group_cols,
            "distinct_cols": self.distinct_cols,
            "null_equal": self.null_equal,
            "filters": self.filters,
        }, ensure_ascii=False))
        self._conn.commit()
        tmp = path + ".tmp"
        if os.path.exists(tmp):
            os.remove(tmp)
        dest = sqlite3.connect(tmp)
        try:
            self._conn.backup(dest)
        finally:
            dest.close()
        os.replace(tmp, path)

    @classmethod
    def restore(cls, path: str, *, max_cached=10000) -> "DistinctCountEngine":
        conn = sqlite3.connect(path)
        row = conn.execute("SELECT v FROM meta WHERE k = 'config'").fetchone()
        if row is None:
            raise ValueError(f"{path}: not a checkpoint (missing config)")
        cfg = json.loads(row[0])
        return cls(cfg["group_cols"], cfg["distinct_cols"],
                   null_equal=cfg["null_equal"], filters=cfg["filters"],
                   max_cached=max_cached, _conn=conn)

    # ---- 独立全量参考核验 ----
    def verify(self, events: Iterable[ChangeEvent]) -> bool:
        from .reference import reference_counts
        return self.snapshot_counts() == reference_counts(
            events, self.group_cols, self.distinct_cols,
            null_equal=self.null_equal, filters=self.filters)
