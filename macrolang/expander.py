"""基于语法树的参数化宏展开器。

特性：
  - 卫生（hygiene）：宏模板内 `let` 引入的局部绑定一律改名为
    用户不可书写的 `名字#序号`，不会捕获调用方同名变量；
  - 实参语法树原样移植，绝不被无关重命名波及；
  - 每个由模板生成的节点都记录 (调用点, 定义点) 双重源码映射；
  - 展开深度与产生节点数均有上限，超限时抛出带完整展开链的错误。
"""
from __future__ import annotations

from dataclasses import dataclass

from .errors import (
    ArityError,
    DepthLimitExceeded,
    MacroExpansionError,
    NodeLimitExceeded,
)
from .nodes import ExpansionRecord, Form, IntLit, Node, Span, StrLit, Sym


@dataclass
class Macro:
    name: str
    params: list[str]
    body: Node
    def_span: Span  # 整个 defmacro 形式的字节区间


class ExpansionContext:
    """一次展开会话的全局状态：预算、计数与展开链。"""

    def __init__(self, max_depth: int = 64, max_nodes: int = 100_000):
        self.max_depth = max_depth
        self.max_nodes = max_nodes
        self.nodes_created = 0
        self.chain: list[tuple[str, Span]] = []
        self._fresh_counter = 0

    def count_node(self) -> None:
        self.nodes_created += 1
        if self.nodes_created > self.max_nodes:
            raise NodeLimitExceeded(self.max_nodes, self.chain)

    def fresh_name(self, base: str) -> str:
        """生成用户源码中不可书写的新名字（词法器拒绝 '#'）。"""
        self._fresh_counter += 1
        return f"{base}#{self._fresh_counter}"


class Expander:
    def __init__(self, max_depth: int = 64, max_nodes: int = 100_000):
        self.macros: dict[str, Macro] = {}
        self.ctx = ExpansionContext(max_depth, max_nodes)

    def expand_program(self, forms: list[Node]) -> list[Node]:
        return [self.expand(form) for form in forms]

    def expand(self, node: Node) -> Node:
        if isinstance(node, Form):
            return self._expand_form(node)
        return node

    # ---------------------------------------------------------- 内部分发

    def _expand_form(self, node: Form) -> Node:
        items = node.items
        if items and isinstance(items[0], Sym):
            head = items[0].name
            if head == "defmacro":
                self._register_macro(node)
                # defmacro 本身展开为空序列，执行期无效果
                return Form(span=node.span, items=[Sym(span=node.span, name="seq")])
            macro = self.macros.get(head)
            if macro is not None:
                return self._expand_call(macro, node)
        return Form(
            span=node.span,
            items=[self.expand(item) for item in items],
            provenance=list(node.provenance),
        )

    def _register_macro(self, node: Form) -> None:
        items = node.items
        if len(items) != 4:
            raise MacroExpansionError(
                "defmacro expects (defmacro name (params...) body)",
                self.ctx.chain,
                node.span,
            )
        name_node, params_node, body = items[1], items[2], items[3]
        if not isinstance(name_node, Sym):
            raise MacroExpansionError(
                "defmacro name must be a symbol", self.ctx.chain, node.span
            )
        if not isinstance(params_node, Form) or not all(
            isinstance(p, Sym) for p in params_node.items
        ):
            raise MacroExpansionError(
                "defmacro params must be a list of symbols",
                self.ctx.chain,
                params_node.span,
            )
        params = [p.name for p in params_node.items]
        if len(set(params)) != len(params):
            raise MacroExpansionError(
                f"defmacro {name_node.name!r} has duplicate parameter names",
                self.ctx.chain,
                params_node.span,
            )
        self.macros[name_node.name] = Macro(
            name=name_node.name,
            params=params,
            body=body,
            def_span=node.span,
        )

    def _expand_call(self, macro: Macro, call: Form) -> Node:
        args = call.items[1:]
        if len(args) != len(macro.params):
            raise ArityError(
                macro.name, len(macro.params), len(args), self.ctx.chain, call.span
            )
        ctx = self.ctx
        if len(ctx.chain) >= ctx.max_depth:
            raise DepthLimitExceeded(
                ctx.max_depth, ctx.chain + [(macro.name, call.span)]
            )
        ctx.chain.append((macro.name, call.span))
        try:
            subst = dict(zip(macro.params, args))
            inherited = list(call.provenance)
            body = self._instantiate(
                macro.body, subst, {}, macro, call.span, inherited
            )
            # 展开结果中可能含有新的宏调用（含实参），递归展开
            return self.expand(body)
        finally:
            ctx.chain.pop()

    # ---------------------------------------------------------- 模板实例化

    def _instantiate(
        self,
        node: Node,
        subst: dict[str, Node],
        renames: dict[str, str],
        macro: Macro,
        call_span: Span,
        prefix: list[ExpansionRecord],
    ) -> Node:
        """复制模板：形参替换为实参语法树，局部绑定卫生改名。"""
        if isinstance(node, Sym):
            if node.name in subst:
                # 实参来自调用方，原样深拷贝，不参与任何重命名
                return self._clone_arg(subst[node.name])
            return self._emit(
                Sym(span=node.span, name=renames.get(node.name, node.name)),
                macro,
                call_span,
                node.span,
                prefix,
            )
        if isinstance(node, (IntLit, StrLit)):
            return self._emit(
                type(node)(span=node.span, value=node.value),
                macro,
                call_span,
                node.span,
                prefix,
            )
        if isinstance(node, Form):
            items = node.items
            if (
                len(items) == 4
                and isinstance(items[0], Sym)
                and items[0].name == "let"
                and isinstance(items[1], Sym)
            ):
                return self._instantiate_let(
                    node, subst, renames, macro, call_span, prefix
                )
            return self._emit(
                Form(
                    span=node.span,
                    items=[
                        self._instantiate(
                            it, subst, renames, macro, call_span, prefix
                        )
                        for it in items
                    ],
                ),
                macro,
                call_span,
                node.span,
                prefix,
            )
        raise MacroExpansionError(  # pragma: no cover - 防御性分支
            f"cannot instantiate node of type {type(node).__name__}",
            self.ctx.chain,
        )

    def _instantiate_let(
        self,
        node: Form,
        subst: dict[str, Node],
        renames: dict[str, str],
        macro: Macro,
        call_span: Span,
        prefix: list[ExpansionRecord],
    ) -> Node:
        items = node.items
        binder = items[1].name
        head = self._emit(
            Sym(span=items[0].span, name="let"),
            macro,
            call_span,
            items[0].span,
            prefix,
        )
        # 绑定值在外层作用域求值，用外层替换/改名环境
        value = self._instantiate(
            items[2], subst, renames, macro, call_span, prefix
        )
        fresh = self.ctx.fresh_name(binder)
        binder_node = self._emit(
            Sym(span=items[1].span, name=fresh),
            macro,
            call_span,
            items[1].span,
            prefix,
        )
        # 体内：绑定名一律指向新名，即使与形参同名也以新绑定为准
        inner_subst = {k: v for k, v in subst.items() if k != binder}
        inner_renames = dict(renames)
        inner_renames[binder] = fresh
        body = self._instantiate(
            items[3], inner_subst, inner_renames, macro, call_span, prefix
        )
        return self._emit(
            Form(span=node.span, items=[head, binder_node, value, body]),
            macro,
            call_span,
            node.span,
            prefix,
        )

    def _emit(
        self,
        node: Node,
        macro: Macro,
        call_span: Span,
        def_span: Span,
        prefix: list[ExpansionRecord],
    ) -> Node:
        """登记新节点：计入预算，并在外层映射之后附加本次 (调用点, 定义点)。"""
        self.ctx.count_node()
        node.provenance = list(prefix)
        node.provenance.append(ExpansionRecord(macro.name, call_span, def_span))
        return node

    def _clone_arg(self, node: Node) -> Node:
        """深拷贝调用方实参，保留其原有位置与映射信息。"""
        self.ctx.count_node()
        provenance = list(node.provenance)
        if isinstance(node, Sym):
            return Sym(span=node.span, name=node.name, provenance=provenance)
        if isinstance(node, IntLit):
            return IntLit(span=node.span, value=node.value, provenance=provenance)
        if isinstance(node, StrLit):
            return StrLit(span=node.span, value=node.value, provenance=provenance)
        if isinstance(node, Form):
            return Form(
                span=node.span,
                items=[self._clone_arg(item) for item in node.items],
                provenance=provenance,
            )
        raise MacroExpansionError(  # pragma: no cover - 防御性分支
            f"cannot clone node of type {type(node).__name__}", self.ctx.chain
        )
