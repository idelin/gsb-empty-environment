"""有理数域 Q 上的精确线性代数（仅使用标准库 fractions，不调用符号代数库）。"""
from __future__ import annotations

from fractions import Fraction


class LinearForm:
    """线性型：sum(terms[var] * var) + const，系数与常数均为 Fraction。"""

    __slots__ = ("terms", "const")

    def __init__(self, terms=None, const=Fraction(0)):
        self.terms = {}
        if terms:
            for var, coeff in terms.items():
                coeff = Fraction(coeff)
                if coeff != 0:
                    self.terms[var] = coeff
        self.const = Fraction(const)

    @classmethod
    def variable(cls, name):
        return cls({name: Fraction(1)})

    @classmethod
    def constant(cls, value):
        return cls({}, Fraction(value))

    def variables(self):
        return set(self.terms)

    def is_constant(self):
        return not self.terms

    def is_zero(self):
        return not self.terms and self.const == 0

    def __add__(self, other):
        terms = dict(self.terms)
        for var, coeff in other.terms.items():
            terms[var] = terms.get(var, Fraction(0)) + coeff
        return LinearForm(terms, self.const + other.const)

    def __neg__(self):
        return self.scale(Fraction(-1))

    def __sub__(self, other):
        return self + (-other)

    def scale(self, factor):
        factor = Fraction(factor)
        return LinearForm(
            {var: coeff * factor for var, coeff in self.terms.items()},
            self.const * factor,
        )

    def __eq__(self, other):
        return (
            isinstance(other, LinearForm)
            and self.terms == other.terms
            and self.const == other.const
        )

    def __str__(self):
        parts = []
        for var in sorted(self.terms):
            coeff = self.terms[var]
            sign, mag = ("-", -coeff) if coeff < 0 else ("+", coeff)
            body = var if mag == 1 else f"{mag}*{var}"
            if not parts:
                parts.append(body if sign == "+" else f"-{body}")
            else:
                parts.append(f"{sign} {body}")
        if self.const != 0 or not parts:
            if not parts:
                parts.append(str(self.const))
            elif self.const > 0:
                parts.append(f"+ {self.const}")
            else:
                parts.append(f"- {abs(self.const)}")
        return " ".join(parts)

    __repr__ = __str__


def solve_for(form, var):
    """由 form == 0 解出 var，返回 g 使 var = g（调用方负责记录除以系数的适用域）。"""
    coeff = form.terms.get(var)
    if coeff is None or coeff == 0:
        raise ValueError(f"{var} 不在线性型 {form} 中")
    rest = LinearForm({v: c for v, c in form.terms.items() if v != var}, form.const)
    return rest.scale(Fraction(-1) / coeff)


def substitute(form, assignment):
    """把 assignment 中变量的定义等量代换进 form（适用域：Q，恒合法）。"""
    result = LinearForm.constant(form.const)
    for var, coeff in form.terms.items():
        target = assignment.get(var)
        if target is None:
            result = result + LinearForm({var: coeff})
        else:
            result = result + target.scale(coeff)
    return result


def rref(matrix, log=None):
    """对增广矩阵做精确行最简消元；log 为可选字符串列表，记录每次行变换及其适用域。"""
    rows = [[Fraction(x) for x in row] for row in matrix]
    if not rows:
        return rows, []
    n_cols = len(rows[0]) - 1
    pivots = []
    r = 0
    for c in range(n_cols):
        pivot = None
        for i in range(r, len(rows)):
            if rows[i][c] != 0:
                pivot = i
                break
        if pivot is None:
            continue
        if pivot != r:
            rows[r], rows[pivot] = rows[pivot], rows[r]
            if log is not None:
                log.append(f"交换行{r + 1}与行{pivot + 1}（适用域：Q，行交换不改变解集）")
        pivot_value = rows[r][c]
        if pivot_value != 1:
            rows[r] = [x / pivot_value for x in rows[r]]
            if log is not None:
                log.append(
                    f"行{r + 1}除以非零主元 {pivot_value}（适用域：Q，除以非零有理数恒合法）"
                )
        for i in range(len(rows)):
            if i != r and rows[i][c] != 0:
                factor = rows[i][c]
                rows[i] = [a - factor * b for a, b in zip(rows[i], rows[r])]
                if log is not None:
                    log.append(
                        f"行{i + 1}减去行{r + 1}的 {factor} 倍（适用域：Q，等量加减不改变解集）"
                    )
        pivots.append((r, c))
        r += 1
        if r == len(rows):
            break
    return rows, pivots


def solve_linear_system(equations, log=None):
    """高斯消元求解方程组（每个 LinearForm = 0）。

    返回 (consistent, assignments, free_vars)；assignments 以自由变量表示各主元变量。
    """
    variables = (
        sorted(set().union(*(eq.variables() for eq in equations))) if equations else []
    )
    if log is not None:
        log.append(
            f"高斯消元：{len(equations)} 个方程、{len(variables)} 个变量"
            "（Fraction 精确计算，不调用符号代数库）"
        )
    matrix = [
        [eq.terms.get(v, Fraction(0)) for v in variables] + [-eq.const]
        for eq in equations
    ]
    rows, pivots = rref(matrix, log)
    for row in rows:
        if all(x == 0 for x in row[:-1]) and row[-1] != 0:
            if log is not None:
                log.append(f"化简出现 0 = {row[-1]}，方程组不相容")
            return False, {}, []
    pivot_cols = {c for _, c in pivots}
    free_vars = [variables[c] for c in range(len(variables)) if c not in pivot_cols]
    assignments = {}
    for r, c in pivots:
        row = rows[r]
        terms = {
            variables[j]: -row[j]
            for j in range(len(variables))
            if j != c and row[j] != 0
        }
        assignments[variables[c]] = LinearForm(terms, row[-1])
    if log is not None:
        log.append(
            f"行最简形的秩 = {len(pivots)}；自由变量: "
            f"{', '.join(free_vars) if free_vars else '无'}"
        )
    return True, assignments, free_vars
