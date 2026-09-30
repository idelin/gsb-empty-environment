"""运行样例：python3.11 examples/demo.py"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from lexprop import (LexConstraint, Solver, Var, check_assignment,
                     enumerate_supported_values)


def show(title):
    print("=" * 60)
    print(title)
    print("=" * 60)


def show_domains(s):
    print("  保留域:", {v.name: sorted(d) for v, d in s.domains.items()})


# 样例 1：不同长度 + 严格比较，支持过滤并输出删值证明
show("样例 1: lex_less([x0,x1], [y0]) —— 不同长度 + 严格")
x0, x1, y0 = Var("x0"), Var("x1"), Var("y0")
c1 = LexConstraint([x0, x1], [y0], strict=True, name="c1")
s1 = Solver({x0: {1, 2}, x1: {0, 1, 2}, y0: {1, 2}}, [c1])
s1.propagate()
alpha, beta = c1.prefix_stats(s1.domains)
print(f"  前缀相等长度 alpha={alpha}, 首个可能首差位置 beta={beta}")
show_domains(s1)
print("  删值证明：")
for p in s1.proofs:
    print("   " + p.render().replace("\n", "\n   "))
    print(f"    => 独立复核: {'通过' if p.verify(c1) else '失败'}")

# 样例 2：共享变量 [a,b] < [b,a] 当且仅当 a < b
show("样例 2: lex_less([a,b], [b,a]) —— 共享变量")
a, b = Var("a"), Var("b")
c2 = LexConstraint([a, b], [b, a], strict=True, name="c2")
s2 = Solver({a: {0, 1, 2}, b: {0, 1, 2}}, [c2])
s2.propagate()
show_domains(s2)
supported, count = enumerate_supported_values([c2], {a: {0, 1, 2}, b: {0, 1, 2}})
print(f"  独立枚举: {count} 个解, 支持值 a={sorted(supported[a])}, b={sorted(supported[b])}")
print(f"  保留域恰好等于支持值: {s2.domains[a] == supported[a] and s2.domains[b] == supported[b]}")

# 样例 3：push/pop 恢复域与前缀支持缓存
show("样例 3: push/pop —— 恢复域与前缀支持缓存")
x, y = Var("x"), Var("y")
c3 = LexConstraint([x], [y], strict=False, name="c3")
s3 = Solver({x: {0, 1, 2}, y: {1, 2}}, [c3])
s3.propagate()
s3.push()
print("  push 时缓存条目数:", len(c3._cache))
s3.domains[x] = {2}
s3.propagate()
print("  深入后: ", {v.name: sorted(d) for v, d in s3.domains.items()},
      "缓存条目数:", len(c3._cache))
s3.pop()
print("  pop 后: ", {v.name: sorted(d) for v, d in s3.domains.items()},
      "缓存条目数:", len(c3._cache))

# 样例 4：求解 + 独立检查器验证
show("样例 4: 求解并用独立检查器验原约束")
p, q, r_ = Var("p"), Var("q"), Var("r")
c4 = LexConstraint([p, q], [q, r_], strict=True, name="c4")
s4 = Solver({p: {0, 1}, q: {0, 1}, r_: {0, 1}}, [c4])
res = s4.solve()
print(f"  状态: {res.status} —— {res.message}")
print("  解:", {v.name: val for v, val in res.solution.items()})
print("  独立检查器复核:", check_assignment(c4.xs, c4.ys, c4.strict, res.solution))

# 样例 5：预算不足时明确报告未达到固定点
show("样例 5: 预算不足")
u, v = Var("u"), Var("v")
c5 = LexConstraint([u], [v], name="c5")
res5 = Solver({u: {0, 1, 2}, v: {0, 1, 2}}, [c5], budget_steps=0).solve()
print(f"  状态: {res5.status}, fixpoint_reached={res5.fixpoint_reached}")
print(f"  消息: {res5.message}")

# 样例 6：空向量与相同向量的长度规则
show("样例 6: 空向量 / 相同向量")
print("  lex_leq([], [])      :", Solver({}, [LexConstraint([], [])]).solve().status)
print("  lex_less([], [])     :", Solver({}, [LexConstraint([], [], strict=True)]).solve().status)
w = Var("w")
print("  lex_less([], [w])    :", Solver({w: {7}}, [LexConstraint([], [w], strict=True)]).solve().status)
print("  lex_less([w], [])    :", Solver({w: {7}}, [LexConstraint([w], [], strict=True)]).solve().status)
print("  lex_less([w], [w])   :", Solver({w: {7}}, [LexConstraint([w], [w], strict=True)]).solve().status)
print("  lex_leq([w], [w])    :", Solver({w: {7}}, [LexConstraint([w], [w])]).solve().status)
