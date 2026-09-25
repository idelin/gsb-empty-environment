"""合成样例: 费用报销审批的基线推导与临时干预(数据全部为合成)。

运行: python3 demo.py
"""
from engine import (
    Condition,
    Engine,
    Fact,
    Remove,
    Replace,
    replay,
    Rule,
    InterventionError,
)

GOAL = "approval.status"


def build_engine() -> Engine:
    facts = (
        Fact(id="F1", assertion="applicant.role", value="employee", version=1),
        Fact(id="F2", assertion="expense.amount_band", value="low", version=1),
        Fact(id="F3", assertion="expense.has_receipt", value=True, version=1),
    )
    rules = (
        Rule(
            id="R1",
            version=2,
            conditions=(
                Condition("applicant.role", "employee"),
                Condition("expense.amount_band", "low"),
            ),
            conclusion=Condition("expense.precheck", "pass"),
        ),
        Rule(
            id="R2",
            version=1,
            conditions=(
                Condition("expense.precheck", "pass"),
                Condition("expense.has_receipt", True),
            ),
            conclusion=Condition(GOAL, "approved"),
        ),
    )
    return Engine(facts, rules, goal_assertion=GOAL)


def show_chain(title, derivation):
    goal = derivation.goal(GOAL)
    print(f"\n== {title} ==")
    print(f"目标 {GOAL} = {goal.value if goal else None!r}")
    for step in derivation.support_chain(GOAL):
        inputs = ", ".join(f"{fid}v{ver}" for fid, ver in step.inputs)
        print(
            f"  {step.id}: 规则 {step.rule_id}v{step.rule_version} "
            f"基于 [{inputs}] -> {step.output.assertion}={step.output.value!r}"
        )
    print(f"摘要: {derivation.summary()[:16]}...")


def show_scenario(title, result):
    d = result.derivation
    goal = d.goal(GOAL)
    print(f"\n== {title} ==")
    print(f"目标 {GOAL} = {goal.value if goal else None!r}")
    for s in result.removed_steps:
        print(f"  - 消失步骤 {s.id}: 规则 {s.rule_id}v{s.rule_version} -> {s.output.assertion}")
    for s in result.added_steps:
        print(f"  + 新增步骤 {s.id}: 规则 {s.rule_id}v{s.rule_version} -> {s.output.assertion}")
    print(f"基线摘要前: {result.baseline_summary_before[:16]}...")
    print(f"基线摘要后: {result.baseline_summary_after[:16]}...")
    print(f"基线未变: {result.baseline_intact}")


def main() -> None:
    engine = build_engine()

    baseline = engine.baseline()
    show_chain("基线推导", baseline)

    # 证据重放: 相同输入重新推导, 逐步比对
    replay(engine.base_facts, engine.rules, baseline.steps)
    print("\n基线证据重放: 通过")

    # 场景1: 临时移除事实 F3(无发票)
    show_scenario("场景1 移除 F3(expense.has_receipt)", engine.intervene([Remove("F3")]))

    # 场景2: 临时替换事实 F2(金额档位 low -> high)
    show_scenario(
        "场景2 替换 F2(amount_band: low -> high)",
        engine.intervene([Replace("F2", "high")]),
    )

    # 场景3: 多个干预原子隔离(替换 F2 + 替换 F3), 互不泄漏
    show_scenario(
        "场景3 组合干预(替换 F2 与 F3)",
        engine.intervene([Replace("F2", "high"), Replace("F3", False)]),
    )

    # 场景4: 矛盾干预, 预先拒绝且无任何部分变更
    print("\n== 场景4 矛盾干预(移除并替换同一事实 F2) ==")
    try:
        engine.intervene([Remove("F2"), Replace("F2", "high")])
    except InterventionError as exc:
        print(f"已预先拒绝: {exc}")

    # 干预全部结束后, 基线摘要保持不变
    final = engine.baseline()
    print(f"\n干预结束后基线摘要一致: {final.summary() == baseline.summary()}")
    print(f"基线事实未变: {engine.base_facts[1].value == 'low' and engine.base_facts[1].version == 1}")


if __name__ == "__main__":
    main()
