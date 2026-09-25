"""Runnable unittest suite for the dynamic closest-pair scene.

Covers: exact rational geometry, degenerate configurations, tie modes,
exclusions, radius thresholds, persistent-tree local repair, in-flight
query safety under rebalancing, cancellation, historical snapshots and
resource release accounting.
"""

import gc
import random
import unittest
from fractions import Fraction
from itertools import combinations, product

from geometry import seg_seg_dist2
from scene import (CancelToken, Evidence, Node, Query, QueryCancelled,
                   Result, Scene, count_nodes)

F = Fraction


def brute_force(scene, version, group_a, group_b, radius, mode):
    """All-pairs enumeration reference, independent of the tree."""
    self_pair = group_b is None or group_b == group_a
    segs_a = version.segments(group_a)
    segs_b = segs_a if self_pair else version.segments(group_b)
    radius2 = None if radius is None else F(radius) ** 2
    pairs = (combinations(sorted(segs_a), 2) if self_pair
             else product(sorted(segs_a), sorted(segs_b)))
    best = radius2
    evidence = {}
    for id_a, id_b in pairs:
        if scene.excluded(group_a, group_b if not self_pair else group_a,
                          id_a, id_b):
            continue
        a0, a1 = segs_a[id_a]
        b0, b1 = segs_b[id_b]
        d2, t_a, t_b = seg_seg_dist2(a0, a1, b0, b1)
        if best is not None and d2 > best:
            continue
        pa = (a0[0] + t_a * (a1[0] - a0[0]), a0[1] + t_a * (a1[1] - a0[1]))
        pb = (b0[0] + t_b * (b1[0] - b0[0]), b0[1] + t_b * (b1[1] - b0[1]))
        ev = Evidence(id_a, id_b, t_a, t_b, pa, pb, d2)
        if best is None or d2 < best:
            best, evidence = d2, {(id_a, id_b): ev}
        elif d2 == best:
            evidence[(id_a, id_b)] = ev
    if not evidence:
        return Result(None, ())
    ordered = tuple(evidence[k] for k in sorted(evidence))
    if mode == 'representative':
        return Result(ordered[0].dist2, ordered[:1])
    return Result(ordered[0].dist2, ordered)


class TestExactGeometry(unittest.TestCase):
    def test_crossing_distance_zero_exact_params(self):
        d2, ta, tb = seg_seg_dist2((0, 0), (4, 4), (0, 4), (4, 0))
        self.assertEqual(d2, F(0))
        self.assertEqual((ta, tb), (F(1, 2), F(1, 2)))

    def test_collinear_overlap_canonical_params(self):
        d2, ta, tb = seg_seg_dist2((0, 0), (4, 0), (2, 0), (6, 0))
        self.assertEqual(d2, F(0))
        self.assertEqual((ta, tb), (F(1, 2), F(0)))  # smallest (t_a, t_b)

    def test_rational_coordinates_exact(self):
        # vertical gap of 1/3 between two horizontal segments
        d2, ta, tb = seg_seg_dist2((0, 0), (3, 0),
                                   (F(1, 2), F(1, 3)), (2, F(1, 3)))
        self.assertEqual(d2, F(1, 9))
        self.assertEqual((ta, tb), (F(1, 6), F(0)))

    def test_zero_length_segment_is_point(self):
        d2, ta, tb = seg_seg_dist2((5, 5), (5, 5), (3, 5), (4, 5))
        self.assertEqual(d2, F(1))
        self.assertEqual((ta, tb), (F(0), F(1)))

    def test_touching_endpoint_distance_zero(self):
        d2, ta, tb = seg_seg_dist2((0, 0), (1, 0), (1, 0), (2, 1))
        self.assertEqual(d2, F(0))
        self.assertEqual((ta, tb), (F(1), F(0)))


class TestTieModes(unittest.TestCase):
    def build_parallel_scene(self):
        sc = Scene()
        sc.add_group('lower')
        sc.add_group('upper')
        sc.add_segment('lower', 'a', (0, 0), (10, 0))
        sc.add_segment('upper', 'b1', (0, 1), (10, 1))   # dist^2 = 1
        sc.add_segment('upper', 'b2', (0, -1), (10, -1))  # dist^2 = 1
        return sc

    def test_parallel_equidistant_all_mode(self):
        sc = self.build_parallel_scene()
        res = sc.query('lower', 'upper')
        self.assertEqual(res.dist2, F(1))
        self.assertEqual({(e.seg_a, e.seg_b) for e in res.evidence},
                         {('a', 'b1'), ('a', 'b2')})
        for ev in res.evidence:
            self.assertEqual((ev.t_a, ev.t_b), (F(0), F(0)))  # canonical

    def test_parallel_equidistant_representative_mode_is_stable(self):
        sc = self.build_parallel_scene()
        reps = set()
        for _ in range(3):  # independent of query repetition
            res = sc.query('lower', 'upper', mode='representative')
            self.assertEqual(len(res.evidence), 1)
            reps.add((res.evidence[0].seg_a, res.evidence[0].seg_b))
        self.assertEqual(reps, {('a', 'b1')})  # canonical smallest ids

    def test_radius_threshold_filters(self):
        sc = self.build_parallel_scene()
        tight = sc.query('lower', 'upper', radius=F(1, 2))
        self.assertIsNone(tight.dist2)
        self.assertEqual(tight.evidence, ())
        exact = sc.query('lower', 'upper', radius=F(1))
        self.assertEqual(exact.dist2, F(1))  # threshold is inclusive
        self.assertEqual(len(exact.evidence), 2)

    def test_exclusion_relation(self):
        sc = self.build_parallel_scene()
        sc.add_exclusion('upper', 'a', 'b1')
        sc.add_exclusion('upper', 'a', 'b2')
        res = sc.query('lower', 'upper')
        self.assertIsNone(res.dist2)


class TestEditing(unittest.TestCase):
    def test_delete_nearest_reveals_next_candidate(self):
        sc = Scene()
        sc.add_group('g')
        sc.add_segment('g', 'near', (0, 0), (1, 0))
        sc.add_segment('g', 'mid', (0, 2), (1, 2))
        sc.add_segment('g', 'far', (0, 5), (1, 5))
        first = sc.query('g')
        self.assertEqual(first.dist2, F(4))
        self.assertEqual({(e.seg_a, e.seg_b) for e in first.evidence},
                         {('mid', 'near')})
        sc.remove_segment('g', 'mid')
        second = sc.query('g')
        self.assertEqual(second.dist2, F(25))
        self.assertEqual({(e.seg_a, e.seg_b) for e in second.evidence},
                         {('far', 'near')})

    def test_shared_endpoint_not_duplicated(self):
        sc = Scene()
        sc.add_group('poly')
        sc.add_segment('poly', 's1', (0, 0), (1, 0))
        sc.add_segment('poly', 's2', (1, 0), (2, 0))  # shares (1,0)
        sc.add_segment('poly', 's3', (0, 9), (1, 9))
        res = sc.query('poly')
        self.assertEqual(res.dist2, F(0))
        self.assertEqual(len(res.evidence), 1)  # one unordered pair only
        ev = res.evidence[0]
        self.assertEqual((ev.seg_a, ev.seg_b), ('s1', 's2'))
        self.assertEqual((ev.t_a, ev.t_b), (F(1), F(0)))
        self.assertEqual(ev.point_a, ev.point_b)

    def test_zero_length_segment_as_point_in_scene(self):
        sc = Scene()
        sc.add_group('g')
        sc.add_segment('g', 'dot', (5, 5), (5, 5))
        sc.add_segment('g', 'seg', (3, 5), (4, 5))
        res = sc.query('g')
        self.assertEqual(res.dist2, F(1))
        ev = res.evidence[0]
        self.assertEqual((ev.seg_a, ev.seg_b), ('dot', 'seg'))
        self.assertEqual((ev.t_a, ev.t_b), (F(0), F(1)))


class TestPersistentTree(unittest.TestCase):
    def build_scene(self, n=32):
        sc = Scene()
        sc.add_group('g')
        for i in range(n):
            sc.add_segment('g', f's{i:03d}', (i * 4, 0), (i * 4 + 1, 1))
        return sc

    def test_local_move_repairs_only_path_nodes(self):
        sc = self.build_scene(32)
        v1 = sc.snapshot()
        sc.move_segment('g', 's000', (1000, 1000), (1001, 1001))
        v2 = sc.snapshot()
        shared = count_nodes(v1.roots['g'], v2.roots['g'])
        only_v2 = count_nodes(v2.roots['g'])
        height = v2.roots['g'].height
        new_nodes = shared - count_nodes(v1.roots['g'])
        # path-copying: new nodes are O(height), everything else is shared
        self.assertGreaterEqual(new_nodes, 1)
        self.assertLessEqual(new_nodes, 8 * height + 8)
        self.assertGreater(only_v2 - new_nodes, 0)
        # other version still answers historical queries correctly
        hist = sc.query('g', version=v1)
        self.assertEqual(hist, brute_force(sc, v1, 'g', None, None, 'all'))

    def test_rebalance_keeps_inflight_query_safe(self):
        sc = self.build_scene(48)
        v1 = sc.snapshot()
        q = Query(sc, 'g', version=v1)
        q.step(2)  # query now in flight, holding v1
        live_before = Node.live
        # heavy churn forcing rotations on the live tree
        for i in range(48):
            sc.move_segment('g', f's{i:03d}', (i, 50 + i), (i + 1, 51 + i))
        # v1 nodes cannot have been reclaimed while q references them
        self.assertGreaterEqual(Node.live, live_before)
        while not q.done:
            q.step(4)
        self.assertEqual(q.result(),
                         brute_force(sc, v1, 'g', None, None, 'all'))
        del q, v1
        gc.collect()
        self.assertEqual(Node.live, count_nodes(sc._groups['g'].root))


class TestResourceAccounting(unittest.TestCase):
    def test_old_versions_released(self):
        gc.collect()
        base = Node.live
        sc = Scene()
        sc.add_group('g')
        for i in range(40):
            sc.add_segment('g', f's{i}', (i, 0), (i, 1))
        v1 = sc.snapshot()
        rng = random.Random(7)
        for _ in range(30):
            i = rng.randrange(40)
            sc.move_segment('g', f's{i}', (i * 3, i), (i * 3, i + 2))
        v2 = sc.snapshot()
        self.assertEqual(Node.live,
                         base + count_nodes(v1.roots['g'], v2.roots['g']))
        del v1
        gc.collect()
        self.assertEqual(Node.live, base + count_nodes(v2.roots['g']))
        for _ in range(10):
            sc.move_segment('g', 's0', (rng.randrange(50), 0), (0, 1))
        del v2
        gc.collect()
        self.assertEqual(Node.live,
                         base + count_nodes(sc._groups['g'].root))
        del sc
        gc.collect()
        self.assertEqual(Node.live, base)

    def test_cancelled_query_releases_version(self):
        gc.collect()
        sc = Scene()
        sc.add_group('g')
        for i in range(30):
            sc.add_segment('g', f's{i}', (i, 0), (i, 1))
        gc.collect()
        base = Node.live
        token = CancelToken()
        q = Query(sc, 'g', cancel_token=token)
        q.step(2)
        token.cancel()
        with self.assertRaises(QueryCancelled):
            q.step(1)
        del q, token
        gc.collect()
        self.assertEqual(Node.live, base)


class TestEventInterleaving(unittest.TestCase):
    def test_interleaved_events_match_bruteforce(self):
        rng = random.Random(20260925)
        sc = Scene()
        for name in ('g1', 'g2'):
            sc.add_group(name)
        live_ids = {'g1': [], 'g2': []}
        snapshots = []
        checks = 0

        def rand_pt():
            return (F(rng.randrange(-12, 13), rng.choice([1, 1, 2, 3])),
                    F(rng.randrange(-12, 13), rng.choice([1, 1, 2, 3])))

        def verify(version):
            nonlocal checks
            ga = rng.choice(('g1', 'g2'))
            gb = rng.choice((None, 'g1', 'g2'))
            radius = rng.choice((None, F(3), F(7, 2), F(20)))
            mode = rng.choice(('all', 'representative'))
            got = sc.query(ga, gb, radius=radius, mode=mode, version=version)
            want = brute_force(sc, version, ga, gb, radius, mode)
            self.assertEqual(got, want,
                             f'mismatch ga={ga} gb={gb} r={radius} m={mode}')
            checks += 1

        for step in range(160):
            op = rng.random()
            if op < 0.22:
                g = rng.choice(('g1', 'g2'))
                sid = f'{g}#{step}'
                sc.add_segment(g, sid, rand_pt(), rand_pt())
                live_ids[g].append(sid)
            elif op < 0.45 and any(live_ids.values()):
                g = rng.choice([k for k in live_ids if live_ids[k]])
                sid = rng.choice(live_ids[g])
                sc.move_segment(g, sid, rand_pt(), rand_pt())
            elif op < 0.58 and any(live_ids.values()):
                g = rng.choice([k for k in live_ids if live_ids[k]])
                sid = live_ids[g].pop(rng.randrange(len(live_ids[g])))
                sc.remove_segment(g, sid)
            elif op < 0.66 and sum(map(len, live_ids.values())) >= 2:
                g = rng.choice(('g1', 'g2', None))
                pool = [s for ids in live_ids.values() for s in ids]
                a, b = rng.sample(pool, 2)
                sc.add_exclusion(g, a, b)
            elif op < 0.76:
                snapshots.append(sc.snapshot())
                if len(snapshots) > 6:
                    snapshots.pop(rng.randrange(len(snapshots)))
            elif op < 0.86:
                # stepwise query with mid-flight cancellation
                version = sc.snapshot()
                q = Query(sc, 'g1', 'g2', version=version)
                q.step(rng.randrange(1, 4))
                if rng.random() < 0.5:
                    q.cancel()
                    with self.assertRaises(QueryCancelled):
                        q.step(1)
                else:
                    while not q.done:
                        q.step(3)
                    self.assertEqual(
                        q.result(),
                        brute_force(sc, version, 'g1', 'g2', None, 'all'))
                del q, version
                gc.collect()
            else:
                version = (rng.choice(snapshots) if snapshots
                           and rng.random() < 0.5 else sc.snapshot())
                verify(version)
        # final sweep: every group combination, both modes
        for ga, gb in (('g1', None), ('g2', None), ('g1', 'g2')):
            for mode in ('all', 'representative'):
                v = sc.snapshot()
                self.assertEqual(
                    sc.query(ga, gb, mode=mode, version=v),
                    brute_force(sc, v, ga, gb, None, mode))
                checks += 1
        self.assertGreaterEqual(checks, 20)


if __name__ == '__main__':
    unittest.main(verbosity=2)
