"""运行样例：合成数据上的预算预留 / 提交 / 取消与独立校验。

仅使用合成数据；本样例的统计输出不构成形式化隐私证明。
"""

import os
import random
import tempfile

from dpledger import BudgetLedger, BudgetExhaustedError, verify


def main() -> None:
    tmp = tempfile.TemporaryDirectory()
    db = os.path.join(tmp.name, "demo_ledger.db")
    ledger = BudgetLedger(db, total_epsilon=3.0, rng=random.Random(2024))

    # 合成数据：1000 个虚拟用户的年龄段计数（非真实数据）。
    rng = random.Random(42)
    synthetic_ages = [rng.randrange(0, 100) for _ in range(1000)]
    count_under_30 = sum(1 for a in synthetic_ages if a < 30)

    count_query = {"type": "count", "dataset": "synthetic_users",
                   "predicate": "age < 30", "sensitivity": 1.0}
    hist_query = {"type": "histogram", "dataset": "synthetic_users",
                  "bins": 10, "sensitivity": 1.0}

    print("== 预留 ==")
    ledger.reserve("count-under-30", count_query, 1.0)
    ledger.reserve("age-histogram", hist_query, 1.5)
    print("budget:", ledger.budget())

    print("\n== 提交发布（噪声 + 扣减预算，单一持久事务）==")
    out = ledger.commit("count-under-30", count_query,
                        true_value=float(count_under_30))
    print(f"真实计数(合成): {count_under_30}, 发布值: {out['noisy_value']:.3f}")
    print("budget:", ledger.budget())

    print("\n== 幂等重试：复用原噪声与预算记录 ==")
    retry = ledger.commit("count-under-30", count_query,
                          true_value=float(count_under_30))
    print(f"reused={retry['reused']}, 发布值不变: "
          f"{retry['noisy_value'] == out['noisy_value']}")

    print("\n== 取消未发布查询：返还预算 ==")
    ledger.cancel("age-histogram", hist_query)
    print("budget:", ledger.budget())

    print("\n== 已发布查询不可取消（不退还预算）==")
    try:
        ledger.cancel("count-under-30", count_query)
    except Exception as exc:
        print(f"拒绝取消: {type(exc).__name__}: {exc}")

    print("\n== 超预算预留被拒绝 ==")
    try:
        ledger.reserve("too-big", count_query, 99.0)
    except BudgetExhaustedError as exc:
        print(f"拒绝预留: {exc}")

    print("\n== 独立状态机核对公开日志 ==")
    report = verify(ledger.events(), total_epsilon=3.0)
    print("守恒核对:", {k: report[k] for k in
                       ("total", "reserved", "spent", "available")})
    print("已发布(最多一次):", report["published"])

    ledger.close()
    tmp.cleanup()


if __name__ == "__main__":
    main()
