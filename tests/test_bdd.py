import itertools
import unittest

from invcheck.bdd import BDDManager, ResourceLimitExceeded


def all_assignments(variables):
    for bits in itertools.product([False, True], repeat=len(variables)):
        yield dict(zip(variables, bits))


class TestBDDCore(unittest.TestCase):
    def setUp(self):
        self.mgr = BDDManager(["x", "y", "z"])

    def test_constants_and_var(self):
        self.assertEqual(self.mgr.TRUE, 1)
        self.assertEqual(self.mgr.FALSE, 0)
        x = self.mgr.var("x")
        self.assertTrue(self.mgr.evaluate(x, {"x": True}))
        self.assertFalse(self.mgr.evaluate(x, {"x": False}))

    def test_connectives_match_python_semantics(self):
        m = self.mgr
        x, y = m.var("x"), m.var("y")
        ops = {
            "and": (m.and_(x, y), lambda a, b: a and b),
            "or": (m.or_(x, y), lambda a, b: a or b),
            "xor": (m.xor(x, y), lambda a, b: a != b),
            "imp": (m.implies(x, y), lambda a, b: (not a) or b),
            "iff": (m.iff(x, y), lambda a, b: a == b),
            "not": (m.negate(x), lambda a, b: not a),
        }
        for name, (bdd, fn) in ops.items():
            for asn in all_assignments(["x", "y"]):
                got = m.evaluate(bdd, asn)
                want = fn(asn["x"], asn["y"])
                self.assertEqual(got, want, f"{name} at {asn}")

    def test_ite(self):
        m = self.mgr
        f = m.ite(m.var("x"), m.var("y"), m.var("z"))
        for asn in all_assignments(["x", "y", "z"]):
            want = asn["y"] if asn["x"] else asn["z"]
            self.assertEqual(m.evaluate(f, asn), want)

    def test_exists(self):
        m = self.mgr
        f = m.and_(m.var("x"), m.var("y"))
        g = m.exists(f, ["x"])
        self.assertEqual(g, m.var("y"))

    def test_forall(self):
        m = self.mgr
        f = m.or_(m.var("x"), m.var("y"))
        g = m.forall(f, ["x"])
        self.assertEqual(g, m.var("y"))

    def test_rename(self):
        m = BDDManager(["x", "y", "z"])
        f = m.and_(m.var("x"), m.var("y"))
        g = m.rename(f, {"x": "z"})
        for asn in all_assignments(["x", "y", "z"]):
            self.assertEqual(m.evaluate(g, asn), asn["z"] and asn["y"])

    def test_sat_count(self):
        m = self.mgr
        f = m.or_(m.var("x"), m.var("y"))
        self.assertEqual(m.sat_count(f), 6)  # 3 assignments * 2 (z free)
        self.assertEqual(m.sat_count(m.TRUE), 8)
        self.assertEqual(m.sat_count(m.FALSE), 0)

    def test_support(self):
        m = self.mgr
        f = m.and_(m.var("x"), m.var("z"))
        self.assertEqual(m.support(f), {"x", "z"})

    def test_one_cube_satisfies(self):
        m = self.mgr
        f = m.and_(m.var("x"), m.or_(m.var("y"), m.var("z")))
        cube = m.one_cube(f)
        self.assertIsNotNone(cube)
        self.assertTrue(m.evaluate(f, cube))
        self.assertIsNone(m.one_cube(m.FALSE))

    def test_minimize_cube_irredundant(self):
        m = self.mgr
        f = m.and_(m.var("x"), m.var("y"))
        cube = {"x": True, "y": True, "z": False}
        out = m.minimize_cube(f, cube)
        self.assertEqual(out, {"x": True, "y": True})

    def test_sharing_unique_table(self):
        m = self.mgr
        a = m.and_(m.var("x"), m.var("y"))
        b = m.and_(m.var("x"), m.var("y"))
        self.assertEqual(a, b)  # structural sharing: same node id

    def test_node_budget(self):
        m = BDDManager(["x", "y"], max_nodes=3)
        m.var("x")
        with self.assertRaises(ResourceLimitExceeded):
            m.and_(m.var("x"), m.var("y"))

    def test_op_budget(self):
        m = BDDManager(["x", "y"], max_ops=5)
        with self.assertRaises(ResourceLimitExceeded):
            f = m.var("x")
            for _ in range(50):
                f = m.xor(f, m.var("y"))


class TestVariableOrderIndependence(unittest.TestCase):
    """Same semantics regardless of the manager's variable order."""

    def test_semantics_order_independent(self):
        orders = [["a", "b", "c", "d"], ["d", "c", "b", "a"], ["b", "d", "a", "c"]]
        results = []
        for order in orders:
            m = BDDManager(order)
            f = m.and_(m.or_(m.var("a"), m.var("b")),
                       m.xor(m.var("c"), m.var("d")))
            table = {tuple(sorted(asn.items())): m.evaluate(f, asn)
                     for asn in all_assignments(["a", "b", "c", "d"])}
            results.append((m.sat_count(f), table))
        for i in range(1, len(results)):
            self.assertEqual(results[0], results[i])


if __name__ == "__main__":
    unittest.main()
