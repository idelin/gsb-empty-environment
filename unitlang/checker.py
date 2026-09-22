"""静态检查：在执行之前对整个程序做量纲与仿射温度规则检查。"""
from __future__ import annotations

from dataclasses import dataclass

from .errors import LangError
from .parser import BinOp, Convert, ExprStmt, Let, Literal, Unary, Var
from .units import TEMP_DIM, dim_combine, dim_name, make_dim, resolve_unit_expr, role_of


@dataclass
class Type:
    dim: tuple
    role: str  # 'none' | 'absolute' | 'delta' | 'bool'


_SIMPLE_DIMS = {
    make_dim(length=1): "长度",
    make_dim(time=1): "时间",
    make_dim(mass=1): "质量",
    make_dim(temp=1): "温度",
}


def show_type(t: Type) -> str:
    if t.role == "absolute":
        return "绝对温度"
    if t.role == "delta":
        return "温差"
    if t.role == "bool":
        return "布尔值"
    if not t.dim:
        return "无量纲数"
    return _SIMPLE_DIMS.get(t.dim, dim_name(t.dim))


class Checker:
    def __init__(self):
        self.env: dict[str, Type] = {}

    def check_program(self, prog) -> None:
        for stmt in prog.stmts:
            self.check_stmt(stmt)

    def check_stmt(self, stmt) -> None:
        if isinstance(stmt, Let):
            self.env[stmt.name] = self.check_expr(stmt.expr)
        else:
            self.check_expr(stmt.expr)

    def check_expr(self, node) -> Type:
        if isinstance(node, Literal):
            if node.unit is None:
                return Type((), "none")
            unit = resolve_unit_expr(node.unit.terms)
            return Type(unit.dim, role_of(unit))
        if isinstance(node, Var):
            if node.name not in self.env:
                raise LangError(f"未定义的变量：{node.name}", node.start, node.end)
            return self.env[node.name]
        if isinstance(node, Unary):
            t = self.check_expr(node.operand)
            if t.role == "absolute":
                raise LangError("绝对温度不能取负（可写成 0 °C 减温差的形式）", node.start, node.end)
            if t.role == "bool":
                raise LangError("布尔值不能取负", node.start, node.end)
            return t
        if isinstance(node, Convert):
            return self.check_convert(node)
        if isinstance(node, BinOp):
            return self.check_binop(node)
        raise AssertionError(f"未知节点：{node!r}")

    def check_convert(self, node) -> Type:
        src = self.check_expr(node.expr)
        unit = resolve_unit_expr(node.unit.terms)
        span = (node.op_start, node.op_end)
        if src.role == "bool":
            raise LangError("布尔值不能换算单位", *span)
        if src.dim != unit.dim:
            raise LangError(f"量纲不同，无法换算为 {unit.name}：{show_type(src)}", *span)
        if src.role == "absolute" and unit.kind == "delta":
            raise LangError("绝对温度不能换算为温差单位", *span)
        if src.role == "delta" and unit.kind != "delta":
            raise LangError("温差只能换算为温差单位（如 Δ°C、Δ°F、ΔK）", *span)
        if src.role == "none" and unit.kind == "delta":
            raise LangError(f"不能将{show_type(src)}换算为温差单位", *span)
        return Type(unit.dim, role_of(unit))

    def check_binop(self, node) -> Type:
        lt = self.check_expr(node.left)
        rt = self.check_expr(node.right)
        op = node.op
        span = (node.op_start, node.op_end)
        if op in ("+", "-"):
            self._reject_bool(lt, rt, span, "算术运算")
            if lt.dim != rt.dim:
                zh = "相加" if op == "+" else "相减"
                raise LangError(
                    f"量纲不同，不能{zh}：{show_type(lt)} 与 {show_type(rt)}", *span
                )
            combo = (lt.role, rt.role)
            if op == "+":
                if combo == ("absolute", "absolute"):
                    raise LangError("两个绝对温度不能相加（可先将其中一个表示为温差）", *span)
                if "absolute" in combo:
                    role = "absolute"
                elif combo == ("delta", "delta"):
                    role = "delta"
                else:
                    role = "none"
            else:
                if combo == ("delta", "absolute"):
                    raise LangError("温差不能减去绝对温度", *span)
                if combo == ("absolute", "absolute"):
                    role = "delta"
                elif combo == ("absolute", "delta"):
                    role = "absolute"
                elif combo == ("delta", "delta"):
                    role = "delta"
                else:
                    role = "none"
            return Type(lt.dim, role)
        if op in ("*", "/"):
            self._reject_bool(lt, rt, span, "乘除运算")
            for t in (lt, rt):
                if t.role == "absolute":
                    raise LangError(
                        "绝对温度（仿射温标）不能直接参与乘除运算，请先换算为 K", *span
                    )
            sign = 1 if op == "*" else -1
            dim = dim_combine(lt.dim, rt.dim, sign=sign)
            role = "delta" if (dim == TEMP_DIM and "delta" in (lt.role, rt.role)) else "none"
            return Type(dim, role)
        # 比较运算
        self._reject_bool(lt, rt, span, "比较")
        if lt.dim != rt.dim:
            raise LangError(
                f"量纲不同，不能比较：{show_type(lt)} 与 {show_type(rt)}", *span
            )
        if lt.role != rt.role:
            raise LangError(f"{show_type(lt)}与{show_type(rt)}不能直接比较", *span)
        return Type((), "bool")

    @staticmethod
    def _reject_bool(lt: Type, rt: Type, span, what: str) -> None:
        if lt.role == "bool" or rt.role == "bool":
            raise LangError(f"布尔值不能参与{what}", *span)
