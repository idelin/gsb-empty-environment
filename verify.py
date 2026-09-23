"""独立验证：代数恒等式 + 排除集合合法性 + 有理采样对照。

不依赖 RationalFunction 的内部化简逻辑，直接比较原始与化简后的
多项式，作为独立交叉检查。
"""
from __future__ import annotations

import random
from fractions import Fraction

from polyutil import mul, evaluate, count_real_roots
from rational import RationalFunction, is_excluded


def verify_identity(rf: RationalFunction) -> bool:
    """代数恒等式验证：original_num * den == num * original_den。

    这是多项式恒等式（交叉相乘），在排除集合外的所有点上蕴含
    约分前后函数值相等；不要求任何闭式根表达。
    """
    return mul(rf.original_num, rf.den) == mul(rf.num, rf.original_den)


def verify_exclusions(rf: RationalFunction) -> bool:
    """排除集合合法性：每个排除点/区间确实对应原分母的实根。"""
    den = rf.original_den
    for item in rf.exclusions:
        if isinstance(item, tuple):
            lo, hi = item
            if count_real_roots(den, lo, hi) != 1:
                return False
        else:
            if evaluate(den, item) != 0:
                return False
    return True


def sample_verify(rf: RationalFunction, count=32, seed=20260923,
                  lo=-20, hi=20) -> int:
    """有理采样验证：在排除集合外的随机有理点上，

    约分前后函数值必须完全一致（精确 Fraction 比较）。
    返回实际检查的采样点数。
    """
    rng = random.Random(seed)
    checked = 0
    attempts = 0
    while checked < count:
        attempts += 1
        if attempts > count * 100:
            raise RuntimeError("采样点几乎全部落在排除集合内")
        x = Fraction(rng.randint(lo, hi), rng.randint(1, 12))
        if is_excluded(rf.exclusions, x):
            continue
        before = evaluate(rf.original_num, x) / evaluate(rf.original_den, x)
        after = evaluate(rf.num, x) / evaluate(rf.den, x)
        if before != after:
            raise AssertionError(
                f"采样点 x = {x} 处约分前后不一致：{before} != {after}")
        checked += 1
    return checked


def full_verify(rf: RationalFunction, samples=32, seed=20260923) -> dict:
    """三项独立验证一次跑完，返回结果字典。"""
    identity = verify_identity(rf)
    exclusions = verify_exclusions(rf)
    checked = sample_verify(rf, count=samples, seed=seed)
    return {
        "代数恒等式": identity,
        "排除集合合法": exclusions,
        "采样一致点数": checked,
        "通过": identity and exclusions,
    }
