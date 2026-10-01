"""运行样例：有限原料规格 + 切缝 + 有限库存 + 余料跨批次复用。

用法: python3.11 demo.py
"""

from cuttingstock import (
    Demand,
    Inventory,
    Status,
    StockSpec,
    run_batches,
    verify_plan,
)

KERF = 3           # 锯缝宽度
MIN_REMNANT = 400  # 小于该长度的余料视为废料，不回库


def show_batch(name, demands, res, specs_before):
    print(f"== {name} ==")
    print(f"状态: {res.status.value}"
          + ("（预算耗尽，未证最优）" if res.budget_exhausted and res.status is not Status.INFEASIBLE else ""))
    if not res.bars:
        print("  无可行切割清单")
    for i, bar in enumerate(res.bars, 1):
        segs = " + ".join(str(p) for p in bar.pieces)
        print(f"  第{i}根 原料{bar.stock_length}(成本{bar.cost}): "
              f"{segs} | 切{bar.cuts}次 | 余料 {bar.leftover}")
    if res.waste is not None:
        print(f"  消耗总长 {res.consumed}，毛损耗 {res.waste}，"
              f"回库余料 {list(res.reusable_remnants)}，净损耗 {res.net_waste}，成本 {res.cost}")
    print(f"  损耗下界 {res.lower_bound_waste}，搜索节点 {res.nodes}")
    errors = verify_plan(res.bars, demands, specs_before, kerf=KERF)
    print(f"  独立守恒校验: {'通过' if not errors else errors}")


def main():
    inventory = Inventory([
        StockSpec.of(6000, cost=12, count=10),  # 新料 6000，限 10 根
        StockSpec.of(3000, cost=7, count=5),    # 新料 3000，限 5 根
        StockSpec.of(1800, cost=0, count=1),    # 期初余料 1800，1 根
    ])
    batches = [
        ("批次1", [Demand.of(1200, 4), Demand.of(900, 3), Demand.of(1500, 2)]),
        ("批次2", [Demand.of(1100, 2), Demand.of(800, 2)]),
    ]
    print(f"切缝 {KERF}，最小可复用余料 {MIN_REMNANT}")
    print(f"初始库存: {inventory.specs()}")
    print()

    # 逐批次处理：每批求解前快照库存用于独立校验；余料即时回库供下一批使用
    for name, demands in batches:
        specs_before = inventory.specs()
        camp = run_batches([(name, demands)], inventory,
                           kerf=KERF, min_remnant=MIN_REMNANT, max_nodes=200_000)
        show_batch(name, demands, camp.batches[0].result, specs_before)
        print()
    print(f"最终库存: {inventory.specs()}")
    print()

    # 联合模式：两批需求合并为一次精确求解作对照
    inv2 = Inventory([
        StockSpec.of(6000, cost=12, count=10),
        StockSpec.of(3000, cost=7, count=5),
        StockSpec.of(1800, cost=0, count=1),
    ])
    specs_before_joint = inv2.specs()
    camp2 = run_batches(batches, inv2, kerf=KERF, min_remnant=MIN_REMNANT,
                        max_nodes=400_000, joint=True)
    res = camp2.batches[0].result
    merged = [d for _, ds in batches for d in ds]
    print("== 联合模式（两批合并一次求解）==")
    show_batch("<joint>", merged, res, specs_before_joint)


if __name__ == "__main__":
    main()
