"""unitlang：量纲安全的单位计算语言。

执行流程：tokenize -> parse -> check（量纲/仿射规则，执行前完成）-> eval。
"""
from __future__ import annotations

from .checker import Checker
from .errors import LangError, format_error
from .interp import eval_expr, fmt_value
from .lexer import tokenize
from .parser import Let, Parser

__all__ = ["run", "LangError", "format_error"]


def run(source: str) -> list[str]:
    """运行源程序，返回每条语句的输出行；出错抛 LangError。"""
    program = Parser(tokenize(source)).parse_program()
    Checker().check_program(program)  # 量纲检查先于一切执行
    env: dict = {}
    out: list[str] = []
    for stmt in program.stmts:
        if isinstance(stmt, Let):
            value = eval_expr(stmt.expr, env)
            env[stmt.name] = value
            out.append(f"{stmt.name} = {fmt_value(value)}")
        else:
            out.append(fmt_value(eval_expr(stmt.expr, env)))
    return out
