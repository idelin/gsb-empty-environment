"""运行样例：多初态、死锁环/无提供者、活锁、声明终态违例、预算未决。"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(__file__))

from deadlock_checker import Action, analyze, format_report, make_model


def resource_deadlock_model():
    """双方各占一个资源并等待对方释放：finishA/finishB 是同步动作。

    (H_A,H_B) 中 finishA 被 P1 局部提供却要求 P2=I，finishB 反之，
    形成最小阻塞依赖环 P1 -> P2 -> P1。局部可用不等于全局可执行。
    """
    return make_model(
        name="双方资源死锁(多初态)",
        participants=("P1", "P2"),
        local_states={
            "P1": ("I", "H_A", "D"),
            "P2": ("I", "H_B", "D"),
        },
        actions=(
            Action("takeA", ("P1",), {"P1": "I"}, {"P1": "H_A"}),
            Action("takeB", ("P2",), {"P2": "I"}, {"P2": "H_B"}),
            Action("finishA", ("P1", "P2"),
                   {"P1": "H_A", "P2": "I"}, {"P1": "D", "P2": "D"}),
            Action("finishB", ("P2", "P1"),
                   {"P2": "H_B", "P1": "I"}, {"P2": "D", "P1": "D"}),
        ),
        initial_states=(("I", "I"), ("H_A", "I")),
        terminal_states=(("D", "D"),),
    )


def no_provider_model():
    """P1 的唯一出路 sync12 要求 P2 在 R；P2 没有任何局部可邀约动作。"""
    return make_model(
        name="无提供者死锁",
        participants=("P1", "P2"),
        local_states={"P1": ("W",), "P2": ("X", "R")},
        actions=(
            Action("sync12", ("P1", "P2"),
                   {"P1": "W", "P2": "R"}, {"P1": "W", "P2": "X"}),
        ),
        initial_states=(("W", "X"),),
        terminal_states=(),
    )


def livelock_model():
    """W 上唯一可执行动作是自环 spin：活锁候选而非死锁；D 是正常终态。"""
    return make_model(
        name="自环活锁与正常终态(多初态)",
        participants=("P1",),
        local_states={"P1": ("W", "D")},
        actions=(
            Action("spin", ("P1",), {"P1": "W"}, {"P1": "W"}),
        ),
        initial_states=(("W",), ("D",)),
        terminal_states=(("D",),),
    )


def terminal_violation_model():
    """声明终态 T 上仍有可执行动作 leave。"""
    return make_model(
        name="声明终态仍有动作",
        participants=("P1",),
        local_states={"P1": ("S", "T")},
        actions=(
            Action("toT", ("P1",), {"P1": "S"}, {"P1": "T"}),
            Action("leave", ("P1",), {"P1": "T"}, {"P1": "S"}),
        ),
        initial_states=(("S",),),
        terminal_states=(("T",),),
    )


def long_chain_model(length=30):
    """长链 S0 -> S1 -> ... -> Sn(死锁)，用于预算未决演示。"""
    states = tuple(f"S{i}" for i in range(length + 1))
    actions = tuple(
        Action(f"step{i}", ("P1",), {"P1": states[i]}, {"P1": states[i + 1]})
        for i in range(length))
    return make_model(
        name=f"长链隐藏死锁({length})",
        participants=("P1",),
        local_states={"P1": states},
        actions=actions,
        initial_states=((states[0],),),
        terminal_states=(),
    )


def main():
    demos = [
        (resource_deadlock_model(), None),
        (no_provider_model(), None),
        (livelock_model(), None),
        (terminal_violation_model(), None),
        (long_chain_model(), 5),
        (long_chain_model(), None),
    ]
    for i, (model, budget) in enumerate(demos, 1):
        report = analyze(model, state_budget=budget)
        print(f"===== 样例 {i} =====")
        print(format_report(report))
        print()


if __name__ == "__main__":
    main()
