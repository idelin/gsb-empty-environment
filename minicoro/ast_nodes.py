"""AST 节点定义。每个节点携带 UTF-8 字节区间 start..end。"""

from dataclasses import dataclass, fields, is_dataclass


@dataclass
class Program:
    stmts: list


# ---- 语句 ----

@dataclass
class FnDef:
    name: str
    params: list
    body: list
    start: int = 0
    end: int = 0


@dataclass
class Let:
    name: str
    expr: object
    start: int = 0
    end: int = 0


@dataclass
class Assign:
    name: str
    expr: object
    start: int = 0
    end: int = 0


@dataclass
class If:
    arms: list          # [(cond, body_stmts), ...]
    else_body: object   # list | None
    start: int = 0
    end: int = 0


@dataclass
class While:
    cond: object
    body: list
    start: int = 0
    end: int = 0


@dataclass
class TryFinally:
    body: list
    final: list
    start: int = 0
    end: int = 0


@dataclass
class Return:
    expr: object        # None 表示裸 return
    start: int = 0
    end: int = 0


@dataclass
class Break:
    start: int = 0
    end: int = 0


@dataclass
class Continue:
    start: int = 0
    end: int = 0


@dataclass
class ExprStmt:
    expr: object
    start: int = 0
    end: int = 0


# ---- 表达式 ----

@dataclass
class IntLit:
    value: object
    start: int = 0
    end: int = 0


@dataclass
class StrLit:
    value: object
    start: int = 0
    end: int = 0


@dataclass
class BoolLit:
    value: object
    start: int = 0
    end: int = 0


@dataclass
class NoneLit:
    start: int = 0
    end: int = 0


@dataclass
class Name:
    name: str
    start: int = 0
    end: int = 0


@dataclass
class ListLit:
    items: list
    start: int = 0
    end: int = 0


@dataclass
class BinOp:
    op: str
    left: object
    right: object
    start: int = 0
    end: int = 0


@dataclass
class UnaryOp:
    op: str             # '-' | 'not'
    operand: object
    start: int = 0
    end: int = 0


@dataclass
class Call:
    func: object
    args: list
    start: int = 0
    end: int = 0


@dataclass
class Index:
    obj: object
    index: object
    start: int = 0
    end: int = 0


@dataclass
class YieldExpr:
    expr: object        # None 表示裸 yield
    start: int = 0
    end: int = 0


def contains_yield(node):
    """判断语法子树中是否含有 yield（不深入嵌套的 fn 定义）。"""
    if isinstance(node, YieldExpr):
        return True
    if isinstance(node, FnDef):
        return False
    if is_dataclass(node) and not isinstance(node, type):
        return any(contains_yield(getattr(node, f.name)) for f in fields(node))
    if isinstance(node, (list, tuple)):
        return any(contains_yield(item) for item in node)
    return False
