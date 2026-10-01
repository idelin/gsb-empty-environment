"""运行样例：压缩、同步失效、正表/禁表、独立检查。"""

from table_constraints import (
    Engine, Infeasible, NegativeTable, PositiveTable, check_solution, solve,
)


def demo_compression():
    print("=== 样例 1：共享前缀压缩 + 重复元组去重 ===")
    raw = [(0, 0, 0), (0, 0, 1), (0, 1, 0), (1, 0, 0),
           (0, 0, 0), (1, 0, 0), (2, 2, 2), (0, 0, 1)]  # 含 3 个重复
    c = PositiveTable(["x", "y", "z"], raw)
    print(f"输入元组 {len(raw)} 条，重复 {c.table.duplicates} 条，"
          f"去重后 {len(c.table)} 条")
    print(f"trie 节点数 {c.table.node_count()}，"
          f"不压缩需 {c.table.naive_slots()} 个存储槽")
    sol = solve({"x": {0, 1, 2}, "y": {0, 1, 2}, "z": {0, 1, 2}}, [c])
    print(f"求解结果 {sol}，独立检查器验证: {check_solution(sol, [c])}")
    print()


def demo_support_invalidation():
    print("=== 样例 2：域删值后支持同步失效（级联剪枝） ===")
    c = PositiveTable(["x", "y"], [(0, 0), (0, 1), (1, 1), (2, 2)])
    e = Engine({"x": {0, 1, 2}, "y": {0, 1, 2}}, [c])
    print(f"删值前：存活元组 {c.live_tuple_count()}，"
          f"support(x=0)={c.supports[('x', 0)]}，support(y=0)={c.supports[('y', 0)]}")
    e.remove_value("x", 0)
    print(f"删除 x=0 后：存活元组 {c.live_tuple_count()}，"
          f"support(y=0)={c.supports.get(('y', 0), 0)}")
    print(f"y 的当前域 {sorted(e.domains['y'])}  <- y=0 因失去全部支持被级联剪枝")
    print()


def demo_negative_table():
    print("=== 样例 3：禁表传播 ===")
    c = NegativeTable(["a", "b", "d"], [(1, 1, 1)])
    e = Engine({"a": {0, 1}, "b": {0, 1}, "d": {0, 1}}, [c])
    e.remove_value("a", 0)  # a=1
    e.remove_value("b", 0)  # b=1
    print(f"a=1, b=1 后 d 的域 {sorted(e.domains['d'])}  <- d=1 被禁表剪枝")
    bad = NegativeTable(["p", "q"], [(1, 1)])
    print(f"禁表 (1,1) 且 p=q=1 时求解: "
          f"{solve({'p': {1}, 'q': {1}}, [bad])}  <- 冲突，不可行")
    print()


def demo_edge_cases():
    print("=== 样例 4：边界情形 ===")
    empty_pos = PositiveTable(["x"], [])
    print(f"正表为空 -> 求解结果 {solve({'x': {0, 1}}, [empty_pos])}（不可行）")
    empty_neg = NegativeTable(["x"], [])
    sol = solve({"x": {0, 1, 2}}, [empty_neg])
    print(f"禁表为空 -> 无限制，求解结果 {sol}，"
          f"独立检查: {check_solution(sol, [empty_neg])}")
    c = PositiveTable(["x"], [(0,)])
    e = Engine({"x": {0}}, [c])
    try:
        e.remove_value("x", 0)
    except Infeasible as exc:
        print(f"删除域中最后一个值 -> Infeasible: {exc}")
    print()


def demo_checker():
    print("=== 样例 5：独立检查器对照原始约束 ===")
    cons = [PositiveTable(["x", "y"], [(0, 1), (1, 0)]),
            NegativeTable(["x", "y"], [(1, 1)])]
    sol = solve({"x": {0, 1}, "y": {0, 1}}, cons)
    print(f"解 {sol} 通过检查: {check_solution(sol, cons)}")
    forged = {"x": 1, "y": 1}
    print(f"伪造解 {forged} 通过检查: {check_solution(forged, cons)}  <- 命中禁表被拒")


if __name__ == "__main__":
    demo_compression()
    demo_support_invalidation()
    demo_negative_table()
    demo_edge_cases()
    demo_checker()
