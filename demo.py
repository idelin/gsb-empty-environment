"""运行样例：python3.11 demo.py"""
from fractions import Fraction

from resource_alloc import Task, peak_overlap, solve


def show(title, result):
    print(f"== {title} ==")
    print(f"  状态: {result.status.value}")
    if result.assignment is not None:
        print(f"  分配: {result.assignment}")
        print(f"  资源数: {result.num_resources}")
    print(f"  下界: {result.lower_bound}  上界: {result.upper_bound}"
          f"  节点: {result.nodes}/{result.budget}")
    if result.detail:
        print(f"  说明: {result.detail}")
    print()


# 1. 半开区间：[0,5) 与 [5,8) 可共用同一资源
tasks = [Task("a", 0, 5), Task("b", 5, 8)]
show("1. 半开区间端点相接可共享", solve(tasks))

# 2. 无额外约束：资源数 == 峰值重叠
tasks = [Task("a", 0, 4), Task("b", 1, 6), Task("c", 2, 3),
         Task("d", 3, 8), Task("e", 5, 7), Task("f", 6, 9), Task("g", 8, 10)]
print(f"   （峰值重叠 = {peak_overlap(tasks)}）")
show("2. 无额外约束：资源数等于峰值重叠", solve(tasks))

# 3. 指定资源冲突：提前发现，不进入搜索
tasks = [Task("a", 0, 5, pinned=0), Task("b", 2, 6, pinned=0)]
show("3. 指定资源冲突（提前发现）", solve(tasks))

# 4. 不可同资源：互不重叠的任务被迫使用不同资源
tasks = [Task("a", 0, 2), Task("b", 3, 5)]
show("4. 不可同资源迫使增加资源", solve(tasks, cannot_share=[("a", "b")]))

# 5. 有理数端点
tasks = [Task("a", 0, Fraction(5, 2)), Task("b", Fraction(5, 2), 4),
         Task("c", 1, Fraction(3, 2))]
show("5. 有理数（Fraction）端点", solve(tasks))

# 6. 预算分级：同一五元环实例在 未决 / 可行 / 最优 三档间切换
tasks = [Task(f"t{i}", i, i + 1) for i in range(5)]
edges = [(f"t{i}", f"t{(i + 1) % 5}") for i in range(5)]
full = solve(tasks, cannot_share=edges, budget=10**6)
show("6a. 预算未决（budget=4）", solve(tasks, cannot_share=edges, budget=4))
show(f"6b. 可行未证最优（budget={full.nodes - 1}）",
     solve(tasks, cannot_share=edges, budget=full.nodes - 1))
show("6c. 最优（budget 充足）", full)
