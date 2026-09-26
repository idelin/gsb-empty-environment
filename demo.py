"""运行样例：python3.11 demo.py"""
from knapsack2d import Item, Status, solve


def show(title, result):
    print(f"== {title} ==")
    print(f"状态: {result.status.value}")
    if result.status is Status.UNDECIDED:
        print("预算不足，无法给出可信结论\n")
        return
    used1, used2 = result.used
    cap1, cap2 = result.capacity
    print(f"总收益: {result.total_profit}  已用容量: ({used1}, {used2}) / ({cap1}, {cap2})")
    print(f"已选: {', '.join(result.selected) if result.selected else '(空)'}")
    print("未选原因:")
    for name, reason in result.unselected_reasons.items():
        print(f"  - {name}: {reason}")
    print(f"DP 状态数: {result.states_explored}（预算 {result.state_budget}）\n")


def main():
    items = [
        Item("A", profit=6, weight1=4, weight2=3),
        Item("B", profit=7, weight1=5, weight2=5),
        Item("C", profit=5, weight1=3, weight2=4),
        Item("D", profit=4, weight1="1/2", weight2=2),      # 有理数权重
        Item("E", profit=3, weight1=0, weight2=0),          # 零成本正收益
        Item("F", profit=-2, weight1=1, weight2=1),         # 负收益
        Item("G1", profit=8, weight1=6, weight2=4, group="g"),
        Item("G2", profit=9, weight1=7, weight2=5, group="g"),  # 与 G1 互斥
        Item("M1", profit=1, weight1=0, weight2=0, group="m"),
        Item("M2", profit=2, weight1=0, weight2=0, group="m"),  # 零成本，与 M1 互斥
        Item("H", profit=100, weight1=11, weight2=1),       # 只满足资源2，超资源1
    ]
    show("样例 1：容量 (10, 10)，精确求解", solve(items, 10, 10))

    many = [
        Item(f"X{i:02d}", profit=(i * 7) % 13 + 1, weight1=(i * 3) % 7 + 1, weight2=(i * 5) % 6 + 1)
        for i in range(24)
    ]
    show("样例 2：24 件物品但状态预算仅 50（最优性未决）",
         solve(many, 40, 30, state_budget=50))
    show("样例 3：状态预算为 0（无法决断）", solve(items, 10, 10, state_budget=0))


if __name__ == "__main__":
    main()
