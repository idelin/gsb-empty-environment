"""Run sample: verify the example model, replay the counterexample, shrink it.

Usage: python3.11 demo.py
"""
from __future__ import annotations

from example_model import FAULT_UNIVERSE, build_model
from model import replay
from shrinker import Scenario, Shrinker
from verifier import verify


def show(title):
    print("=" * 68)
    print(title)
    print("=" * 68)


def main():
    model = build_model()

    show("1. 有界验证：故障全集内搜索性质违反")
    result = verify(model, fault_universe=FAULT_UNIVERSE,
                    max_states=10000, max_depth=32)
    print(result.summary())

    show("2. 独立重放验证器给出的反例（必须可重放）")
    check = replay(model, result.counterexample.config,
                   result.counterexample.actions, result.counterexample.faults)
    print(f"replay status : {check.status}")
    print(f"replay error  : {check.error}  (与验证器报告一致: "
          f"{check.error == result.error})")

    show("3. 无故障子模型：穷举完成，判定 safe")
    safe = verify(model, fault_universe=frozenset(),
                  max_states=10000, max_depth=32)
    print(safe.summary())

    show("4. 状态预算过小：搜索未完成，保持 undecided")
    undecided = verify(model, fault_universe=FAULT_UNIVERSE,
                       max_states=3, max_depth=32)
    print(undecided.summary())

    show("5. 缩减一个臃肿的反例场景（初始配置 + 动作轨迹 + 故障集合）")
    bloated = Scenario(
        config={"primary_up": True, "cache_warm": True, "debug": True},
        actions=["noop", "write", "noop", "replicate", "noop", "read",
                 "noop", "noop"],
        faults=frozenset({"partition", "logging", "metrics"}),
    )
    print("原始场景:")
    print(bloated.describe())
    before = replay(model, bloated.config, bloated.actions, bloated.faults)
    print(f"原始重放      : status={before.status}, error={before.error}, "
          f"在第 {before.steps} 步触发")
    report = Shrinker(model, budget=1000).shrink(bloated)
    print("\n缩减过程:")
    for entry in report.log:
        print(f"  - {entry}")
    print("\n缩减报告:")
    print(report.summary())

    show("6. 独立重放局部最小场景，确认触发同一性质违反")
    after = replay(model, report.scenario.config, report.scenario.actions,
                   report.scenario.faults)
    print(f"replay status : {after.status}")
    print(f"replay error  : {after.error}  (与目标错误一致: "
          f"{after.error == report.error})")

    show("7. 重放预算耗尽：不得宣称任何最小性")
    tight = Shrinker(model, budget=4).shrink(bloated)
    print(tight.summary())


if __name__ == "__main__":
    main()
