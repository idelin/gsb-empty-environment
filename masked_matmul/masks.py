"""结构掩码:显式集合与补集两种实现,补集绝不物化完整稠密矩阵。"""

from .errors import BoundsError, RetractionError


def _check_dims(rows, cols):
    if rows <= 0 or cols <= 0:
        raise ValueError(f"尺寸必须为正整数: {rows}x{cols}")


class ExplicitMask:
    """显式掩码:只存储被允许的 (i, j) 及其计数。"""

    kind = "explicit"

    def __init__(self, rows, cols):
        _check_dims(rows, cols)
        self.rows = rows
        self.cols = cols
        self._counts = {}

    def _check(self, i, j):
        if not (0 <= i < self.rows and 0 <= j < self.cols):
            raise BoundsError(f"掩码索引越界: ({i}, {j}) 尺寸 {self.rows}x{self.cols}")

    def contains(self, i, j):
        return self._counts.get((i, j), 0) > 0

    def add(self, i, j):
        self._check(i, j)
        key = (i, j)
        self._counts[key] = self._counts.get(key, 0) + 1

    def remove(self, i, j):
        self._check(i, j)
        key = (i, j)
        count = self._counts.get(key, 0)
        if count <= 0:
            raise RetractionError(f"掩码项 ({i}, {j}) 不存在, 无法删除")
        if count == 1:
            del self._counts[key]
        else:
            self._counts[key] = count - 1

    def stored_entries(self):
        return len(self._counts)


class ComplementMask:
    """补集掩码:默认全部可见,只存储被排除的 (i, j)。

    无论尺寸多大,内存占用只与排除项数量相关,绝不物化完整矩阵。
    """

    kind = "complement"

    def __init__(self, rows, cols):
        _check_dims(rows, cols)
        self.rows = rows
        self.cols = cols
        self._excluded = {}

    def _check(self, i, j):
        if not (0 <= i < self.rows and 0 <= j < self.cols):
            raise BoundsError(f"掩码索引越界: ({i}, {j}) 尺寸 {self.rows}x{self.cols}")

    def contains(self, i, j):
        return (
            0 <= i < self.rows
            and 0 <= j < self.cols
            and self._excluded.get((i, j), 0) == 0
        )

    def add(self, i, j):
        """恢复一个先前被排除的掩码项。"""
        self._check(i, j)
        key = (i, j)
        count = self._excluded.get(key, 0)
        if count <= 0:
            raise RetractionError(f"掩码项 ({i}, {j}) 未被排除, 无需恢复")
        if count == 1:
            del self._excluded[key]
        else:
            self._excluded[key] = count - 1

    def remove(self, i, j):
        """排除一个掩码项(可重复,计数累加)。"""
        self._check(i, j)
        key = (i, j)
        self._excluded[key] = self._excluded.get(key, 0) + 1

    def stored_entries(self):
        return len(self._excluded)
