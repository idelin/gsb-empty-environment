"""运行样例：合成数据上的多方求和模拟。

仅演示协议流程与有限域守恒，不构成密码学安全背书。
"""

from secure_sum_sim.protocol import (
    Party,
    RejectionError,
    Round,
    RoundStatus,
    run_round,
)
from secure_sum_sim.sharing import DEFAULT_PRIME, split_secret
from secure_sum_sim.verification import check_conservation, explore_states


def main():
    print("=== 多方求和模拟（仅合成数据，非密码学安全背书）===")
    print(f"有限域模数 p = 2^127 - 1 = {DEFAULT_PRIME}")
    inputs = {"A": 15, "B": 27, "C": 9, "D": 41, "E": 33}
    print(f"参与方输入（合成）: {inputs}")

    print("\n--- 样例 1：1 方掉线，阈值满足（threshold=3，D 掉线）---")
    result, _ = run_round("round-1", inputs, threshold=3, dropped={"D"})
    print(f"状态: {result.status.value}")
    print(f"覆盖集合（活跃方）: {result.covered_parties}")
    print(f"输出总和: {result.value}  （期望 15+27+9+33 = 84）")
    print(f"守恒核验: {check_conservation(result, inputs)}")

    print("\n--- 样例 2：阈值不足（threshold=4，D、E 掉线，活跃方仅 3）---")
    result2, _ = run_round("round-2", inputs, threshold=4, dropped={"D", "E"})
    print(f"状态: {result2.status.value}")
    print(f"输出值: {result2.value}（不输出任何部分和）")
    print(f"原因: {result2.reason}")

    print("\n--- 样例 3：轮次隔离（旧轮份额注入新轮被拒绝）---")
    rnd = Round("round-3", threshold=3)
    for pid, v in inputs.items():
        rnd.commit(Party(pid, v))
    rnd.close_commitment()
    stale = split_secret(5, 5, DEFAULT_PRIME, "round-1", "A", list("ABCDE"))[0]
    try:
        rnd.deliver_share("A", stale)
    except RejectionError as exc:
        print(f"旧轮份额被拒绝: {exc}")
    fresh = rnd.create_shares("A")[0]
    rnd.deliver_share("A", fresh)
    try:
        rnd.deliver_share("A", fresh)
    except RejectionError as exc:
        print(f"重复重传被拒绝: {exc}")

    print("\n--- 样例 4：有限状态探索（200 个随机轮次，统计性核验）---")
    stats = explore_states(200, seed=20261002)
    print(f"轮次总数: {stats['trials']}")
    print(f"完成: {stats['complete']}（守恒核验通过 {stats['conservation_ok']}）")
    print(f"未完成: {stats['incomplete']}（均未输出数值）")
    print(f"违规: {stats['violations']}")
    print("注：以上为统计性 sanity check，不构成形式化隐私证明。")


if __name__ == "__main__":
    main()
