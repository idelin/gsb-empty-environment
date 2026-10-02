"""运行样例：有限域加法分享多方求和模拟（仅合成数据，非密码学安全实现）。"""

from __future__ import annotations

import random

from mpc_sum_sim import (
    DEFAULT_PRIME,
    DuplicateShareError,
    Round,
    RoundStatus,
    Share,
    UnauthorizedRecoveryError,
    WrongRoundError,
    share_secret,
)


def banner(title: str) -> None:
    print(f"\n=== {title} ===")


def show(result) -> None:
    print(f"  状态: {result.status.name}")
    print(f"  输出: {result.total}")
    print(f"  有效参与方: {result.effective_parties}")
    print(f"  掉线方: {result.dropped}")
    if result.missing_recovery:
        print(f"  缺失恢复份额: {result.missing_recovery}")
    print(f"  说明: {result.detail}")


def main() -> None:
    print("有限域加法分享多方求和 —— 协议流程模拟（仅合成数据）")
    print(f"有限域模数 p = 2^61 - 1 = {DEFAULT_PRIME}")
    print("声明：本演示不构成密码学安全背书；串谋与认证不在模拟保证范围内。")

    parties = ("A", "B", "C", "D", "E")
    inputs = {"A": 100, "B": 200, "C": 300, "D": 400, "E": 500}

    banner("场景 1：无掉线，正常完成（阈值 t=3）")
    rnd = Round(round_id=1, threshold=3, rng=random.Random(1))
    rnd.commit(parties)
    for dealer, secret in inputs.items():
        rnd.deal(dealer, secret)
    rnd.close_sharing()
    rnd.close_dropout_confirmation()
    result = rnd.finalize()
    show(result)
    assert result.total == sum(inputs.values()) % DEFAULT_PRIME

    banner("场景 2：C、E 掉线，恢复后完成（输出仅含幸存方输入）")
    rnd = Round(round_id=2, threshold=3, rng=random.Random(2))
    rnd.commit(parties)
    for dealer, secret in inputs.items():
        rnd.deal(dealer, secret)
    rnd.close_sharing()
    rnd.declare_dropout("C")
    rnd.declare_dropout("E")
    rnd.close_dropout_confirmation()
    for dropped in ("C", "E"):
        for survivor in ("A", "B", "D"):
            rnd.submit_recovery(rnd._shares[(survivor, dropped)])
    result = rnd.finalize()
    show(result)
    assert result.total == (100 + 200 + 400) % DEFAULT_PRIME

    banner("场景 3：幸存方低于阈值，中止且无任何输出")
    rnd = Round(round_id=3, threshold=3, rng=random.Random(3))
    rnd.commit(parties)
    for dealer, secret in inputs.items():
        rnd.deal(dealer, secret)
    rnd.close_sharing()
    for party in ("C", "D", "E"):
        rnd.declare_dropout(party)
    rnd.close_dropout_confirmation()
    show(rnd.finalize())

    banner("场景 4：缺少必要恢复份额，返回未完成")
    rnd = Round(round_id=4, threshold=3, rng=random.Random(4))
    rnd.commit(parties)
    for dealer, secret in inputs.items():
        rnd.deal(dealer, secret)
    rnd.close_sharing()
    rnd.declare_dropout("E")
    rnd.close_dropout_confirmation()
    # 只提交部分恢复材料，故意缺 (B, E)
    for survivor in ("A", "C", "D"):
        rnd.submit_recovery(rnd._shares[(survivor, "E")])
    show(rnd.finalize())

    banner("场景 5：违规行为拒绝演示")
    rnd = Round(round_id=5, threshold=3, rng=random.Random(5))
    rnd.commit(parties)
    old_share = share_secret(1, 4, "A", parties, DEFAULT_PRIME, random.Random(9))[0]
    try:
        rnd.submit_share(old_share)
    except WrongRoundError as exc:
        print(f"  旧轮分享被拒绝: {exc}")
    legit = rnd.deal("A", 100)[0]
    try:
        rnd.submit_share(legit)
    except DuplicateShareError as exc:
        print(f"  重复重传被拒绝: {exc}")
    for dealer in ("B", "C", "D", "E"):
        rnd.deal(dealer, inputs[dealer])
    rnd.close_sharing()
    rnd.declare_dropout("E")
    rnd.close_dropout_confirmation()
    stray = rnd._shares[("A", "B")]  # B 未掉线
    try:
        rnd.submit_recovery(stray)
    except UnauthorizedRecoveryError as exc:
        print(f"  未声明掉线方的恢复材料被拒绝: {exc}")
    forged = Share(5, "A", "E", (rnd._shares[("A", "E")].value + 1) % DEFAULT_PRIME)
    try:
        rnd.submit_recovery(forged)
    except Exception as exc:
        print(f"  篡改的恢复份额被拒绝: {exc}")
    print(f"  （轮次 5 最终状态: {rnd.finalize().status.name}）")


if __name__ == "__main__":
    main()
