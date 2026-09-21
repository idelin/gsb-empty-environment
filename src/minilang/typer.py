from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .common import Span, TypeInferenceError
from .nodes import App, BinOp, BoolLit, If, IntLit, Lam, Let, Var

# Global constraint-graph vertex ids:
#   2 * node_id      -> the node itself (a type variable slot / type root)
#   2 * node_id + 1  -> the "head vertex" standing for its head symbol (Int/Bool/->)
# Edge kinds used for explaining a conflict:
#   "unify"  : two type nodes were declared equal
#   "commit" : a node was fixed to a head symbol
#   "child"  : a head's argument/result child slot
_KIND_NAME = {"int": "Int", "bool": "Bool", "arrow": "a function type"}


@dataclass
class _Node:
    ident: int
    parent: int
    level: int
    origin: tuple[Span, str] | None  # where this type variable was introduced
    kind: str | None = None
    origin_struct: tuple[Span, str] | None = None  # for kind "arrow"
    children: tuple[int, int] | None = None  # (parameter, result) for arrow


@dataclass
class _Edge:
    a: int
    b: int
    span: Span
    note: str
    kind: str  # unify | commit | child
    pending: bool = False  # the unify edge that failed (for clash paths)


class Solver:
    def __init__(self, source: bytes):
        self.source = source
        self.nodes: list[_Node] = []
        self.edges: list[_Edge] = []
        self.var_index = 0

    # ---------- basic construction ----------

    def fresh(self, origin: tuple[Span, str] | None = None, level: int = 0) -> int:
        node_id = len(self.nodes)
        self.nodes.append(_Node(ident=node_id, parent=node_id, level=level, origin=origin))
        return node_id

    def _edge(self, a: int, b: int, span: Span, note: str, kind: str, pending: bool = False):
        self.edges.append(_Edge(a, b, span, note, kind, pending))

    def commit_int(self, node_id: int, span: Span, note: str):
        self._edge(2 * node_id, 2 * node_id + 1, span, note, "commit")
        root = self.find(node_id)
        self.nodes[root].kind = "int"
        self.nodes[root].origin_struct = (span, note)

    def commit_bool(self, node_id: int, span: Span, note: str):
        self._edge(2 * node_id, 2 * node_id + 1, span, note, "commit")
        root = self.find(node_id)
        self.nodes[root].kind = "bool"
        self.nodes[root].origin_struct = (span, note)

    def make_arrow(self, param: int, result: int, span: Span, note: str, level: int = 0) -> int:
        node_id = self.fresh(level=level)
        self.nodes[node_id].kind = "arrow"
        self.nodes[node_id].origin_struct = (span, note)
        self.nodes[node_id].children = (param, result)
        self._edge(2 * node_id, 2 * node_id + 1, span, note, "commit")
        self._edge(2 * node_id + 1, 2 * param, span, "its parameter", "child")
        self._edge(2 * node_id + 1, 2 * result, span, "its result", "child")
        return node_id

    # ---------- union find ----------

    def find(self, node_id: int) -> int:
        root = node_id
        while self.nodes[root].parent != root:
            root = self.nodes[root].parent
        while self.nodes[node_id].parent != node_id:
            nxt = self.nodes[node_id].parent
            self.nodes[node_id].parent = root
            node_id = nxt
        return root

    def _merge(self, a: int, b: int):
        ra, rb = self.find(a), self.find(b)
        if ra == rb:
            return
        # Structured representatives keep their identity; otherwise merge by id.
        if self.nodes[ra].kind is None and self.nodes[rb].kind is not None:
            ra, rb = rb, ra
        self.nodes[rb].parent = ra
        if self.nodes[rb].level < self.nodes[ra].level:
            self.nodes[ra].level = self.nodes[rb].level

    # ---------- unification ----------

    def occurs(self, target: int, root: int) -> bool:
        """Whether the live variable `target` occurs inside the type rooted at `root`."""
        root = self.find(root)
        target = self.find(target)
        if root == target:
            return True
        children = self.nodes[root].children
        if children is None:
            return False
        stack = [self.find(children[0]), self.find(children[1])]
        seen = set()
        while stack:
            cur = self.find(stack.pop())
            if cur == target:
                return True
            if cur in seen:
                continue
            seen.add(cur)
            ch = self.nodes[cur].children
            if ch is not None:
                stack.append(self.find(ch[0]))
                stack.append(self.find(ch[1]))
        return False

    def unify(self, a: int, b: int, span: Span, note: str):
        """Declare types a and b equal, recording the constraint (span, note)."""
        ra, rb = self.find(a), self.find(b)
        if ra == rb:
            self._edge(2 * a, 2 * b, span, note, "unify")
            return
        ka, kb = self.nodes[ra].kind, self.nodes[rb].kind

        if ka == "arrow" and kb == "arrow":
            if self.occurs(ra, rb) or self.occurs(rb, ra):
                cycle = self._cycle_path(ra, rb, span, note)
                raise self._occurs_error(cycle, span, ra)
            self._edge(2 * a, 2 * b, span, note, "unify")
            self._merge(ra, rb)
            new_root = self.find(ra)
            pa, qa = self.nodes[ra].children
            pb, qb = self.nodes[rb].children
            origin = self.nodes[ra].origin_struct or self.nodes[rb].origin_struct
            if self.nodes[new_root].origin_struct is None and origin is not None:
                self.nodes[new_root].origin_struct = origin
            self.unify(pa, pb, span, "matching function parameters")
            self.unify(qa, qb, span, "matching function results")
            return

        if ka is not None and kb is not None and ka != kb:
            path = self._clash_path(ra, rb, a, b, span, note)
            raise self._clash_error(ka, kb, path, span)

        if ka == "arrow" or kb == "arrow":
            var = rb if ka == "arrow" else ra
            struct = ra if ka == "arrow" else rb
            if self.occurs(var, struct):
                cycle = self._cycle_path(var, struct, span, note)
                raise self._occurs_error(cycle, span, var)

        self._edge(2 * a, 2 * b, span, note, "unify")
        # _merge keeps the structured side as representative, so head kind,
        # arrow children and their spans survive on the new root.
        self._merge(ra, rb)

    # ---------- explanation paths ----------

    def _adjacency(self, pending: tuple[int, int, Span, str] | None):
        adj: dict[int, list[tuple[int, int]]] = {}
        for idx, edge in enumerate(self.edges):
            adj.setdefault(edge.a, []).append((edge.b, idx))
            adj.setdefault(edge.b, []).append((edge.a, idx))
        if pending is not None:
            va, vb, _, _ = pending
            pid = len(self.edges)
            adj.setdefault(va, []).append((vb, pid))
            adj.setdefault(vb, []).append((va, pid))
        return adj, pid if pending is not None else -1

    def _edge_view(self, idx: int, pending):
        if pending is not None and idx == len(self.edges):
            va, vb, span, note = pending
            return _Edge(va, vb, span, note, "unify", pending=True)
        return self.edges[idx]

    def _clash_path(self, ra, rb, a, b, span, note) -> list[_Edge]:
        start, goal = 2 * ra + 1, 2 * rb + 1
        pending = (2 * a, 2 * b, span, note)
        adj, pid = self._adjacency(pending)
        return self._bfs(start, goal, adj, pid, pending, require_child=False)

    def _cycle_path(self, var, struct, span, note) -> list[_Edge]:
        # The pending constraint closes a cycle through the variable's occurrence.
        pending = (2 * var, 2 * struct, span, note)
        adj, pid = self._adjacency(pending)
        return self._bfs(2 * var, 2 * var, adj, pid, pending, require_child=True)

    def _bfs(self, start, goal, adj, pid, pending, require_child: bool) -> list[_Edge]:
        # State: (vertex, used_child). BFS over edges; no immediate bounce back.
        queue = deque([(start, False, -1, [])])
        seen = {(start, False)}
        while queue:
            vert, used_child, came_edge, path = queue.popleft()
            if vert == goal and path and (not require_child or used_child):
                return [self._edge_view(i, pending) for i in path]
            for nxt, edge_idx in adj.get(vert, ()):
                if edge_idx == came_edge:
                    continue
                view = self._edge_view(edge_idx, pending)
                state = (nxt, used_child or view.kind == "child")
                if state in seen:
                    continue
                seen.add(state)
                queue.append((nxt, state[1], edge_idx, path + [edge_idx]))
        return []

    def _clash_error(self, ka, kb, path, span):
        links = [(e.span, self._describe(e)) for e in path]
        summary = f"type mismatch: cannot use {_KIND_NAME[kb]} where {_KIND_NAME[ka]} is required"
        return TypeInferenceError(summary, links, "clash")

    def _occurs_error(self, cycle, span, start_node):
        links = [(e.span, self._describe(e)) for e in cycle]
        origin = self.nodes[start_node].origin
        if origin is not None:
            # Close the loop: say which variable the cycle returns to.
            links.append((origin[0], "returning to " + origin[1] + " (it occurs inside itself)"))
        summary = "cannot construct the infinite type produced by self-application"
        return TypeInferenceError(summary, links, "occurs")

    def _describe(self, edge: _Edge) -> str:
        if edge.kind == "commit":
            return edge.span.note or edge.note
        if edge.kind == "child":
            return edge.note
        text = edge.span.note or edge.note
        if edge.pending:
            return f"required equal here: {text}"
        return text


# ---------- immutable type schemes (frozen at generalization time) ----------
# tree shapes:
#   ("gv", ident)                generic variable, fresh on each instantiation
#   ("var", live_root_id, None)  monomorphic variable sharing one live slot
#   ("int", span_note)
#   ("bool", span_note)
#   ("arrow", span, note, param_tree, result_tree)


class TypeChecker:
    def __init__(self, source: bytes):
        self.solver = Solver(source)
        self.source = source

    def _span(self, node, note: str) -> Span:
        return Span(node.start, node.end, note)

    # ---- generalization machinery ----

    def _freeze(self, node_id: int, limit_level: int):
        root = self.solver.find(node_id)
        info = self.solver.nodes[root]
        if info.kind == "arrow":
            param, result = info.children
            ospan, onote = info.origin_struct or (self._span_dummy(), "a function")
            return (
                "arrow",
                ospan,
                onote,
                self._freeze(param, limit_level),
                self._freeze(result, limit_level),
            )
        if info.kind == "int":
            return ("con", "int")
        if info.kind == "bool":
            return ("con", "bool")
        if info.level > limit_level:
            return ("gv", root)
        return ("var", root)

    def _span_dummy(self) -> Span:
        return Span(0, 0, "")

    def instantiate(self, tree, usage: tuple[Span, str]):
        # Each generic variable maps to exactly one fresh slot per instantiation,
        # so a scheme like a -> a stays a -> a instead of a -> b.
        self._inst_cache: dict = {}
        return self._instantiate(tree, usage)

    def _instantiate(self, tree, usage: tuple[Span, str]):
        s = self.solver
        tag = tree[0]
        if tag == "con":
            node = s.fresh(origin=usage)
            if tree[1] == "int":
                s.commit_int(node, usage[0], usage[1])
            else:
                s.commit_bool(node, usage[0], usage[1])
            return node
        if tag == "gv":
            key = ("gv", tree[1])
            if key not in self._inst_cache:
                self._inst_cache[key] = s.fresh(origin=usage)
            return self._inst_cache[key]
        if tag == "var":
            return tree[1]
        _, ospan, onote, param_tree, result_tree = tree
        param = self._instantiate(param_tree, (ospan, "instantiated parameter of " + onote))
        result = self._instantiate(result_tree, (ospan, "instantiated result of " + onote))
        return s.make_arrow(param, result, ospan, onote)

    # ---- inference ----

    def infer(self, node, env: dict[str, tuple], level: int) -> int:
        s = self.solver
        if isinstance(node, IntLit):
            tv = s.fresh(level=level)
            s.commit_int(tv, self._span(node, f"integer literal {node.value}"), "an Int here")
            return tv
        if isinstance(node, BoolLit):
            tv = s.fresh(level=level)
            s.commit_bool(
                tv,
                self._span(node, f"boolean literal {'true' if node.value else 'false'}"),
                "a Bool here",
            )
            return tv
        if isinstance(node, Var):
            if node.name not in env:
                from .common import RuntimeErr as _Undef
                raise _Undef(f"unbound variable {node.name!r} at byte {node.start}")
            tree = env[node.name]
            return self.instantiate(tree, (self._span(node, f"use of {node.name!r}"), "a use of the bound value"))
        if isinstance(node, Lam):
            param_tv = s.fresh(
                origin=(
                    Span(node.param_start, node.param_end, f"parameter {node.param!r}"),
                    "the function parameter",
                ),
                level=level,
            )
            body_env = dict(env)
            body_env[node.param] = ("var", param_tv)
            body_tv = self.infer(node.body, body_env, level)
            return s.make_arrow(
                param_tv,
                body_tv,
                self._span(node, "this function"),
                f"function with parameter {node.param!r}",
                level=level,
            )
        if isinstance(node, App):
            fn_tv = self.infer(node.fn, env, level)
            arg_tv = self.infer(node.arg, env, level)
            result_tv = s.fresh(
                origin=(self._span(node, "the application result"), "the result of the application"),
                level=level,
            )
            arrow_tv = s.make_arrow(
                arg_tv,
                result_tv,
                self._span(node.fn, f"callee at this application"),
                "the value is applied as a function here",
                level=level,
            )
            s.unify(fn_tv, arrow_tv, self._span(node, "this function application"), "callee must be a function")
            return result_tv
        if isinstance(node, BinOp):
            left = self.infer(node.left, env, level)
            right = self.infer(node.right, env, level)
            s.unify(
                left,
                right,
                Span(node.op_start, node.op_end, f"operator {node.op!r}"),
                f"operator {node.op!r} requires both operands to share a type",
            )
            result = s.fresh(level=level)
            if node.op in ("+", "-", "*", "/"):
                s.commit_int(
                    result,
                    Span(node.op_start, node.op_end, f"operator {node.op!r}"),
                    f"operator {node.op!r} produces an Int",
                )
                s.unify(
                    left,
                    self._commit_fresh("int", level, node),
                    Span(node.op_start, node.op_end, f"operator {node.op!r}"),
                    f"operator {node.op!r} requires Int operands",
                )
                return result
            if node.op in ("<", ">", "<=", ">="):
                s.commit_bool(
                    result,
                    Span(node.op_start, node.op_end, f"operator {node.op!r}"),
                    f"operator {node.op!r} produces a Bool",
                )
                s.unify(
                    left,
                    self._commit_fresh("int", level, node),
                    Span(node.op_start, node.op_end, f"operator {node.op!r}"),
                    f"operator {node.op!r} requires Int operands",
                )
                return result
            # equality is parametric: any (shared) type, result Bool
            s.commit_bool(
                result,
                Span(node.op_start, node.op_end, f"operator {node.op!r}"),
                f"operator {node.op!r} produces a Bool",
            )
            return result
        if isinstance(node, If):
            cond = self.infer(node.cond, env, level)
            then_tv = self.infer(node.then, env, level)
            else_tv = self.infer(node.otherwise, env, level)
            s.unify(
                cond,
                self._commit_fresh("bool", level, node.cond),
                self._span(node.cond, "the condition"),
                "the condition must be a Bool",
            )
            s.unify(then_tv, else_tv, self._span(node, "the if-expression"), "both branches must share a type")
            return then_tv
        if isinstance(node, Let):
            rhs_tv = self.infer(node.rhs, env, level + 1)
            scheme = self._freeze(rhs_tv, level)
            body_env = dict(env)
            body_env[node.name] = scheme
            return self.infer(node.body, body_env, level)
        raise AssertionError(f"unhandled node {node!r}")

    def _commit_fresh(self, kind: str, level: int, node) -> int:
        tv = self.solver.fresh(level=level)
        if kind == "int":
            self.solver.commit_int(tv, self._span(node, "an Int requirement"), "required Int")
        else:
            self.solver.commit_bool(tv, self._span(node, "a Bool requirement"), "required Bool")
        return tv

    # ---- result reconstruction ----

    def result_type(self, root_node) -> str:
        root = self.solver.find(self.infer(root_node, {}, 0))
        names: dict[int, str] = {}
        counter = [0]

        def render(node_id: int) -> str:
            info = self.solver.nodes[self.solver.find(node_id)]
            if info.kind == "int":
                return "Int"
            if info.kind == "bool":
                return "Bool"
            if info.kind == "arrow":
                p, q = info.children
                rp, rq = render(p), render(q)
                if self.solver.nodes[self.solver.find(p)].kind == "arrow":
                    rp = f"({rp})"
                return f"{rp} -> {rq}"
            rid = self.solver.find(node_id)
            if rid not in names:
                names[rid] = chr(ord("a") + counter[0]) if counter[0] < 26 else f"t{counter[0]}"
                counter[0] += 1
            return names[rid]

        return render(root)


def infer_program(source: bytes, ast) -> str:
    return TypeChecker(source).result_type(ast)
