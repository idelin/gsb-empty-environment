"""基于多重集合（bag）语义的增量视图维护引擎。

支持算子：过滤 Filter、投影 Project、等值连接 Join、分组聚合 GroupBy。
输入为基础关系上带行 ID 的插入/删除，算子间以 (元组, 重数差量) 传播，
视图终点物化 元组 -> 重数。任何元组重数变为负数即抛 NegativeMultiplicityError。

NULL（None）语义：
  - 比较谓词：任一侧为 None 时结果为假（三值逻辑中取 false 分支）；
  - 连接：键为 None 的行不与任何行匹配；
  - 分组：None 是合法分组键，自成一组；
  - 求和：忽略 None 值，计数仍计入该行。
"""
from __future__ import annotations

import operator as _op


class IVMError(Exception):
    """视图维护错误基类。"""


class NegativeMultiplicityError(IVMError):
    """某元组的重数变为负数。"""


class UnknownRowError(IVMError):
    """删除不存在的行 ID。"""


class DuplicateRowError(IVMError):
    """插入已存在的行 ID。"""


def _bump(state, key, delta):
    """state[key] += delta；结果为负抛错，为零则移除。"""
    new = state.get(key, 0) + delta
    if new < 0:
        raise NegativeMultiplicityError(
            f"元组 {key!r} 重数变为负数: {state.get(key, 0)} + ({delta})"
        )
    if new:
        state[key] = new
    else:
        state.pop(key, None)


def _cmp(fn, col, value):
    return lambda row: row[col] is not None and fn(row[col], value)


def eq(col, value):
    return _cmp(_op.eq, col, value)


def ne(col, value):
    return _cmp(_op.ne, col, value)


def lt(col, value):
    return _cmp(_op.lt, col, value)


def le(col, value):
    return _cmp(_op.le, col, value)


def gt(col, value):
    return _cmp(_op.gt, col, value)


def ge(col, value):
    return _cmp(_op.ge, col, value)


class Node:
    """算子基类：outputs 为下游回调列表，接收 [(元组, 差量), ...]。"""

    def __init__(self):
        self.outputs = []

    def _emit(self, deltas):
        if deltas:
            for out in self.outputs:
                out(list(deltas))


class Relation(Node):
    """基础关系：行 ID -> 元组。插入/删除产生 ±1 差量并推给下游。"""

    def __init__(self, name):
        super().__init__()
        self.name = name
        self.rows = {}

    def insert(self, row_id, row):
        if row_id in self.rows:
            raise DuplicateRowError(f"{self.name}: 行 ID {row_id!r} 已存在")
        row = tuple(row)
        self.rows[row_id] = row
        self._emit([(row, 1)])

    def delete(self, row_id):
        if row_id not in self.rows:
            raise UnknownRowError(f"{self.name}: 行 ID {row_id!r} 不存在")
        row = self.rows.pop(row_id)
        self._emit([(row, -1)])

    def load(self, rows):
        for row_id, row in rows:
            self.insert(row_id, row)


class Filter(Node):
    """过滤：仅放行谓词为真的元组差量。"""

    def __init__(self, pred):
        super().__init__()
        self.pred = pred
        self.state = {}

    def feed(self, deltas):
        out = []
        for row, m in deltas:
            if self.pred(row):
                _bump(self.state, row, m)
                out.append((row, m))
        self._emit(out)


class Project(Node):
    """投影：按列子集映射，重复元组折叠为重数。"""

    def __init__(self, cols):
        super().__init__()
        self.cols = tuple(cols)
        self.state = {}

    def feed(self, deltas):
        net = {}
        for row, m in deltas:
            key = tuple(row[c] for c in self.cols)
            net[key] = net.get(key, 0) + m
        out = []
        for key, m in net.items():
            if m:
                _bump(self.state, key, m)
                out.append((key, m))
        self._emit(out)


class Join(Node):
    """等值连接：输出元组 = 左元组 + 右元组，重数为两侧重数之积。

    一侧新增差量 m 时，与另一侧每个匹配行（重数 om）产生 m*om 的输出差量；
    任一侧连接键为 None 时不匹配。
    """

    def __init__(self, left_key, right_key):
        super().__init__()
        self.left_key = left_key
        self.right_key = right_key
        self.left = {}
        self.right = {}
        self.state = {}

    def feed_left(self, deltas):
        self._emit(self._apply(deltas, self.left, self.right,
                               self.left_key, self.right_key, flip=False))

    def feed_right(self, deltas):
        self._emit(self._apply(deltas, self.right, self.left,
                               self.right_key, self.left_key, flip=True))

    def _apply(self, deltas, mine, other, my_key, other_key, flip):
        out = []
        for row, m in deltas:
            _bump(mine, row, m)
            key = row[my_key]
            if key is None:
                continue
            for orow, om in other.items():
                if orow[other_key] == key:
                    joined = orow + row if flip else row + orow
                    delta = m * om
                    _bump(self.state, joined, delta)
                    out.append((joined, delta))
        return out


class GroupBy(Node):
    """分组聚合：输出 (键..., count, sum)。

    count 计行数，sum 对 val_col 求和（忽略 None）。
    组内计数变化时输出 撤回旧行 + 插入新行 两条差量；计数归零时组行被撤回。
    """

    def __init__(self, key_cols, val_col):
        super().__init__()
        self.key_cols = tuple(key_cols)
        self.val_col = val_col
        self.inputs = {}
        self.groups = {}
        self.state = {}

    def feed(self, deltas):
        net = {}
        for row, m in deltas:
            net[row] = net.get(row, 0) + m
        per_group = {}
        for row, m in net.items():
            if not m:
                continue
            _bump(self.inputs, row, m)
            key = tuple(row[c] for c in self.key_cols)
            dc, ds = per_group.get(key, (0, 0))
            val = row[self.val_col]
            if val is not None:
                ds += val * m
            per_group[key] = (dc + m, ds)
        out = []
        for key, (dc, ds) in per_group.items():
            count, total = self.groups.get(key, (0, 0))
            old = key + (count, total) if count else None
            ncount, ntotal = count + dc, total + ds
            if ncount < 0:
                raise NegativeMultiplicityError(
                    f"分组 {key!r} 计数变为负数: {count} + ({dc})"
                )
            new = key + (ncount, ntotal) if ncount else None
            if ncount:
                self.groups[key] = (ncount, ntotal)
            else:
                self.groups.pop(key, None)
            if old is not None:
                _bump(self.state, old, -1)
                out.append((old, -1))
            if new is not None:
                _bump(self.state, new, 1)
                out.append((new, 1))
        self._emit(out)


class View:
    """视图终点：物化 元组 -> 重数，并记录全部差量日志。"""

    def __init__(self, name):
        self.name = name
        self.state = {}
        self.log = []

    def feed(self, deltas):
        for row, m in deltas:
            _bump(self.state, row, m)
            self.log.append((row, m))

    def rows(self):
        return sorted(self.state.items(), key=lambda kv: repr(kv[0]))
