"""示例模型：互斥协议（含缺陷版本）与带领导者模型。

局部状态（互斥 worker）：0=非临界区，1=尝试进入，2=临界区。
共享变量：锁，0=空闲，1=被占用。
"""

from __future__ import annotations

from typing import List

from .model import Model, Role

NONCRIT, TRYING, CRITICAL = 0, 1, 2


def mutex_worker_role(buggy: bool = False) -> Role:
    """互斥协议 worker。``buggy=True`` 时进入临界区前不检查锁（缺陷版本）。"""

    def transitions(local, shared):
        if local == NONCRIT:
            yield TRYING, shared
        elif local == TRYING:
            if buggy or shared == 0:
                yield CRITICAL, 1
        elif local == CRITICAL:
            yield NONCRIT, 0

    name = "buggy-worker" if buggy else "worker"
    return Role(name=name, initial=NONCRIT, transitions=transitions)


def leader_role() -> Role:
    """领导者：在 0->1->2->0 上循环，与 worker 行为不同，属特殊角色。"""

    def transitions(local, shared):
        yield (local + 1) % 3, shared

    return Role(name="leader", initial=0, transitions=transitions)


def at_most_one_critical(state) -> bool:
    _, locals_ = state
    return sum(1 for local in locals_ if local == CRITICAL) <= 1


def build_mutex_model(num_workers: int = 3, buggy: bool = False) -> Model:
    """N 个同构 worker 的互斥模型；默认按角色自动声明一个对称组。"""
    roles: List[Role] = [mutex_worker_role(buggy) for _ in range(num_workers)]
    name = f"mutex-{num_workers}{'-buggy' if buggy else ''}"
    return Model(
        name=name,
        roles=roles,
        initial_shared=0,
        invariant=at_most_one_critical,
    )


def build_leader_model(num_workers: int = 2, buggy: bool = False) -> Model:
    """1 个领导者 + N 个 worker：只有 worker 之间可置换，领导者不参与。"""
    roles: List[Role] = [leader_role()]
    roles += [mutex_worker_role(buggy) for _ in range(num_workers)]

    def workers_at_most_one_critical(state) -> bool:
        _, locals_ = state
        return sum(1 for local in locals_[1:] if local == CRITICAL) <= 1

    return Model(
        name=f"leader-{num_workers}{'-buggy' if buggy else ''}",
        roles=roles,
        initial_shared=0,
        invariant=workers_at_most_one_critical,
    )
