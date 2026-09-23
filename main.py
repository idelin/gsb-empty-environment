"""命令行入口：无参数时运行内置样例，否则求解命令行给出的方程。

用法：
    python3.11 main.py                      # 运行全部内置样例
    python3.11 main.py "x = y + 1" "y = x"  # 求解指定方程组
"""
import sys

from eliminator import solve_text

SAMPLES = [
    ("样例1：自由族（相容，非矛盾）", ["x = y + 1", "y = x - 1"]),
    ("样例2：矛盾方程组", ["x = y + 1", "y = x"]),
    ("样例3：除以可能为 0 的项（分支）", ["x / (y - 1) = 2", "y = z + 3"]),
    ("样例4：无环代入链", ["x = y + 1", "y = z + 2"]),
    ("样例5：有理数精确计算", ["2*x = 3*y", "y = 1/4"]),
]


def print_solution(solution):
    for i, step in enumerate(solution.log, 1):
        print(f"  {i:>2}. [{step.kind}] {step.text}")
        if step.condition:
            print(f"      条件: {step.condition}")
        if step.domain:
            print(f"      适用域: {step.domain}")
    if solution.status == "consistent":
        status = "相容（自由族，非矛盾）" if solution.free_vars else "相容（唯一解）"
    elif solution.status == "contradiction":
        status = "矛盾（无解）"
    else:
        status = "无可行解（非零条件不成立）"
    print(f"  状态: {status}")
    if solution.status == "consistent":
        for var in sorted(solution.assignments):
            print(f"    {var} = {solution.assignments[var]}")
        if solution.free_vars:
            print(f"    自由变量: {', '.join(solution.free_vars)}")
        for cond in solution.conditions:
            print(f"    保留条件: {cond} != 0")


def run(title, texts):
    print(f"=== {title} ===")
    for text in texts:
        print(f"  {text}")
    for branch in solve_text(texts):
        print(f"-- {branch.name} --")
        if branch.assumptions:
            print(f"  假设: {' 且 '.join(branch.assumptions)}")
        if branch.solution is None:
            print(f"  {branch.note}")
        else:
            print_solution(branch.solution)
    print()


def main(argv):
    if len(argv) > 1:
        run("输入方程组", argv[1:])
    else:
        for title, texts in SAMPLES:
            run(title, texts)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
