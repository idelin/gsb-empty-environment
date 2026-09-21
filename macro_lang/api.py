"""对外 API:展开源码 / 执行源码。"""
from __future__ import annotations

from .expand import Expander
from .interp import eval_node, make_global_env
from .parser import parse


def expand_source(source: str, *, max_depth: int = 64, max_nodes: int = 20000):
    """解析并展开,返回 (展开后的 AST 列表, Expander)。Expander.source_map
    持有全部展开产物的"定义处 + 调用处"双重源码映射。"""
    expander = Expander(max_depth=max_depth, max_nodes=max_nodes)
    expanded = expander.expand_program(parse(source))
    return expanded, expander


def run_source(source: str, *, max_depth: int = 64, max_nodes: int = 20000, emit=print):
    """解析、展开并解释执行,返回 (各顶层表达式的值, Expander)。"""
    expanded, expander = expand_source(
        source, max_depth=max_depth, max_nodes=max_nodes
    )
    env = make_global_env(emit)
    values = [eval_node(node, env) for node in expanded]
    return values, expander
