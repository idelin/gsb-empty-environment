"""编辑距离定义与计算。

所有距离都以 **Unicode 码点**（code point）为单位，即先把字符串当作
码点序列处理，不做字素簇（grapheme cluster）合并。

提供两个函数：

1. ``weighted_distance`` —— 对查询返回的 **精确距离**。
   支持加权插入 / 删除 / 替换 / 相邻换位（OSA 风格的 Wagner–Fischer DP）。
   注意：当权重不对称（insert != delete）或启用相邻换位时，
   该距离 **一般不满足三角不等式**（见 README 的反例），
   因此不能直接作为 BK 树的键。

2. ``damerau_levenshtein`` —— 单位成本的真 Damerau–Levenshtein 距离
   （Lowrance–Wagner 算法，允许同一子串被多次编辑）。
   它是字符串编辑图上的最短路径长度，**是度量（metric）**，
   乘以 ``c_min = min(各操作权重)`` 后作为 BK 树的键，
   是精确距离的可证明下界（证明见 README）。
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Weights:
    """四种编辑操作的非负成本。

    insert    —— 在目标侧插入一个码点
    delete    —— 从源侧删除一个码点
    substitute—— 替换一个码点
    transpose —— 交换两个相邻码点
    """

    insert: float = 1.0
    delete: float = 1.0
    substitute: float = 1.0
    transpose: float = 1.0

    def __post_init__(self) -> None:
        for name in ("insert", "delete", "substitute", "transpose"):
            value = getattr(self, name)
            if value < 0:
                raise ValueError(f"权重 {name} 必须非负，得到 {value!r}")

    @property
    def min_cost(self) -> float:
        """所有操作成本的最小值 c_min，用于构造下界键。"""
        return min(self.insert, self.delete, self.substitute, self.transpose)


def weighted_distance(a: str, b: str, w: Weights = Weights()) -> float:
    """加权编辑距离（OSA 风格相邻换位），以码点为单位。

    递推（d[i][j] 表示 a 前 i 个码点到 b 前 j 个码点的距离）::

        d[i][j] = min(
            d[i-1][j]   + w.delete,
            d[i][j-1]   + w.insert,
            d[i-1][j-1] + (0 if a[i]==b[j] else w.substitute),
            d[i-2][j-2] + w.transpose   # 仅当 a[i-1..i] 与 b[j-1..j] 互为换位
        )
    """
    m, n = len(a), len(b)
    if m == 0:
        return n * w.insert
    if n == 0:
        return m * w.delete

    d = [[0.0] * (n + 1) for _ in range(m + 1)]
    for i in range(1, m + 1):
        d[i][0] = d[i - 1][0] + w.delete
    for j in range(1, n + 1):
        d[0][j] = d[0][j - 1] + w.insert

    for i in range(1, m + 1):
        ai = a[i - 1]
        for j in range(1, n + 1):
            cost = 0.0 if ai == b[j - 1] else w.substitute
            best = min(
                d[i - 1][j] + w.delete,
                d[i][j - 1] + w.insert,
                d[i - 1][j - 1] + cost,
            )
            if (
                i > 1
                and j > 1
                and ai == b[j - 2]
                and a[i - 2] == b[j - 1]
            ):
                best = min(best, d[i - 2][j - 2] + w.transpose)
            d[i][j] = best
    return d[m][n]


def damerau_levenshtein(a: str, b: str) -> int:
    """单位成本的真 Damerau–Levenshtein 距离（Lowrance–Wagner）。

    操作集：插入、删除、替换、相邻换位，成本均为 1，
    不限制同一子串的编辑次数。它是字符串编辑无向图上的最短路径，
    因此满足度量的全部公理（证明见 README）。
    """
    m, n = len(a), len(b)
    inf = m + n
    # d[i+1][j+1] 对应 DP 中的 d(i, j)；第 0 行/列为哨兵。
    d = [[0] * (n + 2) for _ in range(m + 2)]
    d[0][0] = inf
    for i in range(m + 1):
        d[i + 1][0] = inf
        d[i + 1][1] = i
    for j in range(n + 1):
        d[0][j + 1] = inf
        d[1][j + 1] = j

    last_row: dict[str, int] = {}
    for i in range(1, m + 1):
        db = 0
        for j in range(1, n + 1):
            i1 = last_row.get(b[j - 1], 0)
            j1 = db
            cost = 1
            if a[i - 1] == b[j - 1]:
                cost = 0
                db = j
            d[i + 1][j + 1] = min(
                d[i][j] + cost,          # 替换
                d[i + 1][j] + 1,         # 插入
                d[i][j + 1] + 1,         # 删除
                d[i1][j1] + (i - i1 - 1) + 1 + (j - j1 - 1),  # 换位
            )
        last_row[a[i - 1]] = i
    return d[m + 1][n + 1]


def key_distance(a: str, b: str, w: Weights) -> float:
    """BK 树使用的键：``c_min * damerau_levenshtein(a, b)``。

    它是 ``weighted_distance(a, b, w)`` 的下界（README 引理 2），
    且本身是度量（缩放的度量，c_min = 0 时退化为零伪度量，
    三角不等式仍成立，索引依然正确，只是不再剪枝）。
    """
    return w.min_cost * damerau_levenshtein(a, b)
