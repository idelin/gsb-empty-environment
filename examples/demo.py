"""可运行样例：被动查询 + 事件驱动模拟（含计划动作与碰撞后轨迹替换）。

运行：python3 examples/demo.py
"""

import os
import sys
from fractions import Fraction as Fr

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from stcollide import World


def build_world():
    w = World(dim=2, t0=0, t1=12, cell_size=2, event_budget=100)
    w.add_object("A", (0, 0), (1, 1), (1, 0))        # 向右
    w.add_object("B", (6, 0), (7, 1), (-1, 0))       # 向左（将被计划动作转向）
    w.add_object("C", (2, 4), (3, 5), (0, -1))       # 向下
    w.add_object("D", (0, 3), (1, 4), (1, 0))        # 向右，将与 C 相撞
    return w


def main():
    # ---------- 1. 被动查询：全部碰撞区间与首碰撞 ----------
    w = build_world()
    print("== 被动查询（当前轨迹，不施加响应）==")
    for pair, ivs in sorted(w.collision_intervals().items()):
        for iv in ivs:
            print(f"  {pair}: [{iv.start}, {iv.end}] kind={iv.kind}")
    t, pair, kind = w.first_collision()
    print(f"  首碰撞: t={t} pair={pair} kind={kind}")
    print(f"  t=3 快照: {w.snapshot(3)}")

    # ---------- 2. 事件驱动模拟 ----------
    w = build_world()
    # 计划动作：t=1 时 B 掉头向上 → A-B 原预测事件（t=5/2）失效
    w.schedule(1, lambda world: world.set_velocity("B", 1, (0, 1)))

    def respond(world, records):
        for rec in records:  # 碰撞后轨迹替换：交换双方速度
            va = world.bodies[rec.a].traj.velocity(rec.time)
            vb = world.bodies[rec.b].traj.velocity(rec.time)
            world.set_velocity(rec.a, rec.time, vb)
            world.set_velocity(rec.b, rec.time, va)

    result = w.simulate(respond=respond)
    print("\n== 事件驱动模拟（交换速度响应 + t=1 计划转向）==")
    print(f"  状态: {result.status}，失效旧预测: {result.stale_discarded} 个")
    for rec in result.events:
        print(f"  t={rec.time}: {rec.a}~{rec.b} 前态 {rec.box_a} vs {rec.box_b}")
    print(f"  t=6 快照: {w.snapshot(6)}")

    # ---------- 3. 退化情形：瞬时相切只报告、不产生事件 ----------
    w = World(dim=2, t0=0, t1=4)
    w.add_object("P", (0, 0), (1, 1), (0, 0))
    w.add_object("Q", (2, 0), (3, 1), (-1, 1))  # 在 t=1 掠过 P 的角点
    print("\n== 瞬时相切（显式规则：报告 touch，不触发事件）==")
    print(f"  区间: {w.collision_intervals()}")
    print(f"  首碰撞: {w.first_collision()}")
    print(f"  模拟事件数: {len(w.simulate().events)}")


if __name__ == "__main__":
    main()
