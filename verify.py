"""验证入口：对给定有限状态模型执行安全性与公平进展检查。

用法: python3.11 verify.py
"""

from barrier_model import Model, canon, fmt_trace
from model_checker import (
    LASSO, PENDING, SAFE, VIOLATION,
    check_liveness, check_safety, replay_violation, verify_lasso,
)


def show_safety(title, result):
    print(f"== {title} ==")
    print(f"  结论: {result.status}  已探索状态: {result.states}  迁移: {result.transitions}")
    if result.status == VIOLATION:
        print(f"  违反性质: {', '.join(result.violated)}")
        print(f"  最短反例（{len(result.trace)} 步）: {fmt_trace(result.trace)}")
    elif result.status == SAFE:
        print(f"  完整闭包证书: {result.certificate}")
    elif result.status == PENDING:
        print("  预算不足，搜索未完成：结论保持未决（PENDING），不声称无错误。")
    print()


def main():
    # 场景 1：正确模型，全交错安全性，完整闭包证书。
    r1 = check_safety(Model(n=3, max_round=3))
    show_safety("场景1 正确模型安全性 (n=3, max_round=3, 全交错)", r1)

    # 场景 2：正确模型，公平进展 -> 可重放套索。
    print("== 场景2 正确模型公平进展 (n=2, max_round=2, 逐动作弱公平) ==")
    m2 = Model(n=2, max_round=2)
    r2 = check_liveness(m2)
    print(f"  结论: {r2.status}  可达状态: {r2.states}")
    if r2.status == LASSO:
        print(f"  stem: {fmt_trace(r2.stem)}")
        print(f"  loop: {fmt_trace(r2.loop)}")
        verify_lasso(m2, r2.stem, r2.loop)
        print("  套索重放校验通过：loop 回到起点、无放行、有人等待、弱公平。")
    print()

    # 场景 3：轮次混淆缺陷 -> 最短反例。
    m3 = Model(n=2, max_round=3, variant="round_confusion")
    r3 = check_safety(m3, )
    show_safety("场景3 轮次混淆缺陷 (round_confusion, n=2)", r3)
    _, bad = replay_violation(m3, r3.trace)
    print(f"  反例重放确认违反: {', '.join(bad)}\n")

    # 场景 4：旧轮延迟通知缺陷 -> 最短反例。
    m4 = Model(n=2, max_round=3, variant="stale_notif")
    r4 = check_safety(m4)
    show_safety("场景4 旧轮延迟通知缺陷 (stale_notif, n=2)", r4)
    _, bad4 = replay_violation(m4, r4.trace)
    print(f"  反例重放确认违反: {', '.join(bad4)}\n")

    # 场景 5：预算不足 -> PENDING。
    r5 = check_safety(Model(n=3, max_round=3), max_states=10)
    show_safety("场景5 预算不足 (max_states=10)", r5)

    # 场景 6：对称归约 vs 全交错参考核验。
    print("== 场景6 对称归约核验 (n=3, max_round=2) ==")
    m6 = Model(n=3, max_round=2)
    full = check_safety(m6, reduced=False)
    red = check_safety(m6, reduced=True)
    same_verdict = full.status == red.status
    same_cover = {canon(s) for s in full.explored} == set(red.explored)
    print(f"  全交错: {full.status}, 状态 {full.states}")
    print(f"  归约后: {red.status}, 状态 {red.states}")
    print(f"  结论一致: {same_verdict}  规范化状态集一致: {same_cover}")
    print()


if __name__ == "__main__":
    main()
