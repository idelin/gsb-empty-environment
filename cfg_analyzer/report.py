"""诊断编排：把各分析器的结果汇总为 Diagnosis，并生成中文报告。"""
from __future__ import annotations

from dataclasses import dataclass

from .chains import ProblemChain, problem_chains
from .cleanup import CleanupResult, eliminate_unit_cycles, remove_useless
from .core import (FixpointCertificate, YieldInfo, generating_certificate,
                   nullable_certificate, shortest_yields, verify_certificate)
from .cycles import CycleAnalysis, CycleWitness, analyze_cycles
from .enumerate import bounded_enumerate
from .grammar import Grammar
from .reach import (ReachWitness, reach_witness, reachable_bfs,
                    reachable_fixpoint, useful_symbols)


@dataclass
class AmbiguityWitness:
    """无限歧义见证：w = u·x·v 有无限多推导树。"""

    symbol: str
    string: str
    reach: ReachWitness       # S ⇒* α A β
    cycle: CycleWitness       # A ⇒+ γ A δ，γδ ⇒* ε
    u: str
    x: str
    v: str


@dataclass
class LangInfiniteWitness:
    """语言无限见证：u·y1^k·x·y2^k·v（k ≥ 0）两两不同。"""

    symbol: str
    reach: ReachWitness
    cycle: CycleWitness
    u: str
    x: str
    v: str
    pump_left: str
    pump_right: str

    def sample(self, k: int) -> str:
        return self.u + self.pump_left * k + self.x + self.pump_right * k + self.v


@dataclass
class Diagnosis:
    grammar: Grammar
    gen_cert: FixpointCertificate
    null_cert: FixpointCertificate
    cert_problems: list[str]
    null_cert_problems: list[str]
    yields: YieldInfo
    reachable: frozenset[str]
    reachable_alt: frozenset[str]
    reach_consistent: bool
    useful: frozenset[str]
    chains: dict[str, ProblemChain]
    cycle_analysis: CycleAnalysis
    ambiguity: AmbiguityWitness | None
    lang_inf: LangInfiniteWitness | None
    language_empty: bool
    empty_explanation: str | None
    cleanup: CleanupResult
    unit_elim_grammar: Grammar | None
    unit_elim_notes: list[str]
    unit_elim_cleanup: CleanupResult | None
    enum_lines: list[str]

    @property
    def generating(self) -> frozenset[str]:
        return self.gen_cert.result

    @property
    def non_generating(self) -> frozenset[str]:
        return frozenset(self.grammar.nonterminals - self.gen_cert.result)

    @property
    def nullable(self) -> frozenset[str]:
        return self.null_cert.result

    @property
    def unreachable(self) -> frozenset[str]:
        return frozenset(self.grammar.nonterminals - self.reachable)


def _yield_of_context(grammar: Grammar, yields: YieldInfo, symbols) -> str:
    parts = []
    for s in symbols:
        parts.append(s if s in grammar.terminals else yields.best_str[s])
    return "".join(parts)


def analyze(grammar: Grammar, enum_depth: int = 8) -> Diagnosis:
    gen_cert = generating_certificate(grammar)
    null_cert = nullable_certificate(grammar)
    cert_problems = verify_certificate(grammar, gen_cert)
    null_cert_problems = verify_certificate(grammar, null_cert)
    generating = gen_cert.result
    nullable = null_cert.result
    yields = shortest_yields(grammar, generating)

    reach1 = reachable_bfs(grammar)
    reach2 = reachable_fixpoint(grammar)
    useful = useful_symbols(grammar, generating)

    chains = problem_chains(grammar, frozenset(grammar.nonterminals - generating))
    cyc = analyze_cycles(grammar, generating, nullable, useful, yields)

    # ---- 无限歧义见证（ε-环 + 有用符号）----
    ambiguity = None
    if cyc.epsilon_cycles:
        w0 = cyc.epsilon_cycles[0]
        rw = reach_witness(grammar, generating, w0.symbol)
        if rw is not None:
            u = _yield_of_context(grammar, yields, rw.left_context)
            v = _yield_of_context(grammar, yields, rw.right_context)
            x = yields.best_str[w0.symbol]
            ambiguity = AmbiguityWitness(w0.symbol, u + x + v, rw, w0, u, x, v)

    # ---- 语言无限见证（增长环 + 有用符号）----
    lang_inf = None
    if cyc.growth_cycles:
        w0 = cyc.growth_cycles[0]
        rw = reach_witness(grammar, generating, w0.symbol)
        if rw is not None:
            u = _yield_of_context(grammar, yields, rw.left_context)
            v = _yield_of_context(grammar, yields, rw.right_context)
            x = yields.best_str[w0.symbol]
            y1 = _yield_of_context(grammar, yields, w0.left_context)
            y2 = _yield_of_context(grammar, yields, w0.right_context)
            lang_inf = LangInfiniteWitness(w0.symbol, rw, w0, u, x, v, y1, y2)

    # ---- 开始符无用 ⇒ 空语言 ----
    language_empty = grammar.start not in generating
    empty_explanation = None
    if language_empty:
        ch = chains.get(grammar.start)
        reason = ""
        if ch is not None:
            if ch.root_kind == "no_rules":
                reason = f"根因：{ch.root!r} 没有任何产生式"
            else:
                reason = f"根因：环 {' ⇒ '.join(ch.root_cycle)} 无终结出口"
        empty_explanation = (
            f"开始符 {grammar.start!r} 不可生成任何终结串（{reason}），"
            f"故 L(G) = ∅。注意：这与文法是否存在 ε-环 / 是否无限歧义无关。"
        )

    cleanup = remove_useless(grammar, generating, chains)

    # 可选：单位环消除（语言等价的进一步去歧简化）
    unit_grammar, unit_notes = eliminate_unit_cycles(grammar)
    unit_cleanup = None
    if unit_grammar != grammar:
        unit_gen = generating_certificate(unit_grammar).result
        unit_chains = problem_chains(
            unit_grammar, frozenset(unit_grammar.nonterminals - unit_gen))
        unit_cleanup = remove_useless(unit_grammar, unit_gen, unit_chains)
    else:
        unit_grammar = None
        unit_notes = []

    # ---- 有界枚举交叉核验 ----
    enum_lines: list[str] = []
    if grammar.start in generating:
        need = yields.best_height[grammar.start]
        depth = max(enum_depth, need)
        res = bounded_enumerate(grammar, max_depth=depth)
        w = yields.best_str[grammar.start]
        cnt = res.count(w)
        status = "✓" if cnt >= 1 else "✗（不一致！）"
        enum_lines.append(
            f"{status} 证书见证串 {w!r}（最短，长度 {len(w)}）在深度 ≤ {need} 内被枚举发现，"
            f"推导数 = {cnt}"
        )
        if ambiguity is not None:
            wcnt = res.count(ambiguity.string)
            status = "✓" if wcnt >= 2 else "（深度不足，增大 --enum-depth 可观察增长）"
            enum_lines.append(
                f"{status} 歧义见证串 {ambiguity.string!r} 在深度 ≤ {depth} 内的推导数 = {wcnt}"
                f"（随深度严格增长 ⇒ 无限歧义的枚举佐证）"
            )
        if res.truncated:
            enum_lines.append("注意：枚举因资源上限被截断，计数为下界")
    else:
        enum_lines.append("开始符不可生成：无可枚举的终结串（由不动点证书证明，非枚举结论）")
    enum_lines.append("说明：枚举有界，'未找到' 不构成不可生成证明；不可生成性仅由最小不动点证书证明。")

    return Diagnosis(
        grammar=grammar,
        gen_cert=gen_cert,
        null_cert=null_cert,
        cert_problems=cert_problems,
        null_cert_problems=null_cert_problems,
        yields=yields,
        reachable=reach1,
        reachable_alt=reach2,
        reach_consistent=reach1 == reach2,
        useful=useful,
        chains=chains,
        cycle_analysis=cyc,
        ambiguity=ambiguity,
        lang_inf=lang_inf,
        language_empty=language_empty,
        empty_explanation=empty_explanation,
        cleanup=cleanup,
        unit_elim_grammar=unit_grammar,
        unit_elim_notes=unit_notes,
        unit_elim_cleanup=unit_cleanup,
        enum_lines=enum_lines,
    )


# ---------------------------------------------------------------- 报告文本

def _fmt_set(s) -> str:
    return "{" + ", ".join(sorted(s)) + "}" if s else "∅"


def format_report(diag: Diagnosis) -> str:
    g = diag.grammar
    out: list[str] = []
    add = out.append
    add("=" * 60)
    add("文法诊断报告")
    add("=" * 60)
    add(f"开始符: {g.start}   终结符: {_fmt_set(g.terminals)}   "
        f"非终结符: {_fmt_set(g.nonterminals)}   产生式数: {len(g.productions)}")

    add("")
    add("[可生成性 · 最小不动点证书]")
    if diag.gen_cert.rounds:
        for r, entries in enumerate(diag.gen_cert.rounds):
            items = ", ".join(f"{s}（经 #{i}: {g.productions[i]}）" for s, i in entries)
            add(f"  第 {r} 轮: {items}")
    else:
        add("  第 0 轮即为空：没有任何符号可生成终结串")
    add(f"  可生成符号: {_fmt_set(diag.generating)}")
    add(f"  不可生成符号: {_fmt_set(diag.non_generating)}")
    add(f"  证书独立校验: {'通过' if not diag.cert_problems else '失败: ' + '; '.join(diag.cert_problems)}")
    add(f"  可空性: {_fmt_set(diag.nullable)}（证书校验: {'通过' if not diag.null_cert_problems else '失败'}）")

    add("")
    add("[最短问题链 · 不可生成符号]")
    if not diag.chains:
        add("  （无不可生成符号）")
    for sym in sorted(diag.chains):
        add(f"  符号 {sym!r}:")
        add(diag.chains[sym].describe(g))

    add("")
    add("[可达性 · 双独立方法交叉核验]")
    add(f"  BFS 与不动点迭代结果一致: {'✓' if diag.reach_consistent else '✗ 不一致！'}")
    add(f"  可达符号: {_fmt_set(diag.reachable)}")
    add(f"  不可达符号: {_fmt_set(diag.unreachable)}")
    add(f"  有用符号（可达且可生成）: {_fmt_set(diag.useful)}")

    add("")
    add("[无用规则清理映射]")
    if diag.cleanup.removed_count == 0:
        add("  （所有规则均有用，无删除）")
    for fate in diag.cleanup.fates:
        if fate.kept:
            add(f"  #{fate.index}: {fate.production}   保留 → 新 #{fate.new_index}")
        else:
            add(f"  #{fate.index}: {fate.production}   删除: {'; '.join(fate.reasons)}")
    add(f"  简化后文法（语言等价，{len(diag.cleanup.simplified.productions)} 条产生式）:")
    if diag.cleanup.simplified.productions:
        for i, p in enumerate(diag.cleanup.simplified.productions):
            add(f"    #{i}: {p}")
    else:
        add("    （无产生式）")

    if diag.unit_elim_grammar is not None:
        add("")
        add("[可选 · 单位环消除（语言等价的去歧简化）]")
        for note in diag.unit_elim_notes:
            add(f"  {note}")
        final = diag.unit_elim_cleanup.simplified if diag.unit_elim_cleanup else diag.unit_elim_grammar
        add(f"  消除单位环并再清理后（{len(final.productions)} 条产生式）:")
        for i, p in enumerate(final.productions):
            add(f"    #{i}: {p}")

    add("")
    add("[ε-环 / 可空环 与 无限歧义]（与语言无限性分别诊断）")
    if diag.cycle_analysis.epsilon_cycles:
        for w in diag.cycle_analysis.epsilon_cycles:
            add(f"  环见证: {w.describe(g)}")
            add(f"    左上下文 {_fmt_set(w.left_context)} 与右上下文 {_fmt_set(w.right_context)} 均可空（⇒* ε）")
        add("  诊断: 存在无限歧义 —— 某终结串具有无限多棵推导树")
        if diag.ambiguity is not None:
            a = diag.ambiguity
            add(f"  见证串: w = u·x·v = {a.u!r}·{a.x!r}·{a.v!r} = {a.string!r}")
            add(f"  （沿环每多绕一圈即多一棵推导树，产出串不变）")
    else:
        add("  无可空环 ⇒ 非无限歧义（每个终结串的推导树数有限）")

    add("")
    add("[语言无限性]")
    if diag.cycle_analysis.language_infinite:
        li = diag.lang_inf
        add("  诊断: 语言无限 —— 存在带增长上下文的有用环")
        if li is not None:
            add(f"  环见证: {li.cycle.describe(g)}")
            add(f"  泵串族: u·y1^k·x·y2^k·v，u={li.u!r} y1={li.pump_left!r} "
                f"x={li.x!r} y2={li.pump_right!r} v={li.v!r}")
            add(f"  样例: {[li.sample(k) for k in range(3)]}")
    else:
        add("  诊断: 语言有限（不存在带增长上下文的有用环）")
    add("  注: 同一终结串可有无限多推导（无限歧义）≠ 语言无限；两者已分别判定。")

    add("")
    add("[语言为空判定]")
    if diag.language_empty:
        add(f"  L(G) = ∅。{diag.empty_explanation}")
    else:
        w = diag.yields.best_str[g.start]
        add(f"  L(G) ≠ ∅：开始符可生成，例如最短见证串 {w!r}")

    add("")
    add("[有界推导枚举 · 交叉核验]")
    for line in diag.enum_lines:
        add(f"  {line}")
    return "\n".join(out)
