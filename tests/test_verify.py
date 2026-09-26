import unittest
from fractions import Fraction
from random import Random

from timefit import verify
from timefit.piecewise import Anchor, PiecewiseAffineFitter
from timefit.timebase import Duration, Instant


class SolverTests(unittest.TestCase):
    def test_solve_square(self):
        sol = verify._solve_square(
            [[Fraction(2), Fraction(1)], [Fraction(1), Fraction(-1)]],
            [Fraction(5), Fraction(1)],
        )
        self.assertEqual(sol, [Fraction(2), Fraction(1)])

    def test_solve_square_singular(self):
        sol = verify._solve_square(
            [[Fraction(1), Fraction(1)], [Fraction(2), Fraction(2)]],
            [Fraction(3), Fraction(6)],
        )
        self.assertIsNone(sol)

    def test_vertices_of_unit_square(self):
        ineqs = [
            ((Fraction(1), Fraction(0)), Fraction(1)),
            ((Fraction(-1), Fraction(0)), Fraction(0)),
            ((Fraction(0), Fraction(1)), Fraction(1)),
            ((Fraction(0), Fraction(-1)), Fraction(0)),
        ]
        verts = set(verify.vertices(2, ineqs))
        self.assertEqual(
            verts,
            {
                (Fraction(0), Fraction(0)),
                (Fraction(0), Fraction(1)),
                (Fraction(1), Fraction(0)),
                (Fraction(1), Fraction(1)),
            },
        )
        self.assertEqual(
            verify.objective_range(2, ineqs, (Fraction(1), Fraction(1))),
            (Fraction(0), Fraction(2)),
        )

    def test_infeasible_system(self):
        ineqs = [
            ((Fraction(1),), Fraction(1)),
            ((Fraction(-1),), Fraction(-2)),
        ]
        self.assertFalse(verify.feasible(1, ineqs))
        self.assertIsNone(verify.objective_range(1, ineqs, (Fraction(1),)))


def _random_anchors(rng, n, drift, corrupt):
    """Anchors whose true slope stays within `drift` (feasible unless corrupt)."""
    anchors = []
    t = 0
    offset = Fraction(rng.randint(-5, 5))
    limit = int(drift * 800)  # true slope comfortably inside the drift bound
    for _ in range(n):
        dt = rng.randint(5, 30)
        t += dt
        slope = Fraction(rng.randint(-limit, limit), 1000) if limit else Fraction(0)
        offset += slope * dt
        err = rng.randint(0, 200)
        noise = Fraction(rng.randint(-err, err), 1000)
        anchors.append(
            Anchor(
                Instant(Fraction(t)),
                Instant(Fraction(t) + offset + noise),
                Duration(milliseconds=err),
            )
        )
    if corrupt and anchors:
        i = rng.randrange(len(anchors))
        a = anchors[i]
        anchors[i] = Anchor(a.local, Instant(a.ref.seconds + rng.randint(5, 50)), a.error)
    return anchors


class CrossCheckTests(unittest.TestCase):
    """Fitter (difference constraints) vs. rational vertex enumeration."""

    def test_feasibility_and_query_bounds_match(self):
        rng = Random(20260926)
        checked = 0
        for _ in range(30):
            drift = Fraction(rng.choice([0, 1, 5, 100]), 1000)
            anchors = _random_anchors(
                rng, rng.randint(2, 5), drift, corrupt=rng.random() < 0.3
            )
            fitter = PiecewiseAffineFitter(anchors, drift, Duration(10_000_000))
            n, ineqs, times = verify.build_inequalities(anchors, drift)
            verts = list(verify.vertices(n, ineqs))
            self.assertEqual(fitter.feasible(), bool(verts))
            if not verts:
                continue
            checked += 1
            queries = []
            for k in range(n - 1):
                queries.append((times[k] + times[k + 1]) / 2)
            queries.append(times[-1] + (times[-1] - times[-2]) / 2)
            for t in queries:
                if t <= times[0]:
                    seg = 0
                elif t >= times[-1]:
                    seg = n - 2
                else:
                    seg = max(k for k in range(n - 1) if times[k] <= t)
                mu = (t - times[seg]) / (times[seg + 1] - times[seg])
                objective = [Fraction(0)] * n
                objective[seg] = 1 - mu
                objective[seg + 1] = mu
                expected = [
                    sum(c * x for c, x in zip(objective, point)) for point in verts
                ]
                qr = fitter.query(Instant(t))
                self.assertEqual(qr.ref_lo.seconds, min(expected))
                self.assertEqual(qr.ref_hi.seconds, max(expected))
        self.assertGreater(checked, 5)


if __name__ == "__main__":
    unittest.main()
