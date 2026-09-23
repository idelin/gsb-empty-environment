"""运行样例：有理矩阵高斯消元（精确有理运算，不转浮点）。

用法: python3 demo.py
"""

from fractions import Fraction

from rational_gauss import (
    Solution,
    format_matrix,
    format_vector,
    matvec,
    print_trace,
    rank,
    solve,
)


def show_solution(sol: Solution) -> None:
    print(f"rank(A) = {sol.rank_a}, rank([A|b]) = {sol.rank_augmented}, "
          f"变量数 n = {sol.num_variables}")
    if not sol.consistent:
        print("结论: 方程组不相容（无解）。不相容证据:")
        for line in sol.inconsistency_evidence:
            print(f"  * {line}")
        return
    kind = "唯一解" if sol.unique else f"无穷多解（自由变量 {len(sol.free_variables)} 个）"
    print(f"结论: 方程组相容，{kind}")
    print(f"  主元变量: {[f'x{i + 1}' for i in sol.pivot_variables]}")
    print(f"  自由变量: {[f'x{i + 1}' for i in sol.free_variables] or '无'}")
    print(f"  特解 x_p = {format_vector(sol.particular)}")
    for k, vec in enumerate(sol.nullspace_basis, 1):
        print(f"  零空间基 v{k} = {format_vector(vec)}")
    if sol.nullspace_basis:
        terms = " + ".join(
            f"t{k}*v{k}" for k in range(1, len(sol.nullspace_basis) + 1)
        )
        print(f"  通解: x = x_p + {terms}（t_k 为任意有理数）")


def verify(A, b, sol: Solution) -> None:
    """用精确矩阵乘法验证特解与零空间基。"""
    if not sol.consistent:
        return
    assert matvec(A, sol.particular) == [Fraction(v) for v in b]
    for vec in sol.nullspace_basis:
        assert all(v == 0 for v in matvec(A, vec))
    print("  验证: A*x_p = b 且 A*v_k = 0（精确成立）")


def example(title: str, A, b, *, column_pivoting: bool = False) -> None:
    print("=" * 72)
    print(title)
    print("=" * 72)
    print("系数矩阵 A:")
    print(format_matrix(A))
    print(f"右端 b = {format_vector(b)}")
    sol = solve(A, b, column_pivoting=column_pivoting)
    print()
    print_trace(sol.operations)
    print()
    print("增广矩阵的 RREF" + ("（列已置换，结果已还原原变量顺序）"
                              if column_pivoting else "") + ":")
    print(format_matrix(sol.rref_augmented))
    print()
    show_solution(sol)
    verify(A, b, sol)
    print()


def main() -> None:
    example(
        "样例 1：唯一解（整数系数，含分数中间过程）",
        [[2, 1, -1], [-3, -1, 2], [-2, 1, 2]],
        [8, -11, -3],
    )
    example(
        "样例 2：欠定系统（特解 + 零空间基）",
        [[1, 1, 1], [1, -1, 1]],
        [6, 2],
    )
    example(
        "样例 3：不相容系统（0 = 1 型证据）",
        [[1, 1], [2, 2], [1, -1]],
        [1, 5, 0],
    )
    example(
        "样例 4：列交换（变量重命名后还原原始顺序）",
        [[0, 0, 2], [0, 3, 1]],
        [4, 5],
        column_pivoting=True,
    )
    example(
        "样例 5：分数系数（1/3 级精确，绝不出现 0.333...）",
        [["1/3", "1/2"], ["1/4", "-1/5"]],
        ["5/6", "1/20"],
    )
    print("矩阵求秩示例: rank =", rank([[1, 2, 3], [4, 5, 6], [7, 8, 9]]))


if __name__ == "__main__":
    main()
