"""Runnable demo on synthetic data:  python3.11 sample.py

Pipeline (all data is synthetic, generated inline):

    facts:  user:1, user:2, user:3(added later), rate:eu, rate:us
    rules:  min_age, bonus
    steps:  eligibility -> report
            pricing     -> report
"""
from flowdeps import CycleError, Engine, reference_check, replay


# --------------------------------------------------------------------------
# Step definitions (each has a stable id and an explicit version)
# --------------------------------------------------------------------------

def eligibility_v1(r):
    min_age = r.rule("min_age")
    out = {}
    for uid in ("user:1", "user:2"):
        age = r.field(uid, "age")
        gate = r.branch(f"gate:{uid}", "adult" if age >= min_age else "minor")
        if gate == "adult":
            # tier/bonus are read ONLY on the adult branch
            out[uid] = {"eligible": True, "tier": r.field(uid, "tier"),
                        "bonus": r.rule("bonus")}
        else:
            out[uid] = {"eligible": False}
    # negative dependency: fires when user:3 is added later
    out["waitlist:user:3"] = r.lookup("user:3") is not None
    return out


def pricing_v1(r):
    return {"eu": r.field("rate:eu", "factor"),
            "us": r.field("rate:us", "factor")}


def pricing_v2(r):                      # correction: no longer reads rate:us
    return {"eu": r.field("rate:eu", "factor")}


def report_v1(r):
    elig = r.step("eligibility")
    price = r.step("pricing")
    lines = []
    for key, info in sorted(elig.items()):
        if isinstance(info, dict) and info.get("eligible"):
            lines.append(f"{key}: bonus={info['bonus']} "
                         f"eu={price['eu']} us={price.get('us', 1.0)}")
        elif isinstance(info, dict):
            lines.append(f"{key}: ineligible")
        else:
            lines.append(f"{key}={info}")
    return tuple(lines)


def cycle_x(r):
    return ("x",) + tuple(r.step("cycle-y"))


def cycle_y(r):
    return ("y",) + tuple(r.step("cycle-x"))


REGISTRY = {
    ("eligibility", 1): eligibility_v1,
    ("pricing", 1): pricing_v1,
    ("pricing", 2): pricing_v2,
    ("report", 1): report_v1,
    ("cycle-x", 1): cycle_x,
    ("cycle-y", 1): cycle_y,
}


def build_engine() -> Engine:
    eng = Engine()
    eng.upsert_fact("user:1", {"region": "eu", "age": 34, "tier": "gold"})
    eng.upsert_fact("user:2", {"region": "us", "age": 16, "tier": "free"})
    eng.upsert_fact("rate:eu", {"factor": 0.9})
    eng.upsert_fact("rate:us", {"factor": 1.1})
    eng.set_rule("min_age", 18)
    eng.set_rule("bonus", 100)
    eng.define_step("eligibility", 1, eligibility_v1)
    eng.define_step("pricing", 1, pricing_v1)
    eng.define_step("report", 1, report_v1)
    eng.commit()
    return eng


def show_report(title, report):
    print(f"  {title}")
    print(f"    roots:       {list(report.roots)}")
    print(f"    affected:    {list(report.affected)} "
          f"(direct={list(report.direct)}, transitive={list(report.transitive)})")
    print(f"    revalidated: {list(report.revalidated)}")
    for step, chain in sorted(report.explanations.items()):
        print(f"    explain {step}: " + "  ->  ".join(chain))


def main():
    print("== 1. initial build (synthetic data) ==")
    eng = build_engine()
    for line in eng.ensure("report"):
        print("  " + line)

    print("\n== 2. impact query: revision of user:1 [age] ==")
    imp = eng.impact("user:1", fields={"age"})
    print(f"  direct:     {list(imp.direct)}")
    print(f"  transitive: {list(imp.transitive)}")
    for step, chain in sorted(imp.chains.items()):
        print(f"  shortest chain to {step}: " + " -> ".join(chain))

    print("\n== 3. batch: unconsumed fields + output-neutral change ==")
    eng.upsert_fact("user:1", {"region": "apac", "age": 40, "tier": "gold"})
    eng.upsert_fact("user:2", {"region": "us", "age": 16, "tier": "pro"})
    show_report("age 34->40 (same branch), region/tier unread or branch-gated:",
                eng.commit())

    print("\n== 4. batch: user:2 crosses the age gate ==")
    eng.upsert_fact("user:2", {"region": "us", "age": 21, "tier": "pro"})
    show_report("age 16->21 flips branch minor->adult:", eng.commit())

    print("\n== 5. negative dependency: add previously missing user:3 ==")
    eng.upsert_fact("user:3", {"region": "eu", "age": 50, "tier": "gold"})
    show_report("add user:3 fires the recorded negative edge:", eng.commit())

    print("\n== 6. batch of two changes: minimal explanation paths ==")
    eng.upsert_fact("rate:eu", {"factor": 0.85})
    eng.set_rule("bonus", 120)
    show_report("rate:eu.factor + rule bonus:", eng.commit())

    print("\n== 7. step correction retracts old edges ==")
    eng.define_step("pricing", 2, pricing_v2)
    show_report("pricing corrected v1->v2 (drops rate:us read):", eng.commit())
    eng.upsert_fact("rate:us", {"factor": 9.9})
    show_report("rate:us changes afterwards: nobody listens anymore",
                eng.commit())

    print("\n== 8. illegal cyclic processing chain ==")
    eng.define_step("cycle-x", 1, cycle_x)
    eng.define_step("cycle-y", 1, cycle_y)
    try:
        eng.ensure("cycle-x")
        print("  ERROR: cycle not detected")
    except CycleError as exc:
        print(f"  diagnosed: {exc}")

    print("\n== 9. reference check: full re-execution vs incremental ==")
    ref = reference_check(eng)
    print(f"  checked steps:            {ref.checked_steps}")
    print(f"  missed invalidations:     {list(ref.missed_invalidations)}")
    print(f"  irrelevant branch reads:  {ref.irrelevant_branch_accesses}")
    print(f"  missing dependencies:     {ref.missing_dependencies}")
    print(f"  ok: {ref.ok}")

    print("\n== 10. evidence replay ==")
    rep = replay(eng.log, REGISTRY)
    print(f"  entries: {rep.total_entries}, identical: {rep.ok}")


if __name__ == "__main__":
    main()
