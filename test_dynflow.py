# -*- coding: utf-8 -*-
"""dynflow 的 unittest 测试。

校验策略
--------
1. **独立校验器**（不复用实现内部代码）：容量约束、流守恒（含超级点终端）、
   流值与割容量相等、割证书的残余可达性独立重算。
2. **穷举小割**：枚举原始顶点的全部 2^n 子集求最小割，与流值交叉检验。
3. **多轮随机更正**：固定随机种子，在含多重边/零容量边/孤立点的小图上
   做几十轮随机批更新，每轮都跑独立校验 + 穷举割；非法更新验证快照不变。
"""

import itertools
import random
import unittest
from collections import deque

import dynflow


SUPER_S = "\x00S*"
SUPER_T = "\x00T*"


# ------------------------------------------------------------ 独立校验器
def independent_verify(snap: dynflow.Snapshot, msg: str = "") -> None:
    """仅依据快照数据独立校验合法性、守恒、最大流-最小割等价。"""
    edges = {eid: (u, v, cap, active)
             for eid, u, v, cap, active in snap.edges}
    flow = snap.flow_dict()
    bal = snap.balances_dict()

    # 1) 容量约束
    for eid, f in flow.items():
        u, v, cap, active = edges[eid]
        assert isinstance(f, int), f"{msg}: 流必须是整数 ({eid}={f})"
        if active:
            assert 0 <= f <= cap, f"{msg}: 超容量 {eid} f={f} cap={cap}"
        else:
            assert f == 0, f"{msg}: 停用边 {eid} 仍有流 {f}"

    # 2) 流守恒：净出流必须落在终端弧允许区间内
    net_out = {n: 0 for n in snap.nodes}
    for eid, f in flow.items():
        u, v, _, active = edges[eid]
        if active:
            net_out[u] += f
            net_out[v] -= f
    value_from_supply = 0
    value_from_demand = 0
    for n, b in bal.items():
        d = net_out[n]
        if b > 0:
            assert 0 <= d <= b, f"{msg}: 供应点 {n} 净出流 {d} 越界 [0,{b}]"
            value_from_supply += d
        elif b < 0:
            assert b <= d <= 0, f"{msg}: 需求点 {n} 净出流 {d} 越界 [{b},0]"
            value_from_demand += -d
        else:
            assert d == 0, f"{msg}: 转运点 {n} 不守恒 ({d})"
    for n in snap.nodes:
        if n not in bal:
            assert net_out[n] == 0, f"{msg}: 节点 {n} 不守恒"
    assert value_from_supply == snap.value, (
        f"{msg}: 供应侧合计 {value_from_supply} != 流值 {snap.value}")
    assert value_from_demand == snap.value, (
        f"{msg}: 需求侧合计 {value_from_demand} != 流值 {snap.value}")

    # 3) 独立重建残余网络，重算源侧可达集
    adj: dict[str, list[tuple[str, int]]] = {
        n: [] for n in snap.nodes}
    adj[SUPER_S] = []
    adj[SUPER_T] = []
    for eid, (u, v, cap, active) in edges.items():
        if active:
            adj[u].append((v, cap - flow[eid]))
            adj[v].append((u, flow[eid]))
    for n, b in bal.items():
        if b > 0:
            # 快照不含终端流；供应终端流等于净出流
            tf = net_out[n]
            adj[SUPER_S].append((n, b - tf))
            adj[n].append((SUPER_S, tf))
        elif b < 0:
            tf = -net_out[n]
            adj[n].append((SUPER_T, -b - tf))
            adj[SUPER_T].append((n, tf))
    seen = {SUPER_S}
    q = deque([SUPER_S])
    while q:
        x = q.popleft()
        for y, r in adj[x]:
            if r > 0 and y not in seen:
                seen.add(y)
                q.append(y)
    reachable = frozenset(n for n in snap.nodes if n in seen)
    assert reachable == frozenset(snap.cut.source_side), (
        f"{msg}: 割源侧不一致 {reachable} != {snap.cut.source_side}")

    # 4) 独立计算跨割容量
    cut_cap = 0
    for eid, (u, v, cap, active) in edges.items():
        if active and (u in reachable) != (v in reachable) and u in reachable:
            cut_cap += cap
    for n, b in bal.items():
        if b > 0 and n not in reachable:
            cut_cap += b
        elif b < 0 and n in reachable:
            cut_cap += -b
    assert cut_cap == snap.cut.capacity == snap.value, (
        f"{msg}: 割容量 {cut_cap}/{snap.cut.capacity} != 流值 {snap.value}")

    # 5) 割证书中每条弧确实跨割且容量计入
    crossing_sum = 0
    for c in snap.cut.crossing:
        crossing_sum += c.capacity
        if c.kind == "edge":
            assert c.u in reachable and c.v not in reachable, f"{msg}: {c}"
        elif c.kind == "supply":
            assert c.v not in reachable, f"{msg}: {c}"
        else:
            assert c.u in reachable, f"{msg}: {c}"
    assert crossing_sum == snap.value, f"{msg}: 跨割清单合计错误"

    # 6) 穷举所有小割求最小值（n 小）
    nodes = list(snap.nodes)
    assert len(nodes) <= 14
    brute_min = None
    for mask in range(1 << len(nodes)):
        side = {n for i, n in enumerate(nodes) if mask >> i & 1}
        cap = 0
        for eid, (u, v, c, active) in edges.items():
            if active and u in side and v not in side:
                cap += c
        for n, b in bal.items():
            if b > 0 and n not in side:
                cap += b
            elif b < 0 and n in side:
                cap += -b
        brute_min = cap if brute_min is None else min(brute_min, cap)
    assert brute_min == snap.value, (
        f"{msg}: 穷举最小割 {brute_min} != 流值 {snap.value}")


class TestParallelAndSpecExample(unittest.TestCase):
    def test_parallel_5_then_3(self):
        """题面样例：平行 S->T 容量 2、3 最大流 5；3 降到 1 后流值 3。"""
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("e1", "s", "t", 2), ("e2", "s", "t", 3)],
            balances={"s": 5, "t": -5})
        self.assertEqual(net.flow_value(), 5)
        self.assertEqual(net.flow(), {"e1": 2, "e2": 3})
        hist = dynflow.FlowHistory(net, 0)
        independent_verify(hist.latest, "初始平行边")

        rep = hist.apply_update(1, capacity={"e2": 1})
        self.assertEqual(rep.value, 3)
        self.assertEqual(rep.flow, {"e1": 2, "e2": 1})
        self.assertEqual(rep.cut.capacity, 3)
        self.assertFalse(rep.satisfied)  # 供应 5 只能路由 3：报告缺口
        self.assertEqual(rep.total_supply, 5)
        independent_verify(hist.latest, "平行边收缩")
        # 不能留下超容量边
        for eid, f in rep.flow.items():
            self.assertLessEqual(f, 1 if eid == "e2" else 2)
        # 变化原因：撤回而非清零重算（e1 的流 2 完整保留）
        kinds = [c.kind for c in rep.changes]
        self.assertIn("shrink", kinds)
        self.assertIn("cancel", kinds)

    def test_multiedges_and_isolated_nodes_preserved(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t", "iso"],
            edges=[("m1", "s", "t", 1), ("m2", "s", "t", 1),
                   ("m3", "s", "t", 1)],
            balances={"s": 3, "t": -3})
        hist = dynflow.FlowHistory(net, 0)
        snap = hist.latest
        self.assertEqual(snap.nodes, ("s", "t", "iso"))
        self.assertEqual([e[0] for e in snap.edges], ["m1", "m2", "m3"])
        self.assertEqual(snap.value, 3)
        independent_verify(snap, "多重边/孤立点")

    def test_zero_capacity_edge(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("z", "s", "t", 0), ("ok", "s", "t", 2)],
            balances={"s": 2, "t": -2})
        hist = dynflow.FlowHistory(net, 0)
        self.assertEqual(hist.latest.flow_dict(), {"z": 0, "ok": 2})
        # 零容量边仍被保留在边表与快照中
        cap_map = {e[0]: e[3] for e in hist.latest.edges}
        self.assertEqual(cap_map["z"], 0)
        independent_verify(hist.latest, "零容量边")
        # 零容量边扩容（同批放大供需）后立刻在残余网络上增广
        rep = hist.apply_update(1, capacity={"z": 2},
                                balances={"s": 4, "t": -4})
        self.assertEqual(rep.flow["z"], 2)
        self.assertEqual(rep.value, 4)
        independent_verify(hist.latest, "零容量边扩容")


class TestDiamondReverseReroute(unittest.TestCase):
    """容量下降迫使反向残余改道：正向无路径，必须走反向残余弧。"""

    def _build(self):
        # s 分两路 s->a 与 s->c->b，在 b 汇合；a->t 为零容量边。
        # 初始 BFS 最短路径使 ab 打满 2。ab 2->1 后，
        # 改道路径 a->s->c->b 必须使用 s->a 的反向残余弧。
        return dynflow.FlowNetwork(
            nodes=["s", "a", "b", "c", "t"],
            edges=[("sa", "s", "a", 2), ("sc", "s", "c", 1),
                   ("ab", "a", "b", 2), ("cb", "c", "b", 1),
                   ("bt", "b", "t", 2), ("at", "a", "t", 0)],
            balances={"s": 2, "t": -2})

    def test_shrink_forces_reverse_residual_reroute(self):
        net = self._build()
        self.assertEqual(net.flow(),
                         {"sa": 2, "sc": 0, "ab": 2, "cb": 0,
                          "bt": 2, "at": 0})
        hist = dynflow.FlowHistory(net, 0)
        rep = hist.apply_update(1, capacity={"ab": 1})
        # 流值不下降，改道确实发生且使用反向残余弧（路径回到 s）
        self.assertEqual(rep.value, 2)
        self.assertIn("reroute", [c.kind for c in rep.changes])
        reroute = next(c for c in rep.changes if c.kind == "reroute")
        self.assertEqual(reroute.path, ("a", "s", "c", "b"))
        self.assertEqual(reroute.delta, 1)
        self.assertEqual(rep.flow,
                         {"sa": 1, "sc": 1, "ab": 1, "cb": 1,
                          "bt": 2, "at": 0})
        # 只在残余网络上局部搜索；修复阶段没有扫全部残余边
        total_arcs = 2 * (6 + 2)  # 6 条边 + 2 条终端弧
        self.assertLess(rep.repair_visited_edges, total_arcs)
        independent_verify(hist.latest, "菱形反向改道")

    def test_forward_only_repair_would_lose_flow(self):
        """对照组：去掉 sc 支路（反向后无可恢复容量）时只能撤回，流值下降。"""
        net = dynflow.FlowNetwork(
            nodes=["s", "a", "b", "t"],
            edges=[("sa", "s", "a", 2), ("ab", "a", "b", 2),
                   ("bt", "b", "t", 2), ("at", "a", "t", 0)],
            balances={"s": 2, "t": -2})
        self.assertEqual(net.flow()["ab"], 2)
        hist = dynflow.FlowHistory(net, 0)
        rep = hist.apply_update(1, capacity={"ab": 1})
        self.assertEqual(rep.value, 1)
        self.assertNotIn("reroute", [c.kind for c in rep.changes])
        self.assertIn("cancel", [c.kind for c in rep.changes])
        independent_verify(hist.latest, "对照组只能撤回")


class TestDeactivate(unittest.TestCase):
    def test_deactivate_with_flow_cancels_and_is_kept(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("a", "s", "t", 3), ("b", "s", "t", 2)],
            balances={"s": 5, "t": -5})
        hist = dynflow.FlowHistory(net, 0)
        rep = hist.apply_update(1, deactivate=("a",))
        self.assertEqual(rep.value, 2)
        self.assertEqual(rep.flow["a"], 0)
        edge_map = {e[0]: e for e in hist.latest.edges}
        self.assertFalse(edge_map["a"][4])     # active=False
        self.assertEqual(edge_map["a"][3], 3)  # 名义容量保留
        independent_verify(hist.latest, "停用")

    def test_deactivate_is_irreversible_and_validated(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("a", "s", "t", 1)],
            balances={"s": 1, "t": -1})
        hist = dynflow.FlowHistory(net, 0)
        hist.apply_update(1, deactivate=("a",))
        with self.assertRaises(dynflow.InvalidUpdateError):
            hist.apply_update(2, capacity={"a": 5})  # 不能复活
        with self.assertRaises(dynflow.InvalidUpdateError):
            hist.apply_update(2, deactivate=("a",))  # 重复停用
        self.assertEqual(hist.latest.version, 1)  # 快照未增加


class TestBalanceChanges(unittest.TestCase):
    def test_grow_shrink_supply_and_sign_flip(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "x", "t"],
            edges=[("sx", "s", "x", 5), ("xt", "x", "t", 5)],
            balances={"s": 2, "t": -2})
        hist = dynflow.FlowHistory(net, 0)
        self.assertEqual(hist.latest.value, 2)
        # 供需同时增长：残余网络直接增广，既有流保留
        rep = hist.apply_update(1, balances={"s": 5, "t": -5})
        self.assertEqual(rep.value, 5)
        independent_verify(hist.latest, "供应增长")
        # 供应缩减：终端弧反向撤回
        rep = hist.apply_update(2, balances={"s": 3})
        self.assertEqual(rep.value, 3)
        self.assertIn("cancel", [c.kind for c in rep.changes])
        independent_verify(hist.latest, "供应缩减")
        # 变号：供应点变需求点，旧终端弧拆除、新终端弧建立
        rep = hist.apply_update(3, balances={"s": -1, "t": 0})
        independent_verify(hist.latest, "变号")
        self.assertEqual(hist.network.balances, {"s": -1})

    def test_unsatisfiable_demand_best_effort_and_strict(self):
        # 割容量只有 2，需求 5 永远满足不了
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("e", "s", "t", 2)],
            balances={"s": 5, "t": -5})
        hist = dynflow.FlowHistory(net, 0)
        rep = hist.apply_update(1, balances={"t": -5}, strict=False)
        self.assertEqual(rep.value, 2)
        self.assertFalse(rep.satisfied)
        independent_verify(hist.latest, "不可满足-尽力流")
        # strict 模式：不可满足 => 抛异常，原快照保持
        before = hist.latest
        with self.assertRaises(dynflow.InfeasibleFlowError):
            hist.apply_update(2, balances={"t": -99}, strict=True)
        self.assertIs(hist.latest, before)

    def test_constructor_allows_unsatisfiable_initial(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("e", "s", "t", 1)],
            balances={"s": 4, "t": -4})
        self.assertEqual(net.flow_value(), 1)
        independent_verify(dynflow.FlowHistory(net, 0).latest,
                           "初始不可满足")


class TestAtomicFailure(unittest.TestCase):
    def _hist(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t", "x"],
            edges=[("e1", "s", "t", 2), ("e2", "s", "x", 1)],
            balances={"s": 3, "t": -3})
        return dynflow.FlowHistory(net, 0)

    def test_invalid_batch_leaves_snapshot_untouched(self):
        hist = self._hist()
        before_flow = hist.latest.flow_dict()
        bad_batches = [
            dict(capacity={"nope": 1}),                 # 未知边
            dict(capacity={"e1": -1}),                  # 负容量
            dict(capacity={"e1": 1.5}),                 # 非整数
            dict(deactivate=("ghost",)),                # 未知边
            dict(balances={"who": 1}),                  # 未知点
            dict(balances={"t": -1.0}),                 # 非整数
            dict(capacity={"e1": 2}, deactivate=("e1",)),  # 同时改+停
        ]
        for i, kw in enumerate(bad_batches):
            with self.assertRaises(dynflow.InvalidUpdateError, msg=str(kw)):
                hist.apply_update(i + 1, **kw)
            self.assertEqual(hist.latest.flow_dict(), before_flow)
            self.assertEqual(hist.latest.version, 0)

    def test_time_must_strictly_increase(self):
        hist = self._hist()
        hist.apply_update(5, capacity={"e2": 1})
        with self.assertRaises(dynflow.InvalidUpdateError):
            hist.apply_update(5, capacity={"e2": 1})
        with self.assertRaises(dynflow.InvalidUpdateError):
            hist.apply_update(4, capacity={"e2": 1})


class TestSnapshotIntervals(unittest.TestCase):
    def test_half_open_intervals_and_read_old(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("e", "s", "t", 5)],
            balances={"s": 5, "t": -5})
        hist = dynflow.FlowHistory(net, 0)
        hist.apply_update(1, capacity={"e": 4})
        hist.apply_update(2, capacity={"e": 3})
        v0, v1, v2 = hist.snapshots
        # 左闭右开 [0,1) [1,2) [2, +inf)
        self.assertEqual(v0.interval, (0, 1))
        self.assertEqual(v1.interval, (1, 2))
        self.assertEqual(v2.interval, (2, None))
        for t, want in [(0, v0), (0.999, v0), (1, v1), (1.5, v1),
                        (2, v2), (100, v2)]:
            self.assertIs(hist.at(t), want, f"at({t})")
        with self.assertRaises(KeyError):
            hist.at(-1)
        self.assertEqual(hist.version(0).flow_dict(), v0.flow_dict())
        # 旧快照可读出并重建独立网络，流一并恢复
        old = dynflow.FlowNetwork.from_snapshot(v1)
        self.assertEqual(old.flow(), v1.flow_dict())
        self.assertEqual(old.flow_value(), 4)
        independent_verify(v0, "v0")
        independent_verify(v1, "v1")
        independent_verify(v2, "v2")


class TestIncrementality(unittest.TestCase):
    def test_small_change_uses_residual_without_reset(self):
        # 主链 s->a->t (cap 6) + 8 条并联支路 (cap 2)，全部饱和
        edges = [("sa", "s", "a", 6), ("at", "a", "t", 6)]
        for i in range(8):
            edges.append((f"q{i}", "s", f"b{i}", 2))
            edges.append((f"r{i}", f"b{i}", "t", 2))
        nodes = ["s", "a", "t"] + [f"b{i}" for i in range(8)]
        net = dynflow.FlowNetwork(
            nodes=nodes, edges=edges,
            balances={"s": 22, "t": -22})
        hist = dynflow.FlowHistory(net, 0)
        self.assertEqual(hist.latest.value, 22)
        rep = hist.apply_update(1, capacity={"at": 5})
        # 流值 21；只有撤回路径上的边改变，16 条支路边流原样保留
        self.assertEqual(rep.value, 21)
        changed = [eid for eid, f in rep.flow.items()
                   if f != net.flow().get(eid, 0)]
        self.assertEqual(sorted(changed), ["at", "sa"])
        # 与“清零重算”比较：从头构建终态网络的访问量必须显著更大
        fresh = dynflow.FlowNetwork(
            nodes=nodes,
            edges=[(eid, u, v, 5 if eid == "at" else cap)
                   for eid, u, v, cap in edges],
            balances={"s": 21, "t": -21})
        self.assertLess(rep.repair_visited_edges
                        + rep.augment_visited_edges,
                        fresh.augment_visited)
        independent_verify(hist.latest, "增量收缩")

    def test_noop_update_preserves_flow(self):
        net = dynflow.FlowNetwork(
            nodes=["s", "t"],
            edges=[("a", "s", "t", 2), ("b", "s", "t", 3)],
            balances={"s": 5, "t": -5})
        hist = dynflow.FlowHistory(net, 0)
        before = hist.latest.flow_dict()
        rep = hist.apply_update(1, capacity={"a": 2}, balances={"s": 5})
        self.assertEqual(rep.flow, before)
        self.assertEqual(rep.repair_visited_edges, 0)
        independent_verify(hist.latest, "空更新")


class TestRandomCrossCheck(unittest.TestCase):
    """固定种子多轮随机批更新：每轮独立校验 + 穷举 2^7 小割。"""

    def test_50_rounds_random_updates(self):
        rng = random.Random(20260926)
        nodes = ["n0", "n1", "n2", "n3", "n4", "n5", "iso"]
        edges = [
            ("e0", "n0", "n2", 3),
            ("e1", "n0", "n3", 2),
            ("e2", "n1", "n2", 2),
            ("e3", "n2", "n4", 3),
            ("e4", "n3", "n4", 2),
            ("e5", "n3", "n5", 2),
            ("e6", "n4", "n5", 3),
            ("e7", "n2", "n5", 1),
            ("p1", "n0", "n2", 2),   # 与 e0 平行
            ("p2", "n0", "n2", 1),   # 三重边
            ("zero", "n1", "n3", 0),  # 零容量边
            ("loop", "n2", "n2", 2),  # 自环：应始终流 0
        ]
        net = dynflow.FlowNetwork(
            nodes=nodes, edges=edges,
            balances={"n0": 4, "n1": 2, "n4": -2, "n5": -4})
        hist = dynflow.FlowHistory(net, 0)
        independent_verify(hist.latest, "随机:初始")

        clock = 0
        for rnd in range(50):
            active_eids = [e[0] for e in hist.latest.edges if e[4]]
            capacity = {}
            deactivate = []
            balances = {}
            for _ in range(rng.randint(1, 3)):
                pick = rng.random()
                if pick < 0.45 and active_eids:
                    eid = rng.choice(active_eids)
                    capacity[eid] = rng.randint(0, 4)
                elif pick < 0.60 and len(active_eids) > 4:
                    eid = rng.choice(active_eids)
                    deactivate.append(eid)
                    active_eids.remove(eid)
                elif pick < 0.85:
                    balances[rng.choice(nodes[:6])] = rng.randint(-3, 3)
                else:
                    # 故意构造非法请求：快照必须不变
                    bad = rng.choice([
                        lambda: hist.apply_update(
                            clock + 1, capacity={"missing": 1}),
                        lambda: hist.apply_update(
                            clock + 1, capacity={
                                rng.choice(active_eids or ["e0"]): -1}),
                    ])
                    before = hist.latest
                    with self.assertRaises(dynflow.InvalidUpdateError):
                        bad()
                    self.assertIs(hist.latest, before)
            if not capacity and not deactivate and not balances:
                continue
            clock += 1
            rep = hist.apply_update(
                clock, capacity=capacity,
                deactivate=tuple(deactivate), balances=balances)
            # 报告自洽：流值=割容量，无超容量
            self.assertEqual(rep.value, rep.cut.capacity, f"第 {rnd} 轮")
            cap_now = {e[0]: (e[3], e[4]) for e in hist.latest.edges}
            for eid, f in rep.flow.items():
                cap, active = cap_now[eid]
                if active:
                    self.assertLessEqual(f, cap)
                else:
                    self.assertEqual(f, 0)
            independent_verify(hist.latest, f"随机:第 {rnd} 轮")

        # 自环永远不携带流量
        self.assertEqual(hist.latest.flow_dict().get("loop", 0), 0)
        # 孤立点一路保留
        self.assertIn("iso", hist.latest.nodes)
        # 多重边独立计数
        eids = [e[0] for e in hist.latest.edges]
        for eid in ("e0", "p1", "p2"):
            self.assertIn(eid, eids)
        # 至少生成若干版本，且每个历史版本都可独立校验
        self.assertGreaterEqual(len(hist.snapshots), 5)
        for snap in hist.snapshots:
            independent_verify(snap, f"历史版本 v{snap.version}")


if __name__ == "__main__":
    unittest.main(verbosity=2)
