"""运行样例：对称压缩模型检查演示。

用法：python3.11 demo.py
"""

from symmc import Checker, Verdict
from symmc.examples import build_leader_model, build_mutex_model


def show(title):
    print("=" * 64)
    print(title)
    print("=" * 64)


def compare(model):
    compressed = Checker(model, use_symmetry=True).run()
    plain = Checker(model, use_symmetry=False).run()
    print(f"模型 {model.name}: 对称组 {model.groups}")
    print(f"  压缩前: verdict={plain.verdict.value:9s} 状态数={plain.explored}")
    print(f"  压缩后: verdict={compressed.verdict.value:9s} 状态数={compressed.explored}")
    print(f"  性质一致: {compressed.verdict == plain.verdict}")
    return compressed


def main():
    show("1. 安全互斥模型（3 个同构 worker）：压缩前后性质一致")
    compare(build_mutex_model(3))

    show("2. 缺陷互斥模型：发现反例，保留 ID 映射，可逐步重放")
    result = compare(build_mutex_model(3, buggy=True))
    model = build_mutex_model(3, buggy=True)
    print("  反例路径（具体状态，可重放）:")
    for i, step in enumerate(result.counterexample):
        shared, locals_ = step.state
        actor = "init" if step.pid is None else f"P{step.pid}"
        print(f"    步{i} [{actor:5s}] lock={shared} locals={locals_} "
              f"mapping={step.mapping}")
    print(f"  末态违反不变式: {not model.invariant(result.counterexample[-1].state)}")
    print(f"  在原始模型上重放通过: {result.replay(model)}")

    show("3. 领导者模型：特殊角色不参与置换")
    model = build_leader_model(2)
    compare(model)
    state = (0, (2, 1, 0))  # leader=2, w0=1, w1=0
    canon, mapping = model.canonicalize(state)
    print(f"  状态 {state} 规范化 -> {canon}, mapping={mapping}")
    print(f"  领导者位置(0)保持不动: {canon[1][0] == 2}")
    swapped = (0, (0, 1, 2))  # 若把 leader 与 w1 交换
    print(f"  与 leader 交换后的状态 {swapped} 规范化 -> "
          f"{model.canonicalize(swapped)[0]}（与前者不同，未混同）")

    show("4. 对称条件动态审计（audit_symmetry）")
    for m in (build_mutex_model(3), build_leader_model(2)):
        print(f"  {m.name}: audit_symmetry -> {m.audit_symmetry()}")

    show("5. 搜索未完成的模型保持未决（PENDING）")
    model = build_mutex_model(4)
    capped = Checker(model, use_symmetry=True, max_states=3).run()
    print(f"  mutex-4, max_states=3: verdict={capped.verdict.value} "
          f"explored={capped.explored}")


if __name__ == "__main__":
    main()
