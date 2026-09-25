"""Tree versioning: local repair, snapshot isolation, resource release."""

import gc
import unittest
import weakref
from fractions import Fraction as F

from engine import Engine
from tree import check_avl, count_nodes, iter_leaves


def build_engine(n=32, group="G"):
    eng = Engine()
    eng.add_group(group)
    for i in range(n):
        eng.set_segment(group, i, (i * 4, 0), (i * 4 + 1, 1))
    return eng


def collect_nodes(root, into):
    if root is None:
        return
    into.add(id(root))
    if not root.is_leaf:
        collect_nodes(root.left, into)
        collect_nodes(root.right, into)


class TestLocalRepair(unittest.TestCase):
    def test_move_only_rebuilds_path_nodes(self):
        eng = build_engine(64)
        g = eng._groups["G"]
        old_root = g.root
        old_nodes = set()
        collect_nodes(old_root, old_nodes)
        before_created = eng.pool.created

        eng.set_segment("G", 7, (1000, 1000), (1001, 1001))  # local move
        new_root = g.root
        new_nodes = set()
        collect_nodes(new_root, new_nodes)

        created = eng.pool.created - before_created
        height = new_root.height
        # A move touches only the search path (plus rotation nodes):
        # bounded by a small multiple of the tree height, never the
        # whole tree (127 nodes).
        self.assertLessEqual(created, 4 * height)
        # Untouched subtrees are shared by identity with the old version.
        shared = len(old_nodes & new_nodes)
        self.assertGreaterEqual(shared, count_nodes(old_root) - created)
        check_avl(new_root)
        check_avl(old_root)  # old version still intact

    def test_old_version_unchanged_after_edits(self):
        eng = build_engine(16)
        g = eng._groups["G"]
        root_v1 = g.root
        leaves_v1 = [(l.seg_id, l.p0, l.p1) for l in iter_leaves(root_v1)]
        eng.set_segment("G", 3, (500, 500), (501, 501))
        eng.remove_segment("G", 4)
        eng.set_segment("G", 99, (-50, 0), (-49, 0))
        leaves_v1_after = [(l.seg_id, l.p0, l.p1)
                           for l in iter_leaves(root_v1)]
        self.assertEqual(leaves_v1, leaves_v1_after)
        check_avl(g.root)

    def test_avl_balance_under_sequential_inserts(self):
        eng = build_engine(500)
        root = eng._groups["G"].root
        check_avl(root)
        # AVL height stays logarithmic.
        self.assertLessEqual(root.height, 12)


class TestResourceAccounting(unittest.TestCase):
    def test_live_count_matches_reachable(self):
        eng = build_engine(40)
        gc.collect()
        self.assertEqual(eng.pool.live, eng.node_count())
        eng.set_segment("G", 5, (900, 0), (901, 0))
        eng.remove_segment("G", 6)
        gc.collect()
        self.assertEqual(eng.pool.live, eng.node_count())

    def test_old_version_released_after_snapshot_close(self):
        eng = build_engine(64)
        baseline = eng.pool.live
        self.assertEqual(baseline, eng.node_count())

        snap = eng.snapshot()
        old_root = eng._groups["G"].root
        old_refs = [weakref.ref(n) for n in _walk(old_root)]
        del old_root  # drop our own strong ref to the old version

        # Heavy edits: moves, inserts, deletes -> rotations included.
        for i in range(0, 64, 2):
            eng.set_segment("G", i, (i, 100 + i), (i + 1, 101 + i))
        for i in range(100, 164):
            eng.set_segment("G", i, (i, -100), (i, -99))
        for i in range(1, 64, 2):
            eng.remove_segment("G", i)

        # Snapshot pins the old version: every old node is still alive.
        self.assertTrue(all(r() is not None for r in old_refs))
        self.assertGreaterEqual(eng.pool.live, baseline)

        snap.close()
        gc.collect()
        # All old-version-only nodes are reclaimed; live count is exactly
        # the number of nodes reachable from the current version.
        self.assertEqual(eng.pool.live, eng.node_count())
        self.assertTrue(all(r() is None for r in old_refs))
        self.assertEqual(eng.pool.created, eng.pool.live + eng.pool.freed)

    def test_rebalance_does_not_reclaim_inflight_nodes(self):
        eng = build_engine(128)
        snap = eng.snapshot()
        inflight_root = snap.root("G")
        path_refs = [weakref.ref(inflight_root)]
        n = inflight_root
        while not n.is_leaf:
            n = n.left
            path_refs.append(weakref.ref(n))

        # Force many rotations by inserting sequential keys.
        for i in range(128, 384):
            eng.set_segment("G", i, (i, 50), (i, 51))
        check_avl(eng._groups["G"].root)

        # Nodes an in-flight query could be reading are untouched.
        self.assertTrue(all(r() is not None for r in path_refs))
        snap.close()
        del inflight_root, n  # release our strong refs to the old path
        gc.collect()
        self.assertEqual(eng.pool.live, eng.node_count())


def _walk(root):
    if root is None:
        return
    yield root
    if not root.is_leaf:
        yield from _walk(root.left)
        yield from _walk(root.right)


class TestSnapshotQueries(unittest.TestCase):
    def test_historical_snapshot_query(self):
        eng = Engine()
        eng.add_group("A")
        eng.add_group("B")
        eng.set_segment("A", 1, (0, 0), (4, 0))
        eng.set_segment("B", 1, (1, 2), (3, 2))  # dist2 = 4
        snap = eng.snapshot()
        # Move B far away after the snapshot.
        eng.set_segment("B", 1, (100, 100), (101, 100))
        self.assertEqual(eng.query("A", "B").dist2, F(96) ** 2 + F(100) ** 2)
        # The snapshot still answers for the old version.
        with snap:
            old = eng.query("A", "B", snapshot=snap)
            self.assertEqual(old.dist2, 4)
            self.assertEqual(old.evidence[0].param_b, (F(0), F(1)))
        gc.collect()
        self.assertEqual(eng.pool.live, eng.node_count())


if __name__ == "__main__":
    unittest.main()
