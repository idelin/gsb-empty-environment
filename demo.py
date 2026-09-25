"""Runnable demo on synthetic data.

Scenario: an order fact and a customer fact, five derived rules.
Shows field-level correction, precise invalidation, remark isolation,
existence-condition deletion, atomic batch failure and evidence replay.

Run:  python3.11 demo.py
"""
from factver import (
    CorrectionError,
    DeleteField,
    Engine,
    FactStore,
    RuleRegistry,
    SetField,
    SetRemark,
)

ORDER = "order-1001"
CUSTOMER = "customer-77"


def build():
    store = FactStore()
    store.create(
        ORDER,
        fields={
            "qty": (3, "erp:orders"),
            "unit_price": (19.9, "erp:pricing"),
            "gift": ("mug", "promo:2026-q3"),
        },
        remarks={"note": "call before shipping"},
    )
    store.create(CUSTOMER, fields={"level": ("gold", "crm:profile")})

    rules = RuleRegistry()
    rules.register("line-total", 1, lambda v: round(
        v["order"].require("qty") * v["order"].require("unit_price"), 2))
    rules.register("gift-check", 1, lambda v: {"has_gift": v["order"].has("gift")})
    rules.register("order-digest", 1, lambda v: v["order"].all())
    rules.register("remark-length", 1, lambda v: len(v["order"].remark("note", "")))
    rules.register("shipping", 1, lambda v: (
        "free" if v["customer"].require("level") == "gold" else "standard"))

    engine = Engine(store, rules)
    engine.run("line-total", {"order": ORDER})
    engine.run("gift-check", {"order": ORDER})
    engine.run("order-digest", {"order": ORDER})
    engine.run("remark-length", {"order": ORDER})
    engine.run("shipping", {"customer": CUSTOMER})
    return store, engine


def show(engine, label):
    stale = set(engine.stale_steps())
    print(f"\n== {label} ==")
    for step in engine.steps():
        mark = "STALE" if step.step_id in stale else "valid"
        print(f"  {step.step_id:<32} v{step.version}  {mark:<5}  output={step.output}")


def main():
    store, engine = build()
    show(engine, "initial derivation (all valid)")

    print("\n-- correct order.qty 3 -> 5 (source 'erp:correction#T-1042')")
    store.correct(ORDER, [SetField("qty", 5, "erp:correction#T-1042")])
    old = store.get(ORDER, 1).fields["qty"]
    new = store.get(ORDER).fields["qty"]
    print(f"   provenance kept: v1 qty={old.value} src={old.source!r} | "
          f"v{store.current_version(ORDER)} qty={new.value} src={new.source!r}")
    show(engine, "after qty correction: only qty readers + wildcard go stale")

    print("\n-- change remark 'note' only")
    store.correct(ORDER, [SetRemark("note", "leave at front desk")])
    show(engine, "after remark change: numeric rules unaffected")

    print("\n-- delete field 'gift'")
    store.correct(ORDER, [DeleteField("gift")])
    show(engine, "after field deletion: existence condition goes stale")

    print("\n-- batch correction with one bad op (delete missing field on customer)")
    before = (store.current_version(ORDER), store.current_version(CUSTOMER))
    try:
        store.correct_batch({
            ORDER: [SetField("qty", 6, "erp:correction#T-1043")],
            CUSTOMER: [DeleteField("no-such-field")],
        })
    except CorrectionError as exc:
        print(f"   batch rejected: {exc}")
    after = (store.current_version(ORDER), store.current_version(CUSTOMER))
    print(f"   versions before={before} after={after} -> originals kept: "
          f"{before == after}")

    print("\n-- replay evidence of line-total step v1 (recorded before corrections)")
    result = engine.replay("line-total[order=order-1001]", version=1)
    print(f"   ok={result.ok} detail={result.detail!r}")
    print(f"   evidence_hash={result.expected_hash[:16]}... "
          f"recomputed={result.actual_hash[:16]}...")

    print("\n-- re-derive stale line-total against current facts")
    step = engine.run("line-total", {"order": ORDER})
    print(f"   step v{step.version} output={step.output} "
          f"stale={engine.is_stale(step)}")
    show(engine, "final state")


if __name__ == "__main__":
    main()
