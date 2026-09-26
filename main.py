"""运行样例：覆盖 最优 / 不可行 / 预算未决（可行次优与未决）四种情形。"""

from fractions import Fraction

from batching import Instance, Job, Status, solve, verify


def show(title, instance, sol):
    print(f"=== {title} ===")
    print(f"状态: {sol.status.value} | {sol.message} | 转移次数: {sol.transitions_used}")
    if sol.batches:
        print(f"批数: {sol.num_batches} | 总成本: {sol.total_cost} "
              f"(启动成本 {instance.startup_cost}/批 x {sol.num_batches})")
        clock = Fraction(0)
        for k, b in enumerate(sol.batches, 1):
            jobs = [instance.jobs[i] for i in b]
            load = sum((j.resource for j in jobs), Fraction(0))
            proc = sum((j.proc_time for j in jobs), Fraction(0))
            clock += proc
            ids = " ".join(j.job_id for j in jobs)
            print(f"  批{k} [{jobs[0].job_type}] 作业: {ids} | 资源 {load}/{instance.capacity}"
              f" | 完成时刻 {clock}")
        ok, errors = verify(instance, sol.batches)
        print(f"校验: {'通过' if ok else errors}")
    print()


def main():
    # 场景 A：混合相容类型 + 有理数数据，精确求解可证最优
    inst_a = Instance(
        jobs=[
            Job("g1", "gpu", Fraction(4), Fraction(3), Fraction(9)),
            Job("g2", "gpu", Fraction(6), Fraction(2), Fraction(12)),
            Job("g3", "gpu", Fraction(3), Fraction(2), Fraction(20)),
            Job("g4", "gpu", Fraction(7), Fraction(4), Fraction(20)),
            Job("i1", "io", Fraction(5, 2), Fraction(1, 2), Fraction(6)),
            Job("i2", "io", Fraction(7, 2), Fraction(3, 2), Fraction(15)),
            Job("i3", "io", Fraction(4), Fraction(1), Fraction(30)),
        ],
        capacity=10,
        startup_cost=25,
    )
    show("场景 A：混合类型 + 有理数（精确 DP）", inst_a, solve(inst_a))

    # 场景 B1：单作业超容量，直接拒绝
    inst_b1 = Instance(
        jobs=[Job("big", "gpu", 11, 2, 10), Job("ok", "gpu", 5, 2, 10)],
        capacity=10,
        startup_cost=25,
    )
    show("场景 B1：单作业超容量（拒绝）", inst_b1, solve(inst_b1))

    # 场景 B2：截止约束过紧，已证不可行
    inst_b2 = Instance(
        jobs=[
            Job("a", "gpu", 6, 4, 3),   # 资源 6 与 5 不能同批，单独成批也赶不上截止 3
            Job("b", "gpu", 5, 4, 100),
        ],
        capacity=10,
        startup_cost=25,
    )
    show("场景 B2：截止约束不可满足（已证不可行）", inst_b2, solve(inst_b2))

    # 场景 C1：预算耗尽，但贪心找到可行解（未证最优）
    inst_c1 = Instance(
        jobs=[Job(f"j{k}", "gpu", 6, 1, 100) for k in range(3)],
        capacity=10,
        startup_cost=25,
    )
    show("场景 C1：预算耗尽 + 贪心可行（FEASIBLE，未证最优）",
         inst_c1, solve(inst_c1, transition_budget=5))

    # 场景 C2：预算耗尽，贪心也失败（可行性未决）；同例给足预算则可证最优
    inst_c2 = Instance(
        jobs=[
            Job("x", "gpu", 6, 3, 3),    # 必须与 y 分批：同批完成时刻 13 会错过截止 3
            Job("y", "gpu", 4, 10, 100),
        ],
        capacity=10,
        startup_cost=25,
    )
    show("场景 C2：预算耗尽 + 贪心失败（UNDECIDED）",
         inst_c2, solve(inst_c2, transition_budget=4))
    show("场景 C2 对照：预算充足（OPTIMAL）", inst_c2, solve(inst_c2))


if __name__ == "__main__":
    main()
