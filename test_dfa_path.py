"""dfa_path 的 unittest 测试。所有数据均为合成数据。"""

import random
import unittest

from dfa_path import DFA, Edge, Graph, solve


def make_graph(vertices, edges):
    return Graph(vertices=list(vertices),
                 edges=[Edge(*e) for e in edges])  # (eid, src, dst, label, cost)


def make_dfa(states, alphabet, start, accepting, transitions):
    return DFA(states=list(states), alphabet=list(alphabet), start=start,
               accepting=set(accepting), trans={(q, c): q2 for q, c, q2 in transitions})


def brute_force_best(graph, dfa, source, target):
    """在积图上枚举所有不重复积状态的路径，返回最优 (成本,边数,标签,边ID元组)。

    最优解必不重复积状态：若重复，删去中间环段仍合法（积图路径一一对应
    DFA 转移），成本不增、边数严格减少，与 (成本,边数) 最优矛盾。
    故在简单积路径中枚举即可得到真最优，用于对照验证求解器。
    """
    best = None

    def dfs(v, q, cost, ne, lab, eids, seen):
        nonlocal best
        if v == target and q in dfa.accepting:
            cand = (cost, ne, lab, eids)
            if best is None or cand < best:
                best = cand
            # 不 return：继续扩展可能产生更多候选，但 (成本,边数) 只会更差，
            # 字典序也更差（序列变长），故可剪枝。
            return
        for e in graph.adj[v]:
            q2 = dfa.trans.get((q, e.label))
            if q2 is None or (e.dst, q2) in seen:
                continue
            seen.add((e.dst, q2))
            dfs(e.dst, q2, cost + e.cost, ne + 1, lab + e.label,
                eids + (e.eid,), seen)
            seen.discard((e.dst, q2))

    dfs(source, dfa.start, 0, 0, "", (), {(source, dfa.start)})
    return best


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：s->t 直边标 a 成本 1，但 DFA 只接受 ab，不能直接返回。"""

    def test_direct_edge_rejected_detour_accepted(self):
        graph = make_graph(
            ["s", "m", "t"],
            [(0, "s", "t", "a", 1),   # 便宜但不合规的直边
             (1, "s", "m", "a", 1),
             (2, "m", "t", "b", 1)],
        )
        dfa = make_dfa(
            ["q0", "q1", "q2"], ["a", "b"], "q0", ["q2"],
            [("q0", "a", "q1"), ("q1", "b", "q2")],
        )
        r = solve(graph, dfa, "s", "t")
        self.assertTrue(r["found"])
        self.assertEqual(r["cost"], 2)
        self.assertEqual(r["label"], "ab")
        self.assertEqual(r["vertices"], ["s", "m", "t"])
        self.assertEqual(r["edge_ids"], [1, 2])
        self.assertEqual(r["dfa_states"], ["q0", "q1", "q2"])

    def test_only_noncompliant_edge_returns_not_found(self):
        graph = make_graph(["s", "t"], [(0, "s", "t", "a", 1)])
        dfa = make_dfa(["q0", "q1", "q2"], ["a", "b"], "q0", ["q2"],
                       [("q0", "a", "q1"), ("q1", "b", "q2")])
        self.assertFalse(solve(graph, dfa, "s", "t")["found"])


class ZeroCostCycleTest(unittest.TestCase):
    def test_zero_cost_cycle_terminates(self):
        # 零成本环 s->u->s（标签不影响接受），最优仍是直边。
        graph = make_graph(
            ["s", "u", "t"],
            [(0, "s", "u", "a", 0),
             (1, "u", "s", "a", 0),
             (2, "s", "t", "a", 5)],
        )
        dfa = make_dfa(["q0", "q1"], ["a"], "q0", ["q1"], [("q0", "a", "q1")])
        # 注意：走环会离开/回到 q1 后无法再走 a（q1 无出转移），只有直边可行。
        r = solve(graph, dfa, "s", "t")
        self.assertTrue(r["found"])
        self.assertEqual((r["cost"], r["edge_count"]), (5, 1))

    def test_zero_cost_self_loop_with_accepting_cycle(self):
        # DFA 对 a 自循环且接受；图上零成本自环不得导致无限搜索。
        graph = make_graph(
            ["s", "t"],
            [(0, "s", "s", "a", 0),
             (1, "s", "t", "a", 3)],
        )
        dfa = make_dfa(["q0"], ["a"], "q0", ["q0"], [("q0", "a", "q0")])
        r = solve(graph, dfa, "s", "t")
        self.assertTrue(r["found"])
        self.assertEqual((r["cost"], r["edge_count"]), (3, 1))
        self.assertEqual(r["edge_ids"], [1])


class TieBreakTest(unittest.TestCase):
    def test_fewer_edges_wins_over_cost_tie(self):
        graph = make_graph(
            ["s", "m", "t"],
            [(0, "s", "m", "a", 1), (1, "m", "t", "b", 1),  # 2 边，成本 2
             (2, "s", "t", "a", 2)],                          # 1 边，成本 2
        )
        # 接受 a 或 ab
        dfa = make_dfa(["q0", "q1", "q2"], ["a", "b"], "q0", ["q1", "q2"],
                       [("q0", "a", "q1"), ("q1", "b", "q2")])
        r = solve(graph, dfa, "s", "t")
        self.assertEqual((r["cost"], r["edge_count"]), (2, 1))
        self.assertEqual(r["edge_ids"], [2])

    def test_label_lex_wins_over_edge_id(self):
        # 两条路径同成本同边数：标签 "aa" < "ab"，即使后者边ID更小。
        graph = make_graph(
            ["s", "x", "y", "t"],
            [(0, "s", "x", "a", 1), (1, "x", "t", "b", 1),   # 标签 ab, IDs (0,1)
             (2, "s", "y", "a", 1), (3, "y", "t", "a", 1)],  # 标签 aa, IDs (2,3)
        )
        dfa = make_dfa(["q0", "q1", "q2"], ["a", "b"], "q0", ["q2"],
                       [("q0", "a", "q1"),
                        ("q1", "a", "q2"), ("q1", "b", "q2")])
        r = solve(graph, dfa, "s", "t")
        self.assertEqual(r["label"], "aa")
        self.assertEqual(r["edge_ids"], [2, 3])

    def test_edge_id_lex_breaks_final_tie(self):
        # 两条并行边同标签同成本同终点：选边ID序列更小者。
        graph = make_graph(
            ["s", "t"],
            [(5, "s", "t", "a", 1), (2, "s", "t", "a", 1)],
        )
        dfa = make_dfa(["q0", "q1"], ["a"], "q0", ["q1"], [("q0", "a", "q1")])
        r = solve(graph, dfa, "s", "t")
        self.assertEqual(r["edge_ids"], [2])


class MiscTest(unittest.TestCase):
    def test_source_equals_target_empty_word(self):
        graph = make_graph(["s"], [])
        dfa = make_dfa(["q0"], ["a"], "q0", ["q0"], [])
        r = solve(graph, dfa, "s", "s")
        self.assertTrue(r["found"])
        self.assertEqual((r["cost"], r["edge_count"], r["label"]), (0, 0, ""))

    def test_source_equals_target_not_accepting(self):
        graph = make_graph(["s"], [])
        dfa = make_dfa(["q0"], ["a"], "q0", [], [])
        self.assertFalse(solve(graph, dfa, "s", "s")["found"])

    def test_unreachable_target(self):
        graph = make_graph(["s", "t"], [])
        dfa = make_dfa(["q0"], ["a"], "q0", ["q0"], [])
        self.assertFalse(solve(graph, dfa, "s", "t")["found"])

    def test_negative_cost_rejected(self):
        graph = make_graph(["s", "t"], [(0, "s", "t", "a", -1)])
        dfa = make_dfa(["q0", "q1"], ["a"], "q0", ["q1"], [("q0", "a", "q1")])
        with self.assertRaises(ValueError):
            solve(graph, dfa, "s", "t")


class RandomizedOptimalityTest(unittest.TestCase):
    """随机合成小实例上对照暴力枚举，验证求解器结果确为最优。"""

    def test_against_brute_force(self):
        rng = random.Random(20261001)
        for trial in range(300):
            nv = rng.randint(2, 4)
            verts = [f"v{i}" for i in range(nv)]
            ne = rng.randint(0, 6)
            edges = []
            for eid in range(ne):
                edges.append((eid, rng.choice(verts), rng.choice(verts),
                              rng.choice("ab"), rng.randint(0, 3)))
            graph = make_graph(verts, edges)
            nq = rng.randint(1, 3)
            states = [f"q{i}" for i in range(nq)]
            transitions = []
            for q in states:
                for c in "ab":
                    if rng.random() < 0.8:
                        transitions.append((q, c, rng.choice(states)))
            accepting = [q for q in states if rng.random() < 0.4]
            dfa = make_dfa(states, "ab", "q0", accepting, transitions)
            s, t = rng.choice(verts), rng.choice(verts)

            got = solve(graph, dfa, s, t)
            want = brute_force_best(graph, dfa, s, t)
            if want is None:
                self.assertFalse(got["found"],
                                 f"trial {trial}: 求解器找到路径但暴力枚举无解")
            else:
                self.assertTrue(got["found"],
                                f"trial {trial}: 暴力枚举有解 {want} 但求解器未找到")
                self.assertEqual(
                    (got["cost"], got["edge_count"], got["label"],
                     tuple(got["edge_ids"])),
                    want,
                    f"trial {trial}: 求解器结果非最优",
                )


if __name__ == "__main__":
    unittest.main()
