"""不可变语法树节点。

位置约定(关键设计,支撑子树复用):
- 每个节点记录 ``length``:自身覆盖的 UTF-8 字节数。
- 除 Program 的直接子节点外,节点的 ``offset`` 是相对父节点起点的字节偏移。
- Program 的直接子节点(顶层声明)与前一个声明之间的间隙记录在
  ``Program.gaps`` 中,而不是声明节点自身。这样编辑后声明的字节位置
  即使整体平移,被复用的声明节点对象也无需任何修改。
- 绝对字节区间通过 ``compute_positions`` 按需计算,不存储在节点上。
"""


class Node:
    __slots__ = ("offset", "length")

    def __init__(self, offset, length):
        self.offset = offset
        self.length = length

    def children(self):
        return ()

    def __repr__(self):
        return f"{type(self).__name__}(len={self.length})"


class Program(Node):
    __slots__ = ("decls", "gaps")

    def __init__(self, decls, gaps, length):
        super().__init__(0, length)
        self.decls = tuple(decls)
        self.gaps = tuple(gaps)


class FuncDecl(Node):
    __slots__ = ("name", "params", "body", "extras")

    def __init__(self, offset, length, name, params, body, extras=()):
        super().__init__(offset, length)
        self.name = name
        self.params = tuple(params)
        self.body = body
        self.extras = tuple(extras)  # 附属于声明的错误节点(如缺少 ';')

    def children(self):
        return (self.body,) + self.extras


class LetDecl(Node):
    __slots__ = ("name", "value", "extras")

    def __init__(self, offset, length, name, value, extras=()):
        super().__init__(offset, length)
        self.name = name
        self.value = value
        self.extras = tuple(extras)

    def children(self):
        return (self.value,) + self.extras


class Number(Node):
    __slots__ = ("value",)

    def __init__(self, offset, length, value):
        super().__init__(offset, length)
        self.value = value


class Ident(Node):
    __slots__ = ("name",)

    def __init__(self, offset, length, name):
        super().__init__(offset, length)
        self.name = name


class Call(Node):
    __slots__ = ("callee", "args")

    def __init__(self, offset, length, callee, args):
        super().__init__(offset, length)
        self.callee = callee
        self.args = tuple(args)

    def children(self):
        return (self.callee,) + self.args


class Binary(Node):
    __slots__ = ("op", "left", "right")

    def __init__(self, offset, length, op, left, right):
        super().__init__(offset, length)
        self.op = op
        self.left = left
        self.right = right

    def children(self):
        return (self.left, self.right)


class Unary(Node):
    __slots__ = ("op", "operand")

    def __init__(self, offset, length, op, operand):
        super().__init__(offset, length)
        self.op = op
        self.operand = operand

    def children(self):
        return (self.operand,)


class ErrorNode(Node):
    """错误恢复产生的明确错误节点,inner 为已恢复出的部分子树。"""

    __slots__ = ("message", "inner")

    def __init__(self, offset, length, message, inner=()):
        super().__init__(offset, length)
        self.message = message
        self.inner = tuple(inner)

    def children(self):
        return self.inner


def compute_positions(program):
    """计算整棵树的绝对 UTF-8 字节区间,返回 {id(node): (start, end)}。"""
    positions = {id(program): (0, program.length)}

    def visit(node, start):
        positions[id(node)] = (start, start + node.length)
        for child in node.children():
            visit(child, start + child.offset)

    cursor = 0
    for decl, gap in zip(program.decls, program.gaps):
        start = cursor + gap
        visit(decl, start)
        cursor = start + decl.length
    return positions


def walk(node):
    yield node
    if isinstance(node, Program):
        for decl in node.decls:
            yield from walk(decl)
    else:
        for child in node.children():
            yield from walk(child)


def tree_signature(program, positions=None):
    """结构签名:节点类型、标量值与绝对位置构成的嵌套元组。

    用于验证增量解析得到的树与全量解析语义一致。
    """
    if positions is None:
        positions = compute_positions(program)

    def sig(node):
        parts = [type(node).__name__, positions[id(node)]]
        if isinstance(node, Number):
            parts.append(node.value)
        elif isinstance(node, Ident):
            parts.append(node.name)
        elif isinstance(node, (Binary, Unary)):
            parts.append(node.op)
        elif isinstance(node, FuncDecl):
            parts.extend([node.name, node.params])
        elif isinstance(node, LetDecl):
            parts.append(node.name)
        elif isinstance(node, ErrorNode):
            parts.append(node.message)
        parts.append(tuple(sig(child) for child in node.children()))
        return tuple(parts)

    return tuple(sig(decl) for decl in program.decls)
