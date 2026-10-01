"""独立检验器：全赋值枚举，不复用传播器的任何逻辑。

用途：
  * evaluate：按语义规则直接判定一个完整赋值是否满足原约束；
  * solutions / find_witness：枚举全部解 / 找一个解（用于验证"有解"断言）；
  * supported_values：枚举所有解，汇总每个变量在所有解中出现过的值；
  * verify_pruned_domains：断言传播后的保留域恰好等于所有支持值。
"""
from __future__ import annotations

from itertools import product


def evaluate(constraint, assignment) -> bool:
    """直接按定义判定：首个不同位置决定大小；全等则由长度规则裁决。"""
    left, right, strict = constraint.left, constraint.right, constraint.strict
    n, m = len(left), len(right)
    for i in range(min(n, m)):
        a, b = assignment[left[i]], assignment[right[i]]
        if a != b:
            return a < b
    return n < m if strict else n <= m


def solutions(constraint, domains):
    """枚举给定域下满足约束的全部完整赋值（共享变量天然一致）。"""
    variables = constraint.variables
    for values in product(*(sorted(domains[v]) for v in variables)):
        assignment = dict(zip(variables, values))
        if evaluate(constraint, assignment):
            yield assignment


def find_witness(constraint, domains):
    """返回一个满足原约束的完整赋值；无解返回 None。"""
    return next(solutions(constraint, domains), None)


def supported_values(constraint, domains) -> dict:
    """每个变量在所有解中取到的值的集合。"""
    supported = {v: set() for v in constraint.variables}
    for assignment in solutions(constraint, domains):
        for var in constraint.variables:
            supported[var].add(assignment[var])
    return supported


def verify_pruned_domains(constraint, original_domains, pruned_domains):
    """验证保留域恰好包含所有支持值。

    返回 (ok, supported)：ok 为真当且仅当对每个变量，
    传播后的域与枚举得到的支持值集合完全相等。
    """
    supported = supported_values(constraint, original_domains)
    ok = all(set(pruned_domains[v]) == supported[v] for v in constraint.variables)
    return ok, supported
