"""宏展开器:基于语法树的参数化宏,卫生重命名,双重源码映射,展开限制。"""
from __future__ import annotations

from dataclasses import dataclass

from .ast_nodes import (
    Call,
    Node,
    Number,
    Origin,
    Span,
    Symbol,
    Text,
    clone_node,
    subtree_size,
)
from .errors import ExpansionError, ExpansionLimitError


@dataclass
class Macro:
    name: str
    params: list[str]
    body: Node
    def_span: Span


@dataclass
class ExpansionFrame:
    """展开链中的一帧:哪个宏、在哪调用、在哪定义。"""

    macro_name: str
    call_span: Span
    def_span: Span


@dataclass
class SourceMapEntry:
    """展开产物的双重源码映射:宏定义处字节区间 + 宏调用处字节区间。"""

    node_text: str
    macro_name: str
    def_span: Span
    call_span: Span


def _lookup(env: tuple[dict, ...], name: str):
    for scope in reversed(env):
        if name in scope:
            return scope[name]
    return None


class Expander:
    def __init__(self, max_depth: int = 64, max_nodes: int = 20000):
        self.max_depth = max_depth
        self.max_nodes = max_nodes
        self.macros: dict[str, Macro] = {}
        self.source_map: list[SourceMapEntry] = []
        self._counter = 0
        self._nodes = 0

    def expand_program(self, nodes: list[Node]) -> list[Node]:
        out: list[Node] = []
        for node in nodes:
            if self._is_defmacro(node):
                self._register(node)
            else:
                out.append(self._expand(node, ()))
        return out

    @staticmethod
    def _is_defmacro(node: Node) -> bool:
        return (
            isinstance(node, Call)
            and bool(node.items)
            and isinstance(node.items[0], Symbol)
            and node.items[0].name == "defmacro"
        )

    def _register(self, node: Call) -> None:
        items = node.items
        ok = (
            len(items) == 4
            and isinstance(items[1], Symbol)
            and isinstance(items[2], Call)
            and all(isinstance(p, Symbol) for p in items[2].items)
        )
        if not ok:
            raise ExpansionError(
                "malformed defmacro: expected (defmacro name (params...) body)",
                node.span,
            )
        params = [p.name for p in items[2].items]
        if len(set(params)) != len(params):
            raise ExpansionError("duplicate macro parameter name", items[2].span)
        name = items[1].name
        self.macros[name] = Macro(name, params, items[3], node.span)

    def _expand(self, node: Node, chain: tuple[ExpansionFrame, ...]) -> Node:
        if isinstance(node, Call) and node.items:
            head = node.items[0]
            if isinstance(head, Symbol) and head.name in self.macros:
                return self._expand_macro(self.macros[head.name], node, chain)
            return Call(
                node.span,
                node.origin,
                [self._expand(item, chain) for item in node.items],
            )
        return node

    def _expand_macro(
        self, macro: Macro, call: Call, chain: tuple[ExpansionFrame, ...]
    ) -> Node:
        frame = ExpansionFrame(macro.name, call.span, macro.def_span)
        if len(chain) >= self.max_depth:
            raise ExpansionLimitError("depth", self.max_depth, (*chain, frame))
        args = call.items[1:]
        if len(args) != len(macro.params):
            raise ExpansionError(
                f"macro '{macro.name}' expects {len(macro.params)} argument(s),"
                f" got {len(args)}",
                call.span,
            )
        env = ({p: ("param", a) for p, a in zip(macro.params, args)},)
        body = self._instantiate(macro.body, env, frame, chain)
        return self._expand(body, (*chain, frame))

    def _fresh(self, base: str) -> str:
        self._counter += 1
        return f"{base}#{self._counter}"

    def _budget(
        self, n: int, chain: tuple[ExpansionFrame, ...], frame: ExpansionFrame
    ) -> None:
        self._nodes += n
        if self._nodes > self.max_nodes:
            raise ExpansionLimitError("nodes", self.max_nodes, (*chain, frame))

    def _gen(self, cls, frame, chain, template: Node, **fields) -> Node:
        """生成一个模板实例节点,计入预算并登记双重源码映射。"""
        self._budget(1, chain, frame)
        origin = Origin("macro", frame.macro_name, template.span, frame.call_span)
        node = cls(span=template.span, origin=origin, **fields)
        if cls is Symbol:
            text = fields["name"]
        elif cls is Number:
            text = str(fields["value"])
        elif cls is Text:
            text = repr(fields["value"])
        else:
            text = "(...)"
        self.source_map.append(
            SourceMapEntry(text, frame.macro_name, template.span, frame.call_span)
        )
        return node

    def _instantiate(
        self,
        node: Node,
        env: tuple[dict, ...],
        frame: ExpansionFrame,
        chain: tuple[ExpansionFrame, ...],
    ) -> Node:
        """把宏模板实例化:参数替换为实参 AST,let 绑定卫生重命名。"""
        if isinstance(node, Symbol):
            binding = _lookup(env, node.name)
            if binding is None:
                return self._gen(Symbol, frame, chain, node, name=node.name)
            kind, payload = binding
            if kind == "param":
                cloned = clone_node(payload)
                self._budget(subtree_size(cloned), chain, frame)
                return cloned
            return self._gen(Symbol, frame, chain, node, name=payload)
        if isinstance(node, Number):
            return self._gen(Number, frame, chain, node, value=node.value)
        if isinstance(node, Text):
            return self._gen(Text, frame, chain, node, value=node.value)
        if isinstance(node, Call):
            head = node.items[0] if node.items else None
            if isinstance(head, Symbol) and head.name == "let":
                return self._instantiate_let(node, env, frame, chain)
            items = [self._instantiate(it, env, frame, chain) for it in node.items]
            return self._gen(Call, frame, chain, node, items=items)
        raise ExpansionError("unknown node in macro template", node.span)

    def _instantiate_let(
        self,
        node: Call,
        env: tuple[dict, ...],
        frame: ExpansionFrame,
        chain: tuple[ExpansionFrame, ...],
    ) -> Node:
        items = node.items
        if len(items) < 3 or not isinstance(items[1], Call):
            raise ExpansionError("malformed 'let' in macro template", node.span)
        head = self._gen(Symbol, frame, chain, items[0], name="let")
        scope_env = tuple(env)
        new_bindings: list[Node] = []
        for binding in items[1].items:
            if (
                not isinstance(binding, Call)
                or len(binding.items) != 2
                or not isinstance(binding.items[0], Symbol)
            ):
                raise ExpansionError(
                    "malformed 'let' binding in macro template", binding.span
                )
            name = binding.items[0].name
            init = self._instantiate(binding.items[1], scope_env, frame, chain)
            fresh = self._fresh(name)
            scope_env = scope_env + ({name: ("local", fresh)},)
            sym = self._gen(Symbol, frame, chain, binding.items[0], name=fresh)
            new_bindings.append(
                self._gen(Call, frame, chain, binding, items=[sym, init])
            )
        bindings_node = self._gen(Call, frame, chain, items[1], items=new_bindings)
        body = [self._instantiate(it, scope_env, frame, chain) for it in items[2:]]
        return self._gen(Call, frame, chain, node, items=[head, bindings_node, *body])
