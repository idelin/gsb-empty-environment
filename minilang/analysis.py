"""Dataflow analyses over the CFG.

  - reaching definitions (may-analysis, union at joins, fixed point)
  - definitely-assigned variables (must-analysis, intersection at joins,
    fixed point) -- a variable assigned on only one path is NOT in the
    definitely-assigned set after the join
  - use-before-def diagnostics derived from the two analyses
  - unreachable statements, reported separately
"""
from __future__ import annotations

from dataclasses import dataclass

from .ast_nodes import Assign, BinOp, Expr, If, Let, Print, Unary, Var, While
from .cfg import CFG, CFGNode, reachable_nodes


@dataclass
class Diagnostic:
    level: str  # "error" | "warning" | "info"
    message: str
    start: int
    end: int


@dataclass
class AnalysisResult:
    cfg: CFG
    reachable: set[int]
    unreachable_nodes: list[CFGNode]
    reaching_in: dict[int, set[tuple[str, int]]]
    reaching_out: dict[int, set[tuple[str, int]]]
    assigned_in: dict[int, set[str]]
    assigned_out: dict[int, set[str]]
    diagnostics: list[Diagnostic]


def _expr_uses(expr: Expr, out: list[tuple[str, int, int]]) -> None:
    if isinstance(expr, Var):
        out.append((expr.name, expr.start, expr.end))
    elif isinstance(expr, Unary):
        _expr_uses(expr.operand, out)
    elif isinstance(expr, BinOp):
        _expr_uses(expr.left, out)
        _expr_uses(expr.right, out)


def _node_def_use(node: CFGNode) -> tuple[set[str], list[tuple[str, int, int]]]:
    stmt = node.stmt
    defs: set[str] = set()
    uses: list[tuple[str, int, int]] = []
    if isinstance(stmt, (Let, Assign)):
        defs.add(stmt.name)
        _expr_uses(stmt.value, uses)
    elif isinstance(stmt, Print):
        _expr_uses(stmt.value, uses)
    elif isinstance(stmt, (If, While)):
        _expr_uses(stmt.cond, uses)
    return defs, uses


def _reaching_definitions(
    cfg: CFG, defs_map: dict[int, set[str]]
) -> tuple[dict[int, set[tuple[str, int]]], dict[int, set[tuple[str, int]]]]:
    all_defs: set[tuple[str, int]] = set()
    for nid, names in defs_map.items():
        for name in names:
            all_defs.add((name, nid))
    gen: dict[int, set[tuple[str, int]]] = {}
    kill: dict[int, set[tuple[str, int]]] = {}
    for node in cfg.nodes:
        names = defs_map.get(node.id, set())
        gen[node.id] = {(name, node.id) for name in names}
        kill[node.id] = {d for d in all_defs if d[0] in names} - gen[node.id]
    in_sets = {node.id: set() for node in cfg.nodes}
    out_sets = {node.id: set() for node in cfg.nodes}
    changed = True
    while changed:
        changed = False
        for node in cfg.nodes:
            new_in: set[tuple[str, int]] = set()
            for pred in node.preds:
                new_in |= out_sets[pred.id]
            new_out = gen[node.id] | (new_in - kill[node.id])
            if new_in != in_sets[node.id] or new_out != out_sets[node.id]:
                in_sets[node.id] = new_in
                out_sets[node.id] = new_out
                changed = True
    return in_sets, out_sets


def _definitely_assigned(
    cfg: CFG, reachable: set[int], defs_map: dict[int, set[str]]
) -> tuple[dict[int, set[str]], dict[int, set[str]]]:
    all_vars: set[str] = set()
    for names in defs_map.values():
        all_vars |= names
    in_sets: dict[int, set[str]] = {}
    out_sets: dict[int, set[str]] = {}
    for node in cfg.nodes:
        if node.id == cfg.entry.id:
            in_sets[node.id] = set()
        elif node.id in reachable:
            in_sets[node.id] = set(all_vars)  # top element for intersection
        else:
            in_sets[node.id] = set()  # unreachable: nothing is known
        out_sets[node.id] = in_sets[node.id] | defs_map.get(node.id, set())
    changed = True
    while changed:
        changed = False
        for node in cfg.nodes:
            if node.id == cfg.entry.id or node.id not in reachable:
                continue
            preds = [p for p in node.preds if p.id in reachable]
            if preds:
                new_in = set(all_vars)
                for pred in preds:
                    new_in &= out_sets[pred.id]
            else:
                new_in = set()
            new_out = new_in | defs_map.get(node.id, set())
            if new_in != in_sets[node.id] or new_out != out_sets[node.id]:
                in_sets[node.id] = new_in
                out_sets[node.id] = new_out
                changed = True
    return in_sets, out_sets


def analyze(cfg: CFG) -> AnalysisResult:
    reachable = reachable_nodes(cfg)
    defs_map: dict[int, set[str]] = {}
    uses_map: dict[int, list[tuple[str, int, int]]] = {}
    for node in cfg.nodes:
        defs, uses = _node_def_use(node)
        defs_map[node.id] = defs
        uses_map[node.id] = uses

    reaching_in, reaching_out = _reaching_definitions(cfg, defs_map)
    assigned_in, assigned_out = _definitely_assigned(cfg, reachable, defs_map)

    unreachable_nodes = [
        node
        for node in cfg.nodes
        if node.stmt is not None and node.id not in reachable
    ]

    diagnostics: list[Diagnostic] = []
    for node in cfg.nodes:
        if node.stmt is None or node.id not in reachable:
            continue
        for name, start, end in uses_map[node.id]:
            if name in assigned_in[node.id]:
                continue
            if any(d[0] == name for d in reaching_in[node.id]):
                diagnostics.append(
                    Diagnostic(
                        "warning",
                        f"{name!r} may be used before definition "
                        "(not assigned on all paths)",
                        start,
                        end,
                    )
                )
            else:
                diagnostics.append(
                    Diagnostic(
                        "error", f"use of undefined variable {name!r}", start, end
                    )
                )
    for node in unreachable_nodes:
        diagnostics.append(
            Diagnostic("info", "unreachable statement", node.stmt.start, node.stmt.end)
        )
    diagnostics.sort(key=lambda d: (d.start, d.level))

    return AnalysisResult(
        cfg=cfg,
        reachable=reachable,
        unreachable_nodes=unreachable_nodes,
        reaching_in=reaching_in,
        reaching_out=reaching_out,
        assigned_in=assigned_in,
        assigned_out=assigned_out,
        diagnostics=diagnostics,
    )
