"""合成数据生成。仅生成合成数据，不含任何真实个人信息。"""
from __future__ import annotations

import random
from typing import Any, NamedTuple


class Record(NamedTuple):
    class_key: Any  # 等价类键（准标识符取值组合）
    sensitive: Any  # 敏感值，可为 None


# 演示用敏感值层级：具体疾病 -> 科室类别
DISEASE_HIERARCHY = {
    "flu": "respiratory",
    "cold": "respiratory",
    "asthma": "respiratory",
    "diabetes": "endocrine",
    "hypertension": "cardio",
}


def generate_records(seed: int = 42) -> list[Record]:
    """生成固定结构的合成数据集（含重复值与 null）。"""
    rng = random.Random(seed)
    diseases = list(DISEASE_HIERARCHY)
    plan = [
        (("20-29", "100**"), 12),
        (("30-39", "100**"), 9),
        (("40-49", "200**"), 8),
        (("20-29", "200**"), 6),
    ]
    records: list[Record] = []
    for class_key, size in plan:
        dominant = rng.choice(diseases)
        for _ in range(size):
            r = rng.random()
            if r < 0.08:
                value = None
            elif r < 0.72:
                value = dominant
            else:
                value = rng.choice(diseases)
            records.append(Record(class_key, value))
    # 人为制造重复值：同一敏感值出现 10 次，仍只算 1 种
    records.extend(Record(("50-59", "300**"), "flu") for _ in range(10))
    # 人为制造全 null 类
    records.extend(Record(("60-69", "300**"), None) for _ in range(4))
    return records
