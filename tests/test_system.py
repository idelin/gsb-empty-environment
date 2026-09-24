import random
import unittest

from temporal_diff import ConstraintSystem, check_window
from examples.replay import replay


def conflict_pieces(pieces):
    return [p for p in pieces if not p.feasible]


class ConflictIntervalTest(unittest.TestCase):
    def setUp(self):
        self.cs = ConstraintSystem()
        # y - x <= -1 on [0,10)，x - y <= -1 on [5,15)：仅在重叠处冲突
        self.cs.add_constraint("c1", "x", "y", -1, 0, 10, priority=1)
        self.cs.add_constraint("c2", "y", "x", -1, 5, 15, priority=2)

    def test_conflict_only_on_overlap_half_open(self):
        pieces = self.cs.query(0, 20)
        conflicts = conflict_pieces(pieces)
        self.assertEqual(len(conflicts), 1)
        self.assertEqual((conflicts[0].start, conflicts[0].end), (5, 10))
        self.assertEqual(set(conflicts[0].cycle), {"c1", "c2"})
        feasible = [p for p in pieces if p.feasible]
        self.assertEqual([(p.start, p.end) for p in feasible],
                         [(0, 5), (10, 15), (15, 20)])
        self.assertEqual(feasible[0].potentials, {"x": 0, "y": -1})
        self.assertEqual(feasible[1].potentials, {"x": -1, "y": 0})

    def test_adjacent_half_open_intervals_no_conflict(self):
        cs = ConstraintSystem()
        cs.add_constraint("a", "x", "y", -1, 0, 5, priority=1)   # [0,5)
        cs.add_constraint("b", "y", "x", -1, 5, 10, priority=1)  # [5,10)，端点相接不重叠
        self.assertEqual(conflict_pieces(cs.query(0, 10)), [])

    def test_empty_window_rejected(self):
        with self.assertRaises(ValueError):
            self.cs.query(5, 5)


class ResolveAndHistoryTest(unittest.TestCase):
    def setUp(self):
        self.cs = ConstraintSystem()
        self.cs.add_constraint("c1", "x", "y", -1, 0, 10, priority=1)
        self.cs.add_constraint("c2", "y", "x", -1, 5, 15, priority=2)

    def test_soft_revoke_lowest_priority_explicit_decision(self):
        decisions = self.cs.resolve(0, 20)
        self.assertEqual(len(decisions), 1)
        d = decisions[0]
        self.assertEqual(d.cid, "c1")                 # 优先级数值更小者被撤销
        self.assertEqual(d.interval, (5, 10))
        self.assertIn("c1", str(d))                   # 决策显式输出
        self.assertEqual(self.cs.decisions, decisions)
        self.assertEqual(conflict_pieces(self.cs.query(0, 20)), [])

    def test_pop_restores_domain_evidence_and_priority_choices(self):
        self.cs.push()
        self.cs.resolve(0, 20)
        self.assertEqual(self.cs.revoked, {"c1"})
        self.cs.batch_correct([("retract", "c2")])
        self.assertNotIn("c2", self.cs.constraints)
        self.cs.pop()
        self.assertEqual(self.cs.revoked, set())      # 优先选择还原
        self.assertEqual(self.cs.decisions, [])       # 证据还原
        self.assertIn("c2", self.cs.constraints)      # 域还原
        conflicts = conflict_pieces(self.cs.query(0, 20))
        self.assertEqual(len(conflicts), 1)

    def test_nested_push_pop(self):
        self.cs.push()
        self.cs.resolve(0, 20)
        self.cs.push()
        self.cs.batch_correct([("upsert", "c1", "x", "y", 7, 0, 10, 1)])
        self.assertEqual(self.cs.constraints["c1"].version, 2)
        self.cs.pop()
        self.assertEqual(self.cs.constraints["c1"].version, 1)
        self.assertEqual(self.cs.revoked, {"c1"})
        self.cs.pop()
        self.assertEqual(self.cs.revoked, set())


class GraphShapeTest(unittest.TestCase):
    def test_disconnected_negative_cycle_and_isolated_vertex(self):
        cs = ConstraintSystem()
        cs.add_vertex("lonely")
        cs.add_constraint("p", "a", "b", -2, 0, 10, priority=1)
        cs.add_constraint("q", "b", "a", 1, 0, 10, priority=1)   # a<->b 环权 -1
        cs.add_constraint("r", "x", "y", 3, 0, 10, priority=1)   # 另一连通分量，可行
        pieces = cs.query(0, 10)
        self.assertEqual(len(pieces), 1)
        self.assertFalse(pieces[0].feasible)
        self.assertEqual(set(pieces[0].cycle), {"p", "q"})
        cs.resolve(0, 10)
        pot = cs.query(0, 10)[0].potentials
        self.assertEqual(pot["lonely"], 0)            # 孤立点保留
        self.assertEqual(set(pot), {"a", "b", "x", "y", "lonely"})

    def test_parallel_edges_and_isolated_vertices(self):
        cs = ConstraintSystem()
        cs.add_vertex("iso")
        cs.add_constraint("e1", "x", "y", -3, 0, 10, priority=1)
        cs.add_constraint("e2", "x", "y", -5, 0, 10, priority=1)  # 重边，更紧
        pot = cs.query(0, 10)[0].potentials
        self.assertEqual(pot["y"], -5)                # 两条重边同时生效
        self.assertEqual(pot["iso"], 0)

    def test_zero_weight_cycle_feasible(self):
        cs = ConstraintSystem()
        cs.add_constraint("z1", "x", "y", 0, 0, 10, priority=1)
        cs.add_constraint("z2", "y", "x", 0, 0, 10, priority=1)
        pieces = cs.query(0, 10)
        self.assertTrue(all(p.feasible for p in pieces))
        self.assertEqual(pieces[0].potentials, {"x": 0, "y": 0})

    def test_big_integers(self):
        big = 10**40
        cs = ConstraintSystem()
        cs.add_constraint("big", "x", "y", -big, 0, 10, priority=1)
        pot = cs.query(0, 10)[0].potentials
        self.assertEqual(pot["y"], -big)
        cs.add_constraint("big2", "y", "x", big - 1, 0, 10, priority=1)
        conflicts = conflict_pieces(cs.query(0, 10))
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(set(conflicts[0].cycle), {"big", "big2"})


class VersionAndSharingTest(unittest.TestCase):
    def test_same_id_different_versions(self):
        cs = ConstraintSystem()
        cs.add_constraint("e", "x", "y", 5, 0, 10, priority=1)
        cs.add_constraint("f", "y", "x", 0, 0, 10, priority=1)
        self.assertEqual(conflict_pieces(cs.query(0, 10)), [])
        cs.add_constraint("e", "x", "y", -1, 0, 10, priority=1)  # 同 ID 更正
        self.assertEqual(cs.constraints["e"].version, 2)
        conflicts = conflict_pieces(cs.query(0, 10))
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(set(conflicts[0].cycle), {"e", "f"})

    def test_batch_correct_multiple_ops(self):
        cs = ConstraintSystem()
        cs.add_constraint("old", "x", "y", 1, 0, 5, priority=1)
        cs.batch_correct([
            ("upsert", "n1", "a", "b", 2, 0, 8, 1),
            ("upsert", "n2", "b", "c", 3, 0, 8, 2),
            ("retract", "old"),
            ("add_vertex", "solo"),
        ])
        self.assertNotIn("old", cs.constraints)
        self.assertEqual(set(cs.constraints), {"n1", "n2"})
        self.assertIn("solo", cs.vertices)
        with self.assertRaises(ValueError):
            cs.batch_correct([("bogus", "x")])

    def test_unchanged_segments_share_solve_state(self):
        cs = ConstraintSystem()
        cs.add_constraint("ca", "x", "y", 1, 0, 5, priority=1)
        cs.add_constraint("cb", "y", "x", 2, 10, 20, priority=1)
        cs.query(0, 30)
        before = {(s, e): cs.segment_result(s, e) for s, e in cs.atomic_segments(0, 30)}
        # 仅修改 [0,5) 内的约束区间，[5,30) 的分段必须复用原求解状态
        cs.batch_correct([("upsert", "ca", "x", "y", 1, 0, 4, 1)])
        cs.query(0, 30)
        for key in [(5, 10), (10, 20), (20, 30)]:
            self.assertIs(cs.segment_result(*key), before[key])
        self.assertGreater(cs.cache_hits, 0)
        self.assertIsNot(cs.segment_result(0, 4), before.get((0, 5)))


class ReferenceCrossCheckTest(unittest.TestCase):
    def build_random_system(self, seed):
        rng = random.Random(seed)
        cs = ConstraintSystem()
        verts = "abcdef"
        for v in verts:
            cs.add_vertex(v)
        for i in range(20):
            u, v = rng.sample(verts, 2)
            c = rng.randint(-5, 5)
            s = rng.randint(0, 25)
            e = s + rng.randint(1, 8)
            cs.add_constraint(f"k{i}", u, v, c, s, e, rng.randint(1, 3))
        return cs

    def test_random_system_matches_reference(self):
        for seed in (0, 1, 2):
            cs = self.build_random_system(seed)
            self.assertEqual(check_window(cs, 0, 40), [])
            cs.resolve(0, 40)                          # 软撤销后再核对
            self.assertEqual(check_window(cs, 0, 40), [])
            self.assertEqual(conflict_pieces(cs.query(0, 40)), [])

    def test_corrections_keep_reference_consistent(self):
        cs = self.build_random_system(7)
        cs.push()
        cs.resolve(0, 40)
        cs.batch_correct([
            ("upsert", "k0", "a", "b", -9, 3, 12, 1),
            ("retract", "k5"),
            ("upsert", "k11", "c", "d", 4, 20, 30, 2),
        ])
        self.assertEqual(check_window(cs, 0, 40), [])
        cs.pop()
        self.assertEqual(check_window(cs, 0, 40), [])


class ReplaySampleTest(unittest.TestCase):
    def test_replay_transcript(self):
        text = replay()
        self.assertIn("CONFLICT [5, 10) cycle=['c1', 'c2']", text)
        self.assertIn("DECISION revoke c1 on [5, 10)", text)
        self.assertIn("revoked=[] decisions=0", text)
        self.assertIn("c2 version = 2", text)
        self.assertIn("-10000000000000000000000000000000000000000", text)
        self.assertIn("REFERENCE OK", text)
        self.assertNotIn("REFERENCE FAIL", text)


if __name__ == "__main__":
    unittest.main()
