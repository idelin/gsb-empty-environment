"""AST nodes.

Every node carries an absolute UTF-8 byte span ``[start, end)``. Spans are
mutable so the incremental parser can shift a reused subtree in place when
an edit moves it (tree-sitter style: after ``Session.edit`` the previous
tree's positions must be considered stale).

Structural equality (``==``) compares node type, scalar fields, span and
children; identity (``is``) is what tests use to prove subtree reuse.
"""
from __future__ import annotations


class Node:
    def __init__(self, start: int, end: int):
        self.start = start
        self.end = end

    @property
    def span(self) -> tuple[int, int]:
        return (self.start, self.end)

    def child_nodes(self) -> list["Node"]:
        return []

    def scalars(self) -> tuple:
        return ()

    def shift(self, delta: int) -> None:
        """Move this subtree by ``delta`` bytes (used when reusing nodes)."""
        if delta == 0:
            return
        self.start += delta
        self.end += delta
        for child in self.child_nodes():
            child.shift(delta)

    def __eq__(self, other) -> bool:
        if type(self) is not type(other):
            return False
        return (
            (self.start, self.end) == (other.start, other.end)
            and self.scalars() == other.scalars()
            and self.child_nodes() == other.child_nodes()
        )

    __hash__ = None

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"{type(self).__name__}[{self.start}, {self.end})"


class Program(Node):
    def __init__(self, decls, start, end):
        super().__init__(start, end)
        self.decls = decls

    def child_nodes(self):
        return list(self.decls)


class FnDecl(Node):
    def __init__(self, name, params, body, error, start, end):
        super().__init__(start, end)
        self.name = name
        self.params = tuple(params)
        self.body = body
        self.error = error  # optional ErrorNode (e.g. missing ')' in params)

    def scalars(self):
        return (self.name, self.params)

    def child_nodes(self):
        children = [self.body]
        if self.error is not None:
            children.append(self.error)
        return children


class LetDecl(Node):
    def __init__(self, name, value, start, end):
        super().__init__(start, end)
        self.name = name
        self.value = value

    def scalars(self):
        return (self.name,)

    def child_nodes(self):
        return [self.value]


class Binary(Node):
    def __init__(self, op, left, right, start, end):
        super().__init__(start, end)
        self.op = op
        self.left = left
        self.right = right

    def scalars(self):
        return (self.op,)

    def child_nodes(self):
        return [self.left, self.right]


class Unary(Node):
    def __init__(self, op, operand, start, end):
        super().__init__(start, end)
        self.op = op
        self.operand = operand

    def scalars(self):
        return (self.op,)

    def child_nodes(self):
        return [self.operand]


class Call(Node):
    def __init__(self, callee, args, error, start, end):
        super().__init__(start, end)
        self.callee = callee
        self.args = list(args)
        self.error = error  # optional ErrorNode (missing ')' after args)

    def child_nodes(self):
        children = [self.callee, *self.args]
        if self.error is not None:
            children.append(self.error)
        return children


class Group(Node):
    """A parenthesized expression."""

    def __init__(self, inner, error, start, end):
        super().__init__(start, end)
        self.inner = inner
        self.error = error  # optional ErrorNode (missing ')')

    def child_nodes(self):
        children = [self.inner]
        if self.error is not None:
            children.append(self.error)
        return children


class If(Node):
    def __init__(self, cond, then, otherwise, error, start, end):
        super().__init__(start, end)
        self.cond = cond
        self.then = then
        self.otherwise = otherwise
        self.error = error  # optional ErrorNode (missing 'then'/'else')

    def child_nodes(self):
        children = [self.cond, self.then, self.otherwise]
        if self.error is not None:
            children.append(self.error)
        return children


class Num(Node):
    def __init__(self, value, start, end):
        super().__init__(start, end)
        self.value = value

    def scalars(self):
        return (self.value,)


class Name(Node):
    def __init__(self, name, start, end):
        super().__init__(start, end)
        self.name = name

    def scalars(self):
        return (self.name,)


class ErrorNode(Node):
    """Explicit error node produced by error recovery."""

    def __init__(self, message, children, start, end):
        super().__init__(start, end)
        self.message = message
        self.children = list(children)

    def scalars(self):
        return (self.message,)

    def child_nodes(self):
        return list(self.children)


def dump(node: Node, indent: int = 0) -> str:
    """Human-readable tree dump with byte spans, for demos and debugging."""
    pad = "  " * indent
    extra = ""
    if isinstance(node, Num):
        extra = f" {node.value}"
    elif isinstance(node, Name):
        extra = f" {node.name}"
    elif isinstance(node, (Binary, Unary)):
        extra = f" {node.op}"
    elif isinstance(node, FnDecl):
        extra = f" {node.name}({', '.join(node.params)})"
    elif isinstance(node, LetDecl):
        extra = f" {node.name}"
    elif isinstance(node, ErrorNode):
        extra = f" {node.message!r}"
    lines = [f"{pad}{type(node).__name__}{extra} [{node.start}, {node.end})"]
    for child in node.child_nodes():
        lines.append(dump(child, indent + 1))
    return "\n".join(lines)
