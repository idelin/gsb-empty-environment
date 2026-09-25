"""可运行样例：python3.11 demo.py"""

from world import EventBudgetExceeded, World


def scenario_turn_cancels_prediction():
    print("== 场景 1：A 与 B 预测 t=5 碰撞，t=3 时 A 转向，旧事件取消 ==")
    w = World()
    w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
    w.add_object("B", (11, 0), (1, 1), vel=(-1, 0))
    t, pairs = w.first_collision(0, 10)
    print(f"  转向前首碰撞预测: t={t}, pairs={pairs}")
    w.change_velocity("A", (0, 1), 3)
    print(f"  t=3 A 转向后首碰撞预测: {w.first_collision(0, 10)}")
    print(f"  模拟事件: {w.simulate(0, 10)}")


def scenario_three_body_batch():
    print("== 场景 2：三物体同刻碰撞，同批处理 ==")
    w = World()
    w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
    w.add_object("B", (2, 0), (1, 1))
    w.add_object("C", (4, 0), (1, 1), vel=(-1, 0))

    def respond(world, t, collisions):
        for c in collisions:
            print(f"  t={t} 碰撞 {c.pair}: 前态位置 {c.pos_a} / {c.pos_b}")
        for c in collisions:
            for oid in c.pair:
                world.change_velocity(oid, (0, 0), t)

    log = w.simulate(0, 10, respond=respond)
    print(f"  批次数量: {len(log)}（两对在同一批）")


def scenario_all_intervals():
    print("== 场景 3：时间窗内全部碰撞区间（折返两次穿过） ==")
    w = World()
    w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
    w.change_velocity("A", (-1, 0), 10)
    w.add_object("B", (3, 0), (1, 1))
    for pair, ivs in w.collision_intervals(0, 30).items():
        print(f"  {pair}: {ivs}")


def scenario_zero_time_loop_budget():
    print("== 场景 4：零时间环由事件预算终止 ==")
    w = World()
    w.add_object("A", (0, 0), (2, 2))
    w.add_object("B", (1, 0), (2, 2))

    def respond(world, t, collisions):
        for c in collisions:
            world.change_velocity(c.pair[0], (0, 0), t)

    try:
        w.simulate(0, 10, respond=respond, max_events=8)
    except EventBudgetExceeded as exc:
        print(f"  捕获 EventBudgetExceeded: {exc}")


if __name__ == "__main__":
    scenario_turn_cancels_prediction()
    scenario_three_body_batch()
    scenario_all_intervals()
    scenario_zero_time_loop_budget()
