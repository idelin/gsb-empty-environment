"""symbreak：有限域 CSP 的对称破缺（字典序代表约束）工具包。

流程：
  1. 用 Problem/Variable/Constraint 建模（域必须有限）；
  2. 用户声明对称（可交换变量组或显式置换）；
  3. verify_permutation 先验证确为约束与目标的自同构，
     特殊角色变量不得被交换，域不同一律拒绝；
  4. declare_exchangeable / lex_leader_constraint 只使用已证对称
     生成字典序代表约束；
  5. solve_all / solve_one 求解（纯回溯，不用 SAT/SMT），
     声称有解时用独立检查器 check_solution 复核原约束；
  6. verify_orbit_coverage 在小问题上穷举验证不丢等价类，
     recover_solutions / find_recovery 负责恢复被删的对称解。
"""
from .csp import Constraint, Problem, Variable
from .group import group_closure
from .lexleader import ExchangeableResult, declare_exchangeable, lex_leader_constraint
from .orbits import (
    OrbitReport,
    find_recovery,
    orbit,
    recover_solutions,
    verify_orbit_coverage,
)
from .permutation import Permutation
from .solver import check_solution, solve_all, solve_one
from .verify import BudgetExceeded, Counterexample, Verification, verify_permutation

__all__ = [
    "BudgetExceeded",
    "Constraint",
    "Counterexample",
    "ExchangeableResult",
    "OrbitReport",
    "Permutation",
    "Problem",
    "Variable",
    "Verification",
    "check_solution",
    "declare_exchangeable",
    "find_recovery",
    "group_closure",
    "lex_leader_constraint",
    "orbit",
    "recover_solutions",
    "solve_all",
    "solve_one",
    "verify_orbit_coverage",
    "verify_permutation",
]
