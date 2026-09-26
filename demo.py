"""运行样例：python3.11 demo.py"""

from fractions import Fraction

from subset_select import Item, Status, solve


def show(title, result):
    print(f"== {title} ==")
    print(f"状态: {result.status.value}（可行: {result.feasible}, 已证明最优: {result.proven_optimal}）")
    print(f"总收益: {result.total_profit}  资源占用: {result.used1}/{result.capacity1}, "
          f"{result.used2}/{result.capacity2}  访问节点: {result.nodes_visited}")
    print("选中:")
    for it in result.selected:
        print(f"  + {it.name}: 收益={it.profit} 消耗=({it.cost1}, {it.cost2}) 组={it.group}")
    print("未选及原因:")
    for info in result.unselected:
        print(f"  - {info.item.name}: [{info.reason.value}] {info.detail}")
    print()


def main():
    items = [
        Item("alpha", profit=10, cost1=4, cost2=3),
        Item("beta", profit=9, cost1=5, cost2=2, group="g1"),
        Item("gamma", profit=8, cost1=3, cost2=4, group="g1"),
        Item("delta", profit=Fraction(15, 2), cost1=Fraction(5, 2), cost2=2),
        Item("epsilon", profit=6, cost1=0, cost2=0),   # 零成本正收益，可选
        Item("zeta", profit=-3, cost1=1, cost2=1),     # 负收益，可不选
        Item("eta", profit=100, cost1=50, cost2=1),    # 单件超容量
        Item("theta", profit=0, cost1=1, cost2=1),     # 零收益
    ]
    show("样例一：整数+有理数混合，证明最优", solve(items, capacity1=10, capacity2=8))

    # 样例二：合成数据 + 节点预算限制，演示“预算未决”。
    synth = [
        Item(f"s{i:02d}", profit=(i * 37) % 23 + 1,
             cost1=(i * 11) % 9 + 1, cost2=(i * 7) % 8 + 1,
             group=f"g{i % 3}" if i % 4 == 0 else None)
        for i in range(24)
    ]
    show("样例二：24 件合成物品，节点预算 50（预算未决）",
         solve(synth, capacity1=40, capacity2=36, max_nodes=50))
    show("样例三：同一问题不限预算，证明最优",
         solve(synth, capacity1=40, capacity2=36))


if __name__ == "__main__":
    main()
