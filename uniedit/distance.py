"""加权编辑距离：精确 DP，按 Unicode 码点计算。

支持加权插入 / 删除 / 替换与相邻换位（OSA 风格的相邻换位项）。
返回值是该良定义函数的精确值（非近似）。

重要性质（证明见 README 与 metric.py 文档）：

* 任意权重下，即使不带换位，DP 距离也可能违反三角不等式
  （反例：ins(c)=3, ins(b)=1, sub(b,c)=1 时 d('','c')=3 > d('','b')+d('b','c')=2）。
  仅当符号级成本满足三角不等式（见 metric.check_metric）时，
  DP 才等于脚本最小成本，从而是（伪）度量。
* 带相邻换位项时，即使单位成本三角不等式也可能失效
  （经典反例：d(ca,ac)=1, d(ac,abc)=1, 但 d(ca,abc)=3）。
* 若换位成本被"无换位距离"支配（trans(a,b) >= d_notrans(a+b, b+a)），
  则可归纳证明带换位的 DP 与无换位 DP 相等，从而恢复度量性质。
"""
from __future__ import annotations

from .costs import CostModel


def distance(a: str, b: str, costs: CostModel, *, use_transpose: bool = True) -> float:
    """a 与 b 之间的加权编辑距离（按码点）。时间 O(len(a)*len(b))。"""
    m, n = len(a), len(b)
    ins, dele, sub, trans = costs.insert, costs.delete, costs.substitute, costs.transpose

    prevprev = None  # 第 i-2 行
    prev = [0.0] * (n + 1)  # 第 i-1 行；先填第 0 行：空前缀 -> b 前缀，纯插入
    acc = 0.0
    for j in range(1, n + 1):
        acc += ins(b[j - 1])
        prev[j] = acc

    for i in range(1, m + 1):
        ai = a[i - 1]
        cur = [0.0] * (n + 1)
        cur[0] = prev[0] + dele(ai)
        for j in range(1, n + 1):
            bj = b[j - 1]
            best = prev[j] + dele(ai)  # 删除 a[i-1]
            v = cur[j - 1] + ins(bj)  # 插入 b[j-1]
            if v < best:
                best = v
            v = prev[j - 1] + (0.0 if ai == bj else sub(ai, bj))  # 替换/匹配
            if v < best:
                best = v
            if (
                use_transpose
                and i >= 2
                and j >= 2
                and a[i - 2] == b[j - 1]
                and ai == b[j - 2]
            ):
                v = prevprev[j - 2] + trans(a[i - 2], ai)  # 相邻换位
                if v < best:
                    best = v
            cur[j] = best
        prevprev, prev = prev, cur
    return prev[n]


def length_lower_bound(a: str, b: str, costs: CostModel) -> float:
    """可证明的下界，不依赖三角不等式，对任意非负成本安全。

    证明：任何把 a 变成 b 的 DP 路径（编辑脚本）中，插入次数减去删除
    次数恰为 len(b)-len(a)（替换与换位不改变长度）。故 len(b)>len(a) 时
    至少需 len(b)-len(a) 次插入，每次成本 >= min_insert；对称地，
    len(a)>len(b) 时至少需 len(a)-len(b) 次删除，每次 >= min_delete。
    """
    diff = len(b) - len(a)
    if diff > 0:
        return diff * costs.min_insert
    if diff < 0:
        return -diff * costs.min_delete
    return 0.0
