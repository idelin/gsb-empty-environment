"""运行样例：在若干有限状态模型上检查“请求后最终响应”。

用法（仓库根目录下）：
    python3.11 -m examples.run_sample
"""
from liveness import FairnessSet, Verdict, check, replay

from examples import models


def show(title, model, result, do_replay=True, **check_kw):
    print("=" * 68)
    print(f"场景: {title}")
    print(f"模型: {model.name}  状态数={len(model.states)}  迁移数={len(model.transitions)}")
    fairness = check_kw.get("fairness", ())
    if fairness:
        print("公平假设: " + "; ".join(
            f"{fs.name}({fs.kind})={sorted(fs.actions)}" for fs in fairness
        ))
    else:
        print("公平假设: 无")
    limits = {k: v for k, v in check_kw.items() if k.startswith("max_")}
    if limits:
        print(f"搜索限制: {limits}")
    print(f"判定: {result.verdict.value}"
          f"（探索状态 {result.explored_states}，迁移 {result.explored_transitions}，"
          f"搜索{'完备' if result.complete else '被截断'}）")
    for note in result.notes:
        print(f"备注: {note}")
    if result.counterexample is not None:
        print("反例:")
        print(result.counterexample.render())
        if do_replay:
            report = replay(model, result.counterexample, fairness=fairness)
            print("重放验证:")
            print(report.render())
    print()


def main():
    # 1. 自循环即反例：服务器永远忽略请求。
    m = models.deaf_server()
    r = check(m)
    show("自循环反例（无公平假设）", m, r)

    # 2. 同一模型结构下，无公平假设时 ignore 自循环构成反例。
    m = models.unreliable_server()
    r = check(m)
    show("忽略循环构成反例（无公平假设）", m, r)

    # 3. 加入弱公平（respond 不可被持续忽视）后，性质成立。
    fair = [FairnessSet("respond-progress", {"respond"}, kind="weak")]
    r = check(m, fairness=fair)
    show("弱公平假设下性质成立", m, r, fairness=fair)

    # 4. 多状态不履行循环：派发-丢失循环。
    m = models.lossy_channel()
    r = check(m)
    show("多状态循环反例（无公平假设）", m, r)

    # 5. delivered 仅在 sending 可执行：弱公平不足以排除丢失循环，
    #    需要强公平（无限次可执行则必须执行）。
    fair = [FairnessSet("eventual-delivery", {"delivered"}, kind="weak")]
    r = check(m, fairness=fair)
    show("弱公平不足以排除丢失循环（仍违反）", m, r, fairness=fair)
    fair = [FairnessSet("eventual-delivery", {"delivered"}, kind="strong")]
    r = check(m, fairness=fair)
    show("强公平（终将送达）下性质成立", m, r, fairness=fair)

    # 6. 弱公平 vs 强公平：fix 仅在状态 a 可执行。
    m = models.flaky_endpoint()
    r = check(m, fairness=[FairnessSet("fix-weak", {"fix"}, kind="weak")])
    show("弱公平下仍是反例（fix 非持续可执行）", m, r,
         fairness=[FairnessSet("fix-weak", {"fix"}, kind="weak")])
    r = check(m, fairness=[FairnessSet("fix-strong", {"fix"}, kind="strong")])
    show("强公平下性质成立（fix 无限次可执行）", m, r,
         fairness=[FairnessSet("fix-strong", {"fix"}, kind="strong")])

    # 7. 截断 -> 未决：响应链太长，深度限制内看不到响应，不能直接判违反。
    m = models.slow_responder(n=6)
    r = check(m, max_depth=3)
    show("深度截断 -> 未决（有限截断未见响应不算违反）", m, r, max_depth=3)
    r = check(m)
    show("搜索完备后性质成立", m, r)

    # 8. 不可达的坏循环不构成反例。
    m = models.unreachable_bad_loop()
    r = check(m)
    show("不可达坏循环被忽略，性质成立", m, r)


if __name__ == "__main__":
    main()
