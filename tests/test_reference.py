import random
import unittest
from fractions import Fraction as Fr

from stcollide import World
from stcollide.reference import reference_first_collision, reference_intervals


def random_world(rng):
    w = World(dim=2, t0=0, t1=10, cell_size=rng.choice([1, 2, 3, 5]))
    n = rng.randint(2, 4)
    for i in range(n):
        oid = f"o{i}"

        def coord():
            return rng.choice([rng.randint(-4, 4), Fr(rng.randint(-8, 8), 2)])

        lo = tuple(coord() for _ in range(2))
        hi = tuple(l + rng.randint(1, 3) for l in lo)
        vel = tuple(rng.choice([rng.randint(-3, 3), Fr(rng.randint(-3, 3), 2)])
                    for _ in range(2))
        w.add_object(oid, lo, hi, vel)
        if rng.random() < 0.5:  # 窗口内变速
            w.set_velocity(oid, rng.randint(1, 9),
                           tuple(rng.randint(-3, 3) for _ in range(2)))
        if rng.random() < 0.3:  # 窗口内销毁
            w.destroy(oid, rng.randint(1, 10))
    return w


class TestAgainstReference(unittest.TestCase):
    def test_intervals_and_first_collision_match_reference(self):
        for seed in range(300):
            rng = random.Random(seed)
            w = random_world(rng)
            with self.subTest(seed=seed):
                self.assertEqual(w.collision_intervals(), reference_intervals(w))
                self.assertEqual(w.first_collision(), reference_first_collision(w))

    def test_broadphase_candidates_cover_colliding_pairs(self):
        for seed in range(300, 400):
            rng = random.Random(seed)
            w = random_world(rng)
            candidates = {frozenset(p) for p in w.grid.all_pairs()}
            for pair, ivs in reference_intervals(w).items():
                with self.subTest(seed=seed, pair=pair):
                    self.assertIn(frozenset(pair), candidates)

    def test_simulate_first_event_matches_first_overlap(self):
        # 无响应模拟：首个事件应与首个 overlap 区间同时刻、同对象对
        for seed in range(400, 500):
            rng = random.Random(seed)
            w = random_world(rng)
            overlaps = [
                (iv.enter, pair)
                for pair, ivs in w.collision_intervals().items()
                for iv in ivs
                if iv.kind == "overlap"
            ]
            result = w.simulate()
            with self.subTest(seed=seed):
                if not overlaps:
                    self.assertEqual(result.events, [])
                else:
                    first_t = min(t for t, _ in overlaps)
                    first_pairs = {frozenset(p) for t, p in overlaps if t == first_t}
                    got = {(e.time, frozenset((e.a, e.b))) for e in result.events
                           if e.time == first_t}
                    self.assertEqual(got, {(first_t, p) for p in first_pairs})


if __name__ == "__main__":
    unittest.main()
