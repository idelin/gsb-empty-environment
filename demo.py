"""运行样例：覆盖题目要求的三个关键场景。"""

from proofnet import Interval, ProofNetwork


def show(net, vertex, t=0):
    state = "成立" if net.holds(vertex, t) else "不成立"
    print(f"  {vertex} @ t={t}: {state} (cost={net.proof_cost(vertex, t):g})")


print("场景 1: {A,B}->C 是 AND 语义，只有 A 推不出 C")
net = ProofNetwork()
net.add_edge("e1", ("A", "B"), "C", cost=2.0)
net.add_seed("sA", "A")
show(net, "C")
print("  补种 B 之后：")
net.add_seed("sB", "B")
show(net, "C")

print("\n场景 2: 另有 D->C 时，撤回 B 仍可由 D 维持 C（替代证明）")
net.add_edge("e2", ("D",), "C", cost=5.0)
net.add_seed("sD", "D")
net.retract_seed("sB")
show(net, "C")
print("  当前 C 的证明树：")
print(net.proof("C").pretty())

print("\n场景 3: 纯循环 C->E->C 无外部种子时均不成立")
net2 = ProofNetwork()
net2.add_edge("c2e", ("C",), "E")
net2.add_edge("e2c", ("E",), "C")
show(net2, "C")
show(net2, "E")
print("  加入外部种子 sC 后：")
net2.add_seed("sC", "C")
show(net2, "C")
show(net2, "E")
print("  撤回种子 sC 后：")
net2.retract_seed("sC")
show(net2, "C")
show(net2, "E")

print("\n附: 时间区间左闭右开 [2, 5)")
net3 = ProofNetwork()
net3.add_seed("s", "A", Interval(2, 5))
for t in (1, 2, 4, 5):
    show(net3, "A", t)
