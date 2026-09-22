"""AST node definitions. Every node carries a UTF-8 byte span [start, end)."""


class Node:
    __slots__ = ("start", "end")


class IntLit(Node):
    __slots__ = ("value",)

    def __init__(self, value, start, end):
        self.value = value
        self.start = start
        self.end = end


class BoolLit(Node):
    __slots__ = ("value",)

    def __init__(self, value, start, end):
        self.value = value
        self.start = start
        self.end = end


class Name(Node):
    __slots__ = ("name",)

    def __init__(self, name, start, end):
        self.name = name
        self.start = start
        self.end = end


class Unary(Node):
    __slots__ = ("op", "operand")

    def __init__(self, op, operand, start, end):
        self.op = op
        self.operand = operand
        self.start = start
        self.end = end


class Binary(Node):
    __slots__ = ("op", "left", "right")

    def __init__(self, op, left, right, start, end):
        self.op = op
        self.left = left
        self.right = right
        self.start = start
        self.end = end


class Let(Node):
    __slots__ = ("name", "value")

    def __init__(self, name, value, start, end):
        self.name = name
        self.value = value
        self.start = start
        self.end = end


class Assign(Node):
    __slots__ = ("name", "value")

    def __init__(self, name, value, start, end):
        self.name = name
        self.value = value
        self.start = start
        self.end = end


class ExprStmt(Node):
    __slots__ = ("expr",)

    def __init__(self, expr, start, end):
        self.expr = expr
        self.start = start
        self.end = end


class Block(Node):
    __slots__ = ("stmts",)

    def __init__(self, stmts, start, end):
        self.stmts = stmts
        self.start = start
        self.end = end


class If(Node):
    __slots__ = ("cond", "then", "otherwise")

    def __init__(self, cond, then, otherwise, start, end):
        self.cond = cond
        self.then = then
        self.otherwise = otherwise
        self.start = start
        self.end = end


class While(Node):
    __slots__ = ("cond", "body")

    def __init__(self, cond, body, start, end):
        self.cond = cond
        self.body = body
        self.start = start
        self.end = end


class Break(Node):
    def __init__(self, start, end):
        self.start = start
        self.end = end
