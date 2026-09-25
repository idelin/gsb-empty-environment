"""带事件计数的稀疏多重集矩阵,同时维护行、列两个方向的索引。"""

from .errors import BoundsError, RetractionError


class SparseMatrix:
    """每个非零单元保存 [权重, 事件计数]。

    insert 对应 apply_delta(+w, +1), delete 对应 apply_delta(-w, -1)。
    计数归零时权重必须同时归零,否则视为不匹配的撤回。
    """

    def __init__(self, rows, cols):
        if rows <= 0 or cols <= 0:
            raise ValueError(f"尺寸必须为正整数: {rows}x{cols}")
        self.rows = rows
        self.cols = cols
        self._rows = {}
        self._cols = {}

    def weight(self, i, j):
        row = self._rows.get(i)
        if not row:
            return 0
        entry = row.get(j)
        return entry[0] if entry else 0

    def count(self, i, j):
        row = self._rows.get(i)
        if not row:
            return 0
        entry = row.get(j)
        return entry[1] if entry else 0

    def apply_delta(self, i, j, dw, dc):
        if not (0 <= i < self.rows and 0 <= j < self.cols):
            raise BoundsError(f"索引越界: ({i}, {j}) 尺寸 {self.rows}x{self.cols}")
        row = self._rows.get(i)
        entry = row.get(j) if row else None
        weight = entry[0] if entry else 0
        count = entry[1] if entry else 0
        new_weight = weight + dw
        new_count = count + dc
        if new_count < 0:
            raise RetractionError(f"撤回超过插入: ({i}, {j})")
        if new_count == 0 and new_weight != 0:
            raise RetractionError(f"撤回权重不匹配: ({i}, {j}) 剩余权重 {new_weight}")
        if new_count == 0:
            del row[j]
            if not row:
                del self._rows[i]
            col = self._cols[j]
            del col[i]
            if not col:
                del self._cols[j]
        else:
            if row is None:
                row = self._rows.setdefault(i, {})
            row[j] = [new_weight, new_count]
            col = self._cols.setdefault(j, {})
            col[i] = [new_weight, new_count]

    def row(self, i):
        return self._rows.get(i, {})

    def col(self, j):
        return self._cols.get(j, {})

    def rows_items(self):
        return self._rows.items()

    def nnz(self):
        return sum(len(row) for row in self._rows.values())
