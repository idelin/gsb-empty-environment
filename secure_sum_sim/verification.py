"""守恒核验与有限状态探索。

注意：这里的随机化状态探索是统计性 sanity check，
不构成任何形式化隐私或安全证明。
"""

from __future__ import annotations

import random

from .protocol import RoundStatus, run_round
from .sharing import DEFAULT_PRIME


def check_conservation(result, inputs, prime=DEFAULT_PRIME):
    """核验：COMPLETE 轮次的输出 == 覆盖集合实际输入之和 (mod p)。"""
    if result.status is not RoundStatus.COMPLETE:
        raise ValueError("只能核验已完成的轮次")
    expected = sum(inputs[p] % prime for p in result.covered_parties) % prime
    return result.value == expected


def explore_states(trials, prime=DEFAULT_PRIME, seed=None):
    """在随机生成的有限域状态（输入、阈值、掉线集合）上批量跑轮次。

    对每个 COMPLETE 轮次核验输入守恒；对每个 INCOMPLETE 轮次核验
    其不携带任何数值。返回统计计数。
    """
    rng = random.Random(seed)
    stats = {
        "trials": trials,
        "complete": 0,
        "incomplete": 0,
        "conservation_ok": 0,
        "violations": 0,
    }
    for i in range(trials):
        n = rng.randint(3, 8)
        ids = [f"P{j}" for j in range(n)]
        inputs = {pid: rng.randrange(prime) for pid in ids}
        threshold = rng.randint(2, n)
        dropped = {pid for pid in ids if rng.random() < 0.3}
        result, _ = run_round(f"explore-{i}", inputs, threshold, prime, dropped, rng)
        if result.status is RoundStatus.COMPLETE:
            stats["complete"] += 1
            if check_conservation(result, inputs, prime):
                stats["conservation_ok"] += 1
            else:
                stats["violations"] += 1
        else:
            stats["incomplete"] += 1
            if result.value is not None:
                stats["violations"] += 1
    return stats
