"""Minimal dense matrix used as a reference oracle in tests and demos."""
from __future__ import annotations


class DenseMatrix:
    def __init__(self, grid):
        self.grid = [list(row) for row in grid]

    @classmethod
    def zeros(cls, nrows: int, ncols: int) -> "DenseMatrix":
        return cls([[0] * ncols for _ in range(nrows)])

    @property
    def shape(self):
        if not self.grid:
            return (0, 0)
        return (len(self.grid), len(self.grid[0]))

    def get(self, i: int, j: int):
        return self.grid[i][j]

    def set(self, i: int, j: int, value) -> None:
        self.grid[i][j] = value

    def insert_rows(self, k: int, n: int = 1) -> None:
        width = self.shape[1]
        self.grid[k:k] = [[0] * width for _ in range(n)]

    def delete_rows(self, k: int, n: int = 1) -> None:
        del self.grid[k:k + n]

    def insert_cols(self, k: int, n: int = 1) -> None:
        for row in self.grid:
            row[k:k] = [0] * n

    def delete_cols(self, k: int, n: int = 1) -> None:
        for row in self.grid:
            del row[k:k + n]

    def transpose(self) -> "DenseMatrix":
        nrows, ncols = self.shape
        return type(self)(
            [[self.grid[r][c] for r in range(nrows)] for c in range(ncols)]
        )

    def slice(self, row0: int, row1: int, col0: int, col1: int) -> "DenseMatrix":
        return type(self)([row[col0:col1] for row in self.grid[row0:row1]])

    def to_lists(self):
        return [list(row) for row in self.grid]

    def __eq__(self, other) -> bool:
        if isinstance(other, DenseMatrix):
            return other.grid == self.grid
        return NotImplemented

    def __repr__(self) -> str:
        return "\n".join(" ".join(str(v) for v in row) for row in self.grid)
