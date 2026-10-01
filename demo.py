"""运行样例：python3.11 demo.py"""
import checker
from lexprop import Contradiction, LexConstraint, Propagator, Variable


def banner(title):
    print("=" * 66)
    print(title)
    print("=" * 66)


def show_domains(prop):
    for var in prop.constraint.variables:
        print(f"  {var.name} <- {sorted(prop.domains[var])}")


def show_deletions(deletions):
    if not deletions:
        print("  （无删值）")
    for deletion in deletions:
        print(deletion.explanation.render())


def verify_with_checker(con, original_domains, prop):
    witness = checker.find_witness(con, prop.domains)
    assert witness is not None and checker.evaluate(con, witness)
    ok, supported = checker.verify_pruned_domains(con, original_domains, prop.domains)
    named = {v.name: sorted(supported[v]) for v in con.variables}
    print(f"  独立检验器：有解断言成立（见证 { {v.name: a for v, a in witness.items()} }），"
          f"保留域恰好为全部支持值 -> {ok}（支持值 {named}）")


def scenario_prefix_pruning():
    banner("样例 1：非严格字典序，前缀被迫相等 -> 后位过滤")
    x0, x1, y0, y1 = (Variable(s) for s in ("x0", "x1", "y0", "y1"))
    domains = {x0: {2}, x1: {1, 2, 3}, y0: {2}, y1: {2}}
    con = LexConstraint([x0, x1], [y0, y1])
    print(f"约束：{con.describe()}，初始域：x1∈{{1,2,3}}, y1∈{{2}}, x0=y0=2")
    prop = Propagator(con, domains)
    result = prop.propagate()
    print(f"结果：{result.message}")
    print("删值及原约束证明：")
    show_deletions(result.deletions)
    print("剩余域：")
    show_domains(prop)
    verify_with_checker(con, domains, prop)
    print()


def scenario_strict_different_lengths():
    banner("样例 2：严格字典序 + 不同长度（长度规则：真前缀）")
    x0, y0, y1 = (Variable(s) for s in ("x0", "y0", "y1"))
    domains = {x0: {1, 2}, y0: {1}, y1: {1, 2}}
    con = LexConstraint([x0], [y0, y1], strict=True)
    print(f"约束：{con.describe()}，初始域：x0∈{{1,2}}, y0∈{{1}}, y1∈{{1,2}}")
    prop = Propagator(con, domains)
    result = prop.propagate()
    print(f"结果：{result.message}")
    print("删值及原约束证明：")
    show_deletions(result.deletions)
    print("剩余域：")
    show_domains(prop)
    verify_with_checker(con, domains, prop)
    print()


def scenario_shared_variables():
    banner("样例 3：共享变量 (a,b) <lex (b,a)，等价于 a < b")
    a, b = Variable("a"), Variable("b")
    domains = {a: {1, 2, 3}, b: {1, 2, 3}}
    con = LexConstraint([a, b], [b, a], strict=True)
    print(f"约束：{con.describe()}，初始域：a,b∈{{1,2,3}}")
    prop = Propagator(con, domains)
    result = prop.propagate()
    print(f"结果：{result.message}")
    print("删值及原约束证明：")
    show_deletions(result.deletions)
    print("剩余域：")
    show_domains(prop)
    verify_with_checker(con, domains, prop)
    print()


def scenario_same_vector_and_empty():
    banner("样例 4：相同向量与空向量的长度规则")
    a, b = Variable("a"), Variable("b")
    con = LexConstraint([a, b], [a, b], strict=True)
    print(f"约束：{con.describe()}（相同向量，严格）")
    try:
        Propagator(con, {a: {1, 2}, b: {1, 2}}).propagate()
    except Contradiction as exc:
        print("按预期矛盾，证明：")
        print(exc.explanation.render())
    y = Variable("y")
    con_empty = LexConstraint([], [y], strict=True)
    prop = Propagator(con_empty, {y: {1, 2}})
    result = prop.propagate()
    print(f"约束：{con_empty.describe()}（空向量 < 非空向量）-> {result.message}，y 域不变 {sorted(prop.domains[y])}")
    print()


def scenario_push_pop():
    banner("样例 5：push/pop 恢复域与前缀支持缓存")
    x0, x1, y0, y1 = (Variable(s) for s in ("x0", "x1", "y0", "y1"))
    domains = {x0: {2}, x1: {1, 2, 3}, y0: {2}, y1: {2}}
    con = LexConstraint([x0, x1], [y0, y1])
    prop = Propagator(con, domains)
    prop.propagate()
    print(f"传播后 x1 域：{sorted(prop.domains[x1])}，前缀缓存条目：{sorted(prop._prefix_cache)}")
    prop.push()
    prop.domains[x1].discard(1)
    prop._prefix_cache.clear()
    print(f"push 后破坏状态：x1 域 {sorted(prop.domains[x1])}，缓存 {sorted(prop._prefix_cache)}")
    prop.pop()
    print(f"pop 恢复后：x1 域 {sorted(prop.domains[x1])}，缓存条目 {sorted(prop._prefix_cache)}")
    print()


def scenario_budget():
    banner("样例 6：预算不足时明确报告未达到固定点")
    x0, x1, y0, y1 = (Variable(s) for s in ("x0", "x1", "y0", "y1"))
    domains = {x0: {1, 2}, x1: {1}, y0: {2}, y1: {1}}
    con = LexConstraint([x0, x1], [y0, y1], strict=True)
    print(f"约束：{con.describe()}，x0∈{{1,2}}, y0=2, x1=y1=1")
    prop = Propagator(con, domains)
    result = prop.propagate(budget=1)
    print(f"budget=1 -> status={result.status}：{result.message}")
    prop2 = Propagator(con, domains)
    result2 = prop2.propagate()
    print(f"预算充足 -> status={result2.status}：{result2.message}，x0 域 {sorted(prop2.domains[x0])}")
    print()


if __name__ == "__main__":
    scenario_prefix_pruning()
    scenario_strict_different_lengths()
    scenario_shared_variables()
    scenario_same_vector_and_empty()
    scenario_push_pop()
    scenario_budget()
