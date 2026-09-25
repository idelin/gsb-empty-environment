"""可运行样例（全部使用合成数据）：

场景：订单事实 order-1001，字段含数值（amount/quantity）、备注（remark）、
折扣（discount）。三条规则分别演示：仅读数值、存在性条件、通配读取。
"""
from factver import (
    Correction,
    Engine,
    FactStore,
    Rule,
    replay_against_store,
    replay_from_evidence,
)


def build_engine() -> Engine:
    store = FactStore()
    store.put("order-1001", {
        "amount":   (100.0, "erp"),      # 数值字段，来源 ERP
        "quantity": (2, "erp"),
        "remark":   ("常规订单", "manual"),  # 备注字段，人工录入
        "discount": (0.9, "crm"),
    })
    engine = Engine(store)
    engine.register(Rule(
        "rule-total", 1,
        lambda r: r.value("order-1001", "amount") * r.value("order-1001", "quantity"),
    ))
    engine.register(Rule(
        "rule-discount-flag", 1,
        lambda r: {"has_discount": r.exists("order-1001", "discount")},
    ))
    engine.register(Rule(
        "rule-audit-digest", 1,
        lambda r: tuple(sorted(r.snapshot("order-1001").items())),
    ))
    return engine


def show_steps(engine: Engine, title: str) -> None:
    print(f"\n== {title} ==")
    for rec in engine.latest_steps():
        line = f"  {rec.step_id} v{rec.version} [{rec.rule_id} v{rec.rule_version}] {rec.status}"
        if rec.invalid_reason:
            line += f"  <- {rec.invalid_reason}"
        print(line)


def main() -> None:
    engine = build_engine()
    store = engine.store

    print("== 初始推导（合成数据） ==")
    s_total = engine.run("rule-total")
    s_flag = engine.run("rule-discount-flag")
    s_digest = engine.run("rule-audit-digest")
    print(f"  total = {s_total.output}")
    print(f"  discount-flag = {s_flag.output}")
    print(f"  audit-digest = {s_digest.output}")
    show_steps(engine, "初始步骤状态")

    # 1) 更正备注：只读数值的规则不受影响，通配读取的规则失效
    engine.apply_corrections([Correction(
        "order-1001", changes={"remark": ("加急订单", "manual-edit")},
    )])
    show_steps(engine, "更正 remark 后（仅通配步骤失效）")
    assert engine.steps[s_total.step_id][-1].status == "valid"
    assert engine.steps[s_digest.step_id][-1].status == "invalid"

    # 2) 重新执行失效步骤：step_id 稳定，version 递增
    s_digest2 = engine.run("rule-audit-digest", step_id=s_digest.step_id)
    print(f"\n重跑 {s_digest2.step_id} -> v{s_digest2.version}，output={s_digest2.output}")

    # 3) 删除 discount 字段：存在性条件失效，数值规则仍有效
    engine.apply_corrections([Correction("order-1001", removals=frozenset({"discount"}))])
    show_steps(engine, "删除 discount 字段后（存在性条件失效）")

    # 4) 批量更正含非法操作：整体失败，保持原版本
    before = store.current_version("order-1001")
    try:
        engine.apply_corrections([
            Correction("order-1001", changes={"amount": (120.0, "erp-fix")}),
            Correction("order-9999", changes={"amount": (1.0, "erp")}),  # 不存在的事实
        ])
    except Exception as exc:
        print(f"\n批量更正失败（{type(exc).__name__}: {exc}）")
    after = store.current_version("order-1001")
    print(f"  order-1001 版本保持 v{after}（失败前 v{before}），amount={store.get('order-1001').value('amount')}")

    # 5) 合法批量更正：原子生效，total 步骤失效后重跑
    engine.apply_corrections([Correction(
        "order-1001",
        changes={"amount": (120.0, "erp-fix"), "quantity": (3, "erp-fix")},
    )])
    show_steps(engine, "批量更正 amount/quantity 后")
    s_total2 = engine.run("rule-total", step_id=s_total.step_id)
    print(f"  重算 total = {s_total2.output}（{s_total2.step_id} v{s_total2.version}）")

    # 6) 证据重放：所有历史步骤版本均可重放
    print("\n== 证据重放 ==")
    ok_all = True
    for rec in engine.all_steps():
        rule = engine.rules[rec.rule_id]
        r1 = replay_against_store(store, rule, rec)
        r2 = replay_from_evidence(rule, rec)
        ok_all = ok_all and r1.ok and r2.ok
        print(f"  {rec.step_id} v{rec.version} [{rec.rule_id}] "
              f"store-replay={'OK' if r1.ok else 'FAIL'} evidence-replay={'OK' if r2.ok else 'FAIL'}")
    print(f"\n全部重放通过: {ok_all}")

    # 7) 版本历史与字段来源
    print("\n== order-1001 版本历史（来源保留） ==")
    for fv in store.history("order-1001"):
        srcs = {k: v.source for k, v in fv.fields.items()}
        print(f"  v{fv.version}: fields={sorted(fv.fields)} sources={srcs}")


if __name__ == "__main__":
    main()
