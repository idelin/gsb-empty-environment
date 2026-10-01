"""可独立执行的反例 / 见证脚本生成。

每个生成的脚本只依赖 Python 标准库，内嵌文法与见证数据，
可脱离本包单独运行（python3 script.py），独立复核诊断结论。
"""
from __future__ import annotations

import json

from .core import (FixpointCertificate, apply_step, expand_all_to_terminals,
                   nt_positions)
from .grammar import Grammar
from .report import AmbiguityWitness, Diagnosis


def _grammar_json(grammar: Grammar) -> str:
    return json.dumps(grammar.to_dict(), ensure_ascii=False, indent=1)


def emit_certificate_script(grammar: Grammar, cert: FixpointCertificate) -> str:
    """生成可生成性最小不动点证书的独立校验脚本。"""
    cert_data = {
        "rounds": [[list(e) for e in r] for r in cert.rounds],
        "result": sorted(cert.result),
    }
    return f'''#!/usr/bin/env python3
"""独立校验：可生成性最小不动点证书（由 cfg_analyzer 生成，仅用标准库）。"""
import json

GRAMMAR = json.loads({json.dumps(grammar.to_dict(), ensure_ascii=False)!r})
CERT = json.loads({json.dumps(cert_data, ensure_ascii=False)!r})


def main():
    terms = set(GRAMMAR["terminals"])
    nonterms = set(GRAMMAR["nonterminals"])
    prods = [(p["lhs"], tuple(p["rhs"])) for p in GRAMMAR["productions"]]
    seen = set()
    for r, entries in enumerate(CERT["rounds"]):
        for sym, idx in entries:
            assert sym not in seen, f"符号 {{sym}} 重复加入"
            lhs, rhs = prods[idx]
            assert lhs == sym, f"产生式 #{{idx}} 左部非 {{sym}}"
            for s in rhs:
                assert s in terms or s in seen, (
                    f"第 {{r}} 轮 {{sym}} 的见证引用了未证符号 {{s}}")
            seen.add(sym)
    # 完备性：证书外不存在仍可加入的符号 ⇒ 确为最小不动点
    for idx, (lhs, rhs) in enumerate(prods):
        if lhs not in seen and all(s in terms or s in seen for s in rhs):
            raise SystemExit(f"证书不完备：#{{idx}} {{lhs}} -> {{rhs}} 仍可加入")
    assert seen == set(CERT["result"]), "result 字段与轮次不符"
    non_gen = sorted(nonterms - seen)
    print("OK: 可生成性最小不动点证书有效")
    print("可生成符号:", sorted(seen) or "空集")
    print("不可生成符号:", non_gen or "无")
    if GRAMMAR["start"] not in seen:
        print(f"开始符 {{GRAMMAR['start']}} 不可生成 ⇒ L(G) = ∅")


if __name__ == "__main__":
    main()
'''


def build_pumped_derivations(grammar: Grammar, aw: AmbiguityWitness,
                             chosen: dict[str, int], pumps: int = 4) -> list[list[tuple[int, int]]]:
    """构造 k=0..pumps 的完整推导步序列：S ⇒* u γ^k x δ^k v ⇒* w。"""
    derivations = []
    for k in range(pumps + 1):
        form = [grammar.start]
        steps: list[tuple[int, int]] = []

        def apply(pidx, ntpos):
            nonlocal form
            steps.append((pidx, ntpos))
            form = apply_step(grammar, form, steps[-1])

        # 1) S ⇒* α A β
        for st in aw.reach.steps:
            apply(*st)
        # 2) 把 A 左侧的上下文 α 全部终结化，使 A 成为最左非终结符。
        #    用绝对下标跟踪目标出现（α 中可能也含同名符号）。
        dist0 = sum(1 for s in aw.reach.left_context if s in grammar.nonterminals)
        dist_abs = nt_positions(grammar, form)[dist0]
        while True:
            positions = nt_positions(grammar, form)
            if positions[0] >= dist_abs:
                break
            sym = form[positions[0]]
            pidx = chosen[sym]
            dist_abs = dist_abs - 1 + len(grammar.productions[pidx].rhs)
            apply(pidx, 0)
        # 3) 绕环 k 次。环步骤记录的位置以"A 左侧无额外非终结符"为基准；
        #    每完成一圈，左侧多累积一份 γ 的非终结符，位置需相应偏移。
        gamma_nts = sum(1 for s in aw.cycle.left_context if s in grammar.nonterminals)
        for round_i in range(k):
            offset = round_i * gamma_nts
            for pidx, pos in aw.cycle.steps:
                apply(pidx, pos + offset)
        # 4) 全部终结化（可空上下文经最短产出产生式 ⇒* ε）
        tail, _w = expand_all_to_terminals(grammar, form, chosen)
        for st in tail:
            apply(*st)
        derivations.append(steps)
    return derivations


def emit_ambiguity_script(grammar: Grammar, aw: AmbiguityWitness,
                          chosen: dict[str, int], pumps: int = 4) -> str:
    """生成无限歧义反例的独立校验脚本：w 的 k=0..pumps 棵不同推导树。"""
    derivations = build_pumped_derivations(grammar, aw, chosen, pumps)
    data = {
        "grammar": grammar.to_dict(),
        "string": aw.string,
        "derivations": [[[p, n] for p, n in d] for d in derivations],
    }
    return f'''#!/usr/bin/env python3
"""独立校验：无限歧义反例（由 cfg_analyzer 生成，仅用标准库）。

重放 {{len(derivations)}} 条不同推导，断言它们产出同一终结串。
推导条数可沿环任意增多 ⇒ 该终结串有无限多棵推导树（无限歧义）。
注意：无限歧义 ≠ 语言无限。
"""
import json

DATA = json.loads({json.dumps(data, ensure_ascii=False)!r})


def replay(prods, nonterms, start, steps):
    form = [start]
    for pidx, nt_pos in steps:
        lhs, rhs = prods[pidx]
        positions = [i for i, s in enumerate(form) if s in nonterms]
        i = positions[nt_pos]
        assert form[i] == lhs, f"步骤 ({{pidx}}, {{nt_pos}}) 左部不符"
        form = form[:i] + list(rhs) + form[i + 1:]
    return form


def main():
    g = DATA["grammar"]
    prods = [(p["lhs"], tuple(p["rhs"])) for p in g["productions"]]
    nonterms = set(g["nonterminals"])
    w = DATA["string"]
    trees = set()
    for k, steps in enumerate(DATA["derivations"]):
        form = replay(prods, nonterms, g["start"], steps)
        assert all(s not in nonterms for s in form), "最终句型仍含非终结符"
        y = "".join(form)
        assert y == w, f"第 {{k}} 条推导出 {{y!r}} ≠ {{w!r}}"
        trees.add(tuple(tuple(s) for s in steps))
    assert len(trees) == len(DATA["derivations"]), "推导不互异"
    n = len(trees)
    print(f"OK: 终结串 {{w!r}} 至少有 {{n}} 棵不同推导树（绕环 0..{{n-1}} 圈）")
    print("绕环次数可任意增大 ⇒ 该串有无限多推导树（无限歧义）；语言未必无限")


if __name__ == "__main__":
    main()
'''


def emit_generating_script(grammar: Grammar, symbol: str,
                           chosen: dict[str, int]) -> str:
    """生成"某符号可生成最短终结串"的独立回放校验脚本。"""
    steps, w = expand_all_to_terminals(grammar, [symbol], chosen)
    data = {
        "grammar": grammar.to_dict(),
        "symbol": symbol,
        "string": w,
        "steps": [[p, n] for p, n in steps],
    }
    return f'''#!/usr/bin/env python3
"""独立校验：最短终结串见证（由 cfg_analyzer 生成，仅用标准库）。"""
import json

DATA = json.loads({json.dumps(data, ensure_ascii=False)!r})


def main():
    g = DATA["grammar"]
    prods = [(p["lhs"], tuple(p["rhs"])) for p in g["productions"]]
    nonterms = set(g["nonterminals"])
    form = [DATA["symbol"]]
    for pidx, nt_pos in DATA["steps"]:
        lhs, rhs = prods[pidx]
        positions = [i for i, s in enumerate(form) if s in nonterms]
        i = positions[nt_pos]
        assert form[i] == lhs
        form = form[:i] + list(rhs) + form[i + 1:]
    assert all(s not in nonterms for s in form), "未完全终结化"
    y = "".join(form)
    assert y == DATA["string"], f"{{y!r}} ≠ {{DATA['string']!r}}"
    print(f"OK: {{DATA['symbol']}} ⇒* {{y!r}}（回放 {{len(DATA['steps'])}} 步，全部为终结符）")


if __name__ == "__main__":
    main()
'''


def emit_all(diag: Diagnosis) -> dict[str, str]:
    """为一次诊断生成全部可独立执行的见证脚本。"""
    g = diag.grammar
    scripts: dict[str, str] = {}
    scripts["check_generating_certificate.py"] = emit_certificate_script(g, diag.gen_cert)
    if diag.ambiguity is not None:
        scripts["counterexample_infinite_ambiguity.py"] = emit_ambiguity_script(
            g, diag.ambiguity, diag.yields.chosen)
    if g.start in diag.generating:
        scripts["witness_shortest_string.py"] = emit_generating_script(
            g, g.start, diag.yields.chosen)
    return scripts
