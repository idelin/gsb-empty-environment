"""运行样例：4 皇后问题 + 嵌套假设 / 冲突回退 / 取消无污染演示。"""

from csp import CSP, Constraint, Solver, check_solution


def build_queens(n=4):
    domains = {f"q{r}": set(range(n)) for r in range(n)}  # q[r] = 第 r 行皇后所在列
    constraints = []
    for i in range(n):
        for j in range(i + 1, n):
            constraints.append(
                Constraint(
                    (f"q{i}", f"q{j}"),
                    lambda a, b, d=j - i: a != b and abs(a - b) != d,
                    name=f"attack_{i}_{j}",
                )
            )
    return CSP(domains, constraints)


def main():
    csp = build_queens(4)

    print("== 1. 直接求解，并用独立检查器验证 ==")
    solver = Solver(csp)
    solution = solver.solve()
    print("解:", solution)
    print("独立检查器:", check_solution(csp, solution))

    print("\n== 2. 嵌套假设：内层冲突退出，外层仍可解 ==")
    s2 = Solver(csp)
    print("push q0=1 (层1):", s2.push({"q0": 1}))
    print("push q1=1 (层2):", s2.push({"q1": 1}), "<- 同列冲突，自动弹回层1")
    print("当前层号:", s2.layer)
    print("push q1=3 (层2):", s2.push({"q1": 3}))
    print("继续求解:", s2.solve())

    print("\n== 3. 取消不留下缩小域污染 ==")
    s3 = Solver(csp)
    snapshot = {v: set(d) for v, d in s3.domains.items()}
    s3.push({"q0": 1})
    s3.push({"q1": 3})
    s3.pop()
    s3.pop()
    print("回滚后域与快照一致:", s3.domains == snapshot, "| 层号:", s3.layer)

    print("\n== 4. 重复假设计层次 ==")
    s4 = Solver(csp)
    s4.push({"q0": 1})
    s4.push({"q0": 1})  # 同一假设再压一层
    print("压两次同值假设后层号:", s4.layer)
    s4.pop()
    print("弹一层后 q0 域:", sorted(s4.domains["q0"]), "层号:", s4.layer)
    s4.pop()
    print("再弹一层后 q0 域:", sorted(s4.domains["q0"]), "层号:", s4.layer)


if __name__ == "__main__":
    main()
