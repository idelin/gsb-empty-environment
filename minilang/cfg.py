"""Control-flow graph construction.

One node per statement (plus entry/exit). Key structural choices:
  - if: edges into both branches; the join merges both branch exits
    (and the condition node itself when there is no else branch).
  - while: the loop-exit edge leaves the condition node directly, so
    the zero-iteration path is always represented; body exits loop
    back to the condition node.
  - halt: no outgoing edge; following statements become unreachable.
  - constant conditions are folded, pruning impossible edges.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .ast_nodes import Halt, If, Stmt, While
from .interpreter import const_value


@dataclass(eq=False)
class CFGNode:
    id: int
    stmt: Stmt | None  # None for the synthetic entry/exit nodes
    succs: list[CFGNode] = field(default_factory=list)
    preds: list[CFGNode] = field(default_factory=list)

    @property
    def label(self) -> str:
        if self.stmt is None:
            return "entry" if self.id == 0 else "exit"
        return type(self.stmt).__name__


@dataclass
class CFG:
    entry: CFGNode
    exit: CFGNode
    nodes: list[CFGNode]


def build_cfg(program: list[Stmt]) -> CFG:
    builder = _Builder()
    return builder.build(program)


def reachable_nodes(cfg: CFG) -> set[int]:
    seen = {cfg.entry.id}
    stack = [cfg.entry]
    while stack:
        node = stack.pop()
        for succ in node.succs:
            if succ.id not in seen:
                seen.add(succ.id)
                stack.append(succ)
    return seen


class _Builder:
    def __init__(self):
        self.nodes: list[CFGNode] = []
        self.entry = self._node(None)
        self.exit = self._node(None)

    def _node(self, stmt: Stmt | None) -> CFGNode:
        node = CFGNode(len(self.nodes), stmt)
        self.nodes.append(node)
        return node

    @staticmethod
    def _edge(src: CFGNode, dst: CFGNode) -> None:
        if dst not in src.succs:
            src.succs.append(dst)
            dst.preds.append(src)

    def build(self, program: list[Stmt]) -> CFG:
        open_nodes = self._block(program, [self.entry])
        for node in open_nodes:
            self._edge(node, self.exit)
        return CFG(self.entry, self.exit, self.nodes)

    def _block(self, stmts: list[Stmt], open_nodes: list[CFGNode]) -> list[CFGNode]:
        for stmt in stmts:
            open_nodes = self._stmt(stmt, open_nodes)
        return open_nodes

    def _stmt(self, stmt: Stmt, open_nodes: list[CFGNode]) -> list[CFGNode]:
        if isinstance(stmt, If):
            node = self._node(stmt)
            for pred in open_nodes:
                self._edge(pred, node)
            cond = const_value(stmt.cond)
            exits: list[CFGNode] = []
            if cond is None or cond != 0:
                exits += self._block(stmt.then_body, [node])
            else:
                self._block(stmt.then_body, [])  # dead branch
            if stmt.else_body:
                if cond is None or cond == 0:
                    exits += self._block(stmt.else_body, [node])
                else:
                    self._block(stmt.else_body, [])  # dead branch
            elif cond is None or cond == 0:
                exits.append(node)  # no else: condition node falls through
            return exits
        if isinstance(stmt, While):
            node = self._node(stmt)
            for pred in open_nodes:
                self._edge(pred, node)
            cond = const_value(stmt.cond)
            if cond is not None and cond == 0:
                self._block(stmt.body, [])  # body never runs
                return [node]
            body_exits = self._block(stmt.body, [node])
            for body_exit in body_exits:
                self._edge(body_exit, node)  # back edge
            if cond is not None:
                return []  # constant-true loop: no normal exit
            return [node]  # zero-iteration path
        node = self._node(stmt)
        for pred in open_nodes:
            self._edge(pred, node)
        if isinstance(stmt, Halt):
            return []  # nothing executes after halt
        return [node]
