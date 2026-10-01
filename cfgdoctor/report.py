"""汇总诊断报告:一次 analyze() 跑齐全部分析并格式化输出。"""
from dataclasses import dataclass

from .bounded_enum import cross_check
from .chains import shortest_problem_chains
from .cycles import analyze_language_infiniteness, find_epsilon_cycles
from .generating import compute_generating, compute_nullable
from .grammar import format_symbol_string
from .reachability import compute_reachable
from .simplify import simplify


@dataclass
class Report:
    grammar: object
    cert: object
    nullable: object
    reachable: frozenset
    cycles: tuple
    chains: dict
    simplification: object
    infiniteness: object
    cross: object


def analyze(grammar, with_cross_check=True):
    cert = compute_generating(grammar)
    nullable = compute_nullable(grammar)
    reachable = compute_reachable(grammar)
    cycles = tuple(find_epsilon_cycles(grammar, cert, nullable, reachable))
    chains = shortest_problem_chains(grammar, cert, reachable)
    simpl = simplify(grammar, cert, reachable)
    inf = analyze_language_infiniteness(grammar, cert, nullable, reachable)
    cross = cross_check(grammar, cert, reachable) if with_cross_check else None
    return Report(grammar, cert, nullable, reachable, cycles, chains,
                  simpl, inf, cross)


def format_report(rep):
    g = rep.grammar
    out = []
    add = out.append
    add("== 字母表与转移结构(显式输入) ==")
    add(f"终结符: {{{', '.join(sorted(g.terminals))}}}")
    add(f"非终结符: {{{', '.join(sorted(g.nonterminals))}}}; 开始符: {g.start}")
    for i, p in enumerate(g.productions):
        add(f"  #{i}: {p}")
    add("")
    add("== 可生成性: 最小不动点证书 ==")
    for a in sorted(rep.cert.generating):
        add(f"  可生成 {a}: round={rep.cert.rounds[a]}, "
            f"见证产生式 #{rep.cert.witness_production[a]}, "
            f"见证串 {format_symbol_string(rep.cert.witness_string[a])}")
    for a in sorted(rep.cert.non_generating):
        add(f"  不可生成 {a}")
    if not rep.cert.non_generating:
        add("  (全部非终结符均可生成)")
    add("")
    add("== 可达性 ==")
    add(f"  可达符号: {{{', '.join(sorted(rep.reachable))}}}")
    unreachable = sorted(g.nonterminals - rep.reachable)
    if unreachable:
        add(f"  不可达符号: {{{', '.join(unreachable)}}}")
    else:
        add("  (全部可达)")
    add("")
    add("== 最短问题链 ==")
    if not rep.chains:
        add("  (无问题符号)")
    for a, ch in sorted(rep.chains.items()):
        if ch.kind == "non-generating":
            route = " ".join(f"{frm} -#{pi}->" for frm, pi in ch.chain)
            route = (route + " ") if route else ""
            add(f"  [{a}] 不可生成: {route}核心[{ch.core}]")
        else:
            add(f"  [{a}] 不可达: {ch.core}")
    add("")
    add("== epsilon 环诊断 ==")
    if not rep.cycles:
        add("  (无 epsilon 环)")
    for c in rep.cycles:
        add(f"  {c.describe()}")
        add(f"    循环见证(可独立执行): {c.regeneration_steps}")
        if c.ambiguous_string is not None:
            suffix = "" if c.reachable else "(不可达片段内, 不属于语言)"
            add(f"    无限多推导的终结串: "
                f"{format_symbol_string(c.ambiguous_string)}{suffix}")
    add("")
    add("== 无限歧义 vs 语言无限性(分别诊断) ==")
    amb = [c for c in rep.cycles if c.generating]
    if amb:
        strs = sorted({format_symbol_string(c.ambiguous_string) for c in amb})
        add(f"  无限歧义: 终结串 {strs} 有无限多推导(源于 epsilon 环)")
    else:
        add("  无限歧义: 未发现由 epsilon 环引起的无限推导")
    if rep.infiniteness.language_infinite:
        add(f"  语言无限: 是 — {rep.infiniteness.explanation}")
    else:
        add("  语言无限: 否(同一终结串的无限推导不蕴含语言无限)")
    add("")
    add("== 无用规则清理映射 ==")
    for e in rep.simplification.entries:
        add(f"  #{e.index} [{e.action}] {e.production}   -- {e.reason}")
    add("")
    if rep.simplification.empty_language:
        add("== 等价简化 ==")
        add(f"  {rep.simplification.explanation}")
    else:
        add("== 等价简化文法 ==")
        add(f"  {rep.simplification.explanation}")
        sg = rep.simplification.grammar
        add(f"  非终结符: {{{', '.join(sorted(sg.nonterminals))}}}; "
            f"终结符: {{{', '.join(sorted(sg.terminals))}}}")
        for i, p in enumerate(sg.productions):
            add(f"  #{i}: {p}")
        add(f"  旧→新映射: {rep.simplification.mapping}")
    if rep.cross is not None:
        add("")
        add("== 交叉核验(有界枚举 + 独立可达分析) ==")
        add(f"  见证串复现: {rep.cross.witness_confirmed}")
        add(f"  负向一致性(界={rep.cross.negative_bound}): "
            f"{rep.cross.negative_consistent}")
        add(f"  可达性独立复核一致: {rep.cross.reachability_agrees}")
        for n in rep.cross.notes:
            add(f"  注: {n}")
    return "\n".join(out)
