# -*- coding: utf-8 -*-
"""dynflow 可运行样例：python3.11 example.py"""

import dynflow


def show(title, rep):
    print(f"== {title} " + "=" * (60 - len(title)))
    print(rep.explain())
    print("流:", dict(sorted(rep.flow.items())))
    print("割源侧:", rep.cut.source_side, "割容量:", rep.cut.capacity)
    print()


# 1) 题面样例：平行 S->T 容量 2、3，最大流 5；把 3 降为 1 后流值变 3
net = dynflow.FlowNetwork(
    nodes=["s", "t"],
    edges=[("e1", "s", "t", 2), ("e2", "s", "t", 3)],
    balances={"s": 5, "t": -5})
hist = dynflow.FlowHistory(net, initial_time=0)
print("初始最大流:", hist.latest.value, hist.latest.flow_dict())
print()
rep = hist.apply_update(1, capacity={"e2": 1})
show("平行边收缩 e2: 3->1", rep)

# 2) 菱形：容量下降迫使反向残余改道（路径 a->s->c->b 用了 s->a 的反向弧）
net2 = dynflow.FlowNetwork(
    nodes=["s", "a", "b", "c", "t"],
    edges=[("sa", "s", "a", 2), ("sc", "s", "c", 1), ("ab", "a", "b", 2),
           ("cb", "c", "b", 1), ("bt", "b", "t", 2), ("at", "a", "t", 0)],
    balances={"s": 2, "t": -2})
hist2 = dynflow.FlowHistory(net2, initial_time=0)
rep2 = hist2.apply_update(1, capacity={"ab": 1})
show("菱形 ab: 2->1（反向残余改道，流值不降）", rep2)

# 3) 批量更新：停用边 + 供需变化，一次原子提交
rep3 = hist2.apply_update(2, deactivate=("sc",), balances={"s": 1, "t": -1})
show("批量：停用 sc 且供需降为 1", rep3)

# 4) 不可满足供需：默认提交最大可行流并报告缺口；strict 则回滚
net4 = dynflow.FlowNetwork(
    nodes=["s", "t"],
    edges=[("e", "s", "t", 2)],
    balances={"s": 5, "t": -5})
hist4 = dynflow.FlowHistory(net4, initial_time=0)
rep4 = hist4.apply_update(1, balances={"t": -9})
show("需求 9 > 割容量 2：尽力流 + 缺口报告", rep4)
try:
    hist4.apply_update(2, balances={"t": -99}, strict=True)
except dynflow.InfeasibleFlowError as exc:
    print("strict 模式拒绝不可满足更新，快照不变:", exc)
    print("当前版本仍为 v%d，流值 %d" % (hist4.latest.version,
                                        hist4.latest.value))
print()

# 5) 读旧快照：版本区间为左闭右开 [t_k, t_k+1)
print("== 历史快照（左闭右开区间）" + "=" * 36)
for snap in hist2.snapshots:
    lo, hi = snap.interval
    print(f"v{snap.version}: 区间 [{lo}, {hi})，流值 {snap.value}，"
          f"流 {snap.flow_dict()}")
print("at(0) -> v%d, at(1) -> v%d, at(2) -> v%d" % (
    hist2.at(0).version, hist2.at(1).version, hist2.at(2).version))
