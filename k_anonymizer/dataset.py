"""合成数据生成：仅用于演示与测试。

所有记录均由随机数生成器合成，不对应任何真实个人数据；
基于此数据的统计实验结果不构成形式化隐私证明。
"""

from __future__ import annotations

import random

from .hierarchy import Hierarchy


def build_age_hierarchy():
    """age: 具体年龄 -> 10 岁段 -> 20 岁段 -> *（4 层）。"""
    parents = {}
    leaves = [str(a) for a in range(0, 80)]
    for a in range(0, 80):
        leaf = str(a)
        decade = f"{a // 10 * 10}-{a // 10 * 10 + 9}"
        band = f"{a // 20 * 20}-{a // 20 * 20 + 19}"
        parents.setdefault(leaf, []).append(decade)
        parents.setdefault(decade, []).append(band)
        parents.setdefault(band, []).append("*")
    return Hierarchy("age", leaves, parents)


def build_zip_hierarchy():
    """zip: 4 位编码 -> 逐位打星 -> ****（5 层）。"""
    leaves = [f"{r}{c:03d}" for r in (1, 2, 3) for c in range(10)]
    parents = {}
    for z in leaves:
        chain = [z[:3] + "*", z[:2] + "**", z[:1] + "***", "****"]
        prev = z
        for node in chain:
            parents.setdefault(prev, []).append(node)
            prev = node
    return Hierarchy("zip", leaves, parents)


def build_gender_hierarchy():
    """gender: F/M/U -> *（2 层）。"""
    leaves = ["F", "M", "U"]
    parents = {g: ["*"] for g in leaves}
    return Hierarchy("gender", leaves, parents)


def build_city_hierarchy():
    """city: 含 DAG 钻石结构，演示多路径收敛去重（3 层）。

    d0 -> north -> metro 与 d0 -> metro 两条路径在 metro 收敛；
    层级 1 的祖先仍唯一（north），层级 2 的祖先去重后唯一（metro）。
    """
    leaves = ["d0", "d1", "d2", "d3"]
    parents = {
        "d0": ["north", "metro"],
        "d1": ["north"],
        "d2": ["south"],
        "d3": ["south"],
        "north": ["metro"],
        "south": ["metro"],
    }
    return Hierarchy("city", leaves, parents)


def build_demo_hierarchies():
    return [
        build_age_hierarchy(),
        build_zip_hierarchy(),
        build_gender_hierarchy(),
        build_city_hierarchy(),
    ]


def generate_synthetic(n, seed):
    """生成 n 条合成记录，返回 (records, hierarchies)。同一 seed 结果确定。"""
    rng = random.Random(seed)
    hierarchies = build_demo_hierarchies()
    ages = [str(a) for a in range(18, 80)]
    zips = [f"{r}{c:03d}" for r in (1, 2, 3) for c in range(10)]
    genders = ["F", "M", "U"]
    cities = ["d0", "d1", "d2", "d3"]
    records = []
    for _ in range(n):
        records.append(
            (
                rng.choice(ages),
                rng.choice(zips),
                rng.choices(genders, weights=[48, 48, 4])[0],
                rng.choice(cities),
            )
        )
    return records, hierarchies
