"""运行样例：编码、投影等价验证、穷举辅助赋值、传播能力对比、增量收紧与撤回。

用法: python3.11 demo.py
"""

from cnf import VarManager, brute_force_extensions, check_assignment, dpll
from encoders import AT_MOST, EXACTLY, Encoder, tighten_network
from verify import (
    check_projection_equivalence,
    direct_check,
    direct_count,
    extend_assignment,
    propagation_metrics,
    solve_and_verify,
)


def show_assignment(vm, assignment, variables):
    return "(" + ", ".join(
        f"{vm.name(v)}={int(assignment.get(v, False))}" for v in variables
    ) + ")"


def demo_basic():
    print("=" * 64)
    print("示例 1：n=5, exactly 2 —— 两种编码、子句数、投影等价")
    print("=" * 64)
    for method in ("seq", "net"):
        vm = VarManager()
        enc = Encoder(vm)
        lits = [vm.new_var(f"x{i}") for i in range(1, 6)]
        c = enc.encode(EXACTLY, lits, 2, method=method)
        rep = check_projection_equivalence(c, lits)
        print(f"[{method}] 子句数={c.num_clauses:3d}  辅助变量数={c.num_aux:3d}  "
              f"投影等价: {'OK' if rep['equivalent'] else '失败'} "
              f"({rep['ok']}/{rep['total']} 个原赋值验证通过, "
              f"穷举辅助 {rep['brute_forced']} 次, DPLL {rep['dpll']} 次)")

        # 构造性扩展示例：原赋值 -> 辅助赋值 -> 独立检查
        sigma = {lits[0]: True, lits[2]: True}
        full = extend_assignment(c, sigma)
        ok = check_assignment(c.clauses, full)
        print(f"       扩展示例: {show_assignment(vm, sigma, lits)} "
              f"-> 含 {c.num_aux} 个辅助变量的完整赋值, 独立检查: {'通过' if ok else '失败'}")

        # 声称有解 -> 独立检查器验证原约束
        projection, verified = solve_and_verify(c, lits)
        count = direct_count(lits, projection)
        print(f"       求解结果: {show_assignment(vm, projection, lits)} "
              f"真值个数={count}, 独立检查原约束: {'通过' if verified else '失败'}")


def demo_brute_force_aux():
    print()
    print("=" * 64)
    print("示例 2：n=3, exactly 1 (net) —— 穷举全部辅助赋值")
    print("=" * 64)
    vm = VarManager()
    enc = Encoder(vm)
    lits = [vm.new_var(f"x{i}") for i in range(1, 4)]
    c = enc.encode(EXACTLY, lits, 1, method="net")
    print(f"子句数={c.num_clauses}, 辅助变量数={c.num_aux} "
          f"(共 {2 ** c.num_aux} 个辅助赋值被逐一穷举)")
    from itertools import product
    for values in product((False, True), repeat=3):
        base = dict(zip(lits, values))
        cnt = direct_count(lits, base)
        ext = brute_force_extensions(c.clauses, base, c.aux_vars)
        mark = "满足" if direct_check(EXACTLY, lits, 1, base) else "不满足"
        print(f"  计数={cnt} ({mark}): 使 CNF 成立的辅助赋值有 {ext} 个")


def demo_propagation():
    print()
    print("=" * 64)
    print("示例 3：传播能力对比（全部 3^n 个部分赋值上的单元传播）")
    print("=" * 64)
    print(f"{'约束':>8} {'n':>2} {'k':>2} {'方法':>4} {'子句':>5} {'辅助':>5} "
          f"{'冲突检出':>10} {'强制检出':>10} {'UP工作量':>8}")
    from encoders import AT_LEAST
    for sense, n, k in ((AT_MOST, 4, 1), (AT_MOST, 5, 2), (AT_LEAST, 5, 3),
                        (EXACTLY, 4, 2), (EXACTLY, 5, 2)):
        for method in ("seq", "net"):
            vm = VarManager()
            enc = Encoder(vm)
            lits = [vm.new_var(f"x{i}") for i in range(1, n + 1)]
            c = enc.encode(sense, lits, k, method=method)
            m = propagation_metrics(c, lits)
            conf = f"{m['up_conflict']}/{m['inconsistent']}"
            forced = f"{m['up_forced']}/{m['forced']}"
            print(f"{sense:>8} {n:>2} {k:>2} {method:>4} {c.num_clauses:>5} "
                  f"{c.num_aux:>5} {conf:>10} {forced:>10} {m['up_derived']:>8}")
    print("冲突检出 = UP 推出冲突 / 不可扩展部分赋值；强制检出 = UP 推出强制值 / 被强制次数；")
    print("UP工作量 = UP 在全部部分赋值上推导出的赋值总数。")
    print("结论：两种编码在三种约束上均达到完全传播（GAC）；差异在子句规模与 UP 工作量，")
    print("      且网络编码的一元输出支持 O(1) 原生收紧，顺序计数器需按假设机制重编码。")


def demo_incremental():
    print()
    print("=" * 64)
    print("示例 4：增量收紧与假设机制撤回（n=6）")
    print("=" * 64)
    vm = VarManager()
    enc = Encoder(vm)
    lits = [vm.new_var(f"x{i}") for i in range(1, 7)]

    # (a) 网络编码：一元输出单位子句原生收紧
    a1 = vm.new_var("a_net_le3")
    c1 = enc.encode(AT_MOST, lits, 3, method="net", activation=a1)
    before = c1.num_clauses
    tighten_network(c1, 1)
    rep = check_projection_equivalence(c1, lits)
    print(f"(a) 网络 <=3 --(+1 单位子句)--> <=1: 子句 {before} -> {c1.num_clauses}, "
          f"与直接计数 <=1 投影等价: {'OK' if rep['equivalent'] else '失败'}"
          f"({rep['ok']}/{rep['total']})")

    # (b) 顺序计数器：假设机制收紧（新激活变量重新编码）与撤回
    a2 = vm.new_var("a_seq_le2")
    a3 = vm.new_var("a_seq_le1")
    c2 = enc.encode(AT_MOST, lits, 2, method="seq", activation=a2)
    c3 = enc.encode(AT_MOST, lits, 1, method="seq", activation=a3)
    clauses = c2.clauses + c3.clauses
    print(f"(b) 顺序计数器 <=2 (激活 {vm.name(a2)}) 与 <=1 (激活 {vm.name(a3)}) 共存,"
          f" 共 {len(clauses)} 子句")

    def count_solutions(assumptions):
        total = 0
        from itertools import product
        for values in product((False, True), repeat=6):
            base = dict(zip(lits, values))
            base.update(assumptions)
            if dpll(clauses, base) is not None:
                total += 1
        return total

    tightened = count_solutions({a2: False, a3: True})   # 收紧到 <=1
    retracted = count_solutions({a2: True, a3: False})   # 撤回收紧, 恢复 <=2
    both_off = count_solutions({a2: False, a3: False})   # 全部撤回
    print(f"    假设 {vm.name(a2)}=0,{vm.name(a3)}=1 (收紧到 <=1): 解数={tightened} (期望 7)")
    print(f"    假设 {vm.name(a2)}=1,{vm.name(a3)}=0 (撤回, 恢复 <=2): 解数={retracted} (期望 22)")
    print(f"    假设 两者皆 0 (全部撤回): 解数={both_off} (期望 64)")


def demo_boundaries():
    print()
    print("=" * 64)
    print("示例 5：边界化简（k<0 / k>n / n=0）")
    print("=" * 64)
    cases = [
        ("at_most", 3, -1), ("at_most", 3, 0), ("at_most", 3, 3),
        ("at_least", 3, 0), ("at_least", 3, 4),
        ("exactly", 0, 0), ("exactly", 0, 1), ("exactly", 3, -1),
    ]
    for sense, n, k in cases:
        vm = VarManager()
        enc = Encoder(vm)
        lits = [vm.new_var(f"x{i}") for i in range(1, n + 1)]
        c = enc.encode(sense, lits, k, method="net")
        rep = check_projection_equivalence(c, lits)
        desc = {True: "恒真(空 CNF)", False: "恒假(空子句)", None: "常规编码"}[c.constant]
        print(f"  {sense:>8} n={n} k={k:>2}: {desc:<12} 子句数={c.num_clauses:<3} "
              f"投影等价: {'OK' if rep['equivalent'] else '失败'}")


if __name__ == "__main__":
    demo_basic()
    demo_brute_force_aux()
    demo_propagation()
    demo_incremental()
    demo_boundaries()
