"""等式消元器：代入消元、循环检测（线性代数兜底）、除零分支。

设计要点：
1. 每个方程先化为线性型 == 0，再解出某个变量作为定义。
2. 定义之间的依赖图若出现环（如 x=y+1 与 y=x-1），不做无限替换，
   而是把原方程组整体交给高斯消元（线性代数）判定相容性与自由变量。
3. 除以可能为 0 的项时在解析层分支：主分支假设分母非零，
   分母为零的分支因原式无定义而舍弃。
4. 每一步变换都记录适用域（Q 上的等量代换、除以非零有理数、初等行变换）。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from linear import solve_for, solve_linear_system, substitute
from parser import parse_equation


@dataclass
class Step:
    kind: str  # parse/define/substitute/cycle/gauss/branch/condition/result
    text: str
    condition: str = ""  # 本步依赖的假设
    domain: str = ""  # 本步变换的适用域


@dataclass
class Solution:
    status: str  # consistent / contradiction / infeasible
    assignments: dict = field(default_factory=dict)
    free_vars: list = field(default_factory=list)
    conditions: list = field(default_factory=list)
    log: list = field(default_factory=list)


@dataclass
class Branch:
    name: str
    assumptions: list
    solution: Solution | None = None  # None 表示该分支被舍弃
    note: str = ""


def _find_cycle(defs):
    """在定义依赖图上找环，返回环上的变量列表；无环返回 None。"""
    color = {}
    stack = []

    def dfs(node):
        color[node] = 1
        stack.append(node)
        for nxt in sorted(defs[node].variables() & defs.keys()):
            if color.get(nxt) == 1:
                return stack[stack.index(nxt):]
            if color.get(nxt, 0) == 0:
                found = dfs(nxt)
                if found:
                    return found
        stack.pop()
        color[node] = 2
        return None

    for var in sorted(defs):
        if color.get(var, 0) == 0:
            found = dfs(var)
            if found:
                return found
    return None


def _gauss_path(equations, log):
    """循环子系统交给线性代数（高斯消元）处理，避免无限替换。"""
    raw = []
    consistent, assignments, free_vars = solve_linear_system(equations, raw)
    for entry in raw:
        log.append(
            Step(
                "gauss",
                entry,
                domain="Q 上的初等行变换（行交换、乘非零数、倍加）不改变解集",
            )
        )
    return consistent, assignments, free_vars


def solve_system(equations, conditions=None, log=None):
    """对线性型方程组（每个 == 0）消元求解，返回 Solution。"""
    log = log if log is not None else []
    conditions = list(conditions or [])
    work = list(equations)
    defs = {}
    while work:
        form = work.pop(0)
        if form.is_zero():
            log.append(Step("define", "方程化简为 0 = 0（恒等式），丢弃", domain="Q"))
            continue
        if form.is_constant():
            log.append(
                Step(
                    "result",
                    f"方程化简为 {form.const} = 0，常数非零，矛盾",
                    domain="Q",
                )
            )
            return Solution("contradiction", log=log)
        undefined = [v for v in sorted(form.terms) if v not in defs]
        var = undefined[0] if undefined else sorted(form.terms)[0]
        coeff = form.terms[var]
        g = solve_for(form, var)
        log.append(
            Step(
                "define",
                f"解出 {var} = {g}",
                domain=f"两边同除以非零有理数 {coeff}（适用域：Q，恒合法）",
            )
        )
        if var in defs:
            residual = defs[var] - g
            log.append(
                Step(
                    "define",
                    f"{var} 已有定义 {var} = {defs[var]}，"
                    f"两定义相减得新方程 ({residual}) = 0，继续消元",
                    domain="等量相减（适用域：Q）",
                )
            )
            work.append(residual)
        else:
            defs[var] = g
    cycle = _find_cycle(defs)
    if cycle:
        log.append(
            Step(
                "cycle",
                f"检测到循环定义: {' -> '.join(cycle + [cycle[0]])}；"
                "停止代入，改用线性代数（高斯消元）处理，避免无限替换",
            )
        )
        consistent, assignments, free_vars = _gauss_path(equations, log)
        if not consistent:
            log.append(Step("result", "线性代数判定：方程组矛盾", domain="Q"))
            return Solution("contradiction", log=log)
    else:
        assignments = {}
        remaining = dict(defs)
        while remaining:
            progressed = False
            for var in sorted(list(remaining)):
                deps = remaining[var].variables() & remaining.keys()
                if deps:
                    continue
                assignments[var] = substitute(remaining[var], assignments)
                log.append(
                    Step(
                        "substitute",
                        f"代入化简得 {var} = {assignments[var]}",
                        domain="等量代换（适用域：Q，恒合法）",
                    )
                )
                del remaining[var]
                progressed = True
            if not progressed:  # 无环时不会发生
                raise RuntimeError("代入排序失败：定义图本应无环")
        all_vars = set()
        for eq in equations:
            all_vars |= eq.variables()
        for cond in conditions:
            all_vars |= cond.variables()
        free_vars = sorted(all_vars - set(defs))
    final_conditions = []
    for cond in conditions:
        reduced = substitute(cond, assignments)
        if reduced.is_constant():
            if reduced.const == 0:
                log.append(
                    Step(
                        "condition",
                        f"条件 {cond} != 0 化简为 0 != 0，不成立，本分支无可行解",
                        condition=f"{cond} != 0",
                        domain="Q",
                    )
                )
                return Solution("infeasible", assignments, free_vars, [], log)
            log.append(
                Step(
                    "condition",
                    f"条件 {cond} != 0 化简为非零常数，自动成立",
                    condition=f"{cond} != 0",
                    domain="Q",
                )
            )
        else:
            log.append(
                Step(
                    "condition",
                    f"保留条件 {reduced} != 0（对自由变量的限制）",
                    condition=f"{reduced} != 0",
                    domain="Q",
                )
            )
            final_conditions.append(reduced)
    if free_vars:
        log.append(
            Step(
                "result",
                f"无法消去的变量（自由变量）: {', '.join(free_vars)}；"
                "方程组为自由族而非矛盾",
            )
        )
    else:
        log.append(Step("result", "所有变量均被消元/确定，唯一解"))
    return Solution("consistent", assignments, free_vars, final_conditions, log)


def solve_text(equation_texts):
    """解析并求解文本方程组，返回 Branch 列表（含被舍弃的分支）。"""
    log = []
    equations = []
    conditions = []
    for i, text in enumerate(equation_texts, 1):
        form, conds, notes = parse_equation(text)
        log.append(Step("parse", f"方程{i}: {text}  =>  ({form}) = 0", domain="Q"))
        for note in notes:
            log.append(Step("parse", note, domain="Q"))
        equations.append(form)
        conditions.extend(conds)
    assumptions = [f"{c} != 0" for c in conditions]
    if conditions:
        log.append(
            Step(
                "branch",
                "除以可能为 0 的项，须分支；主分支假设: " + " 且 ".join(assumptions),
                condition=" 且 ".join(assumptions),
            )
        )
    solution = solve_system(equations, conditions, log)
    branches = [Branch("主分支", assumptions, solution)]
    for cond in conditions:
        branches.append(
            Branch(
                f"分支 {cond} = 0",
                [f"{cond} = 0"],
                None,
                f"当 {cond} = 0 时原方程含除零，表达式无定义，舍弃该分支",
            )
        )
    return branches
