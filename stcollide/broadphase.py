"""宽阶段：时空哈希网格索引。

每个对象在窗口内的每个匀速分段被扫掠为一个时空包围盒（ST-box），
插入到 (时间, 空间...) 哈希网格中。共享同一网格单元的对象对成为
候选碰撞对，交给窄阶段精确求解。索引是保守的：真实碰撞对一定
是候选对（碰撞点所在单元同时覆盖双方）。
"""

from __future__ import annotations

import itertools
import math

from .core import F


class STGrid:
    def __init__(self, dim, cell_size):
        self.dim = dim
        self.cell = F(cell_size)
        self._obj_cells = {}
        self._cell_objs = {}

    def _cells_of(self, st_boxes):
        cells = set()
        c = self.cell
        for ts, te, los, his in st_boxes:
            ranges = [(math.floor(ts / c), math.floor(te / c))]
            for lo, hi in zip(los, his):
                ranges.append((math.floor(lo / c), math.floor(hi / c)))
            for combo in itertools.product(*[range(a, b + 1) for a, b in ranges]):
                cells.add(combo)
        return cells

    def update(self, oid, st_boxes):
        self.remove(oid)
        cells = self._cells_of(st_boxes)
        self._obj_cells[oid] = cells
        for cell in cells:
            self._cell_objs.setdefault(cell, set()).add(oid)

    def remove(self, oid):
        for cell in self._obj_cells.pop(oid, ()):
            bucket = self._cell_objs.get(cell)
            if bucket is not None:
                bucket.discard(oid)
                if not bucket:
                    del self._cell_objs[cell]

    def pairs_for(self, oid):
        out = set()
        for cell in self._obj_cells.get(oid, ()):
            for other in self._cell_objs.get(cell, ()):
                if other != oid:
                    out.add(frozenset((oid, other)))
        return out

    def all_pairs(self):
        out = set()
        for objs in self._cell_objs.values():
            for a, b in itertools.combinations(objs, 2):
                out.add(frozenset((a, b)))
        return out
