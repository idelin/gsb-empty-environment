"""AST node definitions.  ``start``/``end`` are UTF-8 byte offsets."""


class Node:
    __slots__ = ("start", "end")
    kind = "node"

    def __init__(self, start, end):
        self.start = start
        self.end = end


class Literal(Node):
    __slots__ = ("value",)
    kind = "literal"

    def __init__(self, value, start, end):
        super().__init__(start, end)
        self.value = value


class Name(Node):
    __slots__ = ("name",)
    kind = "name"

    def __init__(self, name, start, end):
        super().__init__(start, end)
        self.name = name


class Unary(Node):
    __slots__ = ("op", "operand")
    kind = "unary"

    def __init__(self, op, operand, start, end):
        super().__init__(start, end)
        self.op = op            # "neg" | "not"
        self.operand = operand


class Binary(Node):
    __slots__ = ("op", "left", "right")
    kind = "binary"

    def __init__(self, op, left, right, start, end):
        super().__init__(start, end)
        self.op = op
        self.left = left
        self.right = right


class If(Node):
    __slots__ = ("cond", "then", "otherwise")
    kind = "if"

    def __init__(self, cond, then, otherwise, start, end):
        super().__init__(start, end)
        self.cond = cond
        self.then = then
        self.otherwise = otherwise


class Call(Node):
    __slots__ = ("name", "name_start", "name_end", "args")
    kind = "call"

    def __init__(self, name, name_start, name_end, args, start, end):
        super().__init__(start, end)
        self.name = name
        self.name_start = name_start
        self.name_end = name_end
        self.args = args


class ListLit(Node):
    __slots__ = ("items",)
    kind = "list"

    def __init__(self, items, start, end):
        super().__init__(start, end)
        self.items = items


def children(node):
    kind = node.kind
    if kind == "unary":
        return (node.operand,)
    if kind == "binary":
        return (node.left, node.right)
    if kind == "if":
        return (node.cond, node.then, node.otherwise)
    if kind == "call":
        return node.args
    if kind == "list":
        return node.items
    return ()
