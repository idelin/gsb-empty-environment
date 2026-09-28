"""离散小样本精确递推：在格点 (n, k) 上递推存活概率质量。

每个 (n, k) 的对数似然比由充分统计量闭式计算，判定与在线规则完全一致：
log_lr >= a 接受 H1；log_lr <= b 接受 H0；到达 n_max 仍在开区间内为未决。
全程只有精确的二项概率质量乘加（0/1 概率的路径质量自然为 0 而被跳过），
不使用固定样本 p 值，也不移动边界。
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from .config import SprtConfig
from .evidence import Decision


@dataclass(frozen=True)
class ExactReport:
    """精确结论（标注 kind='exact'）。"""
    kind: str
    true_p: float
    accept_h1: float       # 第一/二类错误随 true_p 解释：p=p0 时即 alpha，p=p1 时即 power
    accept_h0: float
    inconclusive: float
    expected_n: float
    expected_n_terminal: float  # 条件于在 n_max 前终止的期望停止样本数
    n_max: int


def exact_performance(config: SprtConfig, true_p: float) -> ExactReport:
    if not 0.0 <= true_p <= 1.0:
        raise ValueError("true_p 必须在 [0, 1] 内")
    a, b, n_max = config.log_a, config.log_b, config.n_max

    alive: dict[int, float] = {0: 1.0}   # k -> 概率质量，n 隐含
    p_accept_h1 = 0.0
    p_accept_h0 = 0.0
    expected_n = 0.0

    for n in range(n_max):
        nxt: dict[int, float] = {}
        for k, mass in alive.items():
            if mass <= 0.0:
                continue
            up = mass * true_p
            if up > 0.0:
                _route(nxt, k + 1, up, n + 1, config)
            down = mass * (1.0 - true_p)
            if down > 0.0:
                _route(nxt, k, down, n + 1, config)
        alive = {}
        for k2, item in nxt.items():
            decision, mass2 = item
            if decision == Decision.ACCEPT_H1:
                p_accept_h1 += mass2
                expected_n += (n + 1) * mass2
            elif decision == Decision.ACCEPT_H0:
                p_accept_h0 += mass2
                expected_n += (n + 1) * mass2
            else:
                alive[k2] = mass2
        if not alive:
            break

    p_inconclusive = sum(alive.values())
    expected_n += n_max * p_inconclusive
    p_terminal = p_accept_h0 + p_accept_h1
    exp_n_terminal = expected_n / p_terminal if p_terminal > 0.0 else float("nan")
    return ExactReport(
        kind="exact",
        true_p=true_p,
        accept_h1=p_accept_h1,
        accept_h0=p_accept_h0,
        inconclusive=p_inconclusive,
        expected_n=expected_n,
        expected_n_terminal=exp_n_terminal,
        n_max=n_max,
    )


def _route(nxt: dict, k: int, mass: float, n: int, config: SprtConfig) -> None:
    """把质量送到 (n, k)；同一格点由两个父格点汇入，必须合并不能覆盖。"""
    llr = config.canonical_log_lr(n, k)
    if llr >= config.log_a:
        decision = Decision.ACCEPT_H1
    elif llr <= config.log_b:
        decision = Decision.ACCEPT_H0
    else:
        decision = Decision.CONTINUE
    if k in nxt:
        prev_decision, prev_mass = nxt[k]
        if prev_decision != decision:
            raise RuntimeError("同一格点的判定必须唯一，不应发生")
        nxt[k] = (prev_decision, prev_mass + mass)
    else:
        nxt[k] = (decision, mass)


def exact_operating_points(config: SprtConfig) -> tuple[ExactReport, ExactReport]:
    """返回 H0 下（含 alpha）与 H1 下（含 beta）两份精确报告。"""
    return exact_performance(config, config.p0), exact_performance(config, config.p1)
