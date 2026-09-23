"""精确有理数求值器。只用标准库 Fraction，不调用任何符号代数库。

每步运算的适用域在各分支注释与异常信息中说明。
"""
from __future__ import annotations

from fractions import Fraction

from .ast_nodes import Binary, Call, Cond, Const, Node, Unary, Var


class EvalError(ValueError):
    """求值失败（越出适用域或环境缺失）。"""


def _as_rational(value, what: str) -> Fraction:
    if isinstance(value, float):
        raise EvalError(f"{what} 含 float，无法保证精确；请用 int/Fraction")
    try:
        return Fraction(value)
    except TypeError:
        raise EvalError(f"{what} 不是有理数: {value!r}") from None


def evaluate(node: Node, env: dict | None = None,
             functions: dict | None = None) -> Fraction:
    """把语法树求值为精确的 ``Fraction``。

    - ``env``：变量名 -> int/Fraction/十进制字符串（拒绝 float）。
    - ``functions``：函数名 -> 可调用对象，参数与返回值均为有理数。
    """
    env = env or {}
    functions = functions or {}

    if isinstance(node, Const):
        return node.value  # 适用域：任意有理数

    if isinstance(node, Var):
        if node.name not in env:
            raise EvalError(f"变量 {node.name!r} 未在环境中定义")
        return _as_rational(env[node.name], f"变量 {node.name!r} 的值")

    if isinstance(node, Unary):
        val = evaluate(node.operand, env, functions)
        if node.op == "-":
            return -val  # 适用域：任意有理数
        raise EvalError(f"一元运算符 {node.op!r} 没有内置求值语义")

    if isinstance(node, Binary):
        left = evaluate(node.left, env, functions)
        right = evaluate(node.right, env, functions)
        if node.op == "+":
            return left + right  # 适用域：任意有理数
        if node.op == "-":
            return left - right  # 适用域：任意有理数
        if node.op == "*":
            return left * right  # 适用域：任意有理数
        if node.op == "/":
            if right == 0:
                raise EvalError("除法适用域：除数必须非零")
            return left / right  # 有理数除法，精确
        if node.op == "^":
            # 适用域：指数必须为整数；底数为 0 时指数必须 >= 0。
            if right.denominator != 1:
                raise EvalError(f"幂运算适用域：指数须为整数，得到 {right}")
            exp = right.numerator
            if left == 0 and exp < 0:
                raise EvalError("幂运算适用域：0 的负整数次幂无定义")
            return left ** exp  # Fraction ** int 精确（含负指数）；0**0 按 1
        raise EvalError(f"二元运算符 {node.op!r} 没有内置求值语义")

    if isinstance(node, Cond):
        cond = evaluate(node.cond, env, functions)
        # 适用域：条件为有理数，非零即真。
        branch = node.then if cond != 0 else node.otherwise
        return evaluate(branch, env, functions)

    if isinstance(node, Call):
        if node.name not in functions:
            raise EvalError(f"函数 {node.name!r} 未在环境中定义")
        args = [evaluate(arg, env, functions) for arg in node.args]
        return _as_rational(functions[node.name](*args),
                            f"函数 {node.name!r} 的返回值")

    raise TypeError(f"未知节点类型: {type(node).__name__}")
