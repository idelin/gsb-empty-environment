"""有界、可重绕、可溢写到磁盘的行暂存区。

行在达到内存上限前保存在内存；超过上限后整组溢写到一个临时文件。
之后可以反复从头迭代（merge join 的大组 × 大组笛卡尔积需要这一能力）。
"""

from __future__ import annotations

import os
import pickle
import tempfile
from collections.abc import Iterator


class BoundedSpool:
    """有界暂存区。

    Parameters
    ----------
    memory_limit:
        允许驻留内存的最大行数；超过后溢写磁盘。
    tmp_dir:
        溢写文件所在目录，``None`` 表示使用系统临时目录。
    """

    def __init__(self, memory_limit: int = 1000, tmp_dir: str | None = None):
        if memory_limit < 0:
            raise ValueError("memory_limit must be >= 0")
        self._memory_limit = memory_limit
        self._tmp_dir = tmp_dir
        self._rows: list[tuple] = []
        self._path: str | None = None
        self._fh = None
        self._pickler: pickle.Pickler | None = None
        self._count = 0
        self._closed = False

    @property
    def count(self) -> int:
        return self._count

    @property
    def spilled(self) -> bool:
        return self._path is not None

    @property
    def path(self) -> str | None:
        return self._path

    def _spill(self) -> None:
        fd, path = tempfile.mkstemp(prefix="mjspool_", dir=self._tmp_dir)
        self._path = path
        self._fh = os.fdopen(fd, "w+b")
        self._pickler = pickle.Pickler(self._fh)
        for row in self._rows:
            self._pickler.dump(row)
        self._rows = []

    def append(self, row: tuple) -> None:
        if self._closed:
            raise RuntimeError("spool is closed")
        if self._path is None and self._count >= self._memory_limit:
            self._spill()
        if self._pickler is not None:
            assert self._pickler is not None
            self._pickler.dump(row)
        else:
            self._rows.append(row)
        self._count += 1

    def finish_write(self) -> None:
        """结束写入，落盘缓冲，供之后反复迭代。"""
        if self._fh is not None:
            self._fh.flush()

    def rows(self) -> Iterator[tuple]:
        """从暂存区头部开始迭代一次（可多次调用）。"""
        if self._path is not None:
            assert self._fh is not None
            self._fh.seek(0)
            unpickler = pickle.Unpickler(self._fh)
            for _ in range(self._count):
                yield unpickler.load()
        else:
            yield from self._rows

    def close(self) -> None:
        """幂等关闭并删除溢写文件。"""
        if self._closed:
            return
        self._closed = True
        self._rows = []
        if self._fh is not None:
            try:
                self._fh.close()
            finally:
                if self._path is not None:
                    try:
                        os.unlink(self._path)
                    except FileNotFoundError:
                        pass

    def __enter__(self) -> "BoundedSpool":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()

    def __len__(self) -> int:
        return self._count
