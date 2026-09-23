"""最小必要括号打印器。

括号判定只依据**语法树结构**与语法表（优先级/结合性/一元优先级），
绝不依据数值等价或代数恒等式——因此 ``a-(b-c)`` 永远不会被化简成
``a-b-c``（二者是不同的树）。

结构层级（数字越小优先级越低）：

    条件表达式        0（最低，右结合）
    二元算符          表中优先级
    一元负号          unary_prec（默认 3，位于乘除与幂之间）
    调用 / 原子       最高

各处规则（``child`` 是否需要括号）：

- 父为调用实参：实参位置按完整条件式解析，任何子树都不需要括号；
- 父为一元负号：二元子树仅当优先级 ``< unary_prec`` 时需要，
  如 ``-(a*b)``；条件子树需要（``-(c?a:b)``）；嵌套负号不需要
  （``--x``）；幂比负号紧，``-x^2`` 即 ``-(x^2)`` 不加括号；
- 父为二元算符（优先级 P）：
  * 二元子树按经典规则——更松加括号、更紧不加；同级时，左结合只在
    右侧加、右结合只在左侧加（``a-(b-c)``、``(a^b)^c``）；
  * ``Neg`` 子树：左侧当 ``P >= unary_prec`` 时加（``(-x)^2``），
    右侧永不加（``x^-y``、``a*-b`` 均可解析）；
  * 条件子树恒加括号；
- 父为条件：条件位上的条件式加括号（``(a?b:c)?d:e``），两个分支
  右结合不加（``a?b?c:d:e``）。
"""

from __future__ import annotations

from . import ast as tree
from .errors import StructureError
from .grammar import Grammar
from .lexer import FRAC_SLASH


class Printer:
    def __init__(self, grammar: Grammar):
        self.g = grammar

    def print(self, node) -> str:
        return self._render(node, ("root",))

    # --- 分发 ---

    def _render(self, node, ctx) -> str:
        if isinstance(node, tree.Rat):
            return self._rat(node)
        if isinstance(node, tree.Var):
            return node.name
        if isinstance(node, tree.Call):
            return f"{node.func}({', '.join(self._render(a, ('call',)) for a in node.args)})"
        if isinstance(node, tree.Neg):
            body = self._render(node.operand, ("neg",))
            if self._needs(node.operand, ("neg",)):
                body = f"({body})"
            return f"{self.g.unary_symbol}{body}"
        if isinstance(node, tree.BinOp):
            left = self._render(node.left, ("bin", node.op, "L"))
            right = self._render(node.right, ("bin", node.op, "R"))
            if self._needs(node.left, ("bin", node.op, "L")):
                left = f"({left})"
            if self._needs(node.right, ("bin", node.op, "R")):
                right = f"({right})"
            return f"{left} {node.op} {right}"
        if isinstance(node, tree.Cond):
            kind = ctx[0]
            cond = self._render(node.cond, ("cond", "cond"))
            then = self._render(node.then, ("cond", "then"))
            other = self._render(node.otherwise, ("cond", "else"))
            if self._needs(node.cond, ("cond", "cond")):
                cond = f"({cond})"
            if self._needs(node.then, ("cond", "then")):
                then = f"({then})"
            if self._needs(node.otherwise, ("cond", "else")):
                other = f"({other})"
            return f"{cond} ? {then} : {other}"
        raise StructureError(f"未知语法树节点：{node!r}")

    # --- 括号必要性（纯结构判定） ---

    def _needs(self, child, ctx) -> bool:
        kind = ctx[0]

        if isinstance(child, (tree.Rat, tree.Var, tree.Call)):
            return False

        if kind == "root" or kind == "call":
            return False

        if isinstance(child, tree.Cond):
            if kind == "cond":
                return ctx[1] == "cond"
            # neg / bin 上下文不吸收 '?'，恒需括号
            return True

        if kind == "neg":
            if isinstance(child, tree.Neg):
                return False
            if isinstance(child, tree.BinOp):
                return self.g.prec_of[child.op] < self.g.unary_prec
            return False

        if kind == "bin":
            _, parent_op, side = ctx
            p = self.g.prec_of[parent_op]
            if isinstance(child, tree.Neg):
                return side == "L" and p >= self.g.unary_prec
            if isinstance(child, tree.BinOp):
                q = self.g.prec_of[child.op]
                if q != p:
                    return q < p
                assoc = self.g.assoc_of[parent_op]
                if assoc == "L":
                    return side == "R"
                return side == "L"
            return False

        if kind == "cond":
            return False

        raise StructureError(f"未知上下文：{ctx!r}")

    # --- 有理字面量 ---

    @staticmethod
    def _rat(node: tree.Rat) -> str:
        if node.den == 1:
            return str(node.num)
        if node.num < 0:
            return f"-{abs(node.num)}{FRAC_SLASH}{node.den}"
        return f"{node.num}{FRAC_SLASH}{node.den}"


def to_string(node, grammar: Grammar = None) -> str:
    grammar = grammar or Grammar.build()
    return Printer(grammar).print(node)
