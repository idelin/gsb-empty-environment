"""Synthetic payroll pipeline demonstrating the engine.

Run with:  python3.11 -m depsys.sample

Scenario (all data is synthetic):

    emp:e1  ----base_salary--->  st:gross  ---pay:gross--->  st:tax
    cfg:bonus --enabled/pct--->     |                            |
    tax:2024 ---rate_g5--------->---+----------------------------+
                                     v                            v
                                 pay:net  --->  st:report  --->  rpt:e1
                                                                    |
                                              st:audit  <-----------+
                                                  |
                                                  v
                                              audit:e1
    st:allowance probes for the (initially missing) fact emp:e1.allowance,
    recording a NEGATIVE dependency.
"""
from __future__ import annotations

from .engine import CycleError, Engine
from .model import FactAdd, FactUpdate, RuleUpdate, StepCorrection


# --- step functions ---------------------------------------------------------


def gross_fn(ctx):
    base = ctx.read("emp:e1", "base_salary")
    if ctx.branch("cfg:bonus", "enabled"):
        pct = ctx.read("cfg:bonus", "pct")
    else:
        pct = 0.0
    return {"pay:gross": {"amount": round(base * (1 + pct), 2)}}


def tax_fn(ctx):
    grade = ctx.branch("emp:e1", "grade")
    rate = ctx.read("tax:2024", "rate_g5" if grade == "G5" else "rate_g6")
    gross = ctx.read("pay:gross", "amount")
    return {"pay:net": {"amount": round(gross * (1 - rate), 2)}}


def tax_fn_v2(ctx):  # corrected: flat rate, no longer reads tax:2024
    gross = ctx.read("pay:gross", "amount")
    return {"pay:net": {"amount": round(gross * 0.82, 2)}}


def report_fn(ctx):
    net = ctx.read("pay:net", "amount")
    return {"rpt:e1": {"net_rounded": round(net, -2)}}


def audit_fn(ctx):
    rounded = ctx.read("rpt:e1", "net_rounded")
    return {"audit:e1": {"bucket": int(rounded // 1000)}}


def allowance_fn(ctx):
    if ctx.exists("emp:e1.allowance"):
        amount = ctx.read("emp:e1.allowance", "amount")
    else:
        amount = 0
    return {"pay:allowance": {"amount": amount}}


def build_engine() -> Engine:
    eng = Engine()
    eng.register_rule("rule:payroll", 1)
    eng.register_rule("rule:reporting", 1)
    eng.add_fact("emp:e1", {"name": "Ada", "grade": "G5", "base_salary": 10000})
    eng.add_fact("tax:2024", {"rate_g5": 0.20, "rate_g6": 0.25})
    eng.add_fact("cfg:bonus", {"enabled": True, "pct": 0.10})
    eng.register_step("st:gross", gross_fn, "rule:payroll")
    eng.register_step("st:tax", tax_fn, "rule:payroll")
    eng.register_step("st:report", report_fn, "rule:reporting")
    eng.register_step("st:audit", audit_fn, "rule:reporting")
    eng.register_step("st:allowance", allowance_fn, "rule:payroll")
    return eng


# --- cycle demo functions ----------------------------------------------------


def cyc1_fn(ctx):
    v = ctx.read("cyc:y", "v") if ctx.exists("cyc:y") else 0
    return {"cyc:x": {"v": v + 1}}


def cyc2_fn(ctx):
    v = ctx.read("cyc:x", "v")
    return {"cyc:y": {"v": v + 1}}


def selfish_fn(ctx):
    v = ctx.read("loop:out", "v") if ctx.exists("loop:out") else 0
    return {"loop:out": {"v": v + 1}}


def _show(title, value):
    print(f"  {title}: {value}")


def main() -> None:
    eng = build_engine()
    print("== initial store ==")
    for fid in sorted(eng.facts):
        f = eng.facts[fid]
        _show(f"{fid}@v{f.version}", f.fields)

    print("\n== 1. impact query: revise tax:2024 {rate_g5} ==")
    impact = eng.query_impact("tax:2024", {"rate_g5"})
    _show("direct", sorted(impact.direct))
    _show("transitive", sorted(impact.transitive))
    _show("products", sorted(impact.products))
    for sid in sorted(impact.explanations):
        print(f"    shortest chain to {sid}: {impact.explanations[sid].describe()}")

    print("\n== 2. batch: salary +1 (consumed) and rate_g6 bump (NOT consumed) ==")
    report = eng.apply_batch(
        [
            FactUpdate(
                "emp:e1",
                {"name": "Ada", "grade": "G5", "base_salary": 10001},
                frozenset({"base_salary"}),
            ),
            FactUpdate(
                "tax:2024",
                {"rate_g5": 0.20, "rate_g6": 0.26},
                frozenset({"rate_g6"}),
            ),
        ]
    )
    _show("revalidated", sorted(report.revalidated))
    _show("rescued (re-validated, no longer affected)", sorted(report.rescued))
    for sid in sorted(report.explanations):
        print(f"    min explanation for {sid}: {report.explanations[sid].describe()}")
    _show("evidence replays", all(e.replay(eng) for e in report.explanations.values()))

    print("\n== 3. reference check: full re-execution as ground truth ==")
    ref = eng.reference_check()
    _show("missed invalidations", sorted(ref.missed) or "none")
    _show("irrelevant branch accesses", ref.irrelevant_branch_accesses)

    print("\n== 4. negative dependency: emp:e1.allowance appears ==")
    _show("before", eng.facts["pay:allowance"].fields)
    rep = eng.apply_batch([FactAdd("emp:e1.allowance", {"amount": 500})])
    _show("revalidated", sorted(rep.revalidated))
    _show("after", eng.facts["pay:allowance"].fields)

    print("\n== 5. step correction retracts old edges ==")
    eng.apply_batch([StepCorrection("st:tax", tax_fn_v2, new_version=2)])
    _show("st:tax edges now", sorted({(e.fact_id, e.field) for e in eng.edges if e.step_id == "st:tax"}))
    impact2 = eng.query_impact("tax:2024", {"rate_g5"})
    _show("impact of tax:2024 {rate_g5} after correction", sorted(impact2.explanations) or "none")

    print("\n== 6. rule update invalidates non-compliant steps ==")
    rep = eng.apply_batch([RuleUpdate("rule:payroll", 2)])
    _show("invalidated", sorted(rep.explanations))
    _show("non_compliant", sorted(rep.non_compliant))
    rep = eng.apply_batch(
        [
            StepCorrection("st:gross", gross_fn, new_version=2, new_rule_version=2),
            StepCorrection("st:tax", tax_fn_v2, new_version=3, new_rule_version=2),
            StepCorrection("st:allowance", allowance_fn, new_version=2, new_rule_version=2),
        ]
    )
    _show("non_compliant after corrections", sorted(rep.non_compliant) or "none")

    print("\n== 7. cyclic (illegal) chains are diagnosed ==")
    cyc = Engine()
    cyc.register_rule("rule:cyc", 1)
    cyc.register_step("st:cyc1", cyc1_fn, "rule:cyc")
    cyc.register_step("st:cyc2", cyc2_fn, "rule:cyc")
    try:
        cyc.apply_batch([StepCorrection("st:cyc1", cyc1_fn, new_version=2)])
        print("  ERROR: cycle not detected")
    except CycleError as err:
        _show("diagnosed cycle", " -> ".join(err.chain))
    loop = Engine()
    loop.register_rule("rule:loop", 1)
    loop.register_step("st:loop", selfish_fn, "rule:loop")
    try:
        loop.apply_batch([StepCorrection("st:loop", selfish_fn, new_version=2)])
        print("  ERROR: self-loop not detected")
    except CycleError as err:
        _show("diagnosed self-support", " -> ".join(err.chain))


if __name__ == "__main__":
    main()
