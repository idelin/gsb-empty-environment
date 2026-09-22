"""可更正数据流上的均值变化点索引（仅用 Python 标准库）。

事件以 (source, seq) 唯一标识，按 (source, seq) 排序；同一事件重复提交
不增加样本量。候选点是相邻两个事件之间的间隙，用左右窗口的均值差打分：
两侧样本数分别不少于 left_min / right_min，且左右均值差绝对值达到阈值
（恰好等于阈值也算成立）。所有均值与阈值比较均使用 Fraction 完成。

对一条历史记录的插入、更正或撤回，只重算可能受其影响的局部候选区域，
并通过 add / retract 通知明确告知消费方旧结果何时失效。
"""

from bisect import bisect_left
from dataclasses import dataclass
from fractions import Fraction
from typing import Any, Dict, List, Optional, Tuple

Key = Tuple[Any, Any]
Value = Any


def _as_fraction(value: Value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


@dataclass(frozen=True)
class ChangePoint:
    """一个成立的候选点及其左右证据集合。"""

    gap: Tuple[Key, Key]
    diff: Fraction
    left_evidence: Tuple[Key, ...]
    right_evidence: Tuple[Key, ...]

    @property
    def left_key(self) -> Key:
        return self.gap[0]

    @property
    def right_key(self) -> Key:
        return self.gap[1]


@dataclass(frozen=True)
class Notification:
    """变化点增量通知。kind 取值 "add" 或 "retract"。"""

    kind: str
    point: ChangePoint


class ChangePointIndex:
    """维护全部成立变化点的增量索引。"""

    def __init__(self, left_min: int = 1, right_min: int = 5,
                 threshold: Value = Fraction(3)) -> None:
        if left_min < 1 or right_min < 1:
            raise ValueError("left_min and right_min must be positive")
        self.left_min = left_min
        self.right_min = right_min
        self.threshold = _as_fraction(threshold)
        self._keys: List[Key] = []
        self._values: List[Fraction] = []
        self._by_key: Dict[Key, Fraction] = {}
        self._active: Dict[Tuple[Key, Key], ChangePoint] = {}
        self._events: List[Notification] = []
        self.last_touched: int = 0
        self.last_touched_ids: List[Tuple[Key, Key]] = []
        self.total_touched: int = 0

    # ---- 只读视图 -------------------------------------------------------

    def change_points(self) -> List[ChangePoint]:
        """当前全部成立变化点，按 (seq, source) 升序排列。"""
        points = list(self._active.values())
        points.sort(key=lambda p: (p.gap[0][1], p.gap[0][0],
                                   p.gap[1][1], p.gap[1][0]))
        return points

    def get(self, key: Key) -> Optional[Fraction]:
        return self._by_key.get(key)

    def __contains__(self, key: Key) -> bool:
        return key in self._by_key

    def __len__(self) -> int:
        return len(self._keys)

    def drain_notifications(self) -> List[Notification]:
        """取出并清空自上次调用以来累积的 add / retract 通知。"""
        events = self._events
        self._events = []
        return events

    # ---- 变更操作 -------------------------------------------------------

    def upsert(self, source: Any, seq: Any, value: Value) -> List[Notification]:
        """新增事件，或用新值更正已有事件；同值重复提交不触发重算。"""
        return self._put((source, seq), _as_fraction(value))

    def withdraw(self, source: Any, seq: Any) -> List[Notification]:
        """撤回一条历史记录；记录不存在时为空操作。"""
        return self._remove((source, seq))

    def apply(self, operations: List[tuple]) -> List[Notification]:
        """批量提交操作。

        操作形如 ("upsert", source, seq, value) 或 ("withdraw", source, seq)。
        通知按操作顺序累积返回；last_touched 反映最后一个实际变更操作。
        """
        out: List[Notification] = []
        for op in operations:
            if op[0] == "upsert":
                out.extend(self.upsert(op[1], op[2], op[3]))
            elif op[0] == "withdraw":
                out.extend(self.withdraw(op[1], op[2]))
            else:
                raise ValueError("unknown operation: %r" % (op[0],))
        return out

    # ---- 内部实现 -------------------------------------------------------

    def _put(self, key: Key, value: Fraction) -> List[Notification]:
        pos = bisect_left(self._keys, key)
        if pos < len(self._keys) and self._keys[pos] == key:
            if self._values[pos] == value:
                self.last_touched = 0
                self.last_touched_ids = []
                return self._take_events()
            before_ids = self._ids_around(pos)
            return self._recompute(before_ids, lambda: self._assign(pos, value),
                                   moved=False)
        before_ids = self._ids_around(pos)
        return self._recompute(
            before_ids, lambda: self._insert(pos, key, value),
            moved=True, pos_after=pos)

    def _remove(self, key: Key) -> List[Notification]:
        pos = bisect_left(self._keys, key)
        if pos >= len(self._keys) or self._keys[pos] != key:
            self.last_touched = 0
            self.last_touched_ids = []
            return self._take_events()
        before_ids = self._ids_around(pos)
        return self._recompute(
            before_ids, lambda: self._delete(pos),
            moved=True, pos_after=pos - 1)

    def _assign(self, pos: int, value: Fraction) -> None:
        self._values[pos] = value
        self._by_key[self._keys[pos]] = value

    def _insert(self, pos: int, key: Key, value: Fraction) -> None:
        self._keys.insert(pos, key)
        self._values.insert(pos, value)
        self._by_key[key] = value

    def _delete(self, pos: int) -> None:
        key = self._keys.pop(pos)
        self._values.pop(pos)
        del self._by_key[key]

    def _ids_around(self, pos: int) -> List[Tuple[Key, Key]]:
        """返回当前数组中位置 pos 变动可能影响的候选点 id。

        间隙 j 的左窗口为位置 [j-L+1, j]，右窗口为 [j+1, j+R]，
        因此位置 pos 只可能影响间隙 j ∈ [pos-R, pos+L-1]。
        """

        lo = max(0, pos - self.right_min)
        hi = min(len(self._keys) - 2, pos + self.left_min - 1)
        return [(self._keys[j], self._keys[j + 1])
                for j in range(lo, hi + 1)]

    def _evaluate(self, idx: int) -> Optional[ChangePoint]:
        """评估当前数组中的间隙 idx（keys[idx] | keys[idx+1]）。"""
        n = len(self._keys)
        if idx < 0 or idx >= n - 1:
            return None
        left_lo = idx - self.left_min + 1
        right_hi = idx + self.right_min
        if left_lo < 0 or right_hi > n - 1:
            return None
        left_vals = self._values[left_lo:idx + 1]
        right_vals = self._values[idx + 1:right_hi + 1]
        left_count = idx - left_lo + 1
        right_count = right_hi - idx
        left_mean = sum(left_vals, Fraction(0)) / left_count
        right_mean = sum(right_vals, Fraction(0)) / right_count
        diff = abs(left_mean - right_mean)
        if diff < self.threshold:
            return None
        return ChangePoint(
            gap=(self._keys[idx], self._keys[idx + 1]),
            diff=diff,
            left_evidence=tuple(self._keys[left_lo:idx + 1]),
            right_evidence=tuple(self._keys[idx + 1:right_hi + 1]),
        )

    def _recompute(self, before_ids, mutate, moved,
                   pos_after: Optional[int] = None) -> List[Notification]:
        before_active = dict(self._active)
        mutate()

        all_ids = list(before_ids)
        seen = set(all_ids)
        if moved:
            for gid in self._ids_around(pos_after):
                if gid not in seen:
                    seen.add(gid)
                    all_ids.append(gid)

        payload: Dict[Tuple[Key, Key], Optional[ChangePoint]] = {}
        for gid in all_ids:
            idx = bisect_left(self._keys, gid[1]) - 1
            if (0 <= idx < len(self._keys) - 1
                    and self._keys[idx] == gid[0]
                    and self._keys[idx + 1] == gid[1]):
                payload[gid] = self._evaluate(idx)
            else:
                payload[gid] = None  # 该相邻对已被插入/删除破坏

        for gid in all_ids:
            old = before_active.get(gid)
            new = payload[gid]
            if old is None and new is not None:
                self._active[gid] = new
                self._events.append(Notification("add", new))
            elif old is not None and new is None:
                self._active.pop(gid, None)
                self._events.append(Notification("retract", old))
            elif old is not None and new is not None and old != new:
                self._events.append(Notification("retract", old))
                self._active[gid] = new
                self._events.append(Notification("add", new))

        self.last_touched = len(all_ids)
        self.last_touched_ids = all_ids
        self.total_touched += len(all_ids)
        return self._take_events()

    def _take_events(self) -> List[Notification]:
        events = self._events
        self._events = []
        return events
