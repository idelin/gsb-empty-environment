"""运行样例: 覆盖 AND 语义、替代证明、撤回、循环种子、时间区间与证明成本。"""

from hyperproof import HyperProofGraph


def show(g: HyperProofGraph, vertices: list[str], t: float = 0.0) -> None:
    status = ", ".join(f"{v}={'成立' if g.holds(v, t) else '不成立'}" for v in vertices)
    print(f"  t={t}: {status}")


print("样例 1: AND 语义 —— {A,B}->C 只有 A 不能推 C")
g = HyperProofGraph()
g.add_edge("e_ab_c", ("A", "B"), "C", cost=2.0)
g.assert_seed("A")
show(g, ["A", "B", "C"])
print("  补种 B 后:")
g.assert_seed("B")
show(g, ["A", "B", "C"])
print(f"  C 的证明成本: {g.proof_cost('C')}")

print()
print("样例 2: 替代证明与撤回 —— 另有 D->C 时撤回 B 仍可由 D 维持 C")
g.add_edge("e_d_c", ("D",), "C", cost=5.0)
g.assert_seed("D")
show(g, ["C"])
print(f"  C 的证明成本(取最小): {g.proof_cost('C')}")
g.retract_seed("B")
print("  撤回 B 后:")
show(g, ["C"])
print(f"  C 的证明成本(改走 D->C): {g.proof_cost('C')}")
g.retract_seed("D")
print("  再撤回 D 后:")
show(g, ["C"])

print()
print("样例 3: 循环证明须有外部种子 —— 纯 C->E->C 无种子时均不成立")
g2 = HyperProofGraph()
g2.add_edge("e_c_e", ("C",), "E")
g2.add_edge("e_e_c", ("E",), "C")
show(g2, ["C", "E"])
print("  断言种子 C 后:")
g2.assert_seed("C")
show(g2, ["C", "E"])
print("  撤回种子 C 后:")
g2.retract_seed("C")
show(g2, ["C", "E"])

print()
print("样例 4: 时间区间左闭右开 —— 种子 A 在 [2, 5) 有效, A->B")
g3 = HyperProofGraph()
g3.add_edge("e_a_b", ("A",), "B")
g3.assert_seed("A", 2.0, 5.0)
for t in (1.999, 2.0, 4.999, 5.0):
    show(g3, ["B"], t)

print()
print("样例 5: 孤立点与多重边")
g4 = HyperProofGraph()
g4.add_vertex("isolated")
g4.add_edge("e1", ("A",), "B", cost=1.0)
g4.add_edge("e2", ("A",), "B", cost=3.0)  # 多重边: 同前置同结论, 不同 ID
print(f"  顶点: {list(g4.vertices)}")
print(f"  超边: {[e.id for e in g4.edges]}")
g4.assert_seed("A")
print(f"  B 的最小证明成本: {g4.proof_cost('B')} (经由 {g4.proof('B').via_edge})")
