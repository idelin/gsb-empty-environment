from __future__ import annotations

import argparse
from dataclasses import dataclass, field
from fractions import Fraction
import re
from typing import Iterable, Optional


class EliminationError(ValueError):
    """线性消元基础异常。"""


class ParseError(EliminationError):
    """等式无法按支持的语法解析。"""


class NonLinearError(EliminationError):
    """出现当前线性算法不支持的非线性表达式。"""


class ZeroDivisorError(EliminationError):
    """除以静态已知的 0。"""


@dataclass(frozen=True)
class Poly:
    terms: dict[str, Fraction] = field(default_factory=dict)
    const: Fraction = Fraction(0)

    def __post_init__(self) -> None:
        cleaned = {
            name: Fraction(coef)
            for name, coef in self.terms.items()
            if Fraction(coef) != 0
        }
        object.__setattr__(self, "terms", cleaned)
        object.__setattr__(self, "const", Fraction(self.const))

    def is_zero(self) -> bool:
        return not self.terms and self.const == 0

    def vars(self) -> list[str]:
        return list(self.terms)

    def __add__(self, other: "Poly") -> "Poly":
        terms = dict(self.terms)
        for name, coef in other.terms.items():
            terms[name] = terms.get(name, Fraction(0)) + coef
        return Poly(terms, self.const + other.const)

    def __sub__(self, other: "Poly") -> "Poly":
        terms = dict(self.terms)
        for name, coef in other.terms.items():
            terms[name] = terms.get(name, Fraction(0)) - coef
        return Poly(terms, self.const - other.const)

    def __mul__(self, value: Fraction | int) -> "Poly":
        factor = Fraction(value)
        return Poly({name: coef * factor for name, coef in self.terms.items()}, self.const * factor)

    __rmul__ = __mul__

    def __neg__(self) -> "Poly":
        return self * -1

    def __str__(self) -> str:
        return format_poly(self)


def format_fraction(value: Fraction) -> str:
    value = Fraction(value)
    if value.denominator == 1:
        return str(value.numerator)
    return f"{value.numerator}/{value.denominator}"


def format_monomial(name: str, coef: Fraction, first: bool) -> str:
    abs_coef = abs(coef)
    number = "" if abs_coef == 1 else format_fraction(abs_coef)
    if first and coef > 0:
        return f"{number}{name}"
    if first:
        return f"-{number}{name}"
    if coef > 0:
        return f" + {number}{name}"
    return f" - {number}{name}"


def format_poly(poly: Poly) -> str:
    if not poly.terms:
        return format_fraction(poly.const)
    parts: list[str] = []
    for index, (name, coef) in enumerate(poly.terms.items()):
        parts.append(format_monomial(name, coef, index == 0))
    if poly.const == 0:
        return "".join(parts)
    if poly.const > 0:
        parts.append(f" + {format_fraction(poly.const)}")
    else:
        parts.append(f" - {format_fraction(abs(poly.const))}")
    return "".join(parts)


def format_equation(lhs: Poly, rhs: Optional[Poly] = None) -> str:
    if rhs is None:
        rhs = Poly(const=Fraction(0))
    return f"{lhs} = {rhs}"


# AST: ('num', Fraction), ('var', name), ('neg', x), ('bin', op, left, right)
_TOKEN_RE = re.compile(r"(?:(?P<number>\d+/\d+|\d+\.\d+|\d+)|(?P<name>[A-Za-z_]\w*)|(?P<op>==|=|\+|-|\*|/|\(|\)))")


def tokenize(text: str) -> list[tuple[str, str]]:
    tokens: list[tuple[str, str]] = []
    index = 0
    while index < len(text):
        if text[index].isspace():
            index += 1
            continue
        match = _TOKEN_RE.match(text, index)
        if not match or match.end() == index:
            raise ParseError(f"无法识别的字符: {text[index:]!r}")
        if match.group("number") is not None:
            raw = match.group("number")
            if "." in raw:
                value = Fraction(raw)
            elif "/" in raw:
                numerator, denominator = raw.split("/", 1)
                denominator_value = int(denominator)
                if denominator_value == 0:
                    raise ZeroDivisorError("不能除以 0")
                value = Fraction(int(numerator), denominator_value)
            else:
                value = Fraction(int(raw))
            tokens.append(("num", value))
        else:
            tokens.append(("op", match.group("name") or match.group("op")))
        index = match.end()
    return tokens


class Parser:
    def __init__(self, text: str):
        if text.count("=") != 1:
            raise ParseError("等式必须恰好包含一个 '='")
        left_text, right_text = text.split("=", 1)
        self.left_tokens = tokenize(left_text)
        self.right_tokens = tokenize(right_text)

    def parse_equation(self):
        lhs = self._parse_expr(self.left_tokens)
        rhs = self._parse_expr(self.right_tokens)
        if self.left_tokens or self.right_tokens:
            raise ParseError("等式解析后仍有多余符号")
        return lhs, rhs

    def _parse_expr(self, tokens: list[tuple[str, str]]):
        node = self._parse_term(tokens)
        while tokens and tokens[0] == ("op", "+"):
            tokens.pop(0)
            node = ("bin", "+", node, self._parse_term(tokens))
        while tokens and tokens[0] == ("op", "-"):
            tokens.pop(0)
            node = ("bin", "-", node, self._parse_term(tokens))
        return node

    def _parse_term(self, tokens: list[tuple[str, str]]):
        node = self._parse_unary(tokens)
        while tokens and (tokens[0] == ("op", "*") or self._starts_implicit_factor(tokens[0])):
            if tokens[0] == ("op", "*"):
                tokens.pop(0)
            node = ("bin", "*", node, self._parse_unary(tokens))
        return node

    def _starts_implicit_factor(self, token: tuple[str, str]) -> bool:
        kind, value = token
        if kind == "num":
            return True
        return kind == "op" and (value == "(" or bool(re.fullmatch(r"[A-Za-z_]\w*", value)))

    def _parse_unary(self, tokens: list[tuple[str, str]]):
        if tokens and tokens[0] == ("op", "-"):
            tokens.pop(0)
            return ("neg", self._parse_unary(tokens))
        if tokens and tokens[0] == ("op", "+"):
            tokens.pop(0)
            return self._parse_unary(tokens)
        return self._parse_factor(tokens)

    def _parse_factor(self, tokens: list[tuple[str, str]]):
        if not tokens:
            raise ParseError("表达式意外结束")
        kind, value = tokens.pop(0)
        if kind == "num":
            node = ("num", value)
        elif kind == "op" and re.fullmatch(r"[A-Za-z_]\w*", value):
            node = ("var", value)
        elif value == "(":
            node = self._parse_expr(tokens)
            if not tokens or tokens[0] != ("op", ")"):
                raise ParseError("缺少右括号")
            tokens.pop(0)
        else:
            raise ParseError(f"意外的符号: {value}")
        while tokens and tokens[0] == ("op", "/"):
            tokens.pop(0)
            denominator = self._parse_unary(tokens)
            node = ("bin", "/", node, denominator)
        return node


def parse_equation(text: str):
    return Parser(text).parse_equation()


def collect_vars(node) -> list[str]:
    kind = node[0]
    if kind == "num":
        return []
    if kind == "var":
        return [node[1]]
    if kind == "neg":
        return collect_vars(node[1])
    result: list[str] = []
    for child in node[2:]:
        for name in collect_vars(child):
            if name not in result:
                result.append(name)
    return result


def linearize(node, symbolic_division: bool = False) -> Poly:
    kind = node[0]
    if kind == "num":
        return Poly(const=node[1])
    if kind == "var":
        return Poly(terms={node[1]: Fraction(1)})
    if kind == "neg":
        return -linearize(node[1], symbolic_division)
    op = node[1]
    if op in ("+", "-"):
        left = linearize(node[2], symbolic_division)
        right = linearize(node[3], symbolic_division)
        return left + right if op == "+" else left - right
    if op == "*":
        left = linearize(node[2], symbolic_division)
        right = linearize(node[3], symbolic_division)
        if not left.terms:
            return right * left.const
        if not right.terms:
            return left * right.const
        raise NonLinearError("变量相乘会产生非线性项，当前求解器不支持")
    if op == "/":
        denominator = node[3]
        divisor = linearize(denominator, symbolic_division=False)
        if divisor.terms:
            if symbolic_division:
                raise NonLinearError("符号分母内不能再含变量除法或其他非线性项")
            raise NonLinearError("表达式内部含变量除法；当前仅支持等式顶层除法的条件分支展开")
        if divisor.const == 0:
            raise ZeroDivisorError("不能除以 0")
        numerator = linearize(node[2], symbolic_division=False)
        return numerator * (Fraction(1) / divisor.const)
    raise ParseError(f"未知表达式节点: {node!r}")


def multiply_linear_polys(left: Poly, right: Poly) -> Poly:
    if not left.terms:
        return right * left.const
    if not right.terms:
        return left * right.const
    raise NonLinearError("乘回分母后出现变量相乘，结果不是线性方程")


def _root_division(node):
    if node[0] == "neg":
        inner = _root_division(node[1])
        if inner is None:
            return None
        _side, numerator, denominator = inner
        return _side, ("neg", numerator), denominator
    if node[0] == "bin" and node[1] == "/":
        return node[2], node[2], node[3]
    return None


@dataclass(frozen=True)
class Step:
    name: str
    description: str
    before: str
    after: str
    domain: str


@dataclass(frozen=True)
class Branch:
    condition: str
    kind: str
    result: str
    rationale: str


@dataclass(frozen=True)
class PreparedEquation:
    lhs: Poly
    rhs: Poly
    constraints: tuple[str, ...]
    steps: tuple[Step, ...]
    branches: tuple[Branch, ...]
    variables: tuple[str, ...] = ()


def prepare_equation(text: str) -> PreparedEquation:
    lhs_ast, rhs_ast = parse_equation(text)
    steps: list[Step] = []
    branches: list[Branch] = []
    constraints: list[str] = []
    equation_variables: list[str] = []
    for name in collect_vars(lhs_ast) + collect_vars(rhs_ast):
        if name not in equation_variables:
            equation_variables.append(name)

    lhs_div = _root_division(lhs_ast)
    rhs_div = _root_division(rhs_ast)
    original = text.strip()

    if lhs_div is not None and rhs_div is not None:
        raise NonLinearError("暂不支持等号两边同时是顶层除法；请先分别条件化")
    if lhs_div is not None:
        _raw_numerator, numerator_ast, denominator_ast = lhs_div
        divisor = linearize(denominator_ast, symbolic_division=True)
        if divisor.terms:
            condition = f"{divisor} ≠ 0"
            constraints.append(condition)
            numerator = linearize(numerator_ast, symbolic_division=True)
            rhs = linearize(rhs_ast, symbolic_division=True)
            moved = numerator - multiply_linear_polys(rhs, divisor)
            nonzero_result = format_equation(moved)
            branches.append(Branch(condition, "非零除数分支", nonzero_result, "可乘回分母；线性化后只在该域有效"))
            branches.append(Branch(f"{divisor} = 0", "零除数分支", "原除法未定义；无解", "除数为 0，不属于原等式的适用域"))
            steps.append(Step(
                "条件除法展开",
                f"对 {original} 按 {divisor} 是否为 0 分支；非零分支乘回分母。",
                original,
                f"{nonzero_result}，且 {condition}",
                f"线性化适用于 {condition}",
            ))
            lhs = moved
            rhs = Poly()
        else:
            prepared_poly = linearize(lhs_ast, symbolic_division=False)
            rhs = linearize(rhs_ast, symbolic_division=False)
            lhs = prepared_poly - rhs
            rhs = Poly()
    elif rhs_div is not None:
        _raw_numerator, numerator_ast, denominator_ast = rhs_div
        divisor = linearize(denominator_ast, symbolic_division=True)
        if divisor.terms:
            condition = f"{divisor} ≠ 0"
            constraints.append(condition)
            numerator = linearize(numerator_ast, symbolic_division=True)
            lhs = linearize(lhs_ast, symbolic_division=True)
            moved = multiply_linear_polys(lhs, divisor) - numerator
            nonzero_result = format_equation(moved)
            branches.append(Branch(condition, "非零除数分支", nonzero_result, "可乘回分母；线性化后只在该域有效"))
            branches.append(Branch(f"{divisor} = 0", "零除数分支", "原除法未定义；无解", "除数为 0，不属于原等式的适用域"))
            steps.append(Step(
                "条件除法展开",
                f"对 {original} 按 {divisor} 是否为 0 分支；非零分支乘回分母。",
                original,
                f"{nonzero_result}，且 {condition}",
                f"线性化适用于 {condition}",
            ))
            lhs = moved
            rhs = Poly()
        else:
            lhs_poly = linearize(lhs_ast, symbolic_division=False)
            prepared_poly = linearize(rhs_ast, symbolic_division=False)
            lhs = lhs_poly - prepared_poly
            rhs = Poly()
    else:
        lhs_poly = linearize(lhs_ast, symbolic_division=False)
        rhs_poly = linearize(rhs_ast, symbolic_division=False)
        lhs = lhs_poly - rhs_poly
        rhs = Poly()

    steps.append(Step(
        "移项标准化",
        "把右侧全部移到左侧，得到多项式 = 0。",
        original,
        format_equation(lhs),
        domain_text(constraints) if constraints else "全体有理数；未引入除法或乘回变量分母",
    ))
    return PreparedEquation(
        lhs,
        rhs,
        tuple(constraints),
        tuple(steps),
        tuple(branches),
        tuple(equation_variables),
    )


@dataclass(frozen=True)
class SolveResult:
    status: str
    equations: tuple[str, ...]
    variables: tuple[str, ...]
    pivot_variables: tuple[str, ...]
    free_variables: tuple[str, ...]
    pivot_expressions: tuple[str, ...]
    constraints: tuple[str, ...]
    steps: tuple[Step, ...]
    branches: tuple[Branch, ...]
    matrix_after: tuple[tuple[str, ...], ...]


def domain_text(constraints: Iterable[str]) -> str:
    values = tuple(constraints)
    if not values:
        return "全体有理数；未除以含变量表达式"
    return "满足 " + "；".join(values) + " 的有理数赋值"


def _step(name: str, description: str, before: str, after: str, constraints: Iterable[str]) -> Step:
    return Step(name, description, before, after, domain_text(constraints))


def _matrix_view(matrix: list[list[Fraction]]) -> tuple[tuple[str, ...], ...]:
    return tuple(tuple(format_fraction(value) for value in row) for row in matrix)


def solve_equations(texts: list[str]) -> SolveResult:
    prepared = [prepare_equation(text) for text in texts]
    steps: list[Step] = []
    branches: list[Branch] = []
    constraints: list[str] = []

    for index, item in enumerate(prepared, start=1):
        for step in item.steps:
            steps.append(step)
        for branch in item.branches:
            branches.append(branch)
        for condition in item.constraints:
            if condition not in constraints:
                constraints.append(condition)

    variables: list[str] = []
    for item in prepared:
        for name in item.variables:
            if name not in variables:
                variables.append(name)

    equations = tuple(format_equation(item.lhs) for item in prepared)
    matrix = [
        [item.lhs.terms.get(name, Fraction(0)) for name in variables] + [item.rhs.const - item.lhs.const]
        for item in prepared
    ]
    sources = [[index] for index in range(len(matrix))]

    row_count = len(matrix)
    column_count = len(variables)
    pivot_columns: list[int] = []
    pivot_row_by_column: dict[int, int] = {}
    row_index = 0

    for column in range(column_count):
        pivot = next((candidate for candidate in range(row_index, row_count) if matrix[candidate][column] != 0), None)
        if pivot is None:
            branches.append(Branch(
                f"{variables[column]} 的候选系数 = 0",
                "零系数分支",
                f"{variables[column]} 暂不能作为主元",
                "未除以 0；继续检查后续变量，最终可能为自由变量",
            ))
            steps.append(_step(
                "检查零主元",
                f"当前行起 {variables[column]} 的所有系数均为 0，因此不执行除法。",
                _view_text(variables, matrix),
                _view_text(variables, matrix),
                constraints,
            ))
            continue

        if pivot != row_index:
            before = _view_text(variables, matrix)
            matrix[row_index], matrix[pivot] = matrix[pivot], matrix[row_index]
            sources[row_index], sources[pivot] = sources[pivot], sources[row_index]
            steps.append(_step(
                "交换方程",
                f"交换第 {row_index + 1} 行与第 {pivot + 1} 行，以取得非零主元。",
                before,
                _view_text(variables, matrix),
                constraints,
            ))

        pivot_value = matrix[row_index][column]
        before = _view_text(variables, matrix)
        matrix[row_index] = [value / pivot_value for value in matrix[row_index]]
        steps.append(_step(
            "归一化主元",
            f"主元 {format_fraction(pivot_value)} 是非零有理数；第 {row_index + 1} 行除以它。",
            before,
            _view_text(variables, matrix),
            constraints,
        ))

        for candidate in range(row_count):
            if candidate == row_index:
                continue
            factor = matrix[candidate][column]
            if factor == 0:
                continue
            before = _view_text(variables, matrix)
            old_row = matrix[candidate][:]
            matrix[candidate] = [
                value - factor * pivot_row_value
                for value, pivot_row_value in zip(old_row, matrix[row_index])
            ]
            sources[candidate] = _merge_sources(sources[candidate], sources[row_index])
            steps.append(_step(
                "主元消元",
                (
                    f"用第 {row_index + 1} 行消去第 {candidate + 1} 行中 {variables[column]} 的系数 "
                    f"{format_fraction(factor)}：行{candidate + 1} ← 行{candidate + 1} "
                    f"- ({format_fraction(factor)})·行{row_index + 1}。循环等式通过整行线性组合处理，不做无限回代。"
                ),
                before,
                _view_text(variables, matrix),
                constraints,
            ))

        pivot_columns.append(column)
        pivot_row_by_column[column] = row_index
        row_index += 1
        if row_index == row_count:
            break

    if row_index >= row_count:
        remaining = [column for column in range(column_count) if column not in pivot_row_by_column]
        if remaining:
            steps.append(_step(
                "无法消去剩余变量",
                "独立方程已经用尽；剩余变量缺少主元，不能通过代入唯一确定，记为自由变量。",
                _view_text(variables, matrix),
                "剩余变量：" + ", ".join(variables[column] for column in remaining),
                constraints,
            ))
            for column in remaining:
                branches.append(Branch(
                    f"{variables[column]} 缺少独立方程",
                    "无法消去分支",
                    f"{variables[column]} 为自由变量",
                    "不除以不存在的非零系数；参数化解保留该自由度",
                ))

    inconsistent_rows: list[int] = []
    dependent_rows: list[int] = []
    for index, row in enumerate(matrix):
        if all(value == 0 for value in row[:column_count]):
            if row[-1] != 0:
                inconsistent_rows.append(index)
            else:
                dependent_rows.append(index)

    for index in inconsistent_rows:
        steps.append(_step(
            "发现矛盾",
            f"第 {index + 1} 行变为 0 = {format_fraction(matrix[index][-1])}，由源方程 {_format_sources(sources[index])} 线性组合得到。",
            _view_text(variables, matrix),
            "无解",
            constraints,
        ))

    for index in dependent_rows:
        steps.append(_step(
            "识别循环/依赖等式",
            f"第 {index + 1} 行变为 0 = 0，源方程 {_format_sources(sources[index])} 线性相关；它不制造矛盾，也不继续替换。",
            _view_text(variables, matrix),
            "0 = 0",
            constraints,
        ))

    pivot_variables = tuple(variables[column] for column in pivot_columns)
    free_variables = () if inconsistent_rows else tuple(
        name for column, name in enumerate(variables) if column not in pivot_row_by_column
    )

    pivot_expressions: list[str] = []
    for column in pivot_columns:
        row = pivot_row_by_column[column]
        rhs_value = matrix[row][-1]
        if not inconsistent_rows:
            pieces: list[str] = []
            for free_column, name in enumerate(variables):
                coefficient = matrix[row][free_column]
                if free_column == column or coefficient == 0:
                    continue
                pieces.append(_format_pivot_term(name, -coefficient, not pieces))
            if rhs_value != 0:
                if pieces and rhs_value < 0:
                    pieces.append(f"- {format_fraction(abs(rhs_value))}")
                elif pieces:
                    pieces.append(f"+ {format_fraction(rhs_value)}")
                else:
                    pieces.append(format_fraction(rhs_value))
            expression = " ".join(pieces) if pieces else "0"
            pivot_expressions.append(f"{variables[column]} = {expression}")

    if inconsistent_rows:
        status = "inconsistent"
    elif free_variables:
        status = "free_family"
    else:
        status = "unique"

    return SolveResult(
        status=status,
        equations=equations,
        variables=tuple(variables),
        pivot_variables=pivot_variables,
        free_variables=free_variables,
        pivot_expressions=tuple(pivot_expressions),
        constraints=tuple(constraints),
        steps=tuple(steps),
        branches=tuple(branches),
        matrix_after=_matrix_view(matrix),
    )


def _merge_sources(left: list[int], right: list[int]) -> list[int]:
    result = list(dict.fromkeys(left + right))
    return result


def _format_sources(source: list[int]) -> str:
    return ", ".join(str(index + 1) for index in source)


def _view_text(variables: list[str], matrix: list[list[Fraction]]) -> str:
    header = "[" + ", ".join(variables + ["常数项"]) + "]"
    body = "; ".join(
        "[" + ", ".join(format_fraction(value) for value in row) + "]"
        for row in matrix
    )
    return header + " " + body


def _format_pivot_term(name: str, coefficient: Fraction, first: bool) -> str:
    absolute = abs(coefficient)
    number = "" if absolute == 1 else format_fraction(absolute)
    if first:
        if coefficient < 0:
            return f"-{number}{name}"
        return f"{number}{name}"
    if coefficient < 0:
        return f"- {number}{name}"
    return f"+ {number}{name}"



STATUS_TEXT = {
    "unique": "唯一解",
    "free_family": "自由族（线性相关但不矛盾）",
    "inconsistent": "矛盾，无解",
}


def format_result(result: SolveResult) -> str:
    lines: list[str] = []
    lines.append("输入等式：")
    for equation in result.equations:
        lines.append(f"  - {equation}")
    lines.append(f"变量顺序：{', '.join(result.variables) if result.variables else '（无）'}")
    lines.append("")
    lines.append("逐步变换：")
    for index, step in enumerate(result.steps, start=1):
        lines.append(f"  {index}. [{step.name}] {step.description}")
        lines.append(f"     前：{step.before}")
        lines.append(f"     后：{step.after}")
        lines.append(f"     适用域：{step.domain}")
    if result.branches:
        lines.append("")
        lines.append("除法/零系数分支：")
        for index, branch in enumerate(result.branches, start=1):
            lines.append(f"  {index}. 条件：{branch.condition}")
            lines.append(f"     类型：{branch.kind}")
            lines.append(f"     结果：{branch.result}")
            lines.append(f"     说明：{branch.rationale}")
    lines.append("")
    lines.append(f"结论：{STATUS_TEXT[result.status]}")
    if result.status != "inconsistent":
        if result.pivot_expressions:
            lines.append("主元变量：")
            for expression in result.pivot_expressions:
                lines.append(f"  - {expression}")
        if result.free_variables:
            lines.append(f"自由变量：{', '.join(result.free_variables)}")
    if result.constraints:
        lines.append("解还必须满足：")
        for condition in result.constraints:
            lines.append(f"  - {condition}")
    lines.append("最终 RREF 矩阵（最后一列为常数项）：")
    for row in result.matrix_after:
        lines.append("  [" + ", ".join(row) + "]")
    return "\n".join(lines)


DEMO_CASES: list[tuple[str, list[str]]] = [
    ("自由族：x=y+1 与 y=x-1", ["x=y+1", "y=x-1"]),
    ("矛盾：x=y+1 与 y=x", ["x=y+1", "y=x"]),
    ("精确有理数唯一解", ["2x + 4y = 1/2", "x - y = 1/3"]),
    ("除以可能为 0 的参数：x/(a-1)=2", ["x/(a-1)=2"]),
]


def run_demo() -> str:
    sections: list[str] = []
    for title, equations in DEMO_CASES:
        sections.append("=" * 72)
        sections.append(title)
        sections.append("=" * 72)
        sections.append(format_result(solve_equations(equations)))
    return "\n\n".join(sections)


def main(argv: Optional[list[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="精确有理数线性方程消元器")
    parser.add_argument("equations", nargs="*", help="线性等式，例如 '2x + 4y = 1/2'")
    parser.add_argument("--demo", action="store_true", help="运行内置样例")
    args = parser.parse_args(argv)

    if args.demo or not args.equations:
        print(run_demo())
        return 0
    try:
        print(format_result(solve_equations(args.equations)))
    except EliminationError as error:
        print(f"错误：{error}")
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
