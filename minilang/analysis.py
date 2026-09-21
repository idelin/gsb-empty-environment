"""数据流分析。

1. 到达定义（reaching definitions）：前向 may 分析，汇合取并集，
   工作list迭代到不动点（循环回边自然收敛）。
2. 一定已定义（definitely defined）：前向 must 分析，汇合取交集。
   因此只在 if 一支赋值的变量在汇合点后不算已定义；
   循环条件结点有“零次循环”的入边，循环体内的赋值也不会传播到循环后。
3. 诊断：引用点前“一定已定义”集合不含该变量 → 报“使用前可能未定义”；
   从 entry 不可达的语句单独标记为 unreachable。
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .cfg import CFG, Block
from .nodes import Bin, Expr, Neg, Num, Program, Var

Def = tuple[str, int]  # (变量名, 定义所在 block id)


@dataclass
class Diagnostic:
    kind: str        # 'undefined-use' | 'unreachable'
    message: str
    span: tuple[int, int]


@dataclass
class AnalysisResult:
    cfg: CFG
    reachable: set[int]
    reach_in: dict[int, set[Def]]
    reach_out: dict[int, set[Def]]
    def_in: dict[int, set[str]]
    def_out: dict[int, set[str]]
    diagnostics: list[Diagnostic]


def expr_vars(expr: Expr) -> list[tuple[str, tuple[int, int]]]:
    """按求值顺序收集表达式中的变量引用（名字 + 字节区间）。"""
    if isinstance(expr, Var):
        return [(expr.name, expr.span)]
    if isinstance(expr, Num):
        return []
    if isinstance(expr, Neg):
        return expr_vars(expr.operand)
    if isinstance(expr, Bin):
        return expr_vars(expr.left) + expr_vars(expr.right)
    raise TypeError(f"未知表达式类型: {type(expr).__name__}")


def _reachable_blocks(cfg: CFG) -> set[int]:
    seen = {cfg.entry.id}
    stack = [cfg.entry]
    while stack:
        block = stack.pop()
        for nxt in block.succs:
            if nxt.id not in seen:
                seen.add(nxt.id)
                stack.append(nxt)
    return seen


def _block_uses(block: Block) -> list[tuple[str, tuple[int, int]]]:
    if block.kind == "assign":
        return expr_vars(block.stmt.value)
    if block.kind == "print":
        return expr_vars(block.stmt.value)
    if block.kind == "cond":
        return expr_vars(block.cond)
    return []


def analyze(program: Program, cfg: CFG) -> AnalysisResult:
    reachable = _reachable_blocks(cfg)
    rblocks = [b for b in cfg.blocks if b.id in reachable]

    defs_by_var: dict[str, set[int]] = {}
    for b in rblocks:
        if b.kind == "assign":
            defs_by_var.setdefault(b.stmt.name, set()).add(b.id)

    # ---- 到达定义：IN = ∪ OUT(p)；OUT = gen ∪ (IN − kill) ----
    gen: dict[int, set[Def]] = {}
    kill: dict[int, set[Def]] = {}
    for b in rblocks:
        if b.kind == "assign":
            var = b.stmt.name
            gen[b.id] = {(var, b.id)}
            kill[b.id] = {(var, d) for d in defs_by_var[var] if d != b.id}
        else:
            gen[b.id] = set()
            kill[b.id] = set()

    reach_in: dict[int, set[Def]] = {b.id: set() for b in rblocks}
    reach_out: dict[int, set[Def]] = {b.id: set() for b in rblocks}
    worklist = deque(rblocks)
    while worklist:
        b = worklist.popleft()
        incoming: set[Def] = set()
        for p in b.preds:
            if p.id in reachable:
                incoming |= reach_out[p.id]
        reach_in[b.id] = incoming
        new_out = gen[b.id] | (incoming - kill[b.id])
        if new_out != reach_out[b.id]:
            reach_out[b.id] = new_out
            for s in b.succs:
                if s.id in reachable:
                    worklist.append(s)

    # ---- 一定已定义：IN = ∩ OUT(p)；OUT = IN ∪ {本结点定义的变量} ----
    universe = set(defs_by_var)  # 程序中所有被赋值过的变量
    def_in: dict[int, set[str]] = {b.id: set(universe) for b in rblocks}
    def_out: dict[int, set[str]] = {b.id: set(universe) for b in rblocks}
    def_in[cfg.entry.id] = set()
    def_out[cfg.entry.id] = set()
    worklist = deque(b for b in rblocks if b.id != cfg.entry.id)
    while worklist:
        b = worklist.popleft()
        incoming: set[str] | None = None
        for p in b.preds:
            if p.id not in reachable:
                continue
            incoming = set(def_out[p.id]) if incoming is None else incoming & def_out[p.id]
        if incoming is None:  # 只有 entry 没有前驱
            incoming = set()
        def_in[b.id] = incoming
        new_out = set(incoming)
        if b.kind == "assign":
            new_out.add(b.stmt.name)
        if new_out != def_out[b.id]:
            def_out[b.id] = new_out
            for s in b.succs:
                if s.id in reachable:
                    worklist.append(s)

    # ---- 诊断 ----
    diagnostics: list[Diagnostic] = []
    for b in sorted(rblocks, key=lambda x: x.id):
        for name, span in _block_uses(b):
            if name not in def_in[b.id]:
                diagnostics.append(Diagnostic(
                    "undefined-use",
                    f"变量 {name!r} 在使用前可能未定义",
                    span,
                ))
    for b in cfg.blocks:
        if b.id not in reachable and b.kind not in ("entry", "exit") and b.span:
            diagnostics.append(Diagnostic(
                "unreachable",
                f"不可达语句（{b.kind}）",
                b.span,
            ))
    diagnostics.sort(key=lambda d: d.span[0])

    return AnalysisResult(cfg, reachable, reach_in, reach_out,
                          def_in, def_out, diagnostics)
